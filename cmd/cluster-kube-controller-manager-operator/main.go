package main

import (
	"context"
	goflag "flag"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	utilflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/logs"

	"github.com/openshift/library-go/pkg/operator/staticpod/certsyncpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/installerpod"
	"github.com/openshift/library-go/pkg/operator/staticpod/prune"

	operatorcmd "github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/operator"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/recoverycontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/render"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/cmd/resourcegraph"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator"
)

func main() {
	rand.Seed(time.Now().UTC().UnixNano())

	pflag.CommandLine.SetNormalizeFunc(utilflag.WordSepNormalizeFunc)
	pflag.CommandLine.AddGoFlagSet(goflag.CommandLine)

	logs.InitLogs()
	defer logs.FlushLogs()

	command := NewSSCSCommand(context.Background())
	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
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

	cmd.AddCommand(operatorcmd.NewOperator())
	cmd.AddCommand(render.NewRenderCommand(os.Stderr))
	cmd.AddCommand(installerpod.NewInstaller())
	cmd.AddCommand(prune.NewPrune())
	cmd.AddCommand(resourcegraph.NewResourceChainCommand())
	cmd.AddCommand(certsyncpod.NewCertSyncControllerCommand(operator.CertConfigMaps, operator.CertSecrets))
	cmd.AddCommand(recoverycontroller.NewCertRecoveryControllerCommand(ctx))

	return cmd
}
