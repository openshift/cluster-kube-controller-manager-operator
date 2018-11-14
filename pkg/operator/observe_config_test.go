package operator

import (
	"reflect"
	"testing"

	"github.com/ghodss/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestObserveCloudProviderNames(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	indexer.Add(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-config-v1",
			Namespace: "kube-system",
		},
		Data: map[string]string{
			"install-config": "platform:\n  aws: {}\n",
		},
	})
	listers := Listers{
		configmapLister: corelistersv1.NewConfigMapLister(indexer),
	}
	result, errs := observeCloudProviderNames(listers, map[string]interface{}{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	cloudProvider, _, err := unstructured.NestedSlice(result, "extendedArguments", "cloud-provider")
	if err != nil {
		t.Fatal(err)
	}
	if e, a := 1, len(cloudProvider); e != a {
		t.Fatalf("expected len(cloudProvider) == %d, got %d", e, a)
	}
	if e, a := "aws", cloudProvider[0]; e != a {
		t.Errorf("expected cloud-provider=%s, got %s", e, a)
	}
}

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
	listers := Listers{
		configmapLister: corelistersv1.NewConfigMapLister(indexer),
	}
	result, errs := observeClusterCIDRs(listers, map[string]interface{}{})
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
	listers := Listers{
		configmapLister: corelistersv1.NewConfigMapLister(indexer),
	}
	result, errs := observeServiceClusterIPRanges(listers, map[string]interface{}{})
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
