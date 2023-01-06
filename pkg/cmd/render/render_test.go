package render

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
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
		// {filename: {field: value}}
		expectedContents map[string]map[string]interface{}
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
				"manifests/manifests/0000_00_namespace-openshift-infra.yaml",
				"manifests/manifests/00_namespace-security-allocation-controller-clusterrole.yaml",
				"manifests/manifests/00_namespace-security-allocation-controller-clusterrolebinding.yaml",
				"manifests/manifests/00_openshift-kube-controller-manager-ns.yaml",
				"manifests/manifests/00_openshift-kube-controller-manager-operator-ns.yaml",
				"manifests/manifests/00_podsecurity-admission-label-syncer-controller-clusterrole.yaml",
				"manifests/manifests/00_podsecurity-admission-label-syncer-controller-clusterrolebinding.yaml",
				"manifests/manifests/secret-csr-signer-signer.yaml",
				"manifests/manifests/secret-initial-kube-controller-manager-service-account-private-key.yaml",
			},
			expectedContents: map[string]map[string]interface{}{
				"manifests/bootstrap-manifests/kube-controller-manager-pod.yaml": {
					"spec.containers[0].args": []interface{}{
						"--openshift-config=/etc/kubernetes/config/kube-controller-manager-config.yaml",
						"--kubeconfig=/etc/kubernetes/secrets/kubeconfig",
						"--logtostderr=false",
						"--alsologtostderr",
						"--v=2",
						"--log-file=/var/log/bootstrap-control-plane/kube-controller-manager.log",
						"--allocate-node-cidrs=false",
						"--authentication-kubeconfig=/etc/kubernetes/secrets/kubeconfig",
						"--authorization-kubeconfig=/etc/kubernetes/secrets/kubeconfig",
						"--cert-dir=/var/run/kubernetes",
						"--cluster-signing-cert-file=/etc/kubernetes/secrets/kubelet-signer.crt",
						"--cluster-signing-duration=720h",
						"--cluster-signing-key-file=/etc/kubernetes/secrets/kubelet-signer.key",
						"--configure-cloud-routes=false",
						"--controllers=*",
						"--controllers=-bootstrapsigner",
						"--controllers=-tokencleaner",
						"--controllers=-ttl",
						"--enable-dynamic-provisioning=true",
						"--flex-volume-plugin-dir=/etc/kubernetes/kubelet-plugins/volume/exec",
						"--kube-api-burst=300",
						"--kube-api-qps=150",
						"--leader-elect-resource-lock=configmapsleases",
						"--leader-elect-retry-period=3s",
						"--leader-elect-renew-deadline=12s",
						"--leader-elect=true",
						"--pv-recycler-pod-template-filepath-hostpath=",
						"--pv-recycler-pod-template-filepath-nfs=",
						"--root-ca-file=/etc/kubernetes/secrets/kube-apiserver-complete-server-ca-bundle.crt",
						"--secure-port=10257",
						"--service-account-private-key-file=/etc/kubernetes/secrets/service-account.key",
						"--use-service-account-credentials=true",
					},
				},
			},
		},
	}

	for _, test := range tests {
		teardown, outputDir, err := setupAssetOutputDir(test.name)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", test.name, err)
		}
		defer teardown()

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
			if file, ok := test.expectedContents[f]; ok {
				data, err := ioutil.ReadFile(p)
				if err != nil {
					t.Errorf("error reading file %s: %v", p, err)
					continue
				}
				dataJSON, err := yaml.YAMLToJSON(data)
				if err != nil {
					t.Errorf("error converting file %s: %v", p, err)
					continue
				}
				configObj, err := runtime.Decode(unstructured.UnstructuredJSONScheme, dataJSON)
				if err != nil {
					t.Errorf("error decoding %s: %v", p, err)
					continue
				}
				config := configObj.(*unstructured.Unstructured)
				for field, expectedValue := range file {
					actualValue, err := readPath(config.Object, field)
					if err != nil {
						t.Errorf("error reading field %s: %v", field, err)
						continue
					}
					if !reflect.DeepEqual(actualValue, expectedValue) {
						t.Errorf("error comparing %s: \n%s\nvs\n%s\n", field, expectedValue, actualValue)
						continue
					}
				}
			}
		}
	}
}

func readPath(obj map[string]interface{}, path string) (interface{}, error) {
	if strings.Contains(path, "[") {
		nestedPath := strings.Split(path, "[")
		outerObj, err := readPath(obj, nestedPath[0])
		if err != nil {
			return nil, err
		}
		index, err := strconv.Atoi(string(nestedPath[1][0]))
		if err != nil {
			return nil, err
		}
		newObj := outerObj.([]interface{})
		return readPath(newObj[index].(map[string]interface{}), nestedPath[1][3:])
	}
	actualValue, found, err := unstructured.NestedFieldNoCopy(obj, strings.Split(path, ".")...)
	if !found && err != nil {
		return nil, fmt.Errorf("error reading field %s: %v", path, err)
	}
	return actualValue, nil
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
