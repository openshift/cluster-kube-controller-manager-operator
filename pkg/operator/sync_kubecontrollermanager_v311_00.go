package operator

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	appsclientv1 "k8s.io/client-go/kubernetes/typed/apps/v1"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"

	operatorsv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/apis/kubecontrollermanager/v1alpha1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
)

// syncKubeControllerManager_v311_00_to_latest takes care of synchronizing (not upgrading) the thing we're managing.
// most of the time the sync method will be good for a large span of minor versions
func syncKubeControllerManager_v311_00_to_latest(c KubeControllerManagerOperator, operatorConfig *v1alpha1.KubeControllerManagerOperatorConfig, previousAvailability *operatorsv1alpha1.VersionAvailability) (operatorsv1alpha1.VersionAvailability, []error) {
	versionAvailability := operatorsv1alpha1.VersionAvailability{
		Version: operatorConfig.Spec.Version,
	}

	errors := []error{}
	var err error

	directResourceResults := resourceapply.ApplyDirectly(c.kubeClient, v311_00_assets.Asset,
		"v3.11.0/kube-controller-manager/clusterrolebinding.yaml",
		"v3.11.0/kube-controller-manager/ns.yaml",
		"v3.11.0/kube-controller-manager/public-info-role.yaml",
		"v3.11.0/kube-controller-manager/public-info-rolebinding.yaml",
		"v3.11.0/kube-controller-manager/svc.yaml",
		"v3.11.0/kube-controller-manager/sa.yaml",
		"v3.11.0/openshift-apiserver/sa.yaml",
	)
	resourcesThatForceRedeployment := sets.NewString("v3.11.0/kube-controller-manager/sa.yaml")
	forceRollingUpdate := false

	for _, currResult := range directResourceResults {
		if currResult.Error != nil {
			errors = append(errors, fmt.Errorf("%q (%T): %v", currResult.File, currResult.Type, currResult.Error))
			continue
		}

		if currResult.Changed && resourcesThatForceRedeployment.Has(currResult.File) {
			forceRollingUpdate = true
		}
	}

	controllerManagerConfig, configMapModified, err := manageKubeControllerManagerConfigMap_v311_00_to_latest(c.kubeClient.CoreV1(), operatorConfig)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap", err))
	}

	forceRollout := operatorConfig.ObjectMeta.Generation != operatorConfig.Status.ObservedGeneration
	forceRollout = forceRollout || forceRollingUpdate || configMapModified

	// our configmaps and secrets are in order, now it is time to create the DS
	// TODO check basic preconditions here
	actualDaemonSet, _, err := manageKubeControllerManagerDaemonSet_v311_00_to_latest(c.kubeClient.AppsV1(), operatorConfig, previousAvailability, forceRollout)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "daemonset", err))
	}

	// if our actual daemonset has at least one pod available, then we should delete the kube-system daemonset to make sure we take over for it.
	if actualDaemonSet.Status.NumberAvailable > 0 {
		// we don't care about the return value here.
		c.kubeClient.AppsV1().DaemonSets("kube-system").Delete("kube-controller-manager", nil)
	}

	configData := ""
	if controllerManagerConfig != nil {
		configData = controllerManagerConfig.Data["config.yaml"]
	}
	_, _, err = manageKubeControllerManagerPublicConfigMap_v311_00_to_latest(c.kubeClient.CoreV1(), configData, operatorConfig)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/public-info", err))
	}

	return resourcemerge.ApplyDaemonSetGenerationAvailability(versionAvailability, actualDaemonSet, errors...), errors
}

func manageKubeControllerManagerConfigMap_v311_00_to_latest(client coreclientv1.ConfigMapsGetter, operatorConfig *v1alpha1.KubeControllerManagerOperatorConfig) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/cm.yaml"))
	defaultConfig := v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/defaultconfig.yaml")
	requiredConfigMap, _, err := resourcemerge.MergeConfigMap(configMap, "config.yaml", nil, defaultConfig, operatorConfig.Spec.KubeControllerManagerConfig.Raw)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, requiredConfigMap)
}

func manageKubeControllerManagerDaemonSet_v311_00_to_latest(client appsclientv1.DaemonSetsGetter, options *v1alpha1.KubeControllerManagerOperatorConfig, previousAvailability *operatorsv1alpha1.VersionAvailability, forceRollout bool) (*appsv1.DaemonSet, bool, error) {
	required := resourceread.ReadDaemonSetV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/ds.yaml"))
	required.Spec.Template.Spec.Containers[0].Image = options.Spec.ImagePullSpec
	required.Spec.Template.Spec.Containers[0].Args = append(required.Spec.Template.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", options.Spec.Logging.Level))

	return resourceapply.ApplyDaemonSet(client, required, resourcemerge.ExpectedDaemonSetGeneration(required, previousAvailability), forceRollout)
}

func manageKubeControllerManagerPublicConfigMap_v311_00_to_latest(client coreclientv1.ConfigMapsGetter, controllerManagerConfigString string, operatorConfig *v1alpha1.KubeControllerManagerOperatorConfig) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/public-info.yaml"))
	if operatorConfig.Status.CurrentAvailability != nil {
		configMap.Data["version"] = operatorConfig.Status.CurrentAvailability.Version
	} else {
		configMap.Data["version"] = ""
	}

	return resourceapply.ApplyConfigMap(client, configMap)
}
