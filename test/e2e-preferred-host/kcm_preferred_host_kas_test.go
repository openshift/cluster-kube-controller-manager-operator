package e2e_preferred_host

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	k8se2eframework "k8s.io/kubernetes/test/e2e/framework"

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
	testNs, err := k8se2eframework.CreateTestingNS("kcm-preferred-host", kubeClient, nil)
	require.NoError(t, err)
	err = k8se2eframework.WaitForDefaultServiceAccountInNamespace(kubeClient, testNs.Name)
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
