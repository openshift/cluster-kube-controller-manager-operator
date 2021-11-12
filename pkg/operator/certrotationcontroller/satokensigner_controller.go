package certrotationcontroller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"k8s.io/klog/v2"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/encryption/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const (
	saTokenReadyTimeAnnotation = "kube-controller-manager.openshift.io/ready-to-use"
)

type SATokenSignerController struct {
	operatorClient  v1helpers.StaticPodOperatorClient
	secretClient    corev1client.SecretsGetter
	configMapClient corev1client.ConfigMapsGetter
	endpointClient  corev1client.EndpointsGetter
	podClient       corev1client.PodsGetter

	confirmedBootstrapNodeGone bool
}

func NewSATokenSignerController(
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &SATokenSignerController{
		operatorClient:  operatorClient,
		secretClient:    v1helpers.CachedSecretGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		configMapClient: v1helpers.CachedConfigMapGetter(kubeClient.CoreV1(), kubeInformersForNamespaces),
		endpointClient:  kubeClient.CoreV1(),
		podClient:       kubeClient.CoreV1(),
	}

	return factory.New().WithInformers(
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().Secrets().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer(),
		operatorClient.Informer(),
	).ResyncEvery(time.Minute).WithSync(c.sync).ToController("SATokenSignerController", eventRecorder)
}

func (c *SATokenSignerController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	syncErr := c.syncWorker(ctx, syncCtx)
	condition := operatorv1.OperatorCondition{
		Type:   "SATokenSignerDegraded",
		Status: operatorv1.ConditionFalse,
	}
	if syncErr != nil && !isUnexpectedAddressesError(syncErr) {
		condition.Status = operatorv1.ConditionTrue
		condition.Reason = "Error"
		condition.Message = syncErr.Error()
	}
	if _, _, updateErr := v1helpers.UpdateStatus(ctx, c.operatorClient, v1helpers.UpdateConditionFn(condition)); updateErr != nil {
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
func (c *SATokenSignerController) isPastBootstrapNode(ctx context.Context, syncCtx factory.SyncContext) error {
	if c.confirmedBootstrapNodeGone {
		return nil
	}

	nodeIPs := sets.String{}
	apiServerPods, err := c.podClient.Pods("openshift-kube-apiserver").List(ctx, metav1.ListOptions{LabelSelector: "app=openshift-kube-apiserver"})
	if err != nil {
		return err
	}
	for _, pod := range apiServerPods.Items {
		nodeIPs.Insert(pod.Status.HostIP)
	}

	kubeEndpoints, err := c.endpointClient.Endpoints("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		return err
	}
	if len(kubeEndpoints.Subsets) == 0 {
		err := fmt.Errorf("missing kubernetes endpoints subsets")
		syncCtx.Recorder().Warning("SATokenSignerControllerStuck", err.Error())
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
		syncCtx.Recorder().Event("SATokenSignerControllerStuck", err.Error())
		return err
	}

	// we have confirmed that the bootstrap node is gone
	syncCtx.Recorder().Event("SATokenSignerControllerOK", "found expected kube-apiserver endpoints")
	c.confirmedBootstrapNodeGone = true
	return nil
}

func (c *SATokenSignerController) syncWorker(ctx context.Context, syncCtx factory.SyncContext) error {
	if pastBootstrapErr := c.isPastBootstrapNode(ctx, syncCtx); pastBootstrapErr != nil {
		// if we are not past bootstrapping, then if we're missing the service-account-private-key we need to prime it from the
		// initial provided by the installer.
		_, err := c.secretClient.Secrets(operatorclient.TargetNamespace).Get(ctx, "service-account-private-key", metav1.GetOptions{})
		if err == nil {
			// return this error to be reported and requeue
			return pastBootstrapErr
		}
		if !errors.IsNotFound(err) {
			return err
		}
		// at this point we have not-found condition, sync the original
		_, _, err = resourceapply.SyncSecret(ctx, c.secretClient, syncCtx.Recorder(),
			operatorclient.GlobalUserSpecifiedConfigNamespace, "initial-service-account-private-key",
			operatorclient.TargetNamespace, "service-account-private-key", []metav1.OwnerReference{})
		return err
	}

	needNewSATokenSigningKey := false
	saTokenSigner, err := c.secretClient.Secrets(operatorclient.OperatorNamespace).Get(ctx, "next-service-account-private-key", metav1.GetOptions{})
	if errors.IsNotFound(err) {
		needNewSATokenSigningKey = true
	} else if err != nil {
		return err
	} else {
		err := crypto.CheckRSAKeyPair(saTokenSigner.Data["service-account.pub"], saTokenSigner.Data["service-account.key"])
		if err != nil {
			klog.Errorf("key pair is invalid: %v", err)
			needNewSATokenSigningKey = true
		}
	}

	if needNewSATokenSigningKey {
		pubKeyPEM, privKeyPEM, err := crypto.GenerateRSAKeyPair()
		if err != nil {
			return err
		}

		saTokenSigner = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: operatorclient.OperatorNamespace, Name: "next-service-account-private-key",
				Annotations: map[string]string{saTokenReadyTimeAnnotation: time.Now().Add(5 * time.Minute).Format(time.RFC3339)},
			},
			Data: map[string][]byte{
				"service-account.key": privKeyPEM,
				"service-account.pub": pubKeyPEM,
			},
		}

		saTokenSigner, _, err = resourceapply.ApplySecret(ctx, c.secretClient, syncCtx.Recorder(), saTokenSigner)
		if err != nil {
			return err
		}
		// requeue for after we should have recovered
		syncCtx.Queue().AddAfter(syncCtx.QueueKey(), 5*time.Minute+10*time.Second)
	}

	saTokenSigningCerts, err := c.configMapClient.ConfigMaps(operatorclient.GlobalMachineSpecifiedConfigNamespace).Get(ctx, "sa-token-signing-certs", metav1.GetOptions{})
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
		saTokenSigningCerts, _, err = resourceapply.ApplyConfigMap(ctx, c.configMapClient, syncCtx.Recorder(), saTokenSigningCerts)
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
		_, _, err := resourceapply.SyncSecret(ctx, c.secretClient, syncCtx.Recorder(),
			operatorclient.OperatorNamespace, "next-service-account-private-key",
			operatorclient.TargetNamespace, "service-account-private-key", []metav1.OwnerReference{})
		return err
	}

	return nil
}
