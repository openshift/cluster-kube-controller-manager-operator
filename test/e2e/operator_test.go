package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/kubernetes"
	policyclientv1beta1 "k8s.io/client-go/kubernetes/typed/policy/v1beta1"

	routeclient "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
	"github.com/openshift/library-go/test/library/metrics"
	"github.com/prometheus/common/model"
)

func TestOperatorNamespace(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	if err != nil {
		t.Fatal(err)
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}
	_, err = kubeClient.CoreV1().Namespaces().Get(operatorclient.OperatorNamespace, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

// TestPodDisruptionBudgetAtLimitAlert tests that pdb-atlimit alert exists when there is a pdb at limit
// See https://bugzilla.redhat.com/show_bug.cgi?id=1762888
func TestPodDisruptionBudgetAtLimitAlert(t *testing.T) {
	kubeConfig, err := test.NewClientConfigForTest()
	if err != nil {
		t.Fatal(err)
	}

	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}

	routeClient, err := routeclient.NewForConfig(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}

	policyClient, err := policyclientv1beta1.NewForConfig(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}

	name := names.SimpleNameGenerator.GenerateName("pdbtest-")
	_, err = kubeClient.CoreV1().Namespaces().Create(&corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	})
	if err != nil {
		t.Fatalf("could not create test namespace: %v", err)
	}
	defer kubeClient.CoreV1().Namespaces().Delete(name, &metav1.DeleteOptions{})

	labels := map[string]string{"app": "pdbtest"}
	err = pdbCreate(policyClient, name, labels)
	if err != nil {
		t.Fatal(err)
	}

	testTimeout := time.Second * 120

	err = wait.PollImmediate(time.Second*1, testTimeout, func() (bool, error) {
		if _, err := policyClient.PodDisruptionBudgets(name).List(metav1.ListOptions{LabelSelector: "app=pbtest"}); err != nil {
			return false, fmt.Errorf("waiting for poddisruptionbudget: %w", err)
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = podCreate(kubeClient, name, labels)
	if err != nil {
		t.Fatal(err)
	}
	var pods *corev1.PodList
	// Poll to confirm pod is running
	wait.PollImmediate(time.Second*1, testTimeout, func() (bool, error) {
		pods, err = kubeClient.CoreV1().Pods(name).List(metav1.ListOptions{LabelSelector: "app=pdbtest"})
		if err != nil {
			return false, err
		}
		if len(pods.Items) > 0 && pods.Items[0].Status.Phase == corev1.PodRunning {
			return true, nil
		}
		return false, nil
	})

	// Now check for alert
	prometheusClient, err := metrics.NewPrometheusClient(kubeClient, routeClient)
	if err != nil {
		t.Fatalf("error creating route client for prometheus: %v", err)
	}
	var response model.Value
	// Note: prometheus/client_golang Alerts method only works with the deprecated prometheus-k8s route.
	// Our helper uses the thanos-querier route.  Because of this, have to pass the entire alert as a query.
	// The thanos behavior is to error on partial response.
	query := fmt.Sprintf("ALERTS{alertname=\"PodDisruptionBudgetAtLimit\",alertstate=\"pending\",namespace=\"%s\",poddisruptionbudget=\"%s\",prometheus=\"openshift-monitoring/k8s\",service=\"kube-state-metrics\",severity=\"warning\"}==1", name, name)
	err = wait.PollImmediate(time.Second*3, testTimeout, func() (bool, error) {
		response, _, err = prometheusClient.Query(context.Background(), query, time.Now())
		if err != nil {
			return false, fmt.Errorf("error querying prometheus: %v", err)
		}
		if len(response.String()) == 0 {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("error querying prometheus: %v", err)
	}
}

func TestTargetConfigController(t *testing.T) {
	// This test deletes everything managed by the target config controller and expects it to be recreated
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

	kubeConfig, err := test.NewClientConfigForTest()
	if err != nil {
		t.Fatal(err)
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := testConfigMapDeletion(kubeClient, test.namespace, test.resourceName)
			if err != nil {
				t.Errorf("error waiting for creation of %s: %+v", test.resourceName, err)
			}
		})
	}
}

func TestResourceSyncController(t *testing.T) {
	// This test deletes everything managed by the resource sync controller and expects it to be recreated
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

	kubeConfig, err := test.NewClientConfigForTest()
	if err != nil {
		t.Fatal(err)
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err = testConfigMapDeletion(kubeClient, test.namespace, test.resourceName)
			if err != nil {
				t.Errorf("error waiting for creation of %s: %+v", test.resourceName, err)
			}
		})
	}
}

func testConfigMapDeletion(kubeClient *kubernetes.Clientset, namespace, config string) error {
	err := kubeClient.CoreV1().ConfigMaps(namespace).Delete(config, &metav1.DeleteOptions{})
	if err != nil {
		return err
	}
	err = wait.Poll(time.Second*5, time.Second*120, func() (bool, error) {
		_, err := kubeClient.CoreV1().ConfigMaps(namespace).Get(config, metav1.GetOptions{})
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

func TestKCMRecovery(t *testing.T) {
	// This is an e2e test to verify that KCM can recover from having its lease configmap deleted
	// See https://bugzilla.redhat.com/show_bug.cgi?id=1744984
	kubeConfig, err := test.NewClientConfigForTest()
	if err != nil {
		t.Fatal(err)
	}
	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		t.Fatal(err)
	}

	// Try to delete the kube controller manager's configmap in kube-system
	err = kubeClient.CoreV1().ConfigMaps("kube-system").Delete("kube-controller-manager", &metav1.DeleteOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// Check to see that the configmap then gets recreated
	err = wait.Poll(time.Second*5, time.Second*300, func() (bool, error) {
		_, err := kubeClient.CoreV1().ConfigMaps("kube-system").Get("kube-controller-manager", metav1.GetOptions{})
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

func pdbCreate(client *policyclientv1beta1.PolicyV1beta1Client, name string, labels map[string]string) error {
	minAvailable := intstr.FromInt(1)
	pdb := &policyv1beta1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: policyv1beta1.PodDisruptionBudgetSpec{
			MinAvailable: &minAvailable,
			Selector:     &metav1.LabelSelector{MatchLabels: labels},
		},
	}
	_, err := client.PodDisruptionBudgets(name).Create(pdb)
	return err
}

func podCreate(client *kubernetes.Clientset, name string, labels map[string]string) error {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: name,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test",
					Image: "centos:7",
					Command: []string{
						"sh",
						"-c",
						"trap exit TERM; while true; do sleep 5; done",
					},
				},
			},
		},
	}
	_, err := client.CoreV1().Pods(name).Create(pod)
	return err
}
