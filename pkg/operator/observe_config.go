package operator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/golang/glog"
	"github.com/imdario/mergo"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/diff"
	"k8s.io/apimachinery/pkg/util/rand"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	corelistersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/workqueue"

	"github.com/openshift/api/operator/v1alpha1"
	operatorconfigclientv1alpha1 "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/clientset/versioned/typed/kubecontrollermanager/v1alpha1"
	operatorconfiginformerv1alpha1 "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/informers/externalversions/kubecontrollermanager/v1alpha1"
	"github.com/openshift/library-go/pkg/operator/v1alpha1helpers"
)

const configObservationErrorConditionReason = "ConfigObservationError"

type Listers struct {
	configmapLister corelistersv1.ConfigMapLister
}

// observeConfigFunc observes configuration and returns the observedConfig. This function should not return an
// observedConfig that would cause the service being managed by the operator to crash. For example, if a required
// configuration key cannot be observed, consider reusing the configuration key's previous value. Errors that occur
// while attempting to generate the observedConfig should be returned in the errs slice.
type observeConfigFunc func(listers Listers, existingConfig map[string]interface{}) (observedConfig map[string]interface{}, errs []error)

type ConfigObserver struct {
	operatorConfigClient operatorconfigclientv1alpha1.KubecontrollermanagerV1alpha1Interface

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface

	// observers are called in an undefined order and their results are merged to
	// determine the observed configuration.
	observers []observeConfigFunc

	rateLimiter  flowcontrol.RateLimiter
	listers      Listers
	cachesSynced []cache.InformerSynced
}

func NewConfigObserver(
	operatorConfigInformer operatorconfiginformerv1alpha1.KubeControllerManagerOperatorConfigInformer,
	kubeInformersForOpenShiftKubeControllerManagerNamespace informers.SharedInformerFactory,
	kubeInformersForKubeSystemNamespace informers.SharedInformerFactory,
	operatorConfigClient operatorconfigclientv1alpha1.KubecontrollermanagerV1alpha1Interface,
) *ConfigObserver {
	c := &ConfigObserver{
		operatorConfigClient: operatorConfigClient,
		queue:                workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "ConfigObserver"),
		rateLimiter:          flowcontrol.NewTokenBucketRateLimiter(0.05 /*3 per minute*/, 4),
		observers: []observeConfigFunc{
			observeCloudProviderNames,
		},
		listers: Listers{
			configmapLister: kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Lister(),
		},
		cachesSynced: []cache.InformerSynced{
			kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().HasSynced,
		},
	}

	operatorConfigInformer.Informer().AddEventHandler(c.eventHandler())
	kubeInformersForOpenShiftKubeControllerManagerNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForKubeSystemNamespace.Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())
	return c
}

// sync reacts to a change in controller manager images.
func (c ConfigObserver) sync() error {

	operatorConfig, err := c.operatorConfigClient.KubeControllerManagerOperatorConfigs().Get("instance", metav1.GetOptions{})
	if err != nil {
		return err
	}

	// don't worry about errors
	currentConfig := map[string]interface{}{}
	json.NewDecoder(bytes.NewBuffer(operatorConfig.Spec.ObservedConfig.Raw)).Decode(&currentConfig)

	var (
		errs            []error
		observedConfigs []map[string]interface{}
	)
	for _, i := range rand.Perm(len(c.observers)) {
		observedConfig, currErrs := c.observers[i](c.listers, currentConfig)
		observedConfigs = append(observedConfigs, observedConfig)
		errs = append(errs, currErrs...)
	}

	mergedObservedConfig := map[string]interface{}{}
	for _, observedConfig := range observedConfigs {
		mergo.Merge(&mergedObservedConfig, observedConfig)
	}

	if !equality.Semantic.DeepEqual(currentConfig, mergedObservedConfig) {
		glog.Infof("writing updated observedConfig: %v", diff.ObjectDiff(operatorConfig.Spec.ObservedConfig.Object, mergedObservedConfig))
		operatorConfig.Spec.ObservedConfig = runtime.RawExtension{Object: &unstructured.Unstructured{Object: mergedObservedConfig}}
		updatedOperatorConfig, err := c.operatorConfigClient.KubeControllerManagerOperatorConfigs().Update(operatorConfig)
		if err != nil {
			errs = append(errs, fmt.Errorf("kubecontrollermanageroperatorconfigs/instance: error writing updated observed config: %v", err))
		} else {
			operatorConfig = updatedOperatorConfig
		}
	}

	status := operatorConfig.Status.DeepCopy()
	if len(errs) > 0 {
		var messages []string
		for _, currentError := range errs {
			messages = append(messages, currentError.Error())
		}
		v1alpha1helpers.SetOperatorCondition(&status.Conditions, v1alpha1.OperatorCondition{
			Type:    v1alpha1.OperatorStatusTypeFailing,
			Status:  v1alpha1.ConditionTrue,
			Reason:  configObservationErrorConditionReason,
			Message: strings.Join(messages, "\n"),
		})
	} else {
		condition := v1alpha1helpers.FindOperatorCondition(status.Conditions, v1alpha1.OperatorStatusTypeFailing)
		if condition != nil && condition.Status != v1alpha1.ConditionFalse && condition.Reason == configObservationErrorConditionReason {
			condition.Status = v1alpha1.ConditionFalse
			condition.Reason = ""
			condition.Message = ""
		}
	}

	if !equality.Semantic.DeepEqual(operatorConfig.Status, status) {
		operatorConfig.Status = *status
		_, err = c.operatorConfigClient.KubeControllerManagerOperatorConfigs().UpdateStatus(operatorConfig)
		if err != nil {
			return err
		}
	}

	return nil
}

// observeCloudProviderNames observes cloud provider configuration from
// cluster-config-v1 in order to configure kube-controller-manager's cloud
// provider.
func observeCloudProviderNames(listers Listers, existingConfig map[string]interface{}) (map[string]interface{}, []error) {
	var errs []error
	cloudProvidersPath := []string{"extendedArguments", "cloud-provider"}

	previouslyObservedConfig := map[string]interface{}{}
	if currentCloudProvider, _, _ := unstructured.NestedStringSlice(existingConfig, cloudProvidersPath...); len(currentCloudProvider) > 0 {
		unstructured.SetNestedStringSlice(previouslyObservedConfig, currentCloudProvider, cloudProvidersPath...)
	}

	observedConfig := map[string]interface{}{}
	clusterConfig, err := listers.configmapLister.ConfigMaps("kube-system").Get("cluster-config-v1")
	if errors.IsNotFound(err) {
		glog.Warning("configmap/cluster-config-v1.kube-system: not found")
		return observedConfig, errs
	}
	if err != nil {
		errs = append(errs, err)
		return previouslyObservedConfig, errs
	}

	installConfigYaml, ok := clusterConfig.Data["install-config"]
	if !ok {
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: install-config not found"))
		return previouslyObservedConfig, errs
	}
	installConfig := map[string]interface{}{}
	err = yaml.Unmarshal([]byte(installConfigYaml), &installConfig)
	if err != nil {
		glog.Warningf("Unable to parse install-config: %s", err)
		return previouslyObservedConfig, errs
	}

	// extract needed values
	//  data:
	//   install-config:
	//     platform:
	//       aws: {}
	// only aws supported for now
	cloudProvider := ""
	platform, ok := installConfig["platform"].(map[string]interface{})
	switch {
	case !ok:
		glog.Warning("configmap/cluster-config-v1.kube-system: install-config/platform not found")
		return previouslyObservedConfig, errs
	case platform["aws"] != nil:
		cloudProvider = "aws"
	default:
		errs = append(errs, fmt.Errorf("configmap/cluster-config-v1.kube-system: no recognized cloud provider platform found"))
		return previouslyObservedConfig, errs
	}

	// set observed values
	//  extendedArguments:
	//    cloud-provider:
	//    - "name"
	unstructured.SetNestedStringSlice(observedConfig, []string{cloudProvider}, cloudProvidersPath...)

	return observedConfig, errs
}

func (c *ConfigObserver) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Infof("Starting ConfigObserver")
	defer glog.Infof("Shutting down ConfigObserver")

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *ConfigObserver) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *ConfigObserver) processNextWorkItem() bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

	// before we call sync, we want to wait for token.  We do this to avoid hot looping.
	c.rateLimiter.Accept()

	err := c.sync()
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *ConfigObserver) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}
