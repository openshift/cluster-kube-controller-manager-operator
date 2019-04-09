package configobservation

import (
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
)

type Listers struct {
	FeatureGateLister_   configlistersv1.FeatureGateLister
	InfrastructureLister configlistersv1.InfrastructureLister
	NetworkLister        configlistersv1.NetworkLister
	ConfigMapLister      corev1listers.ConfigMapLister

	ResourceSync       resourcesynccontroller.ResourceSyncer
	PreRunCachesSynced []cache.InformerSynced
}

func (l Listers) FeatureGateLister() configlistersv1.FeatureGateLister {
	return l.FeatureGateLister_
}

func (l Listers) ResourceSyncer() resourcesynccontroller.ResourceSyncer {
	return l.ResourceSync
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.PreRunCachesSynced
}
