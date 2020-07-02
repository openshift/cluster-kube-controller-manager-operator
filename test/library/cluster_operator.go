package library

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	configclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	clusteroperatorhelpers "github.com/openshift/library-go/pkg/config/clusteroperator/v1helpers"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/klog"
)

var (
	ClusterOperatorName          = "kube-controller-manager"
	ClusterOperatorFieldSelector = fields.OneTermEqualSelector("metadata.name", ClusterOperatorName)
)

func KubeControllerManagerCondFn(available, progressing, degraded configv1.ConditionStatus) watchtools.ConditionFunc {
	return func(event watch.Event) (bool, error) {
		switch event.Type {
		case watch.Added, watch.Modified:
			conditions := event.Object.(*configv1.ClusterOperator).Status.Conditions
			availableOK := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorAvailable, available)
			progressingOK := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorProgressing, progressing)
			degradedOK := clusteroperatorhelpers.IsStatusConditionPresentAndEqual(conditions, configv1.OperatorDegraded, degraded)
			klog.V(2).Infof("ClusterOperator/kube-controller-manager: AvailableOK: %v  ProgressingOK: %v  DegradedOK: %v", availableOK, progressingOK, degradedOK)
			return availableOK && progressingOK && degradedOK, nil

		case watch.Error:
			return true, apierrors.FromObject(event.Object)

		default:
			return false, nil
		}
	}
}

// WaitForKubeControllerManagerClusterOperator waits for ClusterOperator/kube-controller-manager to report
// status as available, progressing, and failing as passed through arguments.
func WaitForKubeControllerManagerClusterOperator(ctx context.Context, client configclient.ConfigV1Interface, available, progressing, degraded configv1.ConditionStatus) (*configv1.ClusterOperator, error) {
	fieldSelector := ClusterOperatorFieldSelector.String()
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return client.ClusterOperators().List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector
			return client.ClusterOperators().Watch(ctx, options)
		},
	}
	preconditionFunc := func(store cache.Store) (bool, error) {
		_, exists, err := store.GetByKey(ClusterOperatorName)
		if err != nil {
			return true, err
		}

		if !exists {
			return true, apierrors.NewNotFound(schema.GroupResource{Group: configv1.GroupName, Resource: "clusteroperator"}, ClusterOperatorName)
		}

		return false, nil
	}
	e, err := watchtools.UntilWithSync(ctx, lw, &configv1.ClusterOperator{}, preconditionFunc, KubeControllerManagerCondFn(available, progressing, degraded))
	if err != nil {
		return nil, err
	}
	return e.Object.(*configv1.ClusterOperator), nil
}

// WaitForKubeControllerManagerClusterOperatorFromRV waits for ClusterOperator/kube-controller-manager to report
// status as available, progressing, and failing as passed through arguments.
// It start watching the state from the resourceVersion passed.
func WaitForKubeControllerManagerClusterOperatorFromRV(ctx context.Context, client configclient.ConfigV1Interface, resourceVersion string, available, progressing, degraded configv1.ConditionStatus) (*configv1.ClusterOperator, error) {
	w := &cache.ListWatch{
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = ClusterOperatorFieldSelector.String()
			return client.ClusterOperators().Watch(ctx, options)
		},
	}
	e, err := watchtools.Until(ctx, resourceVersion, w, KubeControllerManagerCondFn(available, progressing, degraded))
	if err != nil {
		return nil, err
	}
	return e.Object.(*configv1.ClusterOperator), nil
}
