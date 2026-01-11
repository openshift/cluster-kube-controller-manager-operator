package network

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/ghodss/yaml"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
)

func TestObserveClusterCIDRs(t *testing.T) {
	type Test struct {
		name            string
		config          *configv1.Network
		input, expected map[string]interface{}
		expectedError   bool
	}
	tests := []Test{
		{
			"single cluster network",
			&configv1.Network{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status: configv1.NetworkStatus{ClusterNetwork: []configv1.ClusterNetworkEntry{
					{CIDR: "podCIDR"},
				}},
			},
			map[string]interface{}{},
			map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"cluster-cidr": []interface{}{"podCIDR"},
				},
			},
			false,
		},
		{
			"clusterNetworks",
			&configv1.Network{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
				Status: configv1.NetworkStatus{ClusterNetwork: []configv1.ClusterNetworkEntry{
					{CIDR: "podCIDR1"}, {CIDR: "podCIDR2"},
				}},
			},
			map[string]interface{}{},
			map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"cluster-cidr": []interface{}{
						"podCIDR1,podCIDR2",
					},
				},
			},
			false,
		},
		{
			"none, no old config",
			&configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			map[string]interface{}{},
			map[string]interface{}{},
			true,
		},
		{
			"none, existing config",
			&configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
			map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"cluster-cidr": []interface{}{
						"oldPodCIDR",
					},
				},
			},
			map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"cluster-cidr": []interface{}{
						"oldPodCIDR",
					},
				},
			},
			true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := indexer.Add(test.config); err != nil {
				t.Fatal(err.Error())
			}
			listers := configobservation.Listers{
				NetworkLister: configlistersv1.NewNetworkLister(indexer),
			}
			result, errs := ObserveClusterCIDRs(listers, events.NewInMemoryRecorder("network", clock.RealClock{}), map[string]interface{}{})
			if len(errs) > 0 && !test.expectedError {
				t.Fatal(errs)
			} else if len(errs) == 0 {
				if test.expectedError {
					t.Fatalf("expected error, but got none")
				}
				if !reflect.DeepEqual(test.expected, result) {
					t.Errorf("\n===== observed config expected:\n%v\n===== observed config actual:\n%v", toYAML(test.expected), toYAML(result))
				}
			}
		})
	}
}

func TestObserveServiceClusterIPRanges(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if err := indexer.Add(&configv1.Network{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}, Status: configv1.NetworkStatus{ServiceNetwork: []string{"serviceCIDRv4", "serviceCIDRv6"}}}); err != nil {
		t.Fatal(err.Error())
	}
	listers := configobservation.Listers{
		NetworkLister: configlistersv1.NewNetworkLister(indexer),
	}
	result, errs := ObserveServiceClusterIPRanges(listers, events.NewInMemoryRecorder("network", clock.RealClock{}), map[string]interface{}{})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	expected := map[string]interface{}{
		"extendedArguments": map[string]interface{}{
			"service-cluster-ip-range": []interface{}{
				"serviceCIDRv4,serviceCIDRv6",
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
