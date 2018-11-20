package network

import (
	"reflect"
	"testing"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"

	"github.com/ghodss/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestObserveClusterCIDRs(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	indexer.Add(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"install-config": "networking:\n  podCIDR: podCIDR",
		},
	})
	listers := configobservation.Listers{
		ConfigmapLister: corelistersv1.NewConfigMapLister(indexer),
	}
	result, errs := ObserveClusterCIDRs(listers, map[string]interface{}{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	expected := map[string]interface{}{
		"extendedArguments": map[string]interface{}{
			"cluster-cidr": []interface{}{
				"podCIDR",
			},
		},
	}
	if !reflect.DeepEqual(expected, result) {
		t.Errorf("\n===== observed config expected:\n%v\n===== observed config actual:\n%v", toYAML(expected), toYAML(result))
	}

}

func TestObserveServiceClusterIPRanges(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	indexer.Add(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"install-config": "networking:\n  serviceCIDR: serviceCIDR",
		},
	})
	listers := configobservation.Listers{
		ConfigmapLister: corelistersv1.NewConfigMapLister(indexer),
	}
	result, errs := ObserveServiceClusterIPRanges(listers, map[string]interface{}{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	expected := map[string]interface{}{
		"extendedArguments": map[string]interface{}{
			"service-cluster-ip-range": []interface{}{
				"serviceCIDR",
			},
		},
	}
	if !reflect.DeepEqual(expected, result) {
		t.Errorf("\n===== observed config expected:\n%v\n===== observed config actual:\n%v", toYAML(expected), toYAML(result))
	}

}

func toYAML(o interface{}) string {
	b, e := yaml.Marshal(o)
	if e != nil {
		return e.Error()
	}
	return string(b)
}
