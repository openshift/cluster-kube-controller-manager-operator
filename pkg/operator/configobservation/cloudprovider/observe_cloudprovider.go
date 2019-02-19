package cloudprovider

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
)

// ObserveCloudProviderNames observes the loud provider from the global cluster infrastructure resource.
func ObserveCloudProviderNames(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	var errs []error
	cloudProvidersPath := []string{"extendedArguments", "cloud-provider"}

	previouslyObservedConfig := map[string]interface{}{}
	if currentCloudProvider, _, _ := unstructured.NestedStringSlice(existingConfig, cloudProvidersPath...); len(currentCloudProvider) > 0 {
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, currentCloudProvider, cloudProvidersPath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}

	infrastructure, err := listers.InfrastructureLister.Get("cluster")
	if errors.IsNotFound(err) {
		recorder.Warningf("ObserveRestrictedCIDRFailed", "Required infrastructures.%s/cluster not found", configv1.GroupName)
		return observedConfig, errs
	}
	if err != nil {
		return previouslyObservedConfig, errs
	}

	cloudProvider := ""
	switch infrastructure.Status.Platform {
	case "":
		recorder.Warningf("ObserveCloudProvidersFailed", "Required status.platform field is not set in infrastructures.%s/cluster", configv1.GroupName)
		return previouslyObservedConfig, errs
	case configv1.AWSPlatform:
		cloudProvider = "aws"
	case configv1.LibvirtPlatform:
	case configv1.OpenStackPlatform:
		// TODO(flaper87): Enable this once we've figured out a way to write the cloud provider config in the master nodes
		//cloudProvider = "openstack"
	case configv1.NonePlatform:
	default:
		// the new doc on the infrastructure fields requires that we treat an unrecognized thing the same bare metal.
		// TODO find a way to indicate to the user that we didn't honor their choice
		recorder.Warningf("ObserveCloudProvidersFailed", fmt.Sprintf("No recognized cloud provider platform found in infrastructures.%s/cluster.status.platform", configv1.GroupName))
	}

	if len(cloudProvider) > 0 {
		if err := unstructured.SetNestedStringSlice(observedConfig, []string{cloudProvider}, cloudProvidersPath...); err != nil {
			errs = append(errs, err)
		}
	}

	return observedConfig, errs
}
