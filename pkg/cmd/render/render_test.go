package render

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
)

var (
	expectedClusterCIDR = []string{"10.128.0.0/14"}
	expectedServiceCIDR = []string{"172.30.0.0/16"}
	clusterAPIConfig    = `
apiVersion: machine.openshift.io/v1beta1
kind: Cluster
metadata:
  creationTimestamp: null
  name: cluster
  namespace: openshift-machine-api
spec:
  clusterNetwork:
    pods:
      cidrBlocks:
        - 10.128.0.0/14
    serviceDomain: ""
    services:
      cidrBlocks:
        - 172.30.0.0/16
  providerSpec: {}
status: {}
`
	networkConfig = `
apiVersion: config.openshift.io/v1
kind: Network
metadata:
  creationTimestamp: null
  name: cluster
spec:
  clusterNetwork:
    - cidr: 10.128.0.0/14
      hostPrefix: 23
  networkType: OpenShiftSDN
  serviceNetwork:
    - 172.30.0.0/16
status: {}
`
)

func runRender(args ...string) (*cobra.Command, error) {
	errOut := &bytes.Buffer{}
	c := NewRenderCommand(errOut)
	os.Args = append([]string{"render.test"}, args...)
	if err := c.Execute(); err != nil {
		panic(err)
	}
	errBytes, err := ioutil.ReadAll(errOut)
	if err != nil {
		panic(err)
	}
	if len(errBytes) == 0 {
		return c, nil
	}
	return c, errors.New(string(errBytes))
}

func setupAssetOutputDir(testName string) (teardown func(), outputDir string, err error) {
	outputDir, err = ioutil.TempDir("", testName)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "manifests"), os.ModePerm); err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Join(outputDir, "configs"), os.ModePerm); err != nil {
		return nil, "", err
	}
	teardown = func() {
		os.RemoveAll(outputDir)
	}
	return
}

func setOutputFlags(args []string, dir string) []string {
	newArgs := []string{}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--asset-output-dir=") {
			newArgs = append(newArgs, "--asset-output-dir="+filepath.Join(dir, "manifests"))
			continue
		}
		if strings.HasPrefix(arg, "--config-output-file=") {
			newArgs = append(newArgs, "--config-output-file="+filepath.Join(dir, "configs", "config.yaml"))
			continue
		}
		if strings.HasPrefix(arg, "--cpc-config-output-file=") {
			newArgs = append(newArgs, "--cpc-config-output-file="+filepath.Join(dir, "configs", "cpc-config.yaml"))
			continue
		}
		newArgs = append(newArgs, arg)
	}
	return newArgs
}

func TestRenderCommand(t *testing.T) {
	assetsInputDir := filepath.Join("testdata", "tls")
	templateDir := filepath.Join("..", "..", "..", "bindata", "bootkube")

	tests := []struct {
		name          string
		args          []string
		expectedErr   error
		expectedFiles []string
	}{
		{
			name: "no-flags",
			args: []string{
				"--templates-input-dir=" + templateDir,
			},
			expectedErr: errors.New("missing required flag: --asset-input-dirfailed loading assets from \"\": lstat : no such file or directory"),
		},
		{
			name: "happy-path",
			args: []string{
				"--asset-input-dir=" + assetsInputDir,
				"--templates-input-dir=" + templateDir,
				"--asset-output-dir=",
				"--config-output-file=",
				"--cpc-config-output-file=",
			},
			expectedErr: nil,
			expectedFiles: []string{
				"configs/config.yaml",
				"configs/cpc-config.yaml",
				"manifests/bootstrap-manifests/kube-controller-manager-pod.yaml",
				"manifests/manifests/00_openshift-kube-controller-manager-ns.yaml",
				"manifests/manifests/00_openshift-kube-controller-manager-operator-ns.yaml",
				"manifests/manifests/secret-csr-signer-signer.yaml",
				"manifests/manifests/secret-initial-kube-controller-manager-service-account-private-key.yaml",
			},
		},
	}

	for _, test := range tests {
		_, outputDir, err := setupAssetOutputDir(test.name)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", test.name, err)
		}
		// defer teardown()

		test.args = setOutputFlags(test.args, outputDir)

		_, err = runRender(test.args...)
		if !reflect.DeepEqual(err, test.expectedErr) {
			t.Fatalf("expected error %#v, got %#v", test.expectedErr, err)
		}

		var files []string
		err = filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
			r, err := filepath.Rel(outputDir, path)
			if err != nil {
				return err
			}

			if !info.IsDir() {
				files = append(files, r)
			}

			return nil
		})
		if err != nil {
			t.Error(err)
		}

		sort.Strings(files)
		sort.Strings(test.expectedFiles)

		if !reflect.DeepEqual(files, test.expectedFiles) {
			t.Errorf("expected and rendered files differ: %s", cmp.Diff(test.expectedFiles, files))
		}

		for _, f := range test.expectedFiles {
			p := path.Join(outputDir, f)
			_, err := os.Stat(p)
			if err != nil {
				t.Errorf("file %q: %v", f, err)
			}
		}
	}
}

func TestDiscoverRestrictedCIDRsFromNetwork(t *testing.T) {
	renderConfig := TemplateData{}
	if err := discoverRestrictedCIDRsFromNetwork([]byte(networkConfig), &renderConfig); err != nil {
		t.Errorf("failed discoverCIDRs: %v", err)
	}
	if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
	}
	if !reflect.DeepEqual(renderConfig.ServiceClusterIPRange, expectedServiceCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ServiceClusterIPRange, expectedServiceCIDR)
	}
}

func TestDiscoverRestrictedCIDRsFromClusterAPI(t *testing.T) {
	renderConfig := TemplateData{}
	if err := discoverRestrictedCIDRsFromClusterAPI([]byte(clusterAPIConfig), &renderConfig); err != nil {
		t.Errorf("failed discoverCIDRs: %v", err)
	}
	if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
	}
	if !reflect.DeepEqual(renderConfig.ServiceClusterIPRange, expectedServiceCIDR) {
		t.Errorf("Got: %v, expected: %v", renderConfig.ServiceClusterIPRange, expectedServiceCIDR)
	}
}

func TestDiscoverRestrictedCIDRs(t *testing.T) {
	testCase := []struct {
		config []byte
	}{
		{
			config: []byte(networkConfig),
		},
		{
			config: []byte(clusterAPIConfig),
		},
	}

	for _, tc := range testCase {
		renderConfig := TemplateData{}
		if err := discoverRestrictedCIDRs(tc.config, &renderConfig); err != nil {
			t.Errorf("failed to discoverCIDRs: %v", err)
		}

		if !reflect.DeepEqual(renderConfig.ClusterCIDR, expectedClusterCIDR) {
			t.Errorf("Got: %v, expected: %v", renderConfig.ClusterCIDR, expectedClusterCIDR)
		}
		if !reflect.DeepEqual(renderConfig.ServiceClusterIPRange, expectedServiceCIDR) {
			t.Errorf("Got: %v, expected: %v", renderConfig.ServiceClusterIPRange, expectedServiceCIDR)
		}
	}
}
