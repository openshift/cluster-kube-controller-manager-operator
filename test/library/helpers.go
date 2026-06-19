package library

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	g "github.com/onsi/ginkgo/v2"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	testdataFS "github.com/openshift/cluster-kube-controller-manager-operator/test/testdata"
	machineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	// coStabilityInterval is the time to wait after an operator reaches the
	// desired status before re-checking, to confirm the status is stable.
	coStabilityInterval = 100 * time.Second

	// PrometheusURL is the in-cluster Prometheus query endpoint.
	PrometheusURL = "https://prometheus-k8s.openshift-monitoring.svc:9091/api/v1/query"
)

var (
	fixtureDir  string
	fixtureOnce sync.Once
)

// FixturePath returns an absolute path to a file under test/testdata.
// When running from source (go test), it resolves relative to the source tree.
// When running from a compiled binary (OTE), it extracts embedded fixtures to a
// temp directory and returns paths there.
func FixturePath(elem ...string) string {
	_, filename, _, ok := runtime.Caller(0)
	if ok {
		base := filepath.Join(filepath.Dir(filename), "..", "testdata")
		path := filepath.Join(append([]string{base}, elem...)...)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	fixtureOnce.Do(func() {
		dir, err := os.MkdirTemp("", "kcm-e2e-fixtures-")
		if err != nil {
			g.Fail(fmt.Sprintf("create fixture temp dir: %v", err))
		}
		if err := fs.WalkDir(testdataFS.Content, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			dest := filepath.Join(dir, path)
			if d.IsDir() {
				return os.MkdirAll(dest, 0o755)
			}
			data, err := testdataFS.Content.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(dest, data, 0o644)
		}); err != nil {
			g.Fail(fmt.Sprintf("extract embedded fixtures: %v", err))
		}
		fixtureDir = dir
	})

	return filepath.Join(append([]string{fixtureDir}, elem...)...)
}

func AssertWaitPollNoErr(err error, msg string) {
	if err != nil {
		g.Fail(fmt.Sprintf("%s: %v", msg, err))
	}
}

func AssertWaitPollWithErr(err error, msg string) {
	if err == nil {
		g.Fail(fmt.Sprintf("%s: expected timeout but poll succeeded", msg))
	}
	if !wait.Interrupted(err) && err != wait.ErrWaitTimeout {
		g.Fail(fmt.Sprintf("%s: %v", msg, err))
	}
}

func WaitCoBecomes(oc *OC, coName string, waitSec int, expectedStatus map[string]string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(waitSec)*time.Second)
	defer cancel()
	return wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		gotten := getCoStatus(oc, coName, expectedStatus)
		if !reflect.DeepEqual(expectedStatus, gotten) {
			return false, nil
		}
		if reflect.DeepEqual(expectedStatus, map[string]string{"Available": "True", "Progressing": "False", "Degraded": "False"}) {
			time.Sleep(coStabilityInterval)
			gotten = getCoStatus(oc, coName, expectedStatus)
			if !reflect.DeepEqual(expectedStatus, gotten) {
				return false, nil
			}
		}
		fmt.Fprintf(g.GinkgoWriter, "operator %s reached status %v\n", coName, gotten)
		return true, nil
	})
}

func getCoStatus(oc *OC, coName string, statusToCompare map[string]string) map[string]string {
	result := make(map[string]string)
	for key := range statusToCompare {
		args := fmt.Sprintf(`-o=jsonpath={.status.conditions[?(.type == '%s')].status}`, key)
		status, err := oc.WithoutNamespace().Run("get").Args("co", args, coName).Output()
		if err != nil {
			g.Fail(fmt.Sprintf("get co status: %v", err))
		}
		result[key] = status
	}
	return result
}

func GetLeaderKCM(oc *OC) string {
	output, err := oc.WithoutNamespace().Run("get").Args("lease/kube-controller-manager", "-n", "kube-system", "-o=jsonpath={.spec.holderIdentity}").Output()
	if err != nil {
		g.Fail(fmt.Sprintf("get KCM leader lease: %v", err))
	}
	leaderIP := strings.Split(output, "_")[0]
	out, err := oc.WithoutNamespace().Run("get").Args("node", "-l", "node-role.kubernetes.io/master=", "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		g.Fail(fmt.Sprintf("get master nodes: %v", err))
	}
	for _, master := range strings.Fields(out) {
		if matched, _ := regexp.MatchString(leaderIP, master); matched {
			return master
		}
	}
	g.Fail("KCM leader node not found")
	return ""
}

func GetClusterNodesBy(oc *OC, role string) []string {
	label := "node-role.kubernetes.io/worker="
	if role == "master" {
		label = "node-role.kubernetes.io/master="
	}
	out, err := oc.WithoutNamespace().Run("get").Args("node", "-l", label, "-o=jsonpath={.items[*].metadata.name}").Output()
	if err != nil {
		g.Fail(fmt.Sprintf("get cluster nodes by %s: %v", role, err))
	}
	return strings.Fields(out)
}

func DebugNodeWithChroot(oc *OC, node string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	debugArgs := append([]string{"debug", "node/" + node, "--", "chroot", "/host"}, args...)
	cmd := exec.CommandContext(ctx, "oc", debugArgs...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func GetSAToken(oc *OC) string {
	token, err := oc.WithoutNamespace().Run("create").Args("token", "prometheus-k8s", "-n", "openshift-monitoring").Output()
	if err != nil {
		g.Fail(fmt.Sprintf("get SA token: %v", err))
	}
	return strings.TrimSpace(token)
}

func RemoteShPod(oc *OC, namespace, pod, shell string, args ...string) (string, error) {
	execArgs := append([]string{"-n", namespace, "pod/" + pod, "--", shell}, args...)
	return oc.WithoutNamespace().Run("exec").Args(execArgs...).Output()
}

func CheckMetric(oc *OC, url, token, metricString string, timeoutSec int) {
	getCmd := "curl -G -k -s -H 'Authorization:Bearer " + token + "' " + url
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	err := wait.PollUntilContextCancel(ctx, 10*time.Second, true, func(ctx context.Context) (bool, error) {
		metrics, err := RemoteShPod(oc, "openshift-monitoring", "prometheus-k8s-0", "sh", "-c", getCmd)
		if err != nil || !strings.Contains(metrics, metricString) {
			return false, nil
		}
		return true, nil
	})
	AssertWaitPollNoErr(err, fmt.Sprintf("metric output should contain %q", metricString))
}

func WaitForAvailableRsRunning(oc *OC, rsKind, rsName, namespace, expected string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	err := wait.PollUntilContextCancel(ctx, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		output, err := oc.WithoutNamespace().Run("get").Args(rsKind, rsName, "-n", namespace, "-o=jsonpath={.status.availableReplicas}").Output()
		if err != nil {
			return false, nil
		}
		return strings.TrimSpace(output) == expected, nil
	})
	return err == nil
}

func CheckPodStatus(oc *OC, podLabel, namespace, expected string) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	err := wait.PollUntilContextCancel(ctx, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		output, err := oc.WithoutNamespace().Run("get").Args("pod", "-n", namespace, "-l", podLabel, "-o=jsonpath={.items[*].status.phase}").Output()
		if err != nil {
			return false, nil
		}
		if strings.Contains(output, expected) && !strings.Contains(strings.ToLower(output), "error") && !strings.Contains(strings.ToLower(output), "crashloopbackoff") {
			return true, nil
		}
		return false, nil
	})
	AssertWaitPollNoErr(err, fmt.Sprintf("pod %s not in expected state %s", podLabel, expected))
}

func IsTechPreviewNoUpgrade() bool {
	kubeConfig, err := NewClientConfigForTest()
	if err != nil {
		g.Fail(fmt.Sprintf("get kube config: %v", err))
	}
	client, err := configclient.NewForConfig(kubeConfig)
	if err != nil {
		g.Fail(fmt.Sprintf("create config client: %v", err))
	}
	fg, err := client.FeatureGates().Get(context.Background(), "cluster", metav1.GetOptions{})
	if machineryerrors.IsNotFound(err) {
		return false
	}
	if err != nil {
		g.Fail(fmt.Sprintf("get feature gate: %v", err))
	}
	return fg.Spec.FeatureSet == configv1.TechPreviewNoUpgrade
}

func IsBaselineCapsSet(oc *OC, component string) bool {
	out, err := oc.WithoutNamespace().Run("get").Args("clusterversion", "version", "-o=jsonpath={.spec.capabilities.baselineCapabilitySet}").Output()
	if err != nil {
		g.Fail(fmt.Sprintf("get baseline capability set: %v", err))
	}
	return strings.Contains(out, component)
}

func IsEnabledCapability(oc *OC, component string) bool {
	out, err := oc.WithoutNamespace().Run("get").Args("clusterversion", "-o=jsonpath={.items[*].status.capabilities.enabledCapabilities}").Output()
	if err != nil {
		g.Fail(fmt.Sprintf("get enabled capabilities: %v", err))
	}
	return strings.Contains(out, component)
}

func IsSNOCluster(oc *OC) bool {
	masters := GetClusterNodesBy(oc, "master")
	workers := GetClusterNodesBy(oc, "worker")
	return len(masters) == 1 && len(workers) == 1 && masters[0] == workers[0]
}

func SkipForSNOCluster(oc *OC) {
	if IsSNOCluster(oc) {
		g.Skip("SNO cluster — test requires multiple nodes")
	}
}

func GetTimeFromTimezone() (schedule, timeZone string) {
	localZone, err := time.LoadLocation("")
	if err != nil {
		g.Fail(fmt.Sprintf("load timezone: %v", err))
	}
	currentTime := time.Now().In(localZone)
	hour, minu, _ := currentTime.Clock()
	if minu >= 58 {
		hour = (hour + 1) % 24
		minu = (minu + 2) % 60
	} else {
		minu += 2
	}
	return fmt.Sprintf("%d %d * * *", minu, hour), localZone.String()
}

func ApplyResourceFromTemplate(oc *OC, parameters ...string) error {
	var configFile string
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := wait.PollUntilContextCancel(ctx, 3*time.Second, true, func(ctx context.Context) (bool, error) {
		out, err := oc.Run("process").Args(parameters...).Output()
		if err != nil {
			return false, nil
		}
		tmp, err := os.CreateTemp("", "kcm-workload-*.json")
		if err != nil {
			return false, err
		}
		if _, err := tmp.WriteString(out); err != nil {
			return false, err
		}
		if err := tmp.Close(); err != nil {
			return false, err
		}
		configFile = tmp.Name()
		return true, nil
	})
	if err != nil {
		return err
	}
	defer os.Remove(configFile)
	return oc.WithoutNamespace().Run("apply").Args("-f", configFile).Execute()
}

func CreateDuplicatePodsRS(oc *OC, name, namespace, template string, replicas int) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := wait.PollUntilContextCancel(ctx, 5*time.Second, true, func(ctx context.Context) (bool, error) {
		err := ApplyResourceFromTemplate(oc, "--ignore-unknown-parameters=true", "-f", template,
			"-p", "DNAME="+name, "NAMESPACE="+namespace, "REPLICASNUM="+strconv.Itoa(replicas))
		return err == nil, nil
	})
	AssertWaitPollNoErr(err, "create duplicate pods RS")
}

func WaitForDaemonsetPodsToBeReady(kubeClient kubernetes.Interface, namespace, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err := wait.PollUntilContextCancel(ctx, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		ds, err := kubeClient.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if machineryerrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return ds.Status.DesiredNumberScheduled > 0 &&
			ds.Status.NumberReady == ds.Status.DesiredNumberScheduled &&
			ds.Status.UpdatedNumberScheduled == ds.Status.DesiredNumberScheduled, nil
	})
	AssertWaitPollNoErr(err, "daemonset not ready")
}

func GetDaemonsetDesiredNum(oc *OC, namespace, name string) int {
	out, err := oc.WithoutNamespace().Run("get").Args("daemonset", name, "-n", namespace, "-o=jsonpath={.status.desiredNumberScheduled}").Output()
	if err != nil {
		g.Fail(fmt.Sprintf("get daemonset desired number: %v", err))
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		g.Fail(fmt.Sprintf("parse daemonset desired number: %v", err))
	}
	return n
}

func CheckNodeUncordoned(oc *OC, node string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	return wait.PollUntilContextCancel(ctx, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		status, err := oc.WithoutNamespace().Run("get").Args("nodes", node, "-o=jsonpath={.spec}").Output()
		if err != nil {
			return false, err
		}
		return !strings.Contains(status, "unschedulable"), nil
	})
}

func SetNamespacePrivileged(oc *OC, namespace string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var lastErr error
	err := wait.PollUntilContextCancel(ctx, 2*time.Second, true, func(ctx context.Context) (bool, error) {
		lastErr = oc.WithoutNamespace().Run("label").Args("namespace", namespace,
			"pod-security.kubernetes.io/enforce=privileged",
			"pod-security.kubernetes.io/warn=privileged",
			"pod-security.kubernetes.io/audit=privileged",
			"--overwrite").Execute()
		return lastErr == nil, nil
	})
	if err != nil {
		g.Fail(fmt.Sprintf("set namespace privileged: %v", lastErr))
	}
}
