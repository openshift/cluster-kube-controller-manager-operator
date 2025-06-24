package recoverycontroller

import (
	"context"
	"fmt"
	"time"

	operatorv1 "github.com/openshift/api/operator/v1"
	configeversionedclient "github.com/openshift/client-go/config/clientset/versioned"
	configexternalinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/status"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

type Options struct {
	controllerContext *controllercmd.ControllerContext
}

func NewCertRecoveryControllerCommand(ctx context.Context) *cobra.Command {
	o := &Options{}
	c := clock.RealClock{}

	ccc := controllercmd.NewControllerCommandConfig("cert-recovery-controller", version.Get(), func(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
		o.controllerContext = controllerContext

		err := o.Validate(ctx)
		if err != nil {
			return err
		}

		err = o.Complete(ctx)
		if err != nil {
			return err
		}

		err = o.Run(ctx, c)
		if err != nil {
			return err
		}

		return nil
	}, c)

	// Disable serving for recovery as it introduces a dependency on kube-system::extension-apiserver-authentication
	// configmap which prevents it to start as the CA bundle is expired.
	// TODO: Remove when the internal logic can start serving without extension-apiserver-authentication
	//  	 and live reload extension-apiserver-authentication after it is available
	ccc.DisableServing = true

	cmd := ccc.NewCommandWithContext(ctx)
	cmd.Use = "cert-recovery-controller"
	cmd.Short = "Start the Cluster Certificate Recovery Controller"

	return cmd
}

func (o *Options) Validate(ctx context.Context) error {
	return nil
}

func (o *Options) Complete(ctx context.Context) error {
	return nil
}

func (o *Options) Run(ctx context.Context, clock clock.Clock) error {
	kubeClient, err := kubernetes.NewForConfig(o.controllerContext.ProtoKubeConfig)
	if err != nil {
		return fmt.Errorf("can't build kubernetes client: %w", err)
	}

	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		kubeClient,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
	)

	operatorClient, dynamicInformers, err := genericoperatorclient.NewStaticPodOperatorClient(
		clock,
		o.controllerContext.KubeConfig,
		operatorv1.GroupVersion.WithResource("kubecontrollermanagers"),
		operatorv1.GroupVersion.WithKind("KubeControllerManager"),
		operator.ExtractStaticPodOperatorSpec,
		operator.ExtractStaticPodOperatorStatus,
	)
	if err != nil {
		return err
	}

	configClient, err := configeversionedclient.NewForConfig(o.controllerContext.KubeConfig)
	if err != nil {
		return fmt.Errorf("failed to create config client: %w", err)
	}

	configInformers := configexternalinformers.NewSharedInformerFactory(configClient, 10*time.Minute)
	desiredVersion := status.VersionForOperatorFromEnv()
	missingVersion := "0.0.1-snapshot"
	featureGateAccessor := featuregates.NewFeatureGateAccess(
		desiredVersion, missingVersion,
		configInformers.Config().V1().ClusterVersions(), configInformers.Config().V1().FeatureGates(),
		o.controllerContext.EventRecorder,
	)

	go configInformers.Start(ctx.Done())
	go featureGateAccessor.Run(ctx)

	select {
	case <-featureGateAccessor.InitialFeatureGatesObserved():
		featureGates, _ := featureGateAccessor.CurrentFeatureGates()
		klog.Infof("FeatureGates initialized: knownFeatureGates=%v", featureGates.KnownFeatures())
	case <-time.After(1 * time.Minute):
		klog.Errorf("timed out waiting for FeatureGate detection")
		return fmt.Errorf("timed out waiting for FeatureGate detection")
	}

	certRotationController, err := certrotationcontroller.NewCertRotationControllerOnlyWhenExpired(
		v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		operatorClient,
		kubeInformersForNamespaces,
		o.controllerContext.EventRecorder,
		featureGateAccessor,
	)
	if err != nil {
		return err
	}

	csrController, err := NewCSRController(
		kubeClient,
		kubeInformersForNamespaces,
		operatorClient,
		o.controllerContext.EventRecorder,
	)
	if err != nil {
		return err
	}

	// We can't start informers until after the resources have been requested. Now is the time.
	kubeInformersForNamespaces.Start(ctx.Done())
	dynamicInformers.Start(ctx.Done())
	configInformers.Start(ctx.Done())

	// FIXME: These are missing a wait group to track goroutines and handle graceful termination
	// (@deads2k wants time to think it through)
	go func() {
		certRotationController.Run(ctx, 1)
	}()

	go func() {
		csrController.Run(ctx)
	}()

	<-ctx.Done()

	return nil
}
