package cloudprovider

import (
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"
)

func TestObserveCloudProviderNames(t *testing.T) {
	cases := []struct {
		infrastructure        *configv1.Infrastructure
		installConfig         string
		expectedCloudProvider string
		cloudProviderCount    int
	}{
		{
			infrastructure: &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.InfrastructureStatus{
					Platform: configv1.AWSPlatform,
				},
			},
			installConfig:         "platform:\n  AWS: {}\n",
			expectedCloudProvider: "aws",
			cloudProviderCount:    1,
		},
		{
			infrastructure: &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.InfrastructureStatus{
					Platform: configv1.LibvirtPlatform,
				},
			},
			installConfig:      "platform:\n  libvirt: {}\n",
			cloudProviderCount: 0,
		},
		{
			infrastructure: &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.InfrastructureStatus{},
			},
			installConfig:      "platform:\n  None: {}\n",
			cloudProviderCount: 0,
		},
	}
	for idx, tc := range cases {
		t.Run(fmt.Sprintf("%d", idx), func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			indexer.Add(tc.infrastructure)
			listers := configobservation.Listers{
				InfrastructureLister: configlistersv1.NewInfrastructureLister(indexer),
				InfrastructureSynced: func() bool { return true },
			}
			result, errs := ObserveCloudProviderNames(listers, events.NewInMemoryRecorder("cloud"), map[string]interface{}{})
			if len(errs) != 0 {
				t.Fatalf("unexpected error: %v", errs)
			}
			cloudProvider, _, err := unstructured.NestedSlice(result, "extendedArguments", "cloud-provider")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if e, a := tc.cloudProviderCount, len(cloudProvider); e != a {
				t.Fatalf("expected len(cloudProvider) == %d, got %d", e, a)
			}
			if tc.cloudProviderCount > 0 {
				if e, a := tc.expectedCloudProvider, cloudProvider[0]; e != a {
					t.Errorf("expected cloud-provider=%s, got %s", e, a)
				}
			}
		})
	}
}
