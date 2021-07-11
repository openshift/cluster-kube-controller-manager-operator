package targetconfigcontroller

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/ghodss/yaml"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/util/cert"

	kubecontrolplanev1 "github.com/openshift/api/kubecontrolplane/v1"
	openshiftcontrolplanev1 "github.com/openshift/api/openshiftcontrolplane/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configv1informers "github.com/openshift/client-go/config/informers/externalversions/config/v1"
	configv1listers "github.com/openshift/client-go/config/listers/config/v1"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/management"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resource/resourcemerge"
	"github.com/openshift/library-go/pkg/operator/resource/resourceread"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/bindata"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/version"
)

type TargetConfigController struct {
	targetImagePullSpec             string
	operatorImagePullSpec           string
	clusterPolicyControllerPullSpec string
	toolsImagePullSpec              string

	operatorClient v1helpers.StaticPodOperatorClient

	kubeClient          kubernetes.Interface
	configMapLister     corev1listers.ConfigMapLister
	secretLister        corev1listers.SecretLister
	infrastuctureLister configv1listers.InfrastructureLister
}

func NewTargetConfigController(
	targetImagePullSpec, operatorImagePullSpec, clusterPolicyControllerPullSpec, toolsImagePullSpec string,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	operatorClient v1helpers.StaticPodOperatorClient,
	kubeClient kubernetes.Interface,
	infrastuctureInformer configv1informers.InfrastructureInformer,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &TargetConfigController{
		targetImagePullSpec:             targetImagePullSpec,
		operatorImagePullSpec:           operatorImagePullSpec,
		clusterPolicyControllerPullSpec: clusterPolicyControllerPullSpec,
		toolsImagePullSpec:              toolsImagePullSpec,

		configMapLister:     kubeInformersForNamespaces.ConfigMapLister(),
		secretLister:        kubeInformersForNamespaces.SecretLister(),
		infrastuctureLister: infrastuctureInformer.Lister(),
		operatorClient:      operatorClient,
		kubeClient:          kubeClient,
	}

	return factory.New().WithInformers(
		// this is for our general configuration input and our status output in case another actor changes it
		operatorClient.Informer(),

		// We use infrastuctureInformer for observing load balancer URL
		infrastuctureInformer.Informer(),

		// these are for watching our outputs in case someone changes them
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().ServiceAccounts().Informer(),

		// for configmaps and secrets from our inputs
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().Secrets().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.GlobalMachineSpecifiedConfigNamespace).Core().V1().Secrets().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().ConfigMaps().Informer(),
		kubeInformersForNamespaces.InformersFor(operatorclient.OperatorNamespace).Core().V1().Secrets().Informer(),

		operatorClient.Informer(),
	).WithNamespaceInformer(
		// we only watch our output namespace
		kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Namespaces().Informer(), operatorclient.TargetNamespace,
	).ResyncEvery(time.Minute).WithSync(c.sync).ToController("TargetConfigController", eventRecorder)
}

func (c TargetConfigController) sync(ctx context.Context, syncCtx factory.SyncContext) error {
	operatorSpec, _, _, err := c.operatorClient.GetStaticPodOperatorStateWithQuorum()
	if err != nil {
		return err
	}

	if !management.IsOperatorManaged(operatorSpec.ManagementState) {
		return nil
	}

	// block until config is observed and specific paths are present
	if err := isRequiredConfigPresent(operatorSpec.ObservedConfig.Raw); err != nil {
		syncCtx.Recorder().Warning("ConfigMissing", err.Error())
		return err
	}

	requeue, err := createTargetConfigController(ctx, syncCtx, c, operatorSpec)
	if err != nil {
		return err
	}

	if requeue {
		return factory.SyntheticRequeueError
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
func createTargetConfigController(ctx context.Context, syncCtx factory.SyncContext, c TargetConfigController, operatorSpec *operatorv1.StaticPodOperatorSpec) (bool, error) {
	errors := []error{}

	_, _, err := manageKubeControllerManagerConfig(ctx, c.kubeClient.CoreV1(), syncCtx.Recorder(), operatorSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap", err))
	}
	_, _, err = manageClusterPolicyControllerConfig(ctx, c.kubeClient.CoreV1(), syncCtx.Recorder(), operatorSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/cluster-policy-controller-config", err))
	}
	_, _, err = manageRecycler(ctx, c.kubeClient.CoreV1(), syncCtx.Recorder(), c.toolsImagePullSpec)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/recycler-config", err))
	}
	_, _, err = ManageCSRIntermediateCABundle(ctx, c.secretLister, c.kubeClient.CoreV1(), syncCtx.Recorder())
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/csr-intermediate-ca", err))
	}
	_, _, err = ManageCSRCABundle(ctx, c.configMapLister, c.kubeClient.CoreV1(), syncCtx.Recorder())
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/csr-controller-ca", err))
	}
	_, requeueDelay, _, err := ManageCSRSigner(ctx, c.secretLister, c.kubeClient.CoreV1(), syncCtx.Recorder())
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "secrets/csr-signer", err))
	}
	if requeueDelay > 0 {
		syncCtx.Queue().AddAfter(syncCtx.QueueKey(), requeueDelay)
	}
	_, _, err = manageServiceAccountCABundle(ctx, c.configMapLister, c.kubeClient.CoreV1(), syncCtx.Recorder())
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/serviceaccount-ca", err))
	}
	err = ensureLocalhostRecoverySAToken(ctx, c.kubeClient.CoreV1(), syncCtx.Recorder())
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "serviceaccount/localhost-recovery-client", err))
	}
	_, _, err = manageControllerManagerKubeconfig(ctx, c.kubeClient.CoreV1(), c.infrastuctureLister, syncCtx.Recorder())
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/controller-manager-kubeconfig", err))
	}

	// Allow the addition of the service ca to token secrets to be enabled by setting an
	// UnsupportedConfigOverride field named
	// enableDeprecatedAndRemovedServiceCAKeyUntilNextRelease_ThisMakesClusterImpossibleToUpgrade
	// to true.
	//
	// This option is provided for backwards-compatibility in 4.5, and should be removed in 4.6.
	addServingServiceCAToTokenSecrets := false
	if len(operatorSpec.UnsupportedConfigOverrides.Raw) > 0 {
		cmConfigOverride := struct {
			EnableDeprecatedAndRemovedServiceCAKeyUntilNextRelease_ThisMakesClusterImpossibleToUpgrade bool
		}{}
		if err := json.Unmarshal(operatorSpec.UnsupportedConfigOverrides.Raw, &cmConfigOverride); err != nil {
			errors = append(errors, fmt.Errorf("failed to load EnableDeprecatedAndRemovedServiceCAKeyUntilNextRelease_ThisMakesClusterImpossibleToUpgrade from UnsupportedConfigOverride: %v", err))
		} else {
			addServingServiceCAToTokenSecrets = cmConfigOverride.EnableDeprecatedAndRemovedServiceCAKeyUntilNextRelease_ThisMakesClusterImpossibleToUpgrade
		}
	}

	_, _, err = managePod(ctx, c.kubeClient.CoreV1(), c.kubeClient.CoreV1(), syncCtx.Recorder(), operatorSpec, c.targetImagePullSpec, c.operatorImagePullSpec, c.clusterPolicyControllerPullSpec, addServingServiceCAToTokenSecrets)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/kube-controller-manager-pod", err))
	}

	err = ensureKubeControllerManagerTrustedCA(ctx, c.kubeClient.CoreV1(), syncCtx.Recorder())
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/trusted-ca-bundle", err))
	}

	// The operator is not upgradeable if serving service CA addition to token secrets is enabled
	// with the UnsupportedConfigOverride field
	// EnableDeprecatedAndRemovedServiceCAKeyUntilNextRelease_ThisMakesClusterImpossibleToUpgrade.
	//
	// This should be removed in 4.6.
	var upgradeableCondition operatorv1.OperatorCondition
	if addServingServiceCAToTokenSecrets {
		upgradeableCondition = operatorv1.OperatorCondition{
			Type:    operatorv1.OperatorStatusTypeUpgradeable,
			Status:  operatorv1.ConditionFalse,
			Reason:  "AddServingServiceCAToTokenSecretsEnabled",
			Message: "Disable the addition of the serving service ca to token secrets by removing EnableDeprecatedAndRemovedServiceCAKeyUntilNextRelease_ThisMakesClusterImpossibleToUpgrade from the operator's UnsupportedConfigOverrdies",
		}
	} else {
		upgradeableCondition = operatorv1.OperatorCondition{
			Type:   operatorv1.OperatorStatusTypeUpgradeable,
			Status: operatorv1.ConditionTrue,
		}
	}
	if _, _, err := v1helpers.UpdateStaticPodStatus(c.operatorClient, v1helpers.UpdateStaticPodConditionFn(upgradeableCondition)); err != nil {
		return true, err
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

func manageKubeControllerManagerConfig(ctx context.Context, client corev1client.ConfigMapsGetter, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/cm.yaml"))
	defaultConfig := v411_00_assets.MustAsset("v4.1.0/config/defaultconfig.yaml")
	requiredConfigMap, _, err := resourcemerge.MergePrunedConfigMap(
		&kubecontrolplanev1.KubeControllerManagerConfig{},
		configMap,
		"config.yaml",
		nil,
		defaultConfig,
		operatorSpec.ObservedConfig.Raw,
		operatorSpec.UnsupportedConfigOverrides.Raw)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredConfigMap)
}

func manageClusterPolicyControllerConfig(ctx context.Context, client corev1client.ConfigMapsGetter, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec) (*corev1.ConfigMap, bool, error) {
	configMap := resourceread.ReadConfigMapV1OrDie(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/cluster-policy-controller-cm.yaml"))
	defaultConfig := v411_00_assets.MustAsset("v4.1.0/config/default-cluster-policy-controller-config.yaml")
	requiredConfigMap, _, err := resourcemerge.MergePrunedConfigMap(
		&openshiftcontrolplanev1.OpenShiftControllerManagerConfig{},
		configMap,
		"config.yaml",
		nil,
		defaultConfig,
		operatorSpec.ObservedConfig.Raw,
		operatorSpec.UnsupportedConfigOverrides.Raw)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredConfigMap)
}

func ensureLocalhostRecoverySAToken(ctx context.Context, client corev1client.CoreV1Interface, recorder events.Recorder) error {
	requiredSA := resourceread.ReadServiceAccountV1OrDie(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/localhost-recovery-sa.yaml"))
	requiredToken := resourceread.ReadSecretV1OrDie(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/localhost-recovery-token.yaml"))

	saClient := client.ServiceAccounts(operatorclient.TargetNamespace)
	serviceAccount, err := saClient.Get(ctx, requiredSA.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// The default token secrets get random names so we have created a custom secret
	// to be populated with SA token so we have a stable name.
	secretsClient := client.Secrets(operatorclient.TargetNamespace)
	token, err := secretsClient.Get(ctx, requiredToken.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Token creation / injection for a SA is asynchronous.
	// We will report and error if it's missing, go degraded and get re-queued when the SA token is updated.

	uid := token.Annotations[corev1.ServiceAccountUIDKey]
	if len(uid) == 0 {
		return fmt.Errorf("secret %s/%s hasn't been populated with SA token yet: missing SA UID", token.Namespace, token.Name)
	}

	if uid != string(serviceAccount.UID) {
		return fmt.Errorf("secret %s/%s hasn't been populated with current SA token yet: SA UID mismatch", token.Namespace, token.Name)
	}

	if len(token.Data) == 0 {
		return fmt.Errorf("secret %s/%s hasn't been populated with any data yet", token.Namespace, token.Name)
	}

	// Explicitly check that the fields we use are there, so we find out easily if some are removed or renamed.

	_, ok := token.Data["token"]
	if !ok {
		return fmt.Errorf("secret %s/%s hasn't been populated with current SA token yet", token.Namespace, token.Name)
	}

	_, ok = token.Data["ca.crt"]
	if !ok {
		return fmt.Errorf("secret %s/%s hasn't been populated with current SA token root CA yet", token.Namespace, token.Name)
	}

	return err
}

func manageControllerManagerKubeconfig(ctx context.Context, client corev1client.CoreV1Interface, infrastructureLister configv1listers.InfrastructureLister, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	cmString := string(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/kubeconfig-cm.yaml"))

	infrastructure, err := infrastructureLister.Get("cluster")
	if err != nil {
		return nil, false, err
	}
	apiServerInternalURL := infrastructure.Status.APIServerInternalURL
	if len(apiServerInternalURL) == 0 {
		return nil, false, fmt.Errorf("infrastucture/cluster: missing APIServerInternalURL")
	}

	for pattern, value := range map[string]string{
		"$LB_INT_URL": apiServerInternalURL,
	} {
		cmString = strings.ReplaceAll(cmString, pattern, value)
	}

	requiredCM := resourceread.ReadConfigMapV1OrDie([]byte(cmString))
	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredCM)
}

// manageRecycler applies a ConfigMap containing the recycler config.
// Owned by storage team/fbertina@redhat.com.
func manageRecycler(ctx context.Context, configMapsGetter corev1client.ConfigMapsGetter, recorder events.Recorder, imagePullSpec string) (*corev1.ConfigMap, bool, error) {
	cmString := string(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/recycler-cm.yaml"))
	for pattern, value := range map[string]string{
		"${TOOLS_IMAGE}": imagePullSpec,
	} {
		cmString = strings.ReplaceAll(cmString, pattern, value)
	}
	requiredCM := resourceread.ReadConfigMapV1OrDie([]byte(cmString))
	return resourceapply.ApplyConfigMap(ctx, configMapsGetter, recorder, requiredCM)
}

func managePod(ctx context.Context, configMapsGetter corev1client.ConfigMapsGetter, secretsGetter corev1client.SecretsGetter, recorder events.Recorder, operatorSpec *operatorv1.StaticPodOperatorSpec, imagePullSpec, operatorImagePullSpec, clusterPolicyControllerPullSpec string, addServingServiceCAToTokenSecrets bool) (*corev1.ConfigMap, bool, error) {
	required := resourceread.ReadPodV1OrDie(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/pod.yaml"))
	// TODO: If the image pull spec is not specified, the "${IMAGE}" will be used as value and the pod will fail to start.
	images := map[string]string{
		"${IMAGE}":                           imagePullSpec,
		"${OPERATOR_IMAGE}":                  operatorImagePullSpec,
		"${CLUSTER_POLICY_CONTROLLER_IMAGE}": clusterPolicyControllerPullSpec,
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

	// This section sets the log levels for all containers that take a "1-line" argument
	logLevel := 2
	switch operatorSpec.LogLevel {
	case operatorv1.Normal:
		logLevel = 2
	case operatorv1.Debug:
		logLevel = 4
	case operatorv1.Trace:
		logLevel = 6
	case operatorv1.TraceAll:
		logLevel = 8
	default:
		logLevel = 2
	}
	// containers[0] = kube-controller-manager
	// containers[1] = cluster-policy-controller
	// containers[2] = kube-controller-manager-cert-syncer
	// containers[3] = kube-controller-manager-recovery-controller
	containerNames := sets.NewString("kube-controller-manager", "cluster-policy-controller", "kube-controller-manager-recovery-controller")
	for i := 0; i < len(required.Spec.Containers); i++ {
		if !containerNames.Has(required.Spec.Containers[i].Name) {
			continue
		}
		containerArgsWithLoglevel := required.Spec.Containers[i].Args
		if argsCount := len(containerArgsWithLoglevel); argsCount > 1 {
			return nil, false, fmt.Errorf("expected only one container argument, got %d", argsCount)
		}
		containerArgsWithLoglevel[0] = strings.TrimSpace(containerArgsWithLoglevel[0])
		containerArgsWithLoglevel[0] += fmt.Sprintf(" -v=%d", logLevel)
	}

	// now we are only handling args for the main KCM container
	kcmContainerArgsWithLoglevel := required.Spec.Containers[0].Args
	if !strings.Contains(kcmContainerArgsWithLoglevel[0], "exec hyperkube kube-controller-manager") {
		return nil, false, fmt.Errorf("exec hyperkube kube-controller-manager not found in first argument %q", kcmContainerArgsWithLoglevel[0])
	}

	kcmContainerArgsWithLoglevel[0] = strings.TrimSpace(kcmContainerArgsWithLoglevel[0])

	if _, err := secretsGetter.Secrets(required.Namespace).Get(ctx, "serving-cert", metav1.GetOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return nil, false, err
	} else if err == nil {
		kcmContainerArgsWithLoglevel[0] += " --tls-cert-file=/etc/kubernetes/static-pod-resources/secrets/serving-cert/tls.crt"
		kcmContainerArgsWithLoglevel[0] += " --tls-private-key-file=/etc/kubernetes/static-pod-resources/secrets/serving-cert/tls.key"
	}

	kubeControllerManagerConfigMap, err := configMapsGetter.ConfigMaps(required.Namespace).Get(ctx, "config", metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, false, err
	}
	if kubeControllerManagerConfigMap != nil {
		var kubeControllerManagerConfig map[string]interface{}
		if err := yaml.Unmarshal([]byte(kubeControllerManagerConfigMap.Data["config.yaml"]), &kubeControllerManagerConfig); err != nil {
			return nil, false, fmt.Errorf("failed to unmarshal the kube-controller-manager config: %v", err)
		}
		if extendedArguments := GetKubeControllerManagerArgs(kubeControllerManagerConfig); len(extendedArguments) > 0 {
			kcmContainerArgsWithLoglevel[0] += " " + strings.Join(extendedArguments, " ")
		}
	}

	var observedConfig map[string]interface{}
	if err := yaml.Unmarshal(operatorSpec.ObservedConfig.Raw, &observedConfig); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal the observedConfig: %v", err)
	}

	cipherSuites, cipherSuitesFound, err := unstructured.NestedStringSlice(observedConfig, "servingInfo", "cipherSuites")
	if err != nil {
		return nil, false, fmt.Errorf("couldn't get the servingInfo.cipherSuites config from observedConfig: %v", err)
	}

	minTLSVersion, minTLSVersionFound, err := unstructured.NestedString(observedConfig, "servingInfo", "minTLSVersion")
	if err != nil {
		return nil, false, fmt.Errorf("couldn't get the servingInfo.minTLSVersion config from observedConfig: %v", err)
	}

	if cipherSuitesFound && len(cipherSuites) > 0 {
		kcmContainerArgsWithLoglevel[0] += fmt.Sprintf(" --tls-cipher-suites=%s", strings.Join(cipherSuites, ","))
	}

	if minTLSVersionFound && len(minTLSVersion) > 0 {
		kcmContainerArgsWithLoglevel[0] += fmt.Sprintf(" --tls-min-version=%s", minTLSVersion)
	}

	kcmContainerArgsWithLoglevel[0] = strings.TrimSpace(kcmContainerArgsWithLoglevel[0])

	proxyConfig, _, err := unstructured.NestedStringMap(observedConfig, "targetconfigcontroller", "proxy")
	if err != nil {
		return nil, false, fmt.Errorf("couldn't get the proxy config from observedConfig: %v", err)
	}

	proxyEnvVars := proxyMapToEnvVars(proxyConfig)
	for i, container := range required.Spec.Containers {
		required.Spec.Containers[i].Env = append(container.Env, proxyEnvVars...)
	}

	if addServingServiceCAToTokenSecrets {
		// Ensure the addition of serving service ca to token secrets by setting the environment
		// variable that will enable the behavior in the controller manager.
		for _, container := range required.Spec.Containers {
			if container.Name == "kube-controller-manager" {
				container.Env = append(container.Env, corev1.EnvVar{
					Name:  "ADD_SERVICE_SERVING_CA_TO_TOKEN_SECRETS",
					Value: "true",
				})
				break
			}
		}
	}

	configMap := resourceread.ReadConfigMapV1OrDie(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/pod-cm.yaml"))
	configMap.Data["pod.yaml"] = resourceread.WritePodV1OrDie(required)
	configMap.Data["forceRedeploymentReason"] = operatorSpec.ForceRedeploymentReason
	configMap.Data["version"] = version.Get().String()
	return resourceapply.ApplyConfigMap(ctx, configMapsGetter, recorder, configMap)
}

func GetKubeControllerManagerArgs(config map[string]interface{}) []string {
	extendedArguments, ok := config["extendedArguments"]
	if !ok || extendedArguments == nil {
		return nil
	}
	args := []string{}
	for key, value := range extendedArguments.(map[string]interface{}) {
		for _, arrayValue := range value.([]interface{}) {
			args = append(args, fmt.Sprintf("--%s=%s", key, arrayValue.(string)))
		}
	}
	// make sure to sort the arguments, otherwise we might get mismatch
	// when comparing revisions leading to new ones being created, unnecessarily
	sort.Strings(args)
	return args
}

func manageServiceAccountCABundle(ctx context.Context, lister corev1listers.ConfigMapLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.TargetNamespace, Name: "serviceaccount-ca"},
		lister,
		// include the ca bundle needed to recognize the server
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "kube-apiserver-server-ca"},
		// include the ca bundle needed to recognize default
		// certificates generated by cluster-ingress-operator
		resourcesynccontroller.ResourceLocation{Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace, Name: "default-ingress-cert"},
	)
	if err != nil {
		return nil, false, err
	}
	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredConfigMap)
}

func ManageCSRCABundle(ctx context.Context, lister corev1listers.ConfigMapLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
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
	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredConfigMap)
}

func ManageCSRSigner(ctx context.Context, lister corev1listers.SecretLister, client corev1client.SecretsGetter, recorder events.Recorder) (*corev1.Secret, time.Duration, bool, error) {
	// get the certkey pair we will sign with. We're going to add the cert to a ca bundle so we can recognize the chain it signs back to the signer
	csrSigner, err := lister.Secrets(operatorclient.OperatorNamespace).Get("csr-signer")
	if apierrors.IsNotFound(err) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}

	// the CSR signing controller only accepts a single cert.  make sure we only ever have one (not multiple to construct a larger chain)
	certBytes, signingKey, useAfter, _, err := extractSigner(csrSigner)
	if certBytes == nil || signingKey == nil || err != nil {
		return nil, 0, false, err
	}

	// make sure we wait five minutes to propagate the change to other components, like kas for trust
	useAfter = useAfter.Add(5 * time.Minute)
	now := time.Now()

	oldSigner, err := client.Secrets(operatorclient.TargetNamespace).Get(ctx, "csr-signer", metav1.GetOptions{})
	_, _, _, oldUseBefore, _ := extractSigner(oldSigner)
	switch {
	case apierrors.IsNotFound(err):
		// apply the secret

	case oldUseBefore.Before(now):
		// apply the secret

	case now.After(useAfter):
		// apply the secret

	default:
		// wait a little while longer until after the useAfter
		return nil, useAfter.Sub(now) + 10*time.Second, false, nil
	}

	csrSigner = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "csr-signer"},
		Data: map[string][]byte{
			"tls.crt": certBytes,
			"tls.key": signingKey,
		},
		Type: corev1.SecretTypeTLS,
	}
	secret, modified, err := resourceapply.ApplySecret(ctx, client, recorder, csrSigner)
	return secret, 0, modified, err
}

func extractSigner(csrSigner *corev1.Secret) ([]byte, []byte, time.Time, time.Time, error) {
	useAfter := time.Unix(0, 0)
	useBefore := time.Unix(0, 0)

	if csrSigner == nil {
		return nil, nil, useAfter, useBefore, nil
	}

	signingCert := csrSigner.Data["tls.crt"]
	if len(signingCert) == 0 {
		return nil, nil, useAfter, useBefore, nil
	}
	signingKey := csrSigner.Data["tls.key"]
	if len(signingKey) == 0 {
		return nil, nil, useAfter, useBefore, nil
	}
	signingCertKeyPair, err := crypto.GetCAFromBytes(signingCert, signingKey)
	if err != nil {
		return nil, nil, useAfter, useBefore, err
	}
	certBytes, err := crypto.EncodeCertificates(signingCertKeyPair.Config.Certs[0])
	if err != nil {
		return nil, nil, useAfter, useBefore, err
	}

	useAfter = signingCertKeyPair.Config.Certs[0].NotBefore
	useBefore = signingCertKeyPair.Config.Certs[0].NotAfter

	return certBytes, signingKey, useAfter, useBefore, nil
}

func ManageCSRIntermediateCABundle(ctx context.Context, lister corev1listers.SecretLister, client corev1client.ConfigMapsGetter, recorder events.Recorder) (*corev1.ConfigMap, bool, error) {
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

	csrSignerCA, err := client.ConfigMaps(operatorclient.OperatorNamespace).Get(ctx, "csr-signer-ca", metav1.GetOptions{})
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

	return resourceapply.ApplyConfigMap(ctx, client, recorder, csrSignerCA)
}

func ensureKubeControllerManagerTrustedCA(ctx context.Context, client corev1client.CoreV1Interface, recorder events.Recorder) error {
	required := resourceread.ReadConfigMapV1OrDie(v411_00_assets.MustAsset("v4.1.0/kube-controller-manager/trusted-ca-cm.yaml"))
	cmCLient := client.ConfigMaps(operatorclient.TargetNamespace)

	cm, err := cmCLient.Get(ctx, "trusted-ca-bundle", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			_, err = cmCLient.Create(ctx, required, metav1.CreateOptions{})
		}
		return err
	}

	// update if modified by the user
	if val, ok := cm.Labels["config.openshift.io/inject-trusted-cabundle"]; !ok || val != "true" {
		cm.Labels["config.openshift.io/inject-trusted-cabundle"] = "true"
		_, err = cmCLient.Update(ctx, cm, metav1.UpdateOptions{})
		return err
	}

	return err
}

func proxyMapToEnvVars(proxyConfig map[string]string) []corev1.EnvVar {
	if proxyConfig == nil {
		return nil
	}

	envVars := []corev1.EnvVar{}
	for k, v := range proxyConfig {
		envVars = append(envVars, corev1.EnvVar{Name: k, Value: v})
	}

	// need to sort the slice so that kube-controller-manager-pod configmap does not change all the time
	sort.Slice(envVars, func(i, j int) bool { return envVars[i].Name < envVars[j].Name })
	return envVars
}
