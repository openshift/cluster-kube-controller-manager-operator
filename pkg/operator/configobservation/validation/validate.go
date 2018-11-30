package validation

import "github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/network"

// Validate verifies whether the observed configuration can be used to make progress.
func Validate(observedConfig map[string]interface{}) (errs []error) {
	errs = append(errs, network.Validate(observedConfig)...)
	return
}
