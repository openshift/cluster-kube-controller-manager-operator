package network

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/network"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
)

var (
	serviceClusterIPRangePath = []string{"extendedArguments", "service-cluster-ip-range"}
	clusterCIDRsPath          = []string{"extendedArguments", "cluster-cidr"}
)

func ObserveClusterCIDRs(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)

	var errs []error

	previouslyObservedConfig := map[string]interface{}{}
	if currentClusterCIDRBlocks, _, err := unstructured.NestedStringSlice(existingConfig, clusterCIDRsPath...); len(currentClusterCIDRBlocks) > 0 {
		if err != nil {
			errs = append(errs, err)
		}
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, currentClusterCIDRBlocks, clusterCIDRsPath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}
	clusterCIDRs, err := network.GetClusterCIDRs(listers.ConfigmapLister, recorder)
	if err != nil {
		errs = append(errs, err)
		return previouslyObservedConfig, errs
	}

	if len(clusterCIDRs) > 0 {
		if err := unstructured.SetNestedStringSlice(observedConfig, clusterCIDRs, clusterCIDRsPath...); err != nil {
			errs = append(errs, err)
		}
	}

	return observedConfig, errs
}

func ObserveServiceClusterIPRanges(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)

	var errs []error

	previouslyObservedConfig := map[string]interface{}{}
	if currentServiceClusterIPRanges, _, _ := unstructured.NestedStringSlice(existingConfig, serviceClusterIPRangePath...); len(currentServiceClusterIPRanges) > 0 {
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, currentServiceClusterIPRanges, serviceClusterIPRangePath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}
	serviceCIDR, err := network.GetServiceCIDR(listers.ConfigmapLister, recorder)
	if err != nil {
		errs = append(errs, err)
		return previouslyObservedConfig, errs
	}

	if err := unstructured.SetNestedStringSlice(observedConfig, []string{serviceCIDR}, serviceClusterIPRangePath...); err != nil {
		errs = append(errs, err)
	}

	return observedConfig, errs
}

// Validate verifies whether the observed configuration can be used to make progress.
func Validate(observedConfig map[string]interface{}) (errs []error) {
	if clusterCIDRs, _, err := unstructured.NestedStringSlice(observedConfig, clusterCIDRsPath...); err != nil {
		errs = append(errs, err)
	} else if len(clusterCIDRs) == 0 {
		errs = append(errs, fmt.Errorf("%s cannot be empty", strings.Join(serviceClusterIPRangePath, ".")))
	}

	if serviceCIDR, _, err := unstructured.NestedString(observedConfig, serviceClusterIPRangePath...); err != nil {
		errs = append(errs, err)
	} else if len(serviceCIDR) == 0 {
		errs = append(errs, fmt.Errorf("%s cannot be empty", strings.Join(serviceClusterIPRangePath, ".")))
	}

	return
}
