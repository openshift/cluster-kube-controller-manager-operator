package operator

import (
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/apis/kubecontrollermanager/v1alpha1"
	operatorconfigclient "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/v311_00_assets"

	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
)

const (
	targetNamespaceName            = "openshift-kube-controller-manager"
	serviceCertSignerNamespaceName = "openshift-service-cert-signer"
	workQueueKey                   = "key"
)

func RunOperator(clientConfig *rest.Config, stopCh <-chan struct{}) error {
	kubeClient, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		return err
	}
	operatorConfigClient, err := operatorconfigclient.NewForConfig(clientConfig)
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(clientConfig)
	if err != nil {
		return err
	}
	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kubeInformersClusterScoped := informers.NewSharedInformerFactory(kubeClient, 10*time.Minute)
	kubeInformersForOpenShiftKubeControllerManagerNamespace := informers.NewFilteredSharedInformerFactory(kubeClient, 10*time.Minute, targetNamespaceName, nil)
	kubeInformersForOpenshiftServiceCertSignerNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(serviceCertSignerNamespaceName))
	staticPodOperatorClient := &staticPodOperatorClient{
		informers: operatorConfigInformers,
		client:    operatorConfigClient.Kubecontrollermanager(),
	}

	v1alpha1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/operator-config.yaml"),
		schema.GroupVersionResource{Group: v1alpha1.GroupName, Version: "v1alpha1", Resource: "kubecontrollermanageroperatorconfigs"},
		v1alpha1helpers.GetImageEnv,
	)

	configObserver := NewConfigObserver(
		operatorConfigInformers.Kubecontrollermanager().V1alpha1().KubeControllerManagerOperatorConfigs(),
		kubeInformersForOpenShiftKubeControllerManagerNamespace,
		operatorConfigClient.KubecontrollermanagerV1alpha1(),
		kubeClient,
		clientConfig,
	)
	targetConfigReconciler := NewTargetConfigReconciler(
		operatorConfigInformers.Kubecontrollermanager().V1alpha1().KubeControllerManagerOperatorConfigs(),
		kubeInformersForOpenShiftKubeControllerManagerNamespace,
		operatorConfigClient.KubecontrollermanagerV1alpha1(),
		kubeClient,
	)

	/*
		deploymentController := staticpodcontroller.NewDeploymentController(
			targetNamespaceName,
			deploymentConfigMaps,
			deploymentSecrets,
			kubeInformersForOpenShiftKubeControllerManagerNamespace,
			staticPodOperatorClient,
			kubeClient,
		)
		installerController := staticpodcontroller.NewInstallerController(
			targetNamespaceName,
			deploymentConfigMaps,
			deploymentSecrets,
			[]string{"cluster-kube-controller-manager-operator", "installer"},
			kubeInformersForOpenShiftKubeControllerManagerNamespace,
			staticPodOperatorClient,
			kubeClient,
		)
		nodeController := staticpodcontroller.NewNodeController(
			staticPodOperatorClient,
			kubeInformersClusterScoped,
		)
	*/
	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"openshift-kube-controller-manager",
		"openshift-kube-controller-manager",
		dynamicClient,
		staticPodOperatorClient,
	)

	operatorConfigInformers.Start(stopCh)
	kubeInformersClusterScoped.Start(stopCh)
	kubeInformersForOpenShiftKubeControllerManagerNamespace.Start(stopCh)
	kubeInformersForOpenshiftServiceCertSignerNamespace.Start(stopCh)

	go targetConfigReconciler.Run(1, stopCh)
	go configObserver.Run(1, stopCh)
	go clusterOperatorStatus.Run(1, stopCh)

	<-stopCh
	return fmt.Errorf("stopped")
}

// deploymentConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var deploymentConfigMaps = []string{
	"kube-controller-manager-pod",
	"deployment-kube-controller-manager-config",
	"client-ca",
}

// deploymentSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var deploymentSecrets = []string{
	"cluster-signing-ca",
	"controller-manager-kubeconfig",
	"service-account-private-key",
	"serving-cert",
}
