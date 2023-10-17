package operator

import (
	"github.com/spf13/cobra"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

func NewOperator() *cobra.Command {
	controllerCommandConfig := controllercmd.
		NewControllerCommandConfig("kube-controller-manager-operator", version.Get(), operator.RunOperator)
	// disable HTTP2 to mitigate rapid reset http2 issue
	controllerCommandConfig.DisableHTTP2 = true
	cmd := controllerCommandConfig.NewCommand()
	cmd.Use = "operator"
	cmd.Short = "Start the Cluster kube-controller-manager Operator"

	return cmd
}
