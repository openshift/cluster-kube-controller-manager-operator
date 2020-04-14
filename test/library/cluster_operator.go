package library

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	clusteroperatorhelpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
)

var (
	WaitPollInterval = time.Second
	WaitPollTimeout  = 15 * time.Minute
)

// WaitForKubeControllerManagerClusterOperator waits for ClusterOperator/kube-controller-manager to report
// status as available, progressing, and failing as passed through arguments.
func WaitForKubeControllerManagerClusterOperator(t *testing.T, ctx context.Context, client configclient.ConfigV1Interface, available, progressing, degraded configv1.ConditionStatus) {
	err := wait.Poll(WaitPollInterval, WaitPollTimeout, func() (bool, error) {
		clusterOperator, err := client.ClusterOperators().Get(ctx, "kube-controller-manager", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			fmt.Println("ClusterOperator/kube-controller-manager does not yet exist.")
			return false, nil
		}
		if err != nil {
			fmt.Println("Unable to retrieve ClusterOperator/kube-controller-manager:", err)
			return false, err
		}
		conditions := clusterOperator.Status.Conditions
		availableOK := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorAvailable, available)
		progressingOK := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorProgressing, progressing)
		degradedOK := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorDegraded, degraded)
		done := availableOK && progressingOK && degradedOK
		fmt.Printf("ClusterOperator/kube-controller-manager: AvailableOK: %v  ProgressingOK: %v  DegradedOK: %v\n", availableOK, progressingOK, degradedOK)
		return done, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
