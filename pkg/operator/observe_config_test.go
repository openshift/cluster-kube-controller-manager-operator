package operator

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestObserveClusterConfig(t *testing.T) {
	kubeClient := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"install-config": "platform:\n  aws: {}\n",
		},
	})
	result, err := observeClusterConfig(kubeClient, &rest.Config{}, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	cloudProvider, _, err := unstructured.NestedSlice(result, "extendedArguments", "cloud-provider")
	if err != nil {
		t.Fatal(err)
	}
	if e, a := 1, len(cloudProvider); e != a {
		t.Fatalf("expected len(cloudProvider) == %d, got %d", e, a)
	}
	if e, a := "aws", cloudProvider[0]; e != a {
		t.Errorf("expected cloud-provider=%s, got %s", e, a)
	}
}
