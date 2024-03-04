package cloud

import (
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	cloudproviderobserver "github.com/openshift/library-go/pkg/operator/configobserver/cloudprovider"
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

	listers := genericListers.(configobservation.Listers)
	infrastructure, err := listers.InfrastructureLister().Get("cluster")
	if err != nil {
		return existingConfig, append(errs, err)
	}

	external, err := cloudprovider.IsCloudProviderExternal(infrastructure.Status.PlatformStatus)
	if err != nil {
		return existingConfig, append(errs, err)
	}

	observedConfig := map[string]interface{}{}
	cloudProvider := cloudproviderobserver.GetPlatformName(infrastructure.Status.PlatformStatus.Type, recorder)

	switch cloudProvider {
	case "aws":
		// CSI migration GA'd more than 1 release ago, so we can safely remove the in-tree plugin.
		// Do nothing on these platforms.
	default:
		// If the cloud provider is external, we should set the option, else leave it empty.
		if external && len(cloudProvider) > 0 {
			if err := unstructured.SetNestedStringSlice(observedConfig, []string{cloudProvider}, volumePluginPath...); err != nil {
				recorder.Warningf("ObserveCloudVolumePlugin", "Failed setting cloudVolumePlugin: %v", err)
				return existingConfig, append(errs, err)
			}
		}
	}

	if !equality.Semantic.DeepEqual(prevObservedConfig, observedConfig) {
		recorder.Event("ObserveCloudVolumePlugin", "observed change in config")
	}

	return observedConfig, errs
}
