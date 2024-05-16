package cloud

import (
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
)

func NewObserveCloudVolumePluginFunc() configobserver.ObserveConfigFunc {
	return (&cloudVolumePlugin{}).ObserveCloudVolumePlugin
}

type cloudVolumePlugin struct {
	featureGateAccessor featuregates.FeatureGateAccess
}

// ObserveCloudVOlumePlugin fills in the extendedArguments.external-cloud-volume-plugin with the value of the current
// platform type, only when the cluster is running an external cloud provider and there is a supported in-tree volume
// plugin.
func (o *cloudVolumePlugin) ObserveCloudVolumePlugin(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (ret map[string]interface{}, errs []error) {
	volumePluginPath := []string{"extendedArguments", "external-cloud-volume-plugin"}
	defer func() {
		ret = configobserver.Pruned(ret, volumePluginPath)
	}()
	prevObservedConfig := map[string]interface{}{}

	if currentCloudVolumePlugin, _, _ := unstructured.NestedStringSlice(existingConfig, volumePluginPath...); len(currentCloudVolumePlugin) > 0 {
		if err := unstructured.SetNestedStringSlice(prevObservedConfig, currentCloudVolumePlugin, volumePluginPath...); err != nil {
			errs = append(errs, err)
		}
	}

	// Observed config will now always be empty, the flag is due to be removed in a future release.
	observedConfig := map[string]interface{}{}

	if !equality.Semantic.DeepEqual(prevObservedConfig, observedConfig) {
		recorder.Event("ObserveCloudVolumePlugin", "observed change in config")
	}

	return observedConfig, errs
}
