package e2e_preferred_host

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	operatorv1client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
	testlibrary "github.com/openshift/library-go/test/library"
)

var (
	// the following parameters specify for how long apis must
	// stay on the same revision to be considered stable
	waitForKCMRevisionSuccessThreshold = 3
	waitForKCMRevisionSuccessInterval  = 1 * time.Minute

	// the following parameters specify max timeout after which
	// KCMs are considered to not converged
	waitForKCMRevisionPollInterval = 30 * time.Second
	waitForKCMRevisionTimeout      = 7 * time.Minute

	nsCreationPollInterval = 2 * time.Second
)

// TestKCMTalksOverPreferredHostToKAS points the KCM to a non available host
// and sets unsupported-kube-api-over-localhost flag which changes it to use localhost instead.
// It then waits for new KCM and tests if creating an SA works.
func TestKCMTalksOverPreferredHostToKAS(t *testing.T) {
	// test data
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientset.NewForConfig(kubeConfig)
	require.NoError(t, err)
	operatorClientSet, err := operatorv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)
	kcmOperator := operatorClientSet.KubeControllerManagers()
	kcmOperatorConfig, err := kcmOperator.Get(context.TODO(), "cluster", metav1.GetOptions{})
	require.NoError(t, err)

	openShiftConfigClient, err := configv1client.NewForConfig(kubeConfig)
	require.NoError(t, err)

	t.Log("getting the current Kube API host")
	scheme, host := readCurrentKubeAPIHostAndScheme(t, openShiftConfigClient)

	t.Logf("setting the \"unsupported-kube-api-over-localhost\" flag and pointing the current master to %q (non available host)", fmt.Sprintf("%s://%s:1234", scheme, host))
	data := map[string]map[string][]string{
		"extendedArguments": {
			"master":                              []string{fmt.Sprintf("%s://%s:1234", scheme, host)},
			"unsupported-kube-api-over-localhost": []string{"true"},
		},
	}
	raw, err := json.Marshal(data)
	require.NoError(t, err)
	kcmOperatorConfig.Spec.UnsupportedConfigOverrides.Raw = raw
	_, err = kcmOperator.Update(context.TODO(), kcmOperatorConfig, metav1.UpdateOptions{})
	require.NoError(t, err)
	testlibrary.WaitForPodsToStabilizeOnTheSameRevision(t, kubeClient.CoreV1().Pods(operatorclient.TargetNamespace), "kube-controller-manager=true", waitForKCMRevisionSuccessThreshold, waitForKCMRevisionSuccessInterval, waitForKCMRevisionPollInterval, waitForKCMRevisionTimeout)

	// act
	t.Log("creating a namespace and waiting for a default sa to be populated")
	testNs, err := createTestingNS(t, "kcm-preferred-host", kubeClient)
	require.NoError(t, err)
	err = test.WaitForServiceAccountInNamespace(kubeClient, testNs.Name, "default")
	require.NoError(t, err)
}

func readCurrentKubeAPIHostAndScheme(t *testing.T, openShiftClientSet configv1client.Interface) (string, string) {
	infra, err := openShiftClientSet.ConfigV1().Infrastructures().Get(context.TODO(), "cluster", metav1.GetOptions{})
	require.NoError(t, err)

	apiServerInternalURL := infra.Status.APIServerInternalURL
	if len(apiServerInternalURL) == 0 {
		t.Fatal(fmt.Errorf("infrastucture/cluster: missing APIServerInternalURL"))
	}

	kubeAPIURL, err := url.Parse(apiServerInternalURL)
	if err != nil {
		t.Fatal(err)
	}

	host, _, err := net.SplitHostPort(kubeAPIURL.Host)
	if err != nil {
		// assume kubeAPIURL contains only host portion
		return kubeAPIURL.Scheme, kubeAPIURL.Host
	}

	return kubeAPIURL.Scheme, host
}

// createTestingNS should be used by every test, note that we append a common prefix to the provided test name.
// Please see NewFramework instead of using this directly.
// note this method has been copied from k/k repo
func createTestingNS(t *testing.T, baseName string, c clientset.Interface) (*v1.Namespace, error) {
	// We don't use ObjectMeta.GenerateName feature, as in case of API call
	// failure we don't know whether the namespace was created and what is its
	// name.
	name := fmt.Sprintf("%v-%v", baseName, strconv.Itoa(rand.Intn(10000)))

	namespaceObj := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "",
		},
		Status: v1.NamespaceStatus{},
	}
	// Be robust about making the namespace creation call.
	var got *v1.Namespace
	if err := wait.PollImmediate(nsCreationPollInterval, 30*time.Second, func() (bool, error) {
		var err error
		got, err = c.CoreV1().Namespaces().Create(context.TODO(), namespaceObj, metav1.CreateOptions{})
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				// regenerate on conflict
				t.Logf("Namespace name %q was already taken, generate a new name and retry", namespaceObj.Name)
				namespaceObj.Name = fmt.Sprintf("%v-%v", baseName, strconv.Itoa(rand.Intn(10000)))
			} else {
				t.Logf("Unexpected error while creating namespace: %v", err)
			}
			return false, nil
		}
		return true, nil
	}); err != nil {
		return nil, err
	}

	return got, nil
}
