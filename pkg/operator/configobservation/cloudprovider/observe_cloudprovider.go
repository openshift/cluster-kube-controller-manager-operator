package cloudprovider

import (
	"fmt"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	"github.com/openshift/library-go/pkg/operator/events"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
)

const (
	clusterConfigNamespace = "kube-system"
	clusterConfigName      = "cluster-config-v1"
)

// ObserveCloudProviderNames observes cloud provider configuration from
// cluster-config-v1 in order to configure kube-controller-manager's cloud
// provider.
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
	clusterConfig, err := listers.ConfigmapLister.ConfigMaps(clusterConfigNamespace).Get(clusterConfigName)
	if errors.IsNotFound(err) {
		glog.Warning("configmap/cluster-config-v1.kube-system: not found")
		recorder.Warningf("ObserveCloudProvidersFailed", "Required %s/%s config map not found", clusterConfigNamespace, clusterConfigName)
		return observedConfig, errs
	}
	if err != nil {
		errs = append(errs, err)
		return previouslyObservedConfig, errs
	}

	installConfigYaml, ok := clusterConfig.Data["install-config"]
	if !ok {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config not found"))
		recorder.Warningf("ObserveCloudProvidersFailed", "ConfigMap %s/%s does not have required 'install-config'", clusterConfigNamespace, clusterConfigName)
		return previouslyObservedConfig, errs
	}
	installConfig := map[string]interface{}{}
	err = yaml.Unmarshal([]byte(installConfigYaml), &installConfig)
	if err != nil {
		glog.Warningf("Unable to parse install-config: %s", err)
		recorder.Warningf("ObserveCloudProvidersFailed", "Unable to decode install config: %v'", err)
		return previouslyObservedConfig, errs
	}

	// extract needed values
	//  data:
	//   install-config:
	//     platform:
	//       aws: {}
	// only aws supported for now
	cloudProvider := ""
	platform, ok := installConfig["platform"].(map[string]interface{})
	switch {
	case !ok:
		glog.Warning("configmap/cluster-config-v1.kube-system: install-config.platform not found")
		recorder.Warningf("ObserveCloudProvidersFailed", "Required platform field is not set in install-config")
		return previouslyObservedConfig, errs
	case platform["libvirt"] != nil:
		// this means we are using libvirt
		return observedConfig, errs
	case platform["none"] != nil:
		// this means we are using bare metal
		return observedConfig, errs
	case platform["aws"] != nil:
		cloudProvider = "aws"
	case platform["openstack"] != nil:
		cloudProvider = "openstack"
	default:
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: no recognized cloud provider platform found: %#v", platform))
		recorder.Warning("ObserveCloudProvidersFailed", fmt.Sprintf("No recognized cloud provider platform found in cloud config: %#v", platform))
		return previouslyObservedConfig, errs
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
