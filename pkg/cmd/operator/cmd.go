package operator

import (
	"github.com/spf13/cobra"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

func NewOperator() *cobra.Command {
	ctrlCmdCfg := controllercmd.NewControllerCommandConfig("kube-controller-manager-operator", version.Get(), operator.RunOperator)
	// enable HTTP2 explicitly
	ctrlCmdCfg.EnableHTTP2 = true
	cmd := ctrlCmdCfg.NewCommand()
	cmd.Use = "operator"
	cmd.Short = "Start the Cluster kube-controller-manager Operator"

	return cmd
}
