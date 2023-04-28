package cloud

import (
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"

	configv1 "github.com/openshift/api/config/v1"
	configlistersv1 "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/ghodss/yaml"
)

func TestObserveCloudVolumePlugin(t *testing.T) {
	defaultFeatureGate := featuregates.NewHardcodedFeatureGateAccess(
		[]configv1.FeatureGateName{},
		[]configv1.FeatureGateName{
			configv1.FeatureGateExternalCloudProvider,
			configv1.FeatureGateExternalCloudProviderAzure,
			configv1.FeatureGateExternalCloudProviderGCP,
		},
	)

	type Test struct {
		name                string
		platform            configv1.PlatformType
		featureGateAccessor featuregates.FeatureGateAccess
		input, expected     map[string]interface{}
		expectedError       bool
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
			"Default FG, on tech preview platform (Azure) pre CSI migration",
			configv1.AzurePlatformType,
			defaultFeatureGate,
			map[string]interface{}{},
			map[string]interface{}{},
			false,
		},
		{
			"With FG, on Tech Preview platform (Azure)",
			configv1.AzurePlatformType,
			featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{configv1.FeatureGateExternalCloudProvider},
				[]configv1.FeatureGateName{},
			),
			map[string]interface{}{},
			map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"external-cloud-volume-plugin": []interface{}{"azure"},
				}},
			false,
		},
		{
			"With FG removed, on Tech Preview platform (Azure)",
			configv1.AzurePlatformType,
			defaultFeatureGate,
			map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"external-cloud-volume-plugin": []interface{}{"azure"},
				}},
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
			featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{configv1.FeatureGateExternalCloudProvider},
				[]configv1.FeatureGateName{},
			),
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
			featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{configv1.FeatureGateExternalCloudProvider},
				[]configv1.FeatureGateName{},
			),
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

			featureGates := featuregates.NewHardcodedFeatureGateAccess(
				[]configv1.FeatureGateName{},
				[]configv1.FeatureGateName{},
			)
			if test.featureGateAccessor != nil {
				featureGates = test.featureGateAccessor
			}

			listers := configobservation.Listers{
				InfrastructureLister_: configlistersv1.NewInfrastructureLister(infraIndexer),
			}

			result, errs := NewObserveCloudVolumePluginFunc(featureGates)(listers, events.NewInMemoryRecorder("cloud"), test.input)
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
