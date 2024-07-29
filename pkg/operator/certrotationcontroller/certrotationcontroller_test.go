package certrotationcontroller

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

func TestNewCertRotationControllerHasUniqueSigners(t *testing.T) {
	ctx := context.Background()
	kubeClient := fake.NewSimpleClientset()

	operatorClient := v1helpers.NewFakeStaticPodOperatorClient(&operatorv1.StaticPodOperatorSpec{OperatorSpec: operatorv1.OperatorSpec{ManagementState: operatorv1.Managed}}, &operatorv1.StaticPodOperatorStatus{}, nil, nil)
	certRotationScale, err := certrotation.GetCertRotationScale(ctx, kubeClient, operatorclient.GlobalUserSpecifiedConfigNamespace)
	require.NoError(t, err)

	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
	)

	eventRecorder := events.NewInMemoryRecorder("")
	c, err := NewCertRotationController(
		v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		operatorClient,
		kubeInformersForNamespaces,
		eventRecorder,
		certRotationScale)
	require.NoError(t, err)

	require.NotEmpty(t, c.certRotators)

	sets := map[string][]metav1.ObjectMeta{
		"Secret":    c.controlledSecrets,
		"ConfigMap": c.controlledConfigMaps,
	}
	for objType, set := range sets {
		slice := make(map[string]bool)
		for _, objMeta := range set {
			objKey := fmt.Sprintf("%s/%s", objMeta.Name, objMeta.Namespace)
			if _, found := slice[objKey]; !found {
				slice[objKey] = true
			} else {
				t.Fatalf("%s %s is being managed by two controllers", objType, objKey)
			}
		}
	}
}
