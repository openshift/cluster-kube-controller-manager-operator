package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	testlib "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
	"github.com/openshift/library-go/test/library/metrics"
	"github.com/prometheus/common/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	"k8s.io/klog"
)

func TestOperatorNamespaceExists(t *testing.T) {
	_, err := testlib.KubeClient().CoreV1().Namespaces().Get(context.Background(), operatorclient.OperatorNamespace, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPodDisruptionBudgetAtLimitAlert tests that pdb-atlimit alert exists when there is a pdb at limit
// See https://bugzilla.redhat.com/show_bug.cgi?id=1762888
func TestPodDisruptionBudgetAtLimitAlert(t *testing.T) {
	ctx := context.Background()

	namespace, err := testlib.CreateTestNamespace(ctx)
	if err != nil {
		t.Fatal(err)
	}

	podLabels := map[string]string{"app": "pdbtest"}

	// ReplicaSet is resilient to infra hickups even though we need just one pod
	replicas := int32(1)
	rs, err := testlib.KubeClient().AppsV1().ReplicaSets(namespace.Name).Create(context.Background(), &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-1",
			Labels: podLabels,
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: podLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: podLabels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test",
							Image: "centos:7",
							Command: []string{
								"/bin/sleep",
								"infinity",
							},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	minAvailable := intstr.FromInt(1)
	pdb, err := testlib.KubeClient().PolicyV1beta1().PodDisruptionBudgets(namespace.Name).Create(
		ctx,
		&policyv1beta1.PodDisruptionBudget{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: policyv1beta1.PodDisruptionBudgetSpec{
				MinAvailable: &minAvailable,
				Selector: &metav1.LabelSelector{
					MatchLabels: podLabels,
				},
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the RS to be available so PDB is directly at its limit
	fieldSelector := fields.OneTermEqualSelector("metadata.name", rs.Name).String()
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.FieldSelector = fieldSelector
			return testlib.KubeClient().AppsV1().ReplicaSets(rs.Namespace).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.FieldSelector = fieldSelector
			return testlib.KubeClient().AppsV1().ReplicaSets(rs.Namespace).Watch(ctx, options)
		},
	}
	rsWaitCtx, rsWaitCtxCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer rsWaitCtxCancel()
	_, err = watchtools.UntilWithSync(rsWaitCtx, lw, &appsv1.ReplicaSet{}, nil, func(event watch.Event) (bool, error) {
		switch event.Type {
		case watch.Added, watch.Modified:
			return true, nil
		case watch.Error:
			return true, apierrors.FromObject(event.Object)
		default:
			return false, nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	// Now check for alert
	prometheusClient, err := metrics.NewPrometheusClient(ctx, testlib.KubeClient(), testlib.RouteClient())
	if err != nil {
		t.Fatalf("error creating route client for prometheus: %v", err)
	}

	var response model.Value
	// Note: prometheus/client_golang Alerts method only works with the deprecated prometheus-k8s route.
	// Our helper uses the thanos-querier route.  Because of this, have to pass the entire alert as a query.
	// The thanos behavior is to error on partial response.
	query := fmt.Sprintf("ALERTS{alertname=\"PodDisruptionBudgetAtLimit\",alertstate=\"pending\",namespace=\"%s\",poddisruptionbudget=\"%s\",prometheus=\"openshift-monitoring/k8s\",severity=\"warning\"}==1", namespace.Name, pdb.Name)
	err = wait.PollImmediate(5*time.Second, 10*time.Minute, func() (bool, error) {
		response, _, err = prometheusClient.Query(context.Background(), query, time.Now())
		if err != nil {
			klog.Infof("error querying prometheus: %v", err)
			return false, nil
		}

		if len(response.String()) == 0 {
			klog.V(2).Info("Prometheus isn't firing the alert yet.")
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		t.Fatalf("checking prometheus alert failed: %v", err)
	}
}

// TestTargetConfigController deletes everything managed by the target config controller and expects it to be recreated
func TestTargetConfigController(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		namespace    string
	}{
		{
			name:         "targetconfigcontroller KCM config",
			resourceName: "config",
			namespace:    operatorclient.TargetNamespace,
		},
		{
			name:         "targetconfigcontroller cluster policy controller config",
			resourceName: "cluster-policy-controller-config",
			namespace:    operatorclient.TargetNamespace,
		},
		{
			name:         "targetconfigcontroller csr-signer-ca",
			resourceName: "csr-signer-ca",
			namespace:    operatorclient.OperatorNamespace,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := testConfigMapDeletion(testlib.KubeClient(), test.namespace, test.resourceName)
			if err != nil {
				t.Errorf("error waiting for creation of %s: %+v", test.resourceName, err)
			}
		})
	}
}

// TestResourceSyncController deletes everything managed by the resource sync controller and expects it to be recreated
func TestResourceSyncController(t *testing.T) {
	tests := []struct {
		name         string
		resourceName string
		namespace    string
	}{
		{
			name:         "resourcesynccontroller csr-controller-ca",
			resourceName: "csr-controller-ca",
			namespace:    operatorclient.OperatorNamespace,
		},
		{
			name:         "resourcesynccontroller service-ca",
			resourceName: "service-ca",
			namespace:    operatorclient.TargetNamespace,
		},
		{
			name:         "resourcesynccontroller client-ca",
			resourceName: "client-ca",
			namespace:    operatorclient.TargetNamespace,
		},
		{
			name:         "resourcesynccontroller aggregator-client-ca",
			resourceName: "aggregator-client-ca",
			namespace:    operatorclient.TargetNamespace,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := testConfigMapDeletion(testlib.KubeClient(), test.namespace, test.resourceName)
			if err != nil {
				t.Errorf("error waiting for creation of %s: %+v", test.resourceName, err)
			}
		})
	}
}

func testConfigMapDeletion(kubeClient *kubernetes.Clientset, namespace, config string) error {
	ctx := context.Background()

	err := kubeClient.CoreV1().ConfigMaps(namespace).Delete(ctx, config, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	err = wait.Poll(time.Second*5, time.Second*120, func() (bool, error) {
		_, err := kubeClient.CoreV1().ConfigMaps(namespace).Get(ctx, config, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	})

	return err
}

// TestKCMRecovery is an e2e test to verify that KCM can recover from having its lease configmap deleted
// See https://bugzilla.redhat.com/show_bug.cgi?id=1744984
func TestKCMRecovery(t *testing.T) {
	ctx := context.Background()

	// Try to delete the kube controller manager's configmap in kube-system
	err := testlib.KubeClient().CoreV1().ConfigMaps("kube-system").Delete(ctx, "kube-controller-manager", metav1.DeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Check to see that the configmap then gets recreated
	err = wait.Poll(time.Second*5, time.Second*300, func() (bool, error) {
		_, err := testlib.KubeClient().CoreV1().ConfigMaps("kube-system").Get(ctx, "kube-controller-manager", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
