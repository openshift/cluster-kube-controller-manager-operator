package e2e

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	testlib "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
)

func TestSATokenSignerControllerSyncCerts(t *testing.T) {
	ctx := context.Background()

	// Make sure the initial state is stable
	klog.Info("Waiting for stabilized operator (true, false, false)")
	_, err := testlib.WaitForKubeControllerManagerClusterOperator(
		ctx,
		testlib.ConfigClient().ConfigV1(),
		configv1.ConditionTrue,
		configv1.ConditionFalse,
		configv1.ConditionFalse,
	)
	if err != nil {
		t.Fatal(err)
	}
	klog.Info("Operator stabilized (true, false, false)")

	// We need to retrieve any RV before we trigger the update to be sure
	// we don't miss watching for the progressing operator.
	// Because we don't know the last time the object was updated, its RV
	// may no longer be available for WATCH so we need to get the latest RV for
	// the resource.
	list, err := testlib.ConfigClient().ConfigV1().ClusterOperators().List(ctx, metav1.ListOptions{
		FieldSelector: testlib.ClusterOperatorFieldSelector.String(),
	})
	if err != nil {
		t.Fatal(err)
	}

	propagationPolicy := metav1.DeletePropagationBackground
	err = testlib.KubeClient().CoreV1().Secrets(operatorclient.TargetNamespace).Delete(ctx, "service-account-private-key", metav1.DeleteOptions{
		PropagationPolicy: &propagationPolicy, // Make sure we don't loose RV for slow deletion
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the operator reporting progressing.
	// We have to start from the snapshotted RV or this would be racy.
	klog.Info("Waiting for progressing operator (true, true, false)")
	_, err = testlib.WaitForKubeControllerManagerClusterOperatorFromRV(
		ctx,
		testlib.ConfigClient().ConfigV1(),
		list.ResourceVersion,
		configv1.ConditionTrue,
		configv1.ConditionTrue,
		configv1.ConditionFalse,
	)
	if err != nil {
		t.Fatal(err)
	}
	klog.Info("Operator progressing (true, true, false)")

	// Check the secret is synced from next-service-private-key
	_, err = testlib.KubeClient().CoreV1().Secrets(operatorclient.TargetNamespace).Get(ctx, "service-account-private-key", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the operator reporting progressing.
	klog.Info("Waiting for stabilized operator (true, false, false)")
	_, err = testlib.WaitForKubeControllerManagerClusterOperator(
		ctx,
		testlib.ConfigClient().ConfigV1(),
		configv1.ConditionTrue,
		configv1.ConditionFalse,
		configv1.ConditionFalse,
	)
	if err != nil {
		t.Fatal(err)
	}
	klog.Info("Operator stabilized (true, false, false)")
}
