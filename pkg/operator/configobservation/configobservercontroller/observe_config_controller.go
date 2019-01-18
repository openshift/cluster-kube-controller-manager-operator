package configobservercontroller

import (
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"

	operatorconfiginformers "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/cloudprovider"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/network"
)

type ConfigObserver struct {
	*configobserver.ConfigObserver
}

func NewConfigObserver(
	operatorClient v1helpers.OperatorClient,
	operatorConfigInformers operatorconfiginformers.SharedInformerFactory,
	kubeInformersForKubeSystemNamespace kubeinformers.SharedInformerFactory,
	resourceSyncer resourcesynccontroller.ResourceSyncer,
	eventRecorder events.Recorder,
) *ConfigObserver {
	c := &ConfigObserver{
		ConfigObserver: configobserver.NewConfigObserver(
			operatorClient,
			eventRecorder,
			configobservation.Listers{
				ConfigmapLister: kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Lister(),
				ResourceSync:    resourceSyncer,
				PreRunCachesSynced: []cache.InformerSynced{
					kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().HasSynced,
				},
			},
			cloudprovider.ObserveCloudProviderNames,
			network.ObserveClusterCIDRs,
			network.ObserveServiceClusterIPRanges,
		),
	}

	operatorConfigInformers.Kubecontrollermanager().V1alpha1().KubeControllerManagerOperatorConfigs().Informer().AddEventHandler(c.EventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.EventHandler())

	return c
}
