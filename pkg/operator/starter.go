package operator

import (
	"fmt"
	"os"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"

	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/apis/kubecontrollermanager/v1alpha1"
	operatorconfigclient "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/clientset/versioned"
	operatorclientinformers "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/v311_00_assets"
)

const (
	targetNamespaceName            = "openshift-kube-controller-manager"
	serviceCertSignerNamespaceName = "openshift-service-cert-signer"
	workQueueKey                   = "key"
)

func RunOperator(ctx *controllercmd.ControllerContext) error {

	kubeClient, err := kubernetes.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	operatorConfigClient, err := operatorconfigclient.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	dynamicClient, err := dynamic.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}
	configClient, err := configv1client.NewForConfig(ctx.KubeConfig)
	if err != nil {
		return err
	}

	operatorConfigInformers := operatorclientinformers.NewSharedInformerFactory(operatorConfigClient, 10*time.Minute)
	kubeInformersClusterScoped := informers.NewSharedInformerFactory(kubeClient, 10*time.Minute)
	kubeInformersForOpenShiftKubeControllerManagerNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(targetNamespaceName))
	kubeInformersForOpenshiftServiceCertSignerNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace(serviceCertSignerNamespaceName))
	kubeInformersForKubeSystemNamespace := informers.NewSharedInformerFactoryWithOptions(kubeClient, 10*time.Minute, informers.WithNamespace("kube-system"))
	staticPodOperatorClient := &staticPodOperatorClient{
		informers: operatorConfigInformers,
		client:    operatorConfigClient.KubecontrollermanagerV1alpha1(),
	}

	v1alpha1helpers.EnsureOperatorConfigExists(
		dynamicClient,
		v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/operator-config.yaml"),
		schema.GroupVersionResource{Group: v1alpha1.GroupName, Version: "v1alpha1", Resource: "kubecontrollermanageroperatorconfigs"},
		v1alpha1helpers.GetImageEnv,
	)

	configObserver := configobservercontroller.NewConfigObserver(
		staticPodOperatorClient,
		operatorConfigInformers,
		kubeInformersForKubeSystemNamespace,
		ctx.EventRecorder,
	)
	targetConfigReconciler := NewTargetConfigReconciler(
		os.Getenv("IMAGE"),
		operatorConfigInformers.Kubecontrollermanager().V1alpha1().KubeControllerManagerOperatorConfigs(),
		kubeInformersForOpenShiftKubeControllerManagerNamespace,
		operatorConfigClient.KubecontrollermanagerV1alpha1(),
		kubeClient,
		ctx.EventRecorder,
	)

	staticPodControllers := staticpod.NewControllers(
		targetNamespaceName,
		"openshift-kube-controller-manager",
		[]string{"cluster-kube-controller-manager-operator", "installer"},
		deploymentConfigMaps,
		deploymentSecrets,
		staticPodOperatorClient,
		kubeClient,
		kubeInformersForOpenShiftKubeControllerManagerNamespace,
		kubeInformersClusterScoped,
		ctx.EventRecorder,
	)

	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"openshift-kube-controller-manager-operator",
		configClient.ConfigV1(),
		staticPodOperatorClient,
		ctx.EventRecorder,
	)

	operatorConfigInformers.Start(ctx.StopCh)
	kubeInformersClusterScoped.Start(ctx.StopCh)
	kubeInformersForOpenShiftKubeControllerManagerNamespace.Start(ctx.StopCh)
	kubeInformersForOpenshiftServiceCertSignerNamespace.Start(ctx.StopCh)
	kubeInformersForKubeSystemNamespace.Start(ctx.StopCh)

	go staticPodControllers.Run(ctx.StopCh)
	go targetConfigReconciler.Run(1, ctx.StopCh)
	go configObserver.Run(1, ctx.StopCh)
	go clusterOperatorStatus.Run(1, ctx.StopCh)

	<-ctx.StopCh
	return fmt.Errorf("stopped")
}

// deploymentConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var deploymentConfigMaps = []string{
	"kube-controller-manager-pod",
	"config",
	"client-ca",
}

// deploymentSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var deploymentSecrets = []string{
	"cluster-signing-ca",
	"controller-manager-kubeconfig",
	"service-account-private-key",
	"serving-cert",
}
