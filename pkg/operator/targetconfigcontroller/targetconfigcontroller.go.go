package targetconfigcontroller

import (
	"fmt"
	"reflect"
	"time"

	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	operatorv1 "github.com/openshift/api/operator/v1"
	v1alpha13 "github.com/openshift/cluster-kube-controller-manager-operator/pkg/apis/kubecontrollermanager/v1alpha1"
	v1alpha1client "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/clientset/versioned/typed/kubecontrollermanager/v1alpha1"
	v1alpha12 "github.com/openshift/cluster-kube-controller-manager-operator/pkg/generated/informers/externalversions/kubecontrollermanager/v1alpha1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/version"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const workQueueKey = "key"

type TargetConfigController struct {
	targetImagePullSpec string

	operatorConfigClient v1alpha1client.KubecontrollermanagerV1alpha1Interface

	kubeClient      kubernetes.Interface
	configMapLister corev1listers.ConfigMapLister
	eventRecorder   events.Recorder

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewTargetConfigController(
	targetImagePullSpec string,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	operatorConfigInformer v1alpha12.KubeControllerManagerOperatorConfigInformer,
	namespacedKubeInformers informers.SharedInformerFactory,
	operatorConfigClient v1alpha1client.KubecontrollermanagerV1alpha1Interface,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
) *TargetConfigController {
	c := &TargetConfigController{
		targetImagePullSpec: targetImagePullSpec,

		configMapLister:      kubeInformersForNamespaces.ConfigMapLister(),
		operatorConfigClient: operatorConfigClient,
		kubeClient:           kubeClient,
		eventRecorder:        eventRecorder,

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "TargetConfigController"),
	}

	operatorConfigInformer.Informer().AddEventHandler(c.eventHandler())
	namespacedKubeInformers.Rbac().V1().Roles().Informer().AddEventHandler(c.eventHandler())
	namespacedKubeInformers.Rbac().V1().RoleBindings().Informer().AddEventHandler(c.eventHandler())
	namespacedKubeInformers.Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())
	namespacedKubeInformers.Core().V1().Secrets().Informer().AddEventHandler(c.eventHandler())
	namespacedKubeInformers.Core().V1().ServiceAccounts().Informer().AddEventHandler(c.eventHandler())
	namespacedKubeInformers.Core().V1().Services().Informer().AddEventHandler(c.eventHandler())

	// for config
	kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())
	kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer().AddEventHandler(c.eventHandler())

	// we only watch some namespaces
	namespacedKubeInformers.Core().V1().Namespaces().Informer().AddEventHandler(c.namespaceEventHandler())

	return c
}

func (c TargetConfigController) sync() error {
	operatorConfig, err := c.operatorConfigClient.KubeControllerManagerOperatorConfigs().Get("cluster", metav1.GetOptions{})
	if err != nil {
		return err
	}

	operatorConfigOriginal := operatorConfig.DeepCopy()

	switch operatorConfig.Spec.ManagementState {
	case operatorv1.Unmanaged:
		return nil

	case operatorv1.Removed:
		// TODO probably just fail
		return nil
	}

	requeue, err := createTargetConfigController(c, c.eventRecorder, operatorConfig)
	if requeue && err == nil {
		return fmt.Errorf("synthetic requeue request")
	}

	if err != nil {
		if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
			v1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1.OperatorCondition{
				Type:    operatorv1.OperatorStatusTypeFailing,
				Status:  operatorv1.ConditionTrue,
				Reason:  "StatusUpdateError",
				Message: err.Error(),
			})
			if _, updateError := c.operatorConfigClient.KubeControllerManagerOperatorConfigs().UpdateStatus(operatorConfig); updateError != nil {
				glog.Error(updateError)
			}
		}
		return err
	}

	return nil
}

// syncKubeControllerManager_v311_00_to_latest takes care of synchronizing (not upgrading) the thing we're managing.
// most of the time the sync method will be good for a large span of minor versions
func createTargetConfigController(c TargetConfigController, recorder events.Recorder, operatorConfig *v1alpha13.KubeControllerManagerOperatorConfig) (bool, error) {
	operatorConfigOriginal := operatorConfig.DeepCopy()
	errors := []error{}

	directResourceResults := resourceapply.ApplyDirectly(c.kubeClient, c.eventRecorder, v311_00_assets.Asset,
		"v3.11.0/kube-controller-manager/ns.yaml",
		"v3.11.0/kube-controller-manager/svc.yaml",
		"v3.11.0/kube-controller-manager/sa.yaml",
	)
	for _, currResult := range directResourceResults {
		if currResult.Error != nil {
			errors = append(errors, fmt.Errorf("%q (%T): %v", currResult.File, currResult.Type, currResult.Error))
		}
	}

	_, _, err := manageKubeControllerManagerConfig(c.kubeClient.CoreV1(), recorder, operatorConfig)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap", err))
	}
	_, _, err = manageServiceAccountCABundle(c.configMapLister, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/serviceaccount-ca", err))
	}
	_, _, err = managePod(c.kubeClient.CoreV1(), recorder, operatorConfig, c.targetImagePullSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/kube-controller-manager-pod", err))
	}

	if len(errors) > 0 {
		message := ""
		for _, err := range errors {
			message = message + err.Error() + "\n"
		}
		v1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1.OperatorCondition{
			Type:    "TargetConfigControllerFailing",
			Status:  operatorv1.ConditionTrue,
			Reason:  "SynchronizationError",
			Message: message,
		})
		if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
			_, updateError := c.operatorConfigClient.KubeControllerManagerOperatorConfigs().UpdateStatus(operatorConfig)
			return true, updateError
		}
		return true, nil
	}

	v1helpers.SetOperatorCondition(&operatorConfig.Status.Conditions, operatorv1.OperatorCondition{
		Type:   "TargetConfigControllerFailing",
		Status: operatorv1.ConditionFalse,
	})
	if !reflect.DeepEqual(operatorConfigOriginal, operatorConfig) {
		_, updateError := c.operatorConfigClient.KubeControllerManagerOperatorConfigs().UpdateStatus(operatorConfig)
		if updateError != nil {
			return true, updateError
		}
	}

	return false, nil
}

func manageKubeControllerManagerConfig(client corev1client.ConfigMapsGetter, recorder events.Recorder, operatorConfig *v1alpha13.KubeControllerManagerOperatorConfig) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/cm.yaml"))
	defaultConfig := v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/defaultconfig.yaml")
	requiredConfigMap, _, err := resourcemerge.MergeConfigMap(configMap, "config.yaml", nil, defaultConfig, operatorConfig.Spec.ObservedConfig.Raw, operatorConfig.Spec.UnsupportedConfigOverrides.Raw)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func managePod(client corev1client.ConfigMapsGetter, recorder events.Recorder, operatorConfig *v1alpha13.KubeControllerManagerOperatorConfig, imagePullSpec string) (*corev1.ConfigMap, bool, error) {
	required := resourceread.ReadPodV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/pod.yaml"))
	required.Spec.Containers[0].ImagePullPolicy = corev1.PullAlways
	if len(imagePullSpec) > 0 {
		required.Spec.Containers[0].Image = imagePullSpec
	}
	required.Spec.Containers[0].Args = append(required.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", 4))

	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/pod-cm.yaml"))
	configMap.Data["pod.yaml"] = resourceread.WritePodV1OrDie(required)
	configMap.Data["forceRedeploymentReason"] = operatorConfig.Spec.ForceRedeploymentReason
	configMap.Data["version"] = version.Get().String()
	return resourceapply.ApplyConfigMap(client, recorder, configMap)
}

func manageServiceAccountCABundle(lister corev1listers.ConfigMapLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "serviceaccount-ca"},
		lister, client, recorder,
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: "initial-serviceaccount-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: "csr-controller-ca"},
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "managed-kube-apiserver-serving-cert-signer"},
	)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

// Run starts the kube-controller-manager and blocks until stopCh is closed.
func (c *TargetConfigController) Run(workers int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()
	defer c.queue.ShutDown()

	glog.Infof("Starting TargetConfigController")
	defer glog.Infof("Shutting down TargetConfigController")

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	<-stopCh
}

func (c *TargetConfigController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *TargetConfigController) processNextWorkItem() bool {
	dsKey, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(dsKey)

	err := c.sync()
	if err == nil {
		c.queue.Forget(dsKey)
		return true
	}

	runtime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *TargetConfigController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}

// this set of namespaces will include things like logging and metrics which are used to drive
var interestingNamespaces = sets.NewString(operatorclient.TargetNamespace)

func (c *TargetConfigController) namespaceEventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				c.queue.Add(workQueueKey)
			}
			if ns.Name == operatorclient.TargetNamespace {
				c.queue.Add(workQueueKey)
			}
		},
		UpdateFunc: func(old, new interface{}) {
			ns, ok := old.(*corev1.Namespace)
			if !ok {
				c.queue.Add(workQueueKey)
			}
			if ns.Name == operatorclient.TargetNamespace {
				c.queue.Add(workQueueKey)
			}
		},
		DeleteFunc: func(obj interface{}) {
			ns, ok := obj.(*corev1.Namespace)
			if !ok {
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					runtime.HandleError(fmt.Errorf("couldn't get object from tombstone %#v", obj))
					return
				}
				ns, ok = tombstone.Obj.(*corev1.Namespace)
				if !ok {
					runtime.HandleError(fmt.Errorf("tombstone contained object that is not a Namespace %#v", obj))
					return
				}
			}
			if ns.Name == operatorclient.TargetNamespace {
				c.queue.Add(workQueueKey)
			}
		},
	}
}
