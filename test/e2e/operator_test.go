package e2e

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	test "github.com/openshift/cluster-kube-controller-manager-operator/test/library"
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
