package cloudprovider

import (
	"fmt"

	"github.com/golang/glog"
	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
)

// ObserveCloudProviderNames observes cloud provider configuration from
// cluster-config-v1 in order to configure kube-controller-manager's cloud
// provider.
func ObserveCloudProviderNames(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	var errs []error
	cloudProvidersPath := []string{"extendedArguments", "cloud-provider"}

	prevObservedConfig := map[string]interface{}{}
	currentCloudProvider, _, err := unstructured.NestedStringSlice(existingConfig, cloudProvidersPath...)
	if err != nil {
		return prevObservedConfig, append(errs, err)
	}
	if len(currentCloudProvider) > 0 {
		if err := unstructured.SetNestedStringSlice(prevObservedConfig, currentCloudProvider, cloudProvidersPath...); err != nil {
			return prevObservedConfig, append(errs, err)
		}
	}

	if !listers.InfrastructureSynced() {
		glog.Warning("infrastructure.config.openshift.io not synced")
		return prevObservedConfig, errs
	}

	observedConfig := map[string]interface{}{}
	clusterConfig, err := listers.InfrastructureLister.Get("cluster")
	if errors.IsNotFound(err) {
		glog.Warning("infrastructure.config.openshift.io/cluster: not found")
		return observedConfig, errs
	}
	if err != nil {
		return prevObservedConfig, append(errs, err)
	}

	cloudProvider := ""
	switch clusterConfig.Status.Platform {
	case configv1.LibvirtPlatform:
		// this means we are using libvirt
		return observedConfig, errs
	case configv1.AWSPlatform:
		cloudProvider = "aws"
	case configv1.OpenStackPlatform:
		// TODO(flaper87): Enable this once
		// we've figured out a way to write
		// the cloud provider config in the
		// master nodes
		//cloudProvider = "openstack"
		return observedConfig, errs
	case configv1.NonePlatform:
		// this means we are using bare metal
		return observedConfig, errs
	default:
		// the new doc on the infrastructure fields requires that we treat an unrecognized thing the same bare metal.
		// TODO find a way to indicate to the user that we didn't honor their choice
		recorder.Warning("ObserveCloudProvidersFailed", fmt.Sprintf("No recognized cloud provider platform found in cloud config: %#v", clusterConfig.Status.Platform))
		return observedConfig, errs
	}

	// set observed values
	//  extendedArguments:
	//    cloud-provider:
	//    - "name"
	if err := unstructured.SetNestedStringSlice(observedConfig, []string{cloudProvider}, cloudProvidersPath...); err != nil {
		errs = append(errs, err)
	}

	return observedConfig, errs
}
