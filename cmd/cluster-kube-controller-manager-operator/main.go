package main

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"k8s.io/cli-runtime/pkg/genericiooptions"
	"k8s.io/component-base/cli"

	"github.com/openshift/library-go/pkg/operator/staticpod/certsyncpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/installerpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/prune"

	operatorcmd "github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/operator"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/recoverycontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/render"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/resourcegraph"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/mom"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator"
)

func main() {
	command := NewSSCSCommand(context.Background())
	code := cli.Run(command)
	os.Exit(code)
}

func NewSSCSCommand(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster-kube-controller-manager-operator",
		Short: "OpenShift cluster kube-controller-manager operator",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
			os.Exit(1)
		},
	}

	ioStreams := genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}

	cmd.AddCommand(operatorcmd.NewOperator())
	cmd.AddCommand(render.NewRenderCommand(nil))
	cmd.AddCommand(installerpod.NewInstaller(ctx))
	cmd.AddCommand(prune.NewPrune())
	cmd.AddCommand(resourcegraph.NewResourceChainCommand())
	cmd.AddCommand(certsyncpod.NewCertSyncControllerCommand(operator.CertConfigMaps, operator.CertSecrets))
	cmd.AddCommand(recoverycontroller.NewCertRecoveryControllerCommand(ctx))
	cmd.AddCommand(mom.NewInputResourcesCommand(ioStreams))
	cmd.AddCommand(mom.NewOutputResourcesCommand(ioStreams))
	cmd.AddCommand(mom.NewApplyConfigurationCommand(ioStreams))

	return cmd
}
