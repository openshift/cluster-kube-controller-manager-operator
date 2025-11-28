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
				// TODO: Fill in discovered resources
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
