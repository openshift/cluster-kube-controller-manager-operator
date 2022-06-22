package configobservercontroller

import (
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/tools/cache"

	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	libgocloudprovider "github.com/openshift/library-go/pkg/cloudprovider"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	libgoapiserver "github.com/openshift/library-go/pkg/operator/configobserver/apiserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	nodeobserver "github.com/openshift/library-go/pkg/operator/configobserver/node"
	"github.com/openshift/library-go/pkg/operator/configobserver/proxy"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/cloud"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/clustername"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/network"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/node"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/serviceca"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

// openShiftOnlyFeatureGates are feature gate names that are only used within
// OpenShift. Passing these to KCM causes it to log an error on startup.
// This list is passed to the feature gate config observer as a blacklist,
// excluding them from the feature gate output passed to KCM.
var openShiftOnlyFeatureGates = sets.NewString(
	libgocloudprovider.ExternalCloudProviderFeature,
)

type ConfigObserver struct {
	factory.Controller
}

func NewConfigObserver(
	operatorClient v1helpers.OperatorClient,
	configinformers configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	resourceSyncer resourcesynccontroller.ResourceSyncer,
	eventRecorder events.Recorder,
) (*ConfigObserver, error) {

	interestingNamespaces := []string{
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.TargetNamespace,
		operatorclient.OperatorNamespace,
	}
	configMapPreRunCacheSynced := []cache.InformerSynced{}
	for _, ns := range interestingNamespaces {
		configMapPreRunCacheSynced = append(configMapPreRunCacheSynced, kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer().HasSynced)
	}

	informers := []factory.Informer{
		operatorClient.Informer(),
		configinformers.Config().V1().FeatureGates().Informer(),
		configinformers.Config().V1().Infrastructures().Informer(),
		configinformers.Config().V1().Networks().Informer(),
		configinformers.Config().V1().Nodes().Informer(),
		configinformers.Config().V1().Proxies().Informer(),
	}
	for _, ns := range interestingNamespaces {
		informers = append(informers, kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer())
	}

	extremeProfileSuppressor, err := nodeobserver.NewSuppressConfigUpdateForExtremeProfilesFunc(
		operatorClient.(v1helpers.StaticPodOperatorClient),
		configinformers.Config().V1().Nodes().Lister(),
		node.LatencyConfigs,
		node.LatencyProfileRejectionScenarios,
	)
	if err != nil {
		return nil, err
	}

	differentConfigProfileSuppressor := nodeobserver.NewSuppressConfigUpdateUntilSameProfileFunc(
		operatorClient.(v1helpers.StaticPodOperatorClient),
		kubeInformersForNamespaces.ConfigMapLister().ConfigMaps(operatorclient.TargetNamespace),
		node.LatencyConfigs,
	)

	c := &ConfigObserver{
		Controller: configobserver.NewConfigObserver(
			operatorClient,
			eventRecorder,
			configobservation.Listers{
				FeatureGateLister_:    configinformers.Config().V1().FeatureGates().Lister(),
				InfrastructureLister_: configinformers.Config().V1().Infrastructures().Lister(),
				NetworkLister:         configinformers.Config().V1().Networks().Lister(),
				NodeLister_:           configinformers.Config().V1().Nodes().Lister(),
				ProxyLister_:          configinformers.Config().V1().Proxies().Lister(),
				APIServerLister_:      configinformers.Config().V1().APIServers().Lister(),

				ResourceSync:     resourceSyncer,
				ConfigMapLister_: kubeInformersForNamespaces.ConfigMapLister(),
				PreRunCachesSynced: append(configMapPreRunCacheSynced,
					operatorClient.Informer().HasSynced,

					kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer().HasSynced,
					kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Informer().HasSynced,

					configinformers.Config().V1().FeatureGates().Informer().HasSynced,
					configinformers.Config().V1().Infrastructures().Informer().HasSynced,
					configinformers.Config().V1().Networks().Informer().HasSynced,
					configinformers.Config().V1().Nodes().Informer().HasSynced,
					configinformers.Config().V1().Proxies().Informer().HasSynced,
				),
			},
			informers,
			cloudprovider.NewCloudProviderObserver(
				"openshift-kube-controller-manager",
				[]string{"extendedArguments", "cloud-provider"},
				[]string{"extendedArguments", "cloud-config"}),
			featuregates.NewObserveFeatureFlagsFunc(
				nil,
				openShiftOnlyFeatureGates,
				[]string{"extendedArguments", "feature-gates"},
			),
			network.ObserveClusterCIDRs,
			network.ObserveServiceClusterIPRanges,
			nodeobserver.NewLatencyProfileObserver(
				node.LatencyConfigs,
				[]nodeobserver.ShouldSuppressConfigUpdatesFunc{
					// for multiple suppressor(s) being called in this observer
					// the more important one: the extreme profile suppressor,
					// will resolve first; extreme profile suppression would take
					// priority over different config profile suppressor.
					extremeProfileSuppressor,
					differentConfigProfileSuppressor,
				},
			),
			proxy.NewProxyObserveFunc([]string{"targetconfigcontroller", "proxy"}),
			serviceca.ObserveServiceCA,
			clustername.ObserveInfraID,
			libgoapiserver.ObserveTLSSecurityProfile,
			cloud.ObserveCloudVolumePlugin,
		),
	}

	return c, nil
}
