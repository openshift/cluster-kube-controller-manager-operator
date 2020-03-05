package recoverycontroller

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/pkg/version"

	"github.com/openshift/library-go/pkg/controller/controllercmd"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

type Options struct {
	controllerContext *controllercmd.ControllerContext
}

func NewCertRecoveryControllerCommand(ctx context.Context) *cobra.Command {
	o := &Options{}

	cmd := controllercmd.
		NewControllerCommandConfig("cert-recovery-controller", version.Get(), func(ctx context.Context, controllerContext *controllercmd.ControllerContext) error {
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
		}).NewCommandWithContext(ctx)
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

	csrController, err := NewCSRController(
		kubeClient,
		kubeInformersForNamespaces,
		o.controllerContext.EventRecorder,
	)
	if err != nil {
		return err
	}

	kubeInformersForNamespaces.Start(ctx.Done())

	// FIXME: These are missing a wait group to track goroutines and handle graceful termination
	// (@deads2k wants time to think it through)

	go func() {
		csrController.Run(ctx)
	}()

	<-ctx.Done()

	return nil
}
