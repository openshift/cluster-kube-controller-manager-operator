package recoverycontroller

import (
	"context"
	"fmt"
	"time"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	operatorresourcesync "github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/resourcesynccontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/targetconfigcontroller"
)

const workQueueKey = "key"

// CSRController composes CSR signers that are needed to sign kubelet CSRs so it can
// login to apiserver and start running pods.
type CSRController struct {
	kubeClient kubernetes.Interface

	secretLister    corev1listers.SecretLister
	configMapLister corev1listers.ConfigMapLister

	eventRecorder events.Recorder

	cachesToSync []cache.InformerSynced

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface

	resourceSyncController *resourcesynccontroller.ResourceSyncController
}

func NewCSRController(
	kubeClient kubernetes.Interface,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	operatorClient v1helpers.StaticPodOperatorClient,
	eventRecorder events.Recorder,
) (*CSRController, error) {
	c := &CSRController{
		kubeClient:      kubeClient,
		secretLister:    kubeInformersForNamespaces.SecretLister(),
		configMapLister: kubeInformersForNamespaces.ConfigMapLister(),
		eventRecorder:   eventRecorder.WithComponentSuffix("csr-controller"),
		queue:           workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "CSRRecoveryController"),
	}

	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}

	// we react to some config changes
	namespaces := []string{
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.OperatorNamespace,
		operatorclient.TargetNamespace,
	}
	for _, namespace := range namespaces {
		informers := kubeInformersForNamespaces.InformersFor(namespace)
		informers.Core().V1().ConfigMaps().Informer().AddEventHandler(handler)
		c.cachesToSync = append(c.cachesToSync, informers.Core().V1().ConfigMaps().Informer().HasSynced)
		informers.Core().V1().Secrets().Informer().AddEventHandler(handler)
		c.cachesToSync = append(c.cachesToSync, informers.Core().V1().Secrets().Informer().HasSynced)
	}

	c.resourceSyncController = resourcesynccontroller.NewResourceSyncController(
		operatorClient,
		kubeInformersForNamespaces,
		v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		c.eventRecorder,
	)
	err := operatorresourcesync.AddSyncCSRControllerCA(c.resourceSyncController)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *CSRController) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()

	klog.Info("Starting CSR controller")
	defer func() {
		klog.Info("Shutting down CSR controller")
		c.queue.ShutDown()
		klog.Info("CSR controller shut down")
	}()

	if !cache.WaitForNamedCacheSync("CSRController", ctx.Done(), c.cachesToSync...) {
		return
	}

	// FIXME: These are missing a wait group to track goroutines and handle graceful termination
	// (@deads2k wants time to think it through)

	go func() {
		wait.UntilWithContext(ctx, c.runWorker, time.Second)
	}()

	go func() {
		c.resourceSyncController.Run(ctx, 1)
	}()

	<-ctx.Done()
}

func (c *CSRController) runWorker(ctx context.Context) {
	for c.processNextItem(ctx) {
	}
}

func (c *CSRController) processNextItem(ctx context.Context) bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)

	err := c.sync(ctx)

	if err == nil {
		c.queue.Forget(key)
		return true
	}

	utilruntime.HandleError(fmt.Errorf("%v failed with : %w", key, err))
	c.queue.AddRateLimited(key)

	return true
}

func (c *CSRController) sync(ctx context.Context) error {
	klog.V(4).Infof("Starting CSRController sync")
	defer klog.V(4).Infof("CSRController sync done")

	// Always start 10 seconds later after a change occurred. Makes us less likely to steal work and logs from the operator.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return nil
	}

	_, changed, err := targetconfigcontroller.ManageCSRIntermediateCABundle(c.secretLister, c.kubeClient.CoreV1(), c.eventRecorder)
	if err != nil {
		return err
	}
	if changed {
		klog.Info("Refreshed CSRIntermediateCABundle.")
	}

	_, changed, err = targetconfigcontroller.ManageCSRCABundle(c.configMapLister, c.kubeClient.CoreV1(), c.eventRecorder)
	if err != nil {
		return err
	}
	if changed {
		klog.Info("Refreshed CSRCABundle.")
	}

	_, requeueDelay, changed, err := targetconfigcontroller.ManageCSRSigner(c.secretLister, c.kubeClient.CoreV1(), c.eventRecorder)
	if err != nil {
		return err
	}
	if requeueDelay > 0 {
		c.queue.AddAfter(workQueueKey, requeueDelay)
	}
	if changed {
		klog.V(2).Info("Refreshed CSRSigner.")
	}

	return nil
}
