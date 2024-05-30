package recoverycontroller

import (
	"context"
	"fmt"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/certrotationcontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/genericoperatorclient"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

type Options struct {
	controllerContext *controllercmd.ControllerContext
}

func NewCertRecoveryControllerCommand(ctx context.Context) *cobra.Command {
	o := &Options{}

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

		err = o.Run(ctx)
		if err != nil {
			return err
		}

		return nil
	})

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

func (o *Options) Run(ctx context.Context) error {
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

	operatorClient, dynamicInformers, err := genericoperatorclient.NewStaticPodOperatorClient(o.controllerContext.KubeConfig, operatorv1.GroupVersion.WithResource("kubecontrollermanagers"))
	if err != nil {
		return err
	}

	certRotationScale, err := certrotation.GetCertRotationScale(ctx, kubeClient, operatorclient.GlobalUserSpecifiedConfigNamespace)
	if err != nil {
		return err
	}

	certRotationController, err := certrotationcontroller.NewCertRotationController(
		v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		operatorClient,
		kubeInformersForNamespaces,
		o.controllerContext.EventRecorder,
		// this is weird, but when we turn down rotation in CI, we go fast enough that kubelets and kas are racing to observe the new signer before the signer is used.
		// we need to establish some kind of delay or back pressure to prevent the rollout.  This ensures we don't trigger kas restart
		// during e2e tests for now.
		certRotationScale*8,
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

	kubeInformersForNamespaces.Start(ctx.Done())
	dynamicInformers.Start(ctx.Done())

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
