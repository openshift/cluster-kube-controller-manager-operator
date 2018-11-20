package configobservercontroller

import (
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/network"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	operatorconfiginformers "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/informers/externalversions"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/configobserver"
)

type ConfigObserver struct {
	*configobserver.ConfigObserver
}

func NewConfigObserver(
	operatorClient configobserver.OperatorClient,
	operatorConfigInformers operatorconfiginformers.SharedInformerFactory,
	kubeInformersForKubeSystemNamespace kubeinformers.SharedInformerFactory,
) *ConfigObserver {
	c := &ConfigObserver{
		ConfigObserver: configobserver.NewConfigObserver(
			operatorClient,
			configobservation.Listers{
				ConfigmapLister: kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Lister(),
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
