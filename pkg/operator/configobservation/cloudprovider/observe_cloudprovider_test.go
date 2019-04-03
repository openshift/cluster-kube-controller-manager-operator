package cloudprovider

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
)

func TestObserveCloudProviderNames(t *testing.T) {
	cases := []struct {
		platform           configv1.PlatformType
		expected           string
		cloudProviderCount int
	}{{
		platform:           configv1.AWSPlatformType,
		expected:           "aws",
		cloudProviderCount: 1,
	}, {
		platform:           configv1.LibvirtPlatformType,
		cloudProviderCount: 0,
	}, {
		platform:           configv1.OpenStackPlatformType,
		cloudProviderCount: 0,
	}, {
		platform:           configv1.GCPPlatformType,
		cloudProviderCount: 0,
	}, {
		platform:           configv1.NonePlatformType,
		cloudProviderCount: 0,
	}, {
		platform:           "",
		cloudProviderCount: 0,
	}}
	for _, c := range cases {
		t.Run(string(c.platform), func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := indexer.Add(&configv1.Infrastructure{ObjectMeta: v1.ObjectMeta{Name: "cluster"}, Status: configv1.InfrastructureStatus{Platform: c.platform}}); err != nil {
				t.Fatal(err.Error())
			}
			listers := configobservation.Listers{
				InfrastructureLister: configlistersv1.NewInfrastructureLister(indexer),
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
				if e, a := c.expected, cloudProvider[0]; e != a {
					t.Errorf("expected cloud-provider=%s, got %s", e, a)
				}
			}
		})
	}
}
