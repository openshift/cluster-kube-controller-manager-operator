package configobservercontroller

import (
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/clustername"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/network"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/serviceca"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/configobserver/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/configobserver/proxy"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"k8s.io/client-go/tools/cache"
)

type ConfigObserver struct {
	*configobserver.ConfigObserver
}

func NewConfigObserver(
	operatorClient v1helpers.OperatorClient,
	configinformers configinformers.SharedInformerFactory,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	resourceSyncer resourcesynccontroller.ResourceSyncer,
	eventRecorder events.Recorder,
) *ConfigObserver {

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

	c := &ConfigObserver{
		ConfigObserver: configobserver.NewConfigObserver(
			operatorClient,
			eventRecorder,
			configobservation.Listers{
				FeatureGateLister_:    configinformers.Config().V1().FeatureGates().Lister(),
				InfrastructureLister_: configinformers.Config().V1().Infrastructures().Lister(),
				NetworkLister:         configinformers.Config().V1().Networks().Lister(),
				ProxyLister_:          configinformers.Config().V1().Proxies().Lister(),

				ResourceSync:    resourceSyncer,
				ConfigMapLister: kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Lister(),
				PreRunCachesSynced: append(configMapPreRunCacheSynced,
					operatorClient.Informer().HasSynced,

					kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer().HasSynced,
					kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Informer().HasSynced,

					configinformers.Config().V1().FeatureGates().Informer().HasSynced,
					configinformers.Config().V1().Infrastructures().Informer().HasSynced,
					configinformers.Config().V1().Networks().Informer().HasSynced,
					configinformers.Config().V1().Proxies().Informer().HasSynced,
				),
			},
			cloudprovider.NewCloudProviderObserver(
				"openshift-kube-controller-manager",
				[]string{"extendedArguments", "cloud-provider"},
				[]string{"extendedArguments", "cloud-config"}),
			featuregates.NewObserveFeatureFlagsFunc(nil, []string{"extendedArguments", "feature-gates"}),
			network.ObserveClusterCIDRs,
			network.ObserveServiceClusterIPRanges,
			proxy.NewProxyObserveFunc([]string{"targetconfigcontroller", "proxy"}),
			serviceca.ObserveServiceCA,
			clustername.ObserveInfraID,
		),
	}

	operatorClient.Informer().AddEventHandler(c.EventHandler())

	for _, ns := range interestingNamespaces {
		kubeInformersForNamespaces.InformersFor(ns).Core().V1().ConfigMaps().Informer().AddEventHandler(c.EventHandler())
	}

	configinformers.Config().V1().FeatureGates().Informer().AddEventHandler(c.EventHandler())
	configinformers.Config().V1().Infrastructures().Informer().AddEventHandler(c.EventHandler())
	configinformers.Config().V1().Networks().Informer().AddEventHandler(c.EventHandler())
	configinformers.Config().V1().Proxies().Informer().AddEventHandler(c.EventHandler())

	return c
}
