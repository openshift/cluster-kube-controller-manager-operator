package resourcesynccontroller

import (
	"time"

	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

func AddSyncCSRControllerCA(resourceSyncController *resourcesynccontroller.ResourceSyncController) error {
	return resourceSyncController.SyncConfigMapConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "csr-controller-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "csr-controller-ca"},
		func() (bool, error) {
			time.Sleep(6 * time.Second)
			return true, nil
		},
	)
}

func AddSyncClientCertKeySecret(resourceSyncController *resourcesynccontroller.ResourceSyncController) error {
	return resourceSyncController.SyncSecretConditionally(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "kube-controller-manager-client-cert-key"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "kube-controller-manager-client-cert-key"},
		func() (bool, error) {
			time.Sleep(6 * time.Second)
			return true, nil
		},
	)
}

func NewResourceSyncController(
	operatorConfigClient v1helpers.OperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	secretsGetter corev1client.SecretsGetter,
	configMapsGetter corev1client.ConfigMapsGetter,
	eventRecorder events.Recorder) (*resourcesynccontroller.ResourceSyncController, error) {

	resourceSyncController := resourcesynccontroller.NewResourceSyncController(
		"kube-controller-manager",
		operatorConfigClient,
		kubeInformersForNamespaces,
		v1helpers.CachedSecretGetter(secretsGetter, kubeInformersForNamespaces),
		v1helpers.CachedConfigMapGetter(configMapsGetter, kubeInformersForNamespaces),
		eventRecorder,
		false,
	)
	if err := AddSyncCSRControllerCA(resourceSyncController); err != nil {
		return nil, err
	}
	if err := AddSyncClientCertKeySecret(resourceSyncController); err != nil {
		return nil, err
	}
	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "service-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "service-ca"},
	); err != nil {
		return nil, err
	}

	// kcm is re-using the generic-apiserver, so if we set the client-ca and front-proxy-ca manually, it won't try to load them
	// dynamically from the cluster and won't crash when API isn't available
	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "client-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "kube-apiserver-client-ca"},
	); err != nil {
		return nil, err
	}
	if err := resourceSyncController.SyncConfigMap(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "aggregator-client-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "kube-apiserver-aggregator-client-ca"},
	); err != nil {
		return nil, err
	}

	return resourceSyncController, nil
}
