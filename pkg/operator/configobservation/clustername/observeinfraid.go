package clustername

import (
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

// ObserveInfraID fills in the cluster-name extended argument for the controller-manager with the cluster's infra ID
func ObserveInfraID(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	clusterNamePath := []string{"extendedArguments", "cluster-name"}
	previouslyObservedConfig := map[string]interface{}{}

	if currentClusterName, _, _ := unstructured.NestedStringSlice(existingConfig, clusterNamePath...); len(currentClusterName) > 0 {
		if err := unstructured.SetNestedStringSlice(previouslyObservedConfig, currentClusterName, clusterNamePath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}
	infrastructure, err := listers.InfrastructureLister().Get("cluster")
	if err != nil {
		if errors.IsNotFound(err) {
			recorder.Warningf("ObserveInfraID", "Required infrastructures.%s/cluster not found", configv1.GroupName)
		}
		return previouslyObservedConfig, errs
	}
	// The infrastructureName value in infrastructure status is always present and cannot be changed during the
	// lifetime of the cluster.
	infraID := infrastructure.Status.InfrastructureName
	if len(infraID) == 0 {
		recorder.Warningf("ObserveInfraID", "Value for infrastructureName in infrastructure.%s/cluster is blank", configv1.GroupName)
		return previouslyObservedConfig, errs
	}
	if err := unstructured.SetNestedStringSlice(observedConfig, []string{infraID}, clusterNamePath...); err != nil {
		errs = append(errs, err)
	}
	return observedConfig, errs
}
