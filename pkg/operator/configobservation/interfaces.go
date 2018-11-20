package configobservation

import (
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type Listers struct {
	ConfigmapLister corelistersv1.ConfigMapLister

	PreRunCachesSynced []cache.InformerSynced
}

func (l Listers) PreRunHasSynced() []cache.InformerSynced {
	return l.PreRunCachesSynced
}
