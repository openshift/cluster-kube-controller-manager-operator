package cloud

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/cloudprovider"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/ghodss/yaml"
)

func TestObserveCloudVolumePlugin(t *testing.T) {
	defaultFeatureGate := &configv1.FeatureGate{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: configv1.FeatureGateSpec{
			FeatureGateSelection: configv1.FeatureGateSelection{
				FeatureSet: configv1.Default,
			},
		},
	}

	type Test struct {
		name            string
		platform        configv1.PlatformType
		featureGate     *configv1.FeatureGate
		input, expected map[string]interface{}
		expectedError   bool
	}
	tests := []Test{
		{
			"Default FG, on GA platform (AWS) post CSI migration",
			configv1.AWSPlatformType,
			defaultFeatureGate,
			map[string]interface{}{},
			map[string]interface{}{},
			false,
		},
		{
			"Default FG, on GA platform (Azure) pre CSI migration",
			configv1.AzurePlatformType,
			defaultFeatureGate,
			map[string]interface{}{},
			map[string]interface{}{},
			false,
		},
		{
			"Default FG, on tech preview platform (GCP) pre CSI migration",
			configv1.GCPPlatformType,
			defaultFeatureGate,
			map[string]interface{}{},
			map[string]interface{}{},
			false,
		},
		{
			"With FG, on Tech Preview platform (GCP)",
			configv1.GCPPlatformType,
			&configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.CustomNoUpgrade,
						CustomNoUpgrade: &configv1.CustomFeatureGates{
							Enabled: []string{cloudprovider.ExternalCloudProviderFeature},
						},
					},
				},
			},
			map[string]interface{}{},
			map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"external-cloud-volume-plugin": []interface{}{"gce"},
				}},
			false,
		},
		{
			"With FG removed, on Tech Preview platform (GCP)",
			configv1.GCPPlatformType,
			defaultFeatureGate,
			map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"external-cloud-volume-plugin": []interface{}{"gce"},
				}},
			map[string]interface{}{},
			false,
		},
		{
			"With FG, on unsupported platform (libvirt)",
			configv1.LibvirtPlatformType,
			&configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.FeatureGateSpec{
					FeatureGateSelection: configv1.FeatureGateSelection{
						FeatureSet: configv1.CustomNoUpgrade,
						CustomNoUpgrade: &configv1.CustomFeatureGates{
							Enabled: []string{cloudprovider.ExternalCloudProviderFeature},
						},
					},
				},
			},
			map[string]interface{}{},
			map[string]interface{}{},
			false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			infra := &configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.InfrastructureStatus{
					PlatformStatus: &configv1.PlatformStatus{
						Type: test.platform,
					},
				},
			}
			infraIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := infraIndexer.Add(infra); err != nil {
				t.Fatal(err.Error())
			}

			fgIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if test.featureGate != nil {
				if err := fgIndexer.Add(test.featureGate); err != nil {
					t.Fatal(err.Error())
				}
			}

			listers := configobservation.Listers{
				InfrastructureLister_: configlistersv1.NewInfrastructureLister(infraIndexer),
				FeatureGateLister_:    configlistersv1.NewFeatureGateLister(fgIndexer),
			}
			result, errs := ObserveCloudVolumePlugin(listers, events.NewInMemoryRecorder("cloud"), test.input)
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

func toYAML(o interface{}) string {
	b, e := yaml.Marshal(o)
	if e != nil {
		return e.Error()
	}
	return string(b)
}
