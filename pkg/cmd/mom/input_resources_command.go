package mom

import (
	"context"

	"github.com/openshift/multi-operator-manager/pkg/library/libraryinputresources"
	"github.com/openshift/multi-operator-manager/pkg/library/libraryoutputresources"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

func NewInputResourcesCommand(streams genericiooptions.IOStreams) *cobra.Command {
	return libraryinputresources.NewInputResourcesCommand(runInputResources, runOutputResources, streams)
}

func runInputResources(ctx context.Context) (*libraryinputresources.InputResources, error) {
	return &libraryinputresources.InputResources{
		ApplyConfigurationResources: libraryinputresources.ResourceList{
			ExactResources: []libraryinputresources.ExactResourceID{
				// Operator CR
				libraryinputresources.ExactLowLevelOperator("kubecontrollermanagers"),

				// Config resources
				libraryinputresources.ExactConfigResource("infrastructures"),
				libraryinputresources.ExactConfigResource("networks"),
				libraryinputresources.ExactConfigResource("featuregates"),
				libraryinputresources.ExactConfigResource("nodes"),
				libraryinputresources.ExactConfigResource("proxies"),
				libraryinputresources.ExactConfigResource("apiservers"),
				libraryinputresources.ExactConfigResource("clusterversions"),

				// Namespaces
				libraryinputresources.ExactNamespace("openshift-config"),
				libraryinputresources.ExactNamespace("openshift-config-managed"),
				libraryinputresources.ExactNamespace("openshift-kube-controller-manager"),
				libraryinputresources.ExactNamespace("openshift-kube-controller-manager-operator"),
				libraryinputresources.ExactNamespace("kube-system"),
				libraryinputresources.ExactNamespace("openshift-infra"),

				// ConfigMaps that may be synced or referenced
				libraryinputresources.ExactConfigMap("openshift-config", "cloud-provider-config"),
				libraryinputresources.ExactConfigMap("openshift-config-managed", "kube-controller-cert-syncer-kubeconfig"),
				libraryinputresources.ExactConfigMap("kube-system", "cluster-config-v1"),

				// Secrets that may be synced or referenced
				libraryinputresources.ExactSecret("openshift-config", "cloud-credentials"),
			},
		},
	}, nil
}

// runOutputResources is defined here to support the input-resources command
// The actual implementation will be in output_resources_command.go
func runOutputResources(ctx context.Context) (*libraryoutputresources.OutputResources, error) {
	return &libraryoutputresources.OutputResources{
		ConfigurationResources: libraryoutputresources.ResourceList{
			ExactResources: []libraryoutputresources.ExactResourceID{},
		},
		ManagementResources: libraryoutputresources.ResourceList{
			ExactResources: []libraryoutputresources.ExactResourceID{},
		},
		UserWorkloadResources: libraryoutputresources.ResourceList{
			ExactResources: []libraryoutputresources.ExactResourceID{},
		},
	}, nil
}
