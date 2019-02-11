package certrotationcontroller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const workQueueKey = "key"

type SATokenSignerController struct {
	operatorClient  v1helpers.OperatorClient
	secretClient    corev1client.SecretsGetter
	configMapClient corev1client.ConfigMapsGetter
	eventRecorder   events.Recorder

	cachesSynced []cache.InformerSynced

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewSATokenSignerController(
	operatorClient v1helpers.OperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,

) (*SATokenSignerController, error) {

	ret := &SATokenSignerController{
		operatorClient:  operatorClient,
		secretClient:    v1helpers.CachedSecretGetter(kubeClient, kubeInformersForNamespaces),
		configMapClient: v1helpers.CachedConfigMapGetter(kubeClient, kubeInformersForNamespaces),
		eventRecorder:   eventRecorder,

		cachesSynced: []cache.InformerSynced{
			kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer().HasSynced,
			operatorClient.Informer().HasSynced,
		},

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "SATokenSignerController"),
	}

	kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer().AddEventHandler(ret.eventHandler())
	kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer().AddEventHandler(ret.eventHandler())
	operatorClient.Informer().AddEventHandler(ret.eventHandler())

	return ret, nil
}

func (c SATokenSignerController) sync() error {
	syncErr := c.syncWorker()

	condition := operatorv1.OperatorCondition{
		Type:   "SATokenSignerFailing",
		Status: operatorv1.ConditionFalse,
	}
	if syncErr != nil {
		condition.Status = operatorv1.ConditionTrue
		condition.Reason = "Error"
		condition.Message = syncErr.Error()
	}
	if _, _, updateErr := v1helpers.UpdateStatus(c.operatorClient, v1helpers.UpdateConditionFn(condition)); updateErr != nil {
		return updateErr
	}

	return syncErr
}

func (c SATokenSignerController) syncWorker() error {
	saTokenSigner, err := c.secretClient.Secrets(operatorclient.TargetNamespace).Get("service-account-private-key", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	needNewSATokenSigningKey := errors.IsNotFound(err) || len(saTokenSigner.Data["service-account.key"]) == 0 || len(saTokenSigner.Data["service-account.pub"]) == 0
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
			ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "service-account-private-key"},
			Data: map[string][]byte{
				"service-account.key": privateKeyToPem(rsaKey),
				"service-account.pub": publicBytes,
			},
		}

		saTokenSigner, _, err = resourceapply.ApplySecret(c.secretClient, c.eventRecorder, saTokenSigner)
		if err != nil {
			return err
		}
	}

	saTokenSigningCerts, err := c.configMapClient.ConfigMaps(operatorclient.GlobalMachineSpecifiedConfigNamespace).Get("sa-token-signing-certs", metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	if errors.IsNotFound(err) {
		saTokenSigningCerts = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "sa-token-signing-certs"},
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
	if hasThisPublicKey {
		return nil
	}
	saTokenSigningCerts.Data[fmt.Sprintf("service-account-%03d.pub", len(saTokenSigningCerts.Data)+1)] = currPublicKey
	saTokenSigningCerts, _, err = resourceapply.ApplyConfigMap(c.configMapClient, c.eventRecorder, saTokenSigningCerts)
	if err != nil {
		return err
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

	glog.Infof("Starting SATokenSignerController")
	defer glog.Infof("Shutting down SATokenSignerController")

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
