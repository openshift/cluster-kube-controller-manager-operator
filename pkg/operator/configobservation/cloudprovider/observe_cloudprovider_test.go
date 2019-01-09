package cloudprovider

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
)

func TestObserveCloudProviderNames(t *testing.T) {
	cases := []struct {
		installConfig         string
		expectedCloudProvider string
		cloudProviderCount    int
	}{{
		installConfig:         "platform:\n  aws: {}\n",
		expectedCloudProvider: "aws",
		cloudProviderCount:    1,
	}, {
		installConfig:      "platform:\n  libvirt: {}\n",
		cloudProviderCount: 0,
	}, {
		installConfig:      "platform:\n  none: {}\n",
		cloudProviderCount: 0,
	}}
	for idx, c := range cases {
		t.Logf("Testing case #%d", idx+1)
		indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
		if err := indexer.Add(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-config-v1",
				Namespace: "kube-system",
			},
			Data: map[string]string{
				"install-config": c.installConfig,
			},
		}); err != nil {
			t.Fatal(err.Error())
		}
		listers := configobservation.Listers{
			ConfigmapLister: corelistersv1.NewConfigMapLister(indexer),
		}
		result, errs := ObserveCloudProviderNames(listers, events.NewInMemoryRecorder("cloud"), map[string]interface{}{})
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		cloudProvider, _, err := unstructured.NestedSlice(result, "extendedArguments", "cloud-provider")
		if err != nil {
			t.Fatal(err)
		}
		if e, a := c.cloudProviderCount, len(cloudProvider); e != a {
			t.Fatalf("expected len(cloudProvider) == %d, got %d", e, a)
		}
		if c.cloudProviderCount > 0 {
			if e, a := c.expectedCloudProvider, cloudProvider[0]; e != a {
				t.Errorf("expected cloud-provider=%s, got %s", e, a)
			}
		}
	}
}
