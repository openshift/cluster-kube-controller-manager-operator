package network

import (
	"fmt"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
)

func ObserveClusterCIDRs(genericListers configobserver.Listers, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)

	var errs []error
	clusterCIDRsPath := []string{"extendedArguments", "cluster-cidr"}

	previouslyObservedConfig := map[string]interface{}{}
	if currentClusterCIDRBlocks, _, _ := unstructured.NestedStringSlice(existingConfig, clusterCIDRsPath...); len(currentClusterCIDRBlocks) > 0 {
		unstructured.SetNestedStringSlice(previouslyObservedConfig, currentClusterCIDRBlocks, clusterCIDRsPath...)
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
		glog.Warning("configmap/cluster-config-v1.kube-system: install-config not found")
		return observedConfig, errs
	}
	installConfig := map[string]interface{}{}
	err = yaml.Unmarshal([]byte(installConfigYaml), &installConfig)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to parse install-config: %s", err))
		return previouslyObservedConfig, errs
	}

	var clusterCIDRs []string
	clusterNetworks, _, err := unstructured.NestedSlice(installConfig, "networking", "clusterNetworks")
	if err != nil {
		errs = append(errs, fmt.Errorf("unabled to parse install-config: %s", err))
		return previouslyObservedConfig, errs
	}
	for i, n := range clusterNetworks {
		obj, ok := n.(map[string]interface{})
		if !ok {
			errs = append(errs, fmt.Errorf("unabled to parse install-config: expected networking.clusterNetworks[%d] to be an object, got: %#v", i, n))
			return previouslyObservedConfig, errs
		}
		cidr, _, err := unstructured.NestedString(obj, "cidr")
		if err != nil {
			errs = append(errs, fmt.Errorf("unabled to parse install-config: %v", err))
			return previouslyObservedConfig, errs
		}
		clusterCIDRs = append(clusterCIDRs, cidr)
	}
	// fallback to podCIDR
	if clusterNetworks == nil {
		podCIDR, _, _ := unstructured.NestedString(installConfig, "networking", "podCIDR")
		if len(podCIDR) == 0 {
			errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config.networking.clusterNetworks and install-config.networking.podCIDR not found"))
			return previouslyObservedConfig, errs
		}
		clusterCIDRs = append(clusterCIDRs, podCIDR)
	}
	if len(clusterCIDRs) > 0 {
		unstructured.SetNestedStringSlice(observedConfig, clusterCIDRs, clusterCIDRsPath...)
	}

	return observedConfig, errs
}

func ObserveServiceClusterIPRanges(genericListers configobserver.Listers, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)

	var errs []error
	serviceClusterIPRangePath := []string{"extendedArguments", "service-cluster-ip-range"}

	previouslyObservedConfig := map[string]interface{}{}
	if currentServiceClusterIPRanges, _, _ := unstructured.NestedStringSlice(existingConfig, serviceClusterIPRangePath...); len(currentServiceClusterIPRanges) > 0 {
		unstructured.SetNestedStringSlice(previouslyObservedConfig, currentServiceClusterIPRanges, serviceClusterIPRangePath...)
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
		glog.Warning("configmap/cluster-config-v1.kube-system: install-config not found")
		return observedConfig, errs
	}
	installConfig := map[string]interface{}{}
	err = yaml.Unmarshal([]byte(installConfigYaml), &installConfig)
	if err != nil {
		errs = append(errs, fmt.Errorf("Unable to parse install-config: %s", err))
		return previouslyObservedConfig, errs
	}

	serviceCIDR, _, _ := unstructured.NestedString(installConfig, "networking", "serviceCIDR")
	if len(serviceCIDR) == 0 {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config.networking.serviceCIDR not found"))
		return previouslyObservedConfig, errs
	}

	unstructured.SetNestedStringSlice(observedConfig, []string{serviceCIDR}, serviceClusterIPRangePath...)

	return observedConfig, errs
}
