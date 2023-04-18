package configobservation

import (
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	operatorlistersv1 "github.com/openshift/client-go/operator/listers/operator/v1"
	"github.com/openshift/library-go/pkg/operator/configobserver/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

var _ cloudprovider.InfrastructureLister = &Listers{}

type Listers struct {
	FeatureGateLister_    configlistersv1.FeatureGateLister
	InfrastructureLister_ configlistersv1.InfrastructureLister
	NetworkLister         configlistersv1.NetworkLister
	NodeLister_           configlistersv1.NodeLister
	ProxyLister_          configlistersv1.ProxyLister
	ConfigMapLister_      corev1listers.ConfigMapLister
	APIServerLister_      configlistersv1.APIServerLister
	StorageLister_        operatorlistersv1.StorageLister

	ResourceSync       resourcesynccontroller.ResourceSyncer
	PreRunCachesSynced []cache.InformerSynced
}

func (l Listers) InfrastructureLister() configlistersv1.InfrastructureLister {
	return l.InfrastructureLister_
}

func (l Listers) FeatureGateLister() configlistersv1.FeatureGateLister {
	return l.FeatureGateLister_
}

func (l Listers) ProxyLister() configlistersv1.ProxyLister {
	return l.ProxyLister_
}

func (l Listers) NodeLister() configlistersv1.NodeLister {
	return l.NodeLister_
}

func (l Listers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	return l.ResourceSync
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.PreRunCachesSynced
}

func (l Listers) ConfigMapLister() corev1listers.ConfigMapLister {
	return l.ConfigMapLister_
}

func (l Listers) APIServerLister() configlistersv1.APIServerLister {
	return l.APIServerLister_
}

func (l Listers) StorageLister() operatorlistersv1.StorageLister {
	return l.StorageLister_
}
