package serviceca

import (
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/configobserver"
	"github.com/openshift/library-go/pkg/operator/events"
)

const (
	serviceCAConfigMapName = "service-ca"
	serviceCABundleKey     = "ca-bundle.crt"
	serviceCAFilePath      = "/etc/kubernetes/static-pod-resources/configmaps/service-ca/ca-bundle.crt"
)

// ObserveServiceCA fills in serviceServingCert.CertFile with the path for a configMap containing the service-ca.crt
func ObserveServiceCA(genericListers configobserver.Listers, recorder events.Recorder, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	listers := genericListers.(configobservation.Listers)
	errs := []error{}
	prevObservedConfig := map[string]interface{}{}

	topLevelServiceCAFilePath := []string{"serviceServingCert", "certFile"}
	currentServiceCAFilePath, _, err := unstructured.NestedString(existingConfig, topLevelServiceCAFilePath...)
	if err != nil {
		errs = append(errs, err)
	}
	if len(currentServiceCAFilePath) > 0 {
		if err := unstructured.SetNestedField(prevObservedConfig, currentServiceCAFilePath, topLevelServiceCAFilePath...); err != nil {
			errs = append(errs, err)
		}
	}

	observedConfig := map[string]interface{}{}

	// add service-ca configmap mount path to ServiceServingCert if configmap exists
	ca, err := listers.ConfigMapLister.ConfigMaps(operatorclient.TargetNamespace).Get(serviceCAConfigMapName)
	if errors.IsNotFound(err) {
		// do nothing because we aren't going to add a path to a missing configmap
		return observedConfig, errs
	}
	if err != nil {
		// we had an error, return what we had before and exit. this really shouldn't happen
		return prevObservedConfig, append(errs, err)
	}
	if len(ca.Data[serviceCABundleKey]) == 0 {
		// do nothing because aren't going to add a path to a configmap with no file
		return observedConfig, errs
	}
	// this means we have this configmap and it has values, so wire up the directory
	if err := unstructured.SetNestedField(observedConfig, serviceCAFilePath, topLevelServiceCAFilePath...); err != nil {
		recorder.Warningf("ObserveServiceCAConfigMap", "Failed setting serviceCAFile: %v", err)
		errs = append(errs, err)
	}
	if !equality.Semantic.DeepEqual(prevObservedConfig, observedConfig) {
		recorder.Event("ObserveServiceCAConfigMap", "observed change in config")
	}
	return observedConfig, errs
}
