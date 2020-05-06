package certrotationcontroller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"reflect"
	"strings"
	"time"

	"k8s.io/klog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/keyutil"
	"k8s.io/client-go/util/workqueue"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	workQueueKey = "key"

	saTokenReadyTimeAnnotation = "kube-controller-manager.openshift.io/ready-to-use"
)

type SATokenSignerController struct {
	ctx             context.Context
	operatorClient  v1helpers.StaticPodOperatorClient
	secretClient    corev1client.SecretsGetter
	configMapClient corev1client.ConfigMapsGetter
	endpointClient  corev1client.EndpointsGetter
	podClient       corev1client.PodsGetter
	eventRecorder   events.Recorder

	confirmedBootstrapNodeGone bool
	cachesSynced               []cache.InformerSynced

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewSATokenSignerController(
	ctx context.Context,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,

) (*SATokenSignerController, error) {

	ret := &SATokenSignerController{
		ctx:             ctx,
		operatorClient:  operatorClient,
		secretClient:    v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		configMapClient: v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		endpointClient:  kubeClient.CoreV1(),
		podClient:       kubeClient.CoreV1(),
		eventRecorder:   eventRecorder.WithComponentSuffix("sa-token-signer-controller"),

		cachesSynced: []cache.InformerSynced{
			kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().Secrets().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer().HasSynced,
			operatorClient.Informer().HasSynced,
		},

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "SATokenSignerController"),
	}

	kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().Secrets().Informer().AddEventHandler(ret.eventHandler())
	kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer().AddEventHandler(ret.eventHandler())
	kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Informer().AddEventHandler(ret.eventHandler())
	kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer().AddEventHandler(ret.eventHandler())
	operatorClient.Informer().AddEventHandler(ret.eventHandler())

	return ret, nil
}

func (c *SATokenSignerController) sync() error {

	syncErr := c.syncWorker()

	condition := operatorv1.OperatorCondition{
		Type:   "SATokenSignerDegraded",
		Status: operatorv1.ConditionFalse,
	}
	if syncErr != nil && !isUnexpectedAddressesError(syncErr) {
		condition.Status = operatorv1.ConditionTrue
		condition.Reason = "Error"
		condition.Message = syncErr.Error()
	}
	if _, _, updateErr := v1helpers.UpdateStatus(c.operatorClient, v1helpers.UpdateConditionFn(condition)); updateErr != nil {
		return updateErr
	}

	return syncErr
}

type unexpectedAddressesError struct {
	message string
}

func (e *unexpectedAddressesError) Error() string {
	return e.message
}

func isUnexpectedAddressesError(err error) bool {
	_, ok := err.(*unexpectedAddressesError)
	return ok
}

// we cannot rotate before the bootstrap server goes away because doing so would mean the bootstrap server would reject
// tokens that should be valid.  To test this, we go through kubernetes.default.svc endpoints and see if any of them
// are not in the list of known pod hosts.  We only have to do this once because the bootstrap node never comes back
func (c *SATokenSignerController) isPastBootstrapNode() error {
	if c.confirmedBootstrapNodeGone {
		return nil
	}

	nodeIPs := sets.String{}
	apiServerPods, err := c.podClient.Pods("openshift-kube-apiserver").List(c.ctx, metav1.ListOptions{LabelSelector: "app=openshift-kube-apiserver"})
	if err != nil {
		return err
	}
	for _, pod := range apiServerPods.Items {
		nodeIPs.Insert(pod.Status.HostIP)
	}

	kubeEndpoints, err := c.endpointClient.Endpoints("default").Get(c.ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(kubeEndpoints.Subsets) == 0 {
		err := fmt.Errorf("missing kubernetes endpoints subsets")
		c.eventRecorder.Warning("SATokenSignerControllerStuck", err.Error())
		return err
	}
	unexpectedEndpoints := sets.String{}
	for _, subset := range kubeEndpoints.Subsets {
		for _, address := range subset.Addresses {
			if !nodeIPs.Has(address.IP) {
				unexpectedEndpoints.Insert(address.IP)
			}
		}
	}
	if len(unexpectedEndpoints) != 0 {
		err := &unexpectedAddressesError{message: fmt.Sprintf("unexpected addresses: %v", strings.Join(unexpectedEndpoints.List(), ","))}
		c.eventRecorder.Event("SATokenSignerControllerStuck", err.Error())
		return err
	}

	// we have confirmed that the bootstrap node is gone
	c.eventRecorder.Event("SATokenSignerControllerOK", "found expected kube-apiserver endpoints")
	c.confirmedBootstrapNodeGone = true
	return nil
}

func (c *SATokenSignerController) syncWorker() error {
	if pastBootstrapErr := c.isPastBootstrapNode(); pastBootstrapErr != nil {
		// if we are not past bootstrapping, then if we're missing the service-account-private-key we need to prime it from the
		// initial provided by the installer.
		_, err := c.secretClient.Secrets(operatorclient.TargetNamespace).Get(c.ctx, "service-account-private-key", metav1.GetOptions{})
		if err == nil {
			// return this error to be reported and requeue
			return pastBootstrapErr
		}
		if err != nil && !errors.IsNotFound(err) {
			return err
		}
		// at this point we have not-found condition, sync the original
		_, _, err = resourceapply.SyncSecret(c.secretClient, c.eventRecorder,
			operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-service-account-private-key",
			operatorclient.TargetNamespace, "service-account-private-key", []metav1.OwnerReference{})
		return err
	}

	needNewSATokenSigningKey := false
	saTokenSigner, err := c.secretClient.Secrets(operatorclient.OperatorNamespace).Get(c.ctx, "next-service-account-private-key", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		needNewSATokenSigningKey = true
	} else if err != nil {
		return err
	} else {
		err := checkKeyPairValidity(saTokenSigner.Data["service-account.pub"], saTokenSigner.Data["service-account.key"])
		if err != nil {
			klog.Errorf("key pair is invalid: %v", err)
			needNewSATokenSigningKey = true
		}
	}

	if needNewSATokenSigningKey {
		rsaKey, err := rsa.GenerateKey(rand.Reader, keySize)
		if err != nil {
			return err
		}
		publicBytes, err := publicKeyToPem(&rsaKey.PublicKey)
		if err != nil {
			return err
		}

		saTokenSigner = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: operatorclient.OperatorNamespace, Name: "next-service-account-private-key",
				Annotations: map[string]string{saTokenReadyTimeAnnotation: time.Now().Add(5 * time.Minute).Format(time.RFC3339)},
			},
			Data: map[string][]byte{
				"service-account.key": privateKeyToPem(rsaKey),
				"service-account.pub": publicBytes,
			},
		}

		saTokenSigner, _, err = resourceapply.ApplySecret(c.secretClient, c.eventRecorder, saTokenSigner)
		if err != nil {
			return err
		}
		// requeue for after we should have recovered
		c.queue.AddAfter(workQueueKey, 5*time.Minute+10*time.Second)
	}

	saTokenSigningCerts, err := c.configMapClient.ConfigMaps(operatorclient.GlobalMachineSpecifiedConfigNamespace).Get(c.ctx, "sa-token-signing-certs", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	if errors.IsNotFound(err) {
		saTokenSigningCerts = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "sa-token-signing-certs"},
			Data:       map[string]string{},
		}
	}
	currPublicKey := string(saTokenSigner.Data["service-account.pub"])
	hasThisPublicKey := false
	for _, value := range saTokenSigningCerts.Data {
		if value == currPublicKey {
			hasThisPublicKey = true
		}
	}
	if !hasThisPublicKey {
		saTokenSigningCerts.Data[fmt.Sprintf("service-account-%03d.pub", len(saTokenSigningCerts.Data)+1)] = currPublicKey
		saTokenSigningCerts, _, err = resourceapply.ApplyConfigMap(c.configMapClient, c.eventRecorder, saTokenSigningCerts)
		if err != nil {
			return err
		}
	}

	// now check to see if the next-sa-private-key has been around long enough to be promoted.  We're waiting for the kube-apiserver
	// to pick up the change
	// TODO have a better signal for determining the level of cert trust.  This is a general problem for observing our cycles.
	readyToPromote := false
	saTokenReadyTime := saTokenSigner.Annotations[saTokenReadyTimeAnnotation]
	if len(saTokenReadyTime) == 0 {
		readyToPromote = true
	}
	promotionTime, err := time.Parse(time.RFC3339, saTokenReadyTime)
	if err != nil {
		readyToPromote = true
	}
	if time.Now().After(promotionTime) {
		readyToPromote = true
	}

	// if we're past our promotion time, go ahead and synchronize over
	if readyToPromote {
		_, _, err := resourceapply.SyncSecret(c.secretClient, c.eventRecorder,
			operatorclient.OperatorNamespace, "next-service-account-private-key",
			operatorclient.TargetNamespace, "service-account-private-key", []metav1.OwnerReference{})
		return err
	}

	return nil
}

// checkKeyPairValidity checks if public key and private key matches.
func checkKeyPairValidity(pubKeyData, privKeyData []byte) error {
	privKey, err := keyutil.ParsePrivateKeyPEM(privKeyData)
	if err != nil {
		return err
	}
	rsaPrivateKey, ok := privKey.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("private key is not of rsa type")
	}
	pubKeys, err := keyutil.ParsePublicKeysPEM(pubKeyData)
	if err != nil {
		return err
	}
	wantRSAPublicKey, ok := pubKeys[0].(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("public key is not of rsa type")
	}
	// private key embeds public key and embedded key must match provided public key
	if !reflect.DeepEqual(rsaPrivateKey.PublicKey, *wantRSAPublicKey) {
		return fmt.Errorf("key pair do not match")
	}
	return nil
}

const keySize = 2048

func privateKeyToPem(key *rsa.PrivateKey) []byte {
	keyInBytes := x509.MarshalPKCS1PrivateKey(key)
	keyinPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: keyInBytes,
		},
	)
	return keyinPem
}

func publicKeyToPem(key *rsa.PublicKey) ([]byte, error) {
	keyInBytes, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, err
	}
	keyinPem := pem.EncodeToMemory(
		&pem.Block{
			Type:  "RSA PUBLIC KEY",
			Bytes: keyInBytes,
		},
	)
	return keyinPem, nil
}

func (c *SATokenSignerController) Run(workers int, stopCh <-chan struct{}) {
	defer utilruntime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting SATokenSignerController")
	defer klog.Infof("Shutting down SATokenSignerController")

	if !cache.WaitForCacheSync(stopCh, c.cachesSynced...) {
		utilruntime.HandleError(fmt.Errorf("caches did not sync"))
		return
	}

	// doesn't matter what workers say, only start one.
	go wait.Until(c.runWorker, time.Second, stopCh)

	// start a time based thread to ensure we stay up to date
	go wait.Until(func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			c.queue.Add(workQueueKey)
			select {
			case <-ticker.C:
			case <-stopCh:
				return
			}
		}

	}, time.Minute, stopCh)

	<-stopCh
}

func (c *SATokenSignerController) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *SATokenSignerController) processNextWorkItem() bool {
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

	utilruntime.HandleError(fmt.Errorf("%v failed with : %v", dsKey, err))
	c.queue.AddRateLimited(dsKey)

	return true
}

// eventHandler queues the operator to check spec and status
func (c *SATokenSignerController) eventHandler() cache.ResourceEventHandler {
	return cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.queue.Add(workQueueKey) },
		UpdateFunc: func(old, new interface{}) { c.queue.Add(workQueueKey) },
		DeleteFunc: func(obj interface{}) { c.queue.Add(workQueueKey) },
	}
}
