package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/klog/v2"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
)

func TestSATokenSignerControllerSyncCerts(t *testing.T) {
	// initialize clients
	kubeConfig, err := test.NewClientConfigForTest()
	require.NoError(t, err)
	kubeClient, err := clientcorev1.NewForConfig(kubeConfig)
	require.NoError(t, err)
	configClient, err := configclient.NewForConfig(kubeConfig)
	require.NoError(t, err)

	ctx := context.Background()

	// wait for the operator readiness
	klog.Infof("Waiting for true, false, false")
	test.WaitForKubeControllerManagerClusterOperator(t, ctx, configClient, configv1.ConditionTrue, configv1.ConditionFalse, configv1.ConditionFalse)

	klog.Infof("About to delete service-account-private-key secret")
	err = kubeClient.Secrets(operatorclient.TargetNamespace).Delete(ctx, "service-account-private-key", metav1.DeleteOptions{})
	require.NoError(t, err)

	// wait for the operator reporting degraded
	// TODO(jchaloup): analyse the original root cause and extend this test to provide more informed testing
	// klog.Infof("Waiting for true, true, false")
	// test.WaitForKubeControllerManagerClusterOperator(t, ctx, configClient, configv1.ConditionTrue, configv1.ConditionTrue, configv1.ConditionFalse)

	// and check for secret being synced from next-service-private-key
	err = wait.Poll(test.WaitPollInterval, test.WaitPollTimeout, func() (bool, error) {
		klog.Infof("Getting service-account-private-key secret")
		_, err := kubeClient.Secrets(operatorclient.TargetNamespace).Get(ctx, "service-account-private-key", metav1.GetOptions{})
		if err == nil {
			klog.Infof("Found service-account-private-key secret")
			return true, nil
		} else if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	})

	require.NoError(t, err)

	klog.Infof("Waiting for true, false, false")
	test.WaitForKubeControllerManagerClusterOperator(t, ctx, configClient, configv1.ConditionTrue, configv1.ConditionFalse, configv1.ConditionFalse)
}
