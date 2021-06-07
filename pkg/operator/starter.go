package operator

import (
	"context"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/installer"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/resourcesynccontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/targetconfigcontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/v411_00_assets"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/staleconditions"
	"github.com/openshift/library-go/pkg/operator/staticpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/controller/revision"
	"github.com/openshift/library-go/pkg/operator/staticresourcecontroller"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func RunOperator(ctx context.Context, cc *controllercmd.ControllerContext) error {
	// This kube client use protobuf, do not use it for CR
	kubeClient, err := kubernetes.NewForConfig(cc.ProtoKubeConfig)
	if err != nil {
		return err
	}
	configClient, err := configv1client.NewForConfig(cc.KubeConfig)
	if err != nil {
		return err
	}

	configInformers := configinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(kubeClient,
		"",
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
		"kube-system",
	)
	operatorClient, dynamicInformers, err := genericoperatorclient.NewStaticPodOperatorClient(cc.KubeConfig, operatorv1.GroupVersion.WithResource("kubecontrollermanagers"))
	if err != nil {
		return err
	}

	resourceSyncController, err := resourcesynccontroller.NewResourceSyncController(
		operatorClient,
		kubeInformersForNamespaces,
		v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		cc.EventRecorder,
	)
	if err != nil {
		return err
	}
	configObserver := configobservercontroller.NewConfigObserver(
		operatorClient,
		configInformers,
		kubeInformersForNamespaces,
		resourceSyncController,
		cc.EventRecorder,
	)

	staticResourceController := staticresourcecontroller.NewStaticResourceController(
		"KubeControllerManagerStaticResources",
		v411_00_assets.Asset,
		[]string{
			"v4.1.0/kube-controller-manager/ns.yaml",
			"v4.1.0/kube-controller-manager/kubeconfig-cert-syncer.yaml",
			"v4.1.0/kube-controller-manager/leader-election-rolebinding.yaml",
			"v4.1.0/kube-controller-manager/leader-election-cluster-policy-controller-role.yaml",
			"v4.1.0/kube-controller-manager/leader-election-cluster-policy-controller-rolebinding.yaml",
			"v4.1.0/kube-controller-manager/leader-election-kube-controller-manager-role-kube-system.yaml",
			"v4.1.0/kube-controller-manager/leader-election-kube-controller-manager-rolebinding-kube-system.yaml",
			"v4.1.0/kube-controller-manager/namespace-security-allocation-controller-clusterrole.yaml",
			"v4.1.0/kube-controller-manager/namespace-security-allocation-controller-clusterrolebinding.yaml",
			"v4.1.0/kube-controller-manager/svc.yaml",
			"v4.1.0/kube-controller-manager/sa.yaml",
			"v4.1.0/kube-controller-manager/localhost-recovery-client-crb.yaml",
			"v4.1.0/kube-controller-manager/localhost-recovery-sa.yaml",
			"v4.1.0/kube-controller-manager/localhost-recovery-token.yaml",
			"v4.1.0/kube-controller-manager/gce/cloud-provider-role.yaml",
			"v4.1.0/kube-controller-manager/gce/cloud-provider-binding.yaml",
		},
		(&resourceapply.ClientHolder{}).WithKubernetes(kubeClient),
		operatorClient,
		cc.EventRecorder,
	).AddKubeInformers(kubeInformersForNamespaces)

	targetConfigController := targetconfigcontroller.NewTargetConfigController(
		ctx,
		os.Getenv("IMAGE"),
		os.Getenv("OPERATOR_IMAGE"),
		os.Getenv("CLUSTER_POLICY_CONTROLLER_IMAGE"),
		os.Getenv("TOOLS_IMAGE"),
		kubeInformersForNamespaces,
		operatorClient,
		kubeClient,
		configInformers.Config().V1().Infrastructures(),
		cc.EventRecorder,
	)

	// don't change any versions until we sync
	versionRecorder := status.NewVersionGetter()
	clusterOperator, err := configClient.ConfigV1().ClusterOperators().Get(ctx, "kube-controller-manager", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	for _, version := range clusterOperator.Status.Versions {
		versionRecorder.SetVersion(version.Name, version.Version)
	}
	versionRecorder.SetVersion("raw-internal", status.VersionForOperatorFromEnv())

	staticPodControllers, err := staticpod.NewBuilder(operatorClient, kubeClient, kubeInformersForNamespaces).
		WithEvents(cc.EventRecorder).
		WithInstaller([]string{"cluster-kube-controller-manager-operator", "installer"}).
		WithPruning([]string{"cluster-kube-controller-manager-operator", "prune"}, "kube-controller-manager-pod").
		WithRevisionedResources(operatorclient.TargetNamespace, "kube-controller-manager", deploymentConfigMaps, deploymentSecrets).
		WithUnrevisionedCerts("dynamic", CertConfigMaps, CertSecrets).
		WithVersioning("kube-controller-manager", versionRecorder).
		ToControllers()
	if err != nil {
		return err
	}

	clusterOperatorStatus := status.NewClusterOperatorStatusController(
		"kube-controller-manager",
		[]configv1.ObjectReference{
			{Group: "operator.openshift.io", Resource: "kubecontrollermanagers", Name: "cluster"},
			{Resource: "namespaces", Name: "openshift-config"},
			{Resource: "namespaces", Name: "openshift-config-managed"},
			{Resource: "namespaces", Name: operatorclient.TargetNamespace},
			{Resource: "namespaces", Name: "openshift-kube-controller-manager-operator"},
			// TODO move to a more appropriate operator. One that creates and approves these.
			{Group: "certificates.k8s.io", Resource: "certificatesigningrequests"},
			// TODO move to a more appropriate operator. One that creates and manages these.
			{Resource: "nodes"},
		},
		configClient.ConfigV1(),
		configInformers.Config().V1().ClusterOperators(),
		operatorClient,
		versionRecorder,
		cc.EventRecorder,
	)

	certRotationScale, err := certrotation.GetCertRotationScale(kubeClient, operatorclient.GlobalUserSpecifiedConfigNamespace)
	if err != nil {
		return err
	}

	certRotationController, err := certrotationcontroller.NewCertRotationController(
		v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		operatorClient,
		kubeInformersForNamespaces,
		cc.EventRecorder,
		// this is weird, but when we turn down rotation in CI, we go fast enough that kubelets and kas are racing to observe the new signer before the signer is used.
		// we need to establish some kind of delay or back pressure to prevent the rollout.  This ensures we don't trigger kas restart
		// during e2e tests for now.
		certRotationScale*8,
	)
	if err != nil {
		return err
	}
	saTokenController, err := certrotationcontroller.NewSATokenSignerController(ctx, operatorClient, kubeInformersForNamespaces, kubeClient, cc.EventRecorder)
	if err != nil {
		return err
	}

	staleConditions := staleconditions.NewRemoveStaleConditionsController(
		[]string{
			// the static pod operator used to directly set these. this removes those conditions since the static pod operator was updated.
			// these can be removed in 4.5
			"Available", "Progressing",
		},
		operatorClient,
		cc.EventRecorder,
	)

	configInformers.Start(ctx.Done())
	kubeInformersForNamespaces.Start(ctx.Done())
	dynamicInformers.Start(ctx.Done())

	go staticPodControllers.Start(ctx)
	go staticResourceController.Run(ctx, 1)
	go targetConfigController.Run(1, ctx.Done())
	go configObserver.Run(ctx, 1)
	go clusterOperatorStatus.Run(ctx, 1)
	go resourceSyncController.Run(ctx, 1)
	go certRotationController.Run(ctx, 1)
	go saTokenController.Run(1, ctx.Done())
	go staleConditions.Run(ctx, 1)

	<-ctx.Done()
	return nil
}

// deploymentConfigMaps is a list of configmaps that are directly copied for the current values.  A different actor/controller modifies these.
// the first element should be the configmap that contains the static pod manifest
var deploymentConfigMaps = []revision.RevisionResource{
	{Name: "kube-controller-manager-pod"},

	{Name: "config"},
	{Name: "cluster-policy-controller-config"},
	{Name: "controller-manager-kubeconfig"},
	{Name: "cloud-config", Optional: true},
	{Name: "kube-controller-cert-syncer-kubeconfig"},
	{Name: "serviceaccount-ca"},
	{Name: "service-ca"},
	{Name: "recycler-config"},
}

// deploymentSecrets is a list of secrets that are directly copied for the current values.  A different actor/controller modifies these.
var deploymentSecrets = []revision.RevisionResource{
	{Name: "service-account-private-key"},

	// this cert is created by the service-ca controller, which doesn't come up until after we are available. this piece of config must be optional.
	{Name: "serving-cert", Optional: true},

	// this needs to be revisioned as certsyncer's kubeconfig isn't wired to be live reloaded, nor will be autorecovery
	{Name: "localhost-recovery-client-token"},
}

var CertConfigMaps = []installer.UnrevisionedResource{
	{Name: "aggregator-client-ca"},
	{Name: "client-ca"},

	// this is a copy of trusted-ca-bundle CM but with key modified to "tls-ca-bundle.pem" so that we can mount it the way we need
	{Name: "trusted-ca-bundle", Optional: true},
}

var CertSecrets = []installer.UnrevisionedResource{
	{Name: "kube-controller-manager-client-cert-key"},
	{Name: "csr-signer"},
}
