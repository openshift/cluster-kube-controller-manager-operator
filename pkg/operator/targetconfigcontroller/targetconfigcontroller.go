package targetconfigcontroller

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/v311_00_assets"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/version"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
)

const workQueueKey = "key"

type TargetConfigController struct {
	targetImagePullSpec   string
	operatorImagePullSpec string

	operatorClient v1helpers.StaticPodOperatorClient

	kubeClient      kubernetes.Interface
	configMapLister corev1listers.ConfigMapLister
	secretLister    corev1listers.SecretLister
	eventRecorder   events.Recorder

	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewTargetConfigController(
	targetImagePullSpec, operatorImagePullSpec string,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	namespacedKubeInformers informers.SharedInformerFactory,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
) *TargetConfigController {
	c := &TargetConfigController{
		targetImagePullSpec:   targetImagePullSpec,
		operatorImagePullSpec: operatorImagePullSpec,

		configMapLister: kubeInformersForNamespaces.ConfigMapLister(),
		secretLister:    kubeInformersForNamespaces.SecretLister(),
		operatorClient:  operatorClient,
		kubeClient:      kubeClient,
		eventRecorder:   eventRecorder.WithComponentSuffix("target-config-controller"),

		queue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "TargetConfigController"),
	}

	operatorClient.Informer().AddEventHandler(c.eventHandler())
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
	operatorSpec, _, _, err := c.operatorClient.GetStaticPodOperatorStateWithQuorum()
	if err != nil {
		return err
	}

	switch operatorSpec.ManagementState {
	case operatorv1.Managed:
	case operatorv1.Unmanaged:
		return nil
	case operatorv1.Removed:
		// TODO probably just fail
		return nil
	default:
		c.eventRecorder.Warningf("ManagementStateUnknown", "Unrecognized operator management state %q", operatorSpec.ManagementState)
		return nil
	}

	// block until config is observed and specific paths are present
	if err := isRequiredConfigPresent(operatorSpec.ObservedConfig.Raw); err != nil {
		c.eventRecorder.Warning("ConfigMissing", err.Error())
		return err
	}

	requeue, err := createTargetConfigController(c, c.eventRecorder, operatorSpec)
	if err != nil {
		return err
	}
	if requeue {
		return fmt.Errorf("synthetic requeue request")
	}

	return nil
}

func isRequiredConfigPresent(config []byte) error {
	if len(config) == 0 {
		return fmt.Errorf("no observedConfig")
	}

	existingConfig := map[string]interface{}{}
	if err := json.NewDecoder(bytes.NewBuffer(config)).Decode(&existingConfig); err != nil {
		return fmt.Errorf("error parsing config, %v", err)
	}

	requiredPaths := [][]string{
		{"extendedArguments", "cluster-name"},
	}
	for _, requiredPath := range requiredPaths {
		configVal, found, err := unstructured.NestedFieldNoCopy(existingConfig, requiredPath...)
		if err != nil {
			return fmt.Errorf("error reading %v from config, %v", strings.Join(requiredPath, "."), err)
		}
		if !found {
			return fmt.Errorf("%v missing from config", strings.Join(requiredPath, "."))
		}
		if configVal == nil {
			return fmt.Errorf("%v null in config", strings.Join(requiredPath, "."))
		}
		if configValSlice, ok := configVal.([]interface{}); ok && len(configValSlice) == 0 {
			return fmt.Errorf("%v empty in config", strings.Join(requiredPath, "."))
		}
		if configValString, ok := configVal.(string); ok && len(configValString) == 0 {
			return fmt.Errorf("%v empty in config", strings.Join(requiredPath, "."))
		}
	}
	return nil
}

// createTargetConfigController takes care of synchronizing (not upgrading) the thing we're managing.
func createTargetConfigController(c TargetConfigController, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec) (bool, error) {
	errors := []error{}

	directResourceResults := resourceapply.ApplyDirectly(c.kubeClient, c.eventRecorder, v311_00_assets.Asset,
		"v3.11.0/kube-controller-manager/ns.yaml",
		"v3.11.0/kube-controller-manager/kubeconfig-cert-syncer.yaml",
		"v3.11.0/kube-controller-manager/kubeconfig-cm.yaml",
		"v3.11.0/kube-controller-manager/leader-election-rolebinding.yaml",
		"v3.11.0/kube-controller-manager/svc.yaml",
		"v3.11.0/kube-controller-manager/sa.yaml",
	)
	for _, currResult := range directResourceResults {
		if currResult.Error != nil {
			errors = append(errors, fmt.Errorf("%q (%T): %v", currResult.File, currResult.Type, currResult.Error))
		}
	}

	_, _, err := manageKubeControllerManagerConfig(c.kubeClient.CoreV1(), recorder, operatorSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap", err))
	}
	_, _, err = manageCSRIntermediateCABundle(c.secretLister, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/csr-intermediate-ca", err))
	}
	_, _, err = manageCSRCABundle(c.configMapLister, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/csr-controller-ca", err))
	}
	_, requeueDelay, _, err := manageCSRSigner(c.secretLister, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "secrets/csr-signer", err))
	}
	if requeueDelay > 0 {
		c.queue.AddAfter(workQueueKey, requeueDelay)
	}
	_, _, err = manageServiceAccountCABundle(c.configMapLister, c.kubeClient.CoreV1(), recorder)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/serviceaccount-ca", err))
	}
	_, _, err = managePod(c.kubeClient.CoreV1(), c.kubeClient.CoreV1(), recorder, operatorSpec, c.targetImagePullSpec, c.operatorImagePullSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/kube-controller-manager-pod", err))
	}

	if len(errors) > 0 {
		condition := operatorv1.OperatorCondition{
			Type:    "TargetConfigControllerDegraded",
			Status:  operatorv1.ConditionTrue,
			Reason:  "SynchronizationError",
			Message: v1helpers.NewMultiLineAggregate(errors).Error(),
		}
		if _, _, err := v1helpers.UpdateStaticPodStatus(c.operatorClient, v1helpers.UpdateStaticPodConditionFn(condition)); err != nil {
			return true, err
		}
		return true, nil
	}

	condition := operatorv1.OperatorCondition{
		Type:   "TargetConfigControllerDegraded",
		Status: operatorv1.ConditionFalse,
	}
	if _, _, err := v1helpers.UpdateStaticPodStatus(c.operatorClient, v1helpers.UpdateStaticPodConditionFn(condition)); err != nil {
		return true, err
	}

	return false, nil
}

func manageKubeControllerManagerConfig(client corev1client.ConfigMapsGetter, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/cm.yaml"))
	defaultConfig := v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/defaultconfig.yaml")
	requiredConfigMap, _, err := resourcemerge.MergeConfigMap(configMap, "config.yaml", nil, defaultConfig, operatorSpec.ObservedConfig.Raw, operatorSpec.UnsupportedConfigOverrides.Raw)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func managePod(configMapsGetter corev1client.ConfigMapsGetter, secretsGetter corev1client.SecretsGetter, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec, imagePullSpec, operatorImagePullSpec string) (*corev1.ConfigMap, bool, error) {
	required := resourceread.ReadPodV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/pod.yaml"))
	// TODO: If the image pull spec is not specified, the "${IMAGE}" will be used as value and the pod will fail to start.
	images := map[string]string{
		"${IMAGE}":          imagePullSpec,
		"${OPERATOR_IMAGE}": operatorImagePullSpec,
	}
	if len(imagePullSpec) > 0 {
		for i := range required.Spec.Containers {
			for pat, img := range images {
				if required.Spec.Containers[i].Image == pat {
					required.Spec.Containers[i].Image = img
					break
				}
			}
		}
		for i := range required.Spec.InitContainers {
			for pat, img := range images {
				if required.Spec.InitContainers[i].Image == pat {
					required.Spec.InitContainers[i].Image = img
					break
				}
			}
		}
	}

	var v int
	switch operatorSpec.LogLevel {
	case operatorv1.Normal:
		v = 2
	case operatorv1.Debug:
		v = 4
	case operatorv1.Trace:
		v = 6
	case operatorv1.TraceAll:
		v = 8
	default:
		v = 2
	}
	required.Spec.Containers[0].Args = append(required.Spec.Containers[0].Args, fmt.Sprintf("-v=%d", v))

	if _, err := secretsGetter.Secrets(required.Namespace).Get("serving-cert", metav1.GetOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return nil, false, err
	} else if err == nil {
		required.Spec.Containers[0].Args = append(required.Spec.Containers[0].Args, "--tls-cert-file=/etc/kubernetes/static-pod-resources/secrets/serving-cert/tls.crt")
		required.Spec.Containers[0].Args = append(required.Spec.Containers[0].Args, "--tls-private-key-file=/etc/kubernetes/static-pod-resources/secrets/serving-cert/tls.key")
	}

	configMap := resourceread.ReadConfigMapV1OrDie(v311_00_assets.MustAsset("v3.11.0/kube-controller-manager/pod-cm.yaml"))
	configMap.Data["pod.yaml"] = resourceread.WritePodV1OrDie(required)
	configMap.Data["forceRedeploymentReason"] = operatorSpec.ForceRedeploymentReason
	configMap.Data["version"] = version.Get().String()
	return resourceapply.ApplyConfigMap(configMapsGetter, recorder, configMap)
}

func manageServiceAccountCABundle(lister corev1listers.ConfigMapLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "serviceaccount-ca"},
		lister,
		// include the ca bundle needed to recognize the server
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "kube-apiserver-server-ca"},
		// include the ca bundle needed to recognize default
		// certificates generated by cluster-ingress-operator
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "router-ca"},
	)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func manageCSRCABundle(lister corev1listers.ConfigMapLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "csr-controller-ca"},
		lister,
		// include the CA we use to sign CSRs
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "csr-signer-ca"},
		// include the CA we use to sign the cert key pairs from from csr-signer
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.OperatorNamespace, Name: "csr-controller-signer-ca"},
	)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(client, recorder, requiredConfigMap)
}

func manageCSRSigner(lister corev1listers.SecretLister, client corev1client.SecretsGetter, recorder events.Recorder) (*corev1.Secret, time.Duration, bool, error) {
	// get the certkey pair we will sign with. We're going to add the cert to a ca bundle so we can recognize the chain it signs back to the signer
	csrSigner, err := lister.Secrets(operatorclient.OperatorNamespace).Get("csr-signer")
	if apierrors.IsNotFound(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}

	// the CSR signing controller only accepts a single cert.  make sure we only ever have one (not multiple to construct a larger chain)
	signingCert := csrSigner.Data["tls.crt"]
	if len(signingCert) == 0 {
		return nil, 0, false, nil
	}
	signingKey := csrSigner.Data["tls.key"]
	if len(signingCert) == 0 {
		return nil, 0, false, nil
	}
	signingCertKeyPair, err := crypto.GetCAFromBytes(signingCert, signingKey)
	if err != nil {
		return nil, 0, false, err
	}
	certBytes, err := crypto.EncodeCertificates(signingCertKeyPair.Config.Certs[0])
	if err != nil {
		return nil, 0, false, err
	}

	// make sure we wait five minutes to propagate the change to other components, like kas for trust
	useAfter := signingCertKeyPair.Config.Certs[0].NotBefore.Add(5 * time.Minute)
	now := time.Now()
	if useAfter.Before(now) {
		// if we have something and it's not expired (yeah that check is missing here), delay
		if _, err := client.Secrets(operatorclient.TargetNamespace).Get("csr-signer", metav1.GetOptions{}); err == nil {
			return nil, useAfter.Sub(now) + 10*time.Second, false, nil
		}
	}

	csrSigner = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "csr-signer"},
		Data: map[string][]byte{
			"tls.crt": certBytes,
			"tls.key": []byte(signingKey),
		},
	}
	secret, modified, err := resourceapply.ApplySecret(client, recorder, csrSigner)
	return secret, 0, modified, err
}

func manageCSRIntermediateCABundle(lister corev1listers.SecretLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	// get the certkey pair we will sign with. We're going to add the cert to a ca bundle so we can recognize the chain it signs back to the signer
	csrSigner, err := lister.Secrets(operatorclient.OperatorNamespace).Get("csr-signer")
	if apierrors.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	signingCert := csrSigner.Data["tls.crt"]
	if len(signingCert) == 0 {
		return nil, false, nil
	}
	signingKey := csrSigner.Data["tls.key"]
	if len(signingCert) == 0 {
		return nil, false, nil
	}
	signingCertKeyPair, err := crypto.GetCAFromBytes(signingCert, signingKey)
	if err != nil {
		return nil, false, err
	}

	csrSignerCA, err := client.ConfigMaps(operatorclient.OperatorNamespace).Get("csr-signer-ca", metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		csrSignerCA = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.OperatorNamespace, Name: "csr-signer-ca"},
			Data:       map[string]string{},
		}
	} else if err != nil {
		return nil, false, err
	}

	certificates := []*x509.Certificate{}
	caBundle := csrSignerCA.Data["ca-bundle.crt"]
	if len(caBundle) > 0 {
		var err error
		certificates, err = cert.ParseCertsPEM([]byte(caBundle))
		if err != nil {
			return nil, false, err
		}
	}
	certificates = append(certificates, signingCertKeyPair.Config.Certs...)
	certificates = crypto.FilterExpiredCerts(certificates...)

	finalCertificates := []*x509.Certificate{}
	// now check for duplicates. n^2, but super simple
	for i := range certificates {
		found := false
		for j := range finalCertificates {
			if reflect.DeepEqual(certificates[i].Raw, finalCertificates[j].Raw) {
				found = true
				break
			}
		}
		if !found {
			finalCertificates = append(finalCertificates, certificates[i])
		}
	}

	caBytes, err := crypto.EncodeCertificates(finalCertificates...)
	if err != nil {
		return nil, false, err
	}
	csrSignerCA.Data["ca-bundle.crt"] = string(caBytes)

	return resourceapply.ApplyConfigMap(client, recorder, csrSignerCA)
}

// Run starts the kube-controller-manager and blocks until stopCh is closed.
func (c *TargetConfigController) Run(workers int, stopCh <-chan struct{}) {
	defer runtime.HandleCrash()
	defer c.queue.ShutDown()

	klog.Infof("Starting TargetConfigController")
	defer klog.Infof("Shutting down TargetConfigController")

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
