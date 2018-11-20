package cloudprovider

import (
	"fmt"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
)

// ObserveCloudProviderNames observes cloud provider configuration from
// cluster-config-v1 in order to configure kube-controller-manager's cloud
// provider.
func ObserveCloudProviderNames(genericListers configobserver.Listers, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	var errs []error
	cloudProvidersPath := []string{"extendedArguments", "cloud-provider"}

	previouslyObservedConfig := map[string]interface{}{}
	if currentCloudProvider, _, _ := unstructured.NestedStringSlice(existingConfig, cloudProvidersPath...); len(currentCloudProvider) > 0 {
		unstructured.SetNestedStringSlice(previouslyObservedConfig, currentCloudProvider, cloudProvidersPath...)
	}

	observedConfig := map[string]interface{}{}
	clusterConfig, err := listers.ConfigmapLister.ConfigMaps("kube-system").Get("cluster-config-v1")
	if errors.IsNotFound(err) {
		glog.Warning("configmap/cluster-config-v1.kube-system: not found")
		return observedConfig, errs
	}
	if err != nil {
		errs = append(errs, err)
		return previouslyObservedConfig, errs
	}

	installConfigYaml, ok := clusterConfig.Data["install-config"]
	if !ok {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config not found"))
		return previouslyObservedConfig, errs
	}
	installConfig := map[string]interface{}{}
	err = yaml.Unmarshal([]byte(installConfigYaml), &installConfig)
	if err != nil {
		glog.Warningf("Unable to parse install-config: %s", err)
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
		return previouslyObservedConfig, errs
	case platform["aws"] != nil:
		cloudProvider = "aws"
	default:
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: no recognized cloud provider platform found"))
		return previouslyObservedConfig, errs
	}

	// set observed values
	//  extendedArguments:
	//    cloud-provider:
	//    - "name"
	unstructured.SetNestedStringSlice(observedConfig, []string{cloudProvider}, cloudProvidersPath...)

	return observedConfig, errs
}
