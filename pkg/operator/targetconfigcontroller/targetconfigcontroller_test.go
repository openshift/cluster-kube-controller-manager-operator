package targetconfigcontroller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ghodss/yaml"
	"github.com/openshift/api/annotations"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	configfake "github.com/openshift/client-go/config/clientset/versioned/fake"
	configinformers "github.com/openshift/client-go/config/informers/externalversions"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/configobservation/configobservercontroller"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/configobserver/featuregates"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
)

var (
	codec = scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...)
)

// fakeResourceSyncer implements resourcesynccontroller.ResourceSyncer for testing
type fakeResourceSyncer struct{}

func (f *fakeResourceSyncer) SyncConfigMap(destination, source resourcesynccontroller.ResourceLocation) error {
	return nil
}

func (f *fakeResourceSyncer) SyncSecret(destination, source resourcesynccontroller.ResourceLocation) error {
	return nil
}

// fakeSyncContext implements factory.SyncContext for testing
type fakeSyncContext struct {
	recorder events.Recorder
}

func (f *fakeSyncContext) Queue() workqueue.RateLimitingInterface {
	return nil
}

func (f *fakeSyncContext) QueueKey() string {
	return ""
}

func (f *fakeSyncContext) Recorder() events.Recorder {
	return f.recorder
}

// fakeOperatorClientWrapper wraps StaticPodOperatorClient to add UpdateOperatorSpec support
type fakeOperatorClientWrapper struct {
	v1helpers.StaticPodOperatorClient
}

func (w *fakeOperatorClientWrapper) UpdateOperatorSpec(ctx context.Context, resourceVersion string, spec *operatorv1.OperatorSpec) (*operatorv1.OperatorSpec, string, error) {
	// Simulate kube-apiserver behavior: convert Object to Raw if Raw is nil
	if spec.ObservedConfig.Object != nil && len(spec.ObservedConfig.Raw) == 0 {
		rawBytes, err := json.Marshal(spec.ObservedConfig.Object)
		if err != nil {
			return nil, "", fmt.Errorf("failed to marshal ObservedConfig.Object to Raw: %v", err)
		}
		spec.ObservedConfig.Raw = rawBytes
	}

	// The library-go fake doesn't support UpdateOperatorSpec, so we call UpdateStaticPodOperatorSpec instead
	currentSpec, _, _, err := w.StaticPodOperatorClient.GetStaticPodOperatorState()
	if err != nil {
		return nil, "", err
	}

	// Update only the OperatorSpec portion
	currentSpec.OperatorSpec = *spec
	_, _, err = w.StaticPodOperatorClient.UpdateStaticPodOperatorSpec(ctx, resourceVersion, currentSpec)
	if err != nil {
		return nil, "", err
	}

	return spec, resourceVersion, nil
}

// newAPIServerWithTLSGroups creates an APIServer with a custom TLS security profile
// The MinTLSVersion is always set to VersionTLS12 and ciphers are predefined.
// Pass nil for groups to create a profile without groups.
func newAPIServerWithTLSGroups(groups []configv1.TLSGroup) *configv1.APIServer {
	return &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.APIServerSpec{
			TLSSecurityProfile: &configv1.TLSSecurityProfile{
				Type: configv1.TLSProfileCustomType,
				Custom: &configv1.CustomTLSProfile{
					TLSProfileSpec: configv1.TLSProfileSpec{
						Ciphers: []string{
							"ECDHE-ECDSA-AES128-GCM-SHA256",
							"ECDHE-RSA-AES128-GCM-SHA256",
						},
						MinTLSVersion: configv1.VersionTLS12,
						Groups:        groups,
					},
				},
			},
		},
	}
}

// setupFakeClients creates fake Kubernetes and config clients with required resources for testing
func setupFakeClients(t *testing.T, apiServer *configv1.APIServer) (
	kubernetes.Interface,
	v1helpers.KubeInformersForNamespaces,
	configinformers.SharedInformerFactory,
) {
	// Create required resources in the target namespace
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "localhost-recovery-client",
			Namespace: operatorclient.TargetNamespace,
			UID:       "test-uid",
		},
	}
	token := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "localhost-recovery-client-token",
			Namespace: operatorclient.TargetNamespace,
			Annotations: map[string]string{
				corev1.ServiceAccountUIDKey: "test-uid",
			},
		},
		Data: map[string][]byte{
			"token":  []byte("test-token"),
			"ca.crt": []byte("test-ca"),
		},
	}

	// Create required ConfigMaps for manageServiceAccountCABundle
	testCA, err := crypto.MakeSelfSignedCAConfig("test-ca", 24*time.Hour)
	if err != nil {
		t.Fatalf("failed to create test CA: %v", err)
	}
	testCACertPEM, _, err := testCA.GetPEMBytes()
	if err != nil {
		t.Fatalf("failed to get CA PEM bytes: %v", err)
	}

	kubeAPIServerServerCA := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver-server-ca",
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		},
		Data: map[string]string{
			"ca-bundle.crt": string(testCACertPEM),
		},
	}
	defaultIngressCert := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default-ingress-cert",
			Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
		},
		Data: map[string]string{
			"ca-bundle.crt": string(testCACertPEM),
		},
	}

	fakeKubeClient := fake.NewSimpleClientset(sa, token, kubeAPIServerServerCA, defaultIngressCert)
	kubeInformersForNamespaces := v1helpers.NewKubeInformersForNamespaces(
		fakeKubeClient,
		operatorclient.GlobalUserSpecifiedConfigNamespace,
		operatorclient.GlobalMachineSpecifiedConfigNamespace,
		operatorclient.TargetNamespace,
		operatorclient.OperatorNamespace,
	)

	// Create Infrastructure object
	infrastructure := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Status: configv1.InfrastructureStatus{
			APIServerInternalURL: "https://127.0.0.1:443",
			InfrastructureName:   "test-cluster",
		},
	}

	// Create Network object
	network := &configv1.Network{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: configv1.NetworkSpec{
			ClusterNetwork: []configv1.ClusterNetworkEntry{
				{CIDR: "10.128.0.0/14"},
			},
			ServiceNetwork: []string{"172.30.0.0/16"},
		},
		Status: configv1.NetworkStatus{
			ClusterNetwork: []configv1.ClusterNetworkEntry{
				{CIDR: "10.128.0.0/14"},
			},
			ServiceNetwork: []string{"172.30.0.0/16"},
		},
	}

	// Create Proxy object
	proxy := &configv1.Proxy{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}

	// Create Node object
	node := &configv1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
	}

	// Build list of objects to pre-populate the fake config client
	configObjects := []runtime.Object{infrastructure, network, proxy, node}
	if apiServer != nil {
		configObjects = append(configObjects, apiServer)
	}
	fakeConfigClient := configfake.NewSimpleClientset(configObjects...)
	configInformers := configinformers.NewSharedInformerFactory(fakeConfigClient, 0)

	// Populate required informer caches
	configInformers.Config().V1().Infrastructures().Informer().GetIndexer().Add(infrastructure)
	configInformers.Config().V1().Networks().Informer().GetIndexer().Add(network)
	configInformers.Config().V1().Proxies().Informer().GetIndexer().Add(proxy)
	configInformers.Config().V1().Nodes().Informer().GetIndexer().Add(node)
	if apiServer != nil {
		configInformers.Config().V1().APIServers().Informer().GetIndexer().Add(apiServer)
	}

	return fakeKubeClient, kubeInformersForNamespaces, configInformers
}

// fakeOperatorLister implements cache.GenericLister for testing
type fakeOperatorLister struct{}

func (f *fakeOperatorLister) List(selector labels.Selector) (ret []runtime.Object, err error) {
	return nil, nil
}

func (f *fakeOperatorLister) Get(name string) (runtime.Object, error) {
	// Return a default KubeControllerManager object as unstructured
	kcm := &operatorv1.KubeControllerManager{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec: operatorv1.KubeControllerManagerSpec{
			UseMoreSecureServiceCA: true,
		},
	}
	// Convert to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(kcm)
	if err != nil {
		return nil, err
	}
	return &unstructured.Unstructured{Object: unstructuredObj}, nil
}

func (f *fakeOperatorLister) ByNamespace(namespace string) cache.GenericNamespaceLister {
	return nil
}

// decodeKCMPod extracts and decodes the controller manager pod from a ConfigMap
func decodeKCMPod(t *testing.T, configMap *corev1.ConfigMap) *corev1.Pod {
	t.Helper()
	rawPod, exist := configMap.Data["pod.yaml"]
	if !exist {
		t.Fatal("didn't find pod.yaml")
	}

	actualPod := &corev1.Pod{}
	if err := runtime.DecodeInto(codec, []byte(rawPod), actualPod); err != nil {
		t.Fatal(err)
	}

	return actualPod
}

func TestIsRequiredConfigPresent(t *testing.T) {
	tests := []struct {
		name          string
		config        string
		expectedError string
	}{
		{
			name: "unparseable",
			config: `{
		 "servingInfo": {
		}
		`,
			expectedError: "error parsing config",
		},
		{
			name:          "empty",
			config:        ``,
			expectedError: "no observedConfig",
		},
		{
			name: "null-cluster-name",
			config: `{
			 "extendedArguments": {
			   "cluster-name": null
			 }
		 }
		`,
			expectedError: "extendedArguments.cluster-name null in config",
		},
		{
			name: "missing-cluster-name",
			config: `{
			 "extendedArguments": {
			   "cluster-name": []
			 }
		 }
		`,
			expectedError: "extendedArguments.cluster-name empty in config",
		},
		{
			name: "empty-string-cluster-name",
			config: `{
			 "extendedArguments": {
			   "cluster-name": ""
			 }
		 }
        `,
			expectedError: "extendedArguments.cluster-name empty in config",
		},
		{
			name: "good",
			config: `{
			 "extendedArguments": {
			   "cluster-name": ["some-name"],
			   "feature-gates": ["some-name"]
			 },
			 "featureGates": ["some-name"]
		 }
		`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := isRequiredConfigPresent([]byte(test.config))
			switch {
			case actual == nil && len(test.expectedError) == 0:
			case actual == nil && len(test.expectedError) != 0:
				t.Fatal(actual)
			case actual != nil && len(test.expectedError) == 0:
				t.Fatal(actual)
			case actual != nil && len(test.expectedError) != 0 && !strings.Contains(actual.Error(), test.expectedError):
				t.Fatal(actual)
			}
		})
	}
}

func TestManageCSRSigner(t *testing.T) {
	type Test struct {
		name           string
		secret         *corev1.Secret
		target         *corev1.Secret
		expectedDelay  time.Duration
		expectedChange bool
		expectedError  bool
	}

	tests := []Test{
		{
			name: "input certificate start date from the past with enough delay for propagation",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now().Add(-10*time.Minute), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: false,
			expectedError:  false,
		},
		{
			name: "input certificate with good dates but missing target",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now().Add(-10*time.Minute), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			target: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "missing-target"},
			},
			expectedDelay:  0,
			expectedChange: true,
			expectedError:  false,
		},
		{
			name: "input certificate with start validity now - expect delay",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now(), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  5 * time.Minute,
			expectedChange: false,
			expectedError:  false,
		},
		{
			name: "input certificate with start validity from the past but within propagation delay - expect delay",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now().Add(-3*time.Minute), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  2 * time.Minute,
			expectedChange: false,
			expectedError:  false,
		},
		{
			name: "input certificate with start validity from the future a lot - expect delay",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now().Add(1*time.Hour), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			target: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.TargetNamespace},
				Data:       makeCerts(t, time.Now().Add(-10*time.Minute), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  65 * time.Minute,
			expectedChange: false,
			expectedError:  false,
		},
		{
			name: "input certificate with start validity from the future, but target outdated",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now().Add(1*time.Hour), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			target: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.TargetNamespace},
				Data:       makeCerts(t, time.Now().Add(-2*time.Hour), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: true,
			expectedError:  false,
		},
		{
			name: "input certificate with start validity now but missing target - must change",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now(), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			target: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "missing-target"},
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: true,
			expectedError:  false,
		},
		{
			name: "both certificate dates are outdated",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now().Add(-2*time.Hour), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: false,
			expectedError:  false,
		},
		{
			name: "both certificate dates are outdated but target is missing",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now().Add(-2*time.Hour), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			target: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.TargetNamespace},
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: true,
			expectedError:  false,
		},
		{
			name: "incomplete target certificate",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now(), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			target: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.TargetNamespace},
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: true,
			expectedError:  false,
		},
		{
			name: "target certificate dates are outdated",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now(), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			target: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.TargetNamespace},
				Data:       makeCerts(t, time.Now().Add(-2*time.Hour), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: true,
			expectedError:  false,
		},
		{
			name: "borked input certs",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       map[string][]byte{"tls.crt": {6, 6, 6}, "tls.key": {6, 6, 6}},
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: false,
			expectedError:  true,
		},
		{
			name: "borked target certs",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.OperatorNamespace},
				Data:       makeCerts(t, time.Now(), 1*time.Hour),
				Type:       corev1.SecretTypeTLS,
			},
			target: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "csr-signer", Namespace: operatorclient.TargetNamespace},
				Data:       map[string][]byte{"tls.crt": {6, 6, 6}, "tls.key": {6, 6, 6}},
				Type:       corev1.SecretTypeTLS,
			},
			expectedDelay:  0,
			expectedChange: true,
			expectedError:  false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target := test.target
			if target == nil {
				target = test.secret.DeepCopy()
				target.Namespace = operatorclient.TargetNamespace
			}
			client := fake.NewSimpleClientset(target)
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			if err := indexer.Add(test.secret); err != nil {
				t.Fatal(err.Error())
			}
			lister := corev1listers.NewSecretLister(indexer)
			_, delay, changed, err := ManageCSRSigner(context.Background(), lister, client.CoreV1(), events.NewInMemoryRecorder("target-config-controller", clock.RealClock{}))
			// there's a 10s difference we need to account for to avoid flakes
			offset := 10 * time.Second
			if delay < test.expectedDelay-offset || delay > test.expectedDelay+offset {
				t.Errorf("Unexpected delay: %v vs %v", test.expectedDelay, delay)
			}
			if test.expectedChange != changed {
				t.Errorf("Unexpected change: %v vs %v", test.expectedChange, changed)
			}
			if !test.expectedError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if test.expectedError && err == nil {
				t.Error("Expected error but got none")
			}
		})
	}

}

func makeCerts(t *testing.T, notAfter time.Time, duration time.Duration) map[string][]byte {
	// below code is copied from vendor/github.com/openshift/library-go/pkg/crypto/crypto.go
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err.Error())
	}
	rootcaPublicKey := &privateKey.PublicKey
	rootcaPrivateKey := privateKey
	var publicKeyHash []byte
	if err == nil {
		hash := sha1.New()
		hash.Write(rootcaPublicKey.N.Bytes())
		publicKeyHash = hash.Sum(nil)
	}
	if err != nil {
		t.Fatal(err.Error())
	}
	rootcaTemplate := &x509.Certificate{
		Subject:               pkix.Name{CommonName: fmt.Sprintf("kube_csr-signer_@%d", time.Now().Unix())},
		SignatureAlgorithm:    x509.SHA256WithRSA,
		NotBefore:             notAfter.Add(-1 * time.Second),
		NotAfter:              notAfter.Add(duration),
		SerialNumber:          big.NewInt(1),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		AuthorityKeyId:        publicKeyHash,
		SubjectKeyId:          publicKeyHash,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, rootcaTemplate, rootcaTemplate, rootcaPublicKey, rootcaPrivateKey)
	if err != nil {
		t.Fatal(err.Error())
	}
	certs, err := x509.ParseCertificates(derBytes)
	if err != nil {
		t.Fatal(err.Error())
	}
	if len(certs) != 1 {
		t.Fatal(err.Error())
	}
	rootcaCert := certs[0]
	ca := &crypto.TLSCertificateConfig{
		Certs: []*x509.Certificate{rootcaCert},
		Key:   rootcaPrivateKey,
	}
	cert, key, err := ca.GetPEMBytes()
	if err != nil {
		t.Fatal(err.Error())
	}
	return map[string][]byte{"tls.crt": cert, "tls.key": key}
}

func TestReadKubeControllerManagerArgs(t *testing.T) {
	testCases := []struct {
		input    map[string]interface{}
		expected []string
	}{
		{
			input: map[string]interface{}{
				"otherArguments": map[string]interface{}{
					"enable-dynamic-provisioning": []interface{}{"true"},
					"allocate-node-cidrs":         []interface{}{"true"},
				},
			},
			expected: nil,
		},
		{
			input: map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"enable-dynamic-provisioning": []interface{}{"true"},
					"allocate-node-cidrs":         []interface{}{"true"},
				},
			},
			expected: []string{"--allocate-node-cidrs=true", "--enable-dynamic-provisioning=true"},
		},
		{
			input: map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"controllers": []interface{}{"*", "-ttl", "-bootstrapsigner", "-tokencleaner"},
				},
			},
			expected: []string{"--controllers=*", "--controllers=-bootstrapsigner", "--controllers=-tokencleaner", "--controllers=-ttl"},
		},
		{
			input: map[string]interface{}{
				"extendedArguments": map[string]interface{}{
					"cluster-signing-cert-file": []interface{}{"/etc/kubernetes/static-pod-certs/secrets/csr-signer/tls.crt"},
					"cluster-signing-key-file":  []interface{}{"/etc/kubernetes/static-pod-certs/secrets/csr-signer/tls.key"},
					"kube-api-qps":              []interface{}{"150"},
					"kube-api-burst":            []interface{}{"300"},
				},
			},
			expected: []string{"--cluster-signing-cert-file=/etc/kubernetes/static-pod-certs/secrets/csr-signer/tls.crt", "--cluster-signing-key-file=/etc/kubernetes/static-pod-certs/secrets/csr-signer/tls.key", "--kube-api-burst=300", "--kube-api-qps=150"},
		},
	}
	for i, tc := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			output := GetKubeControllerManagerArgs(tc.input)
			if !reflect.DeepEqual(output, tc.expected) {
				t.Errorf("Unexpected difference between %s and %s", tc.expected, output)
			}
		})
	}
}

type configMapLister struct {
	client    *fake.Clientset
	namespace string
}

var _ corev1listers.ConfigMapNamespaceLister = &configMapLister{}
var _ corev1listers.ConfigMapLister = &configMapLister{}

func (l *configMapLister) List(selector labels.Selector) (ret []*corev1.ConfigMap, err error) {
	list, err := l.client.CoreV1().ConfigMaps(l.namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: selector.String(),
	})

	var items []*corev1.ConfigMap
	for i := range list.Items {
		items = append(items, &list.Items[i])
	}

	return items, err
}

func (l *configMapLister) ConfigMaps(namespace string) corev1listers.ConfigMapNamespaceLister {
	return &configMapLister{
		client:    l.client,
		namespace: namespace,
	}
}

func (l *configMapLister) Get(name string) (*corev1.ConfigMap, error) {
	return l.client.CoreV1().ConfigMaps(l.namespace).Get(context.Background(), name, metav1.GetOptions{})
}

// generateTemporaryCertificate creates a new temporary, self-signed x509 certificate
// and a corresponding RSA private key. The certificate will be valid for 24 hours.
// It returns the PEM-encoded private key and certificate.
func generateTemporaryCertificate() (certPEM []byte, err error) {
	// 1. Generate a new RSA private key
	// We are using a 2048-bit key, which is a common and secure choice.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	// 2. Create a template for the certificate
	// This template contains all the details about the certificate.
	certTemplate := x509.Certificate{
		// SerialNumber is a unique number for the certificate.
		// We generate a large random number to ensure uniqueness.
		SerialNumber: big.NewInt(time.Now().Unix()),

		// Subject contains information about the owner of the certificate.
		Subject: pkix.Name{
			Organization: []string{"My Company, Inc."},
			Country:      []string{"US"},
			Province:     []string{"California"},
			Locality:     []string{"San Francisco"},
			CommonName:   "localhost", // Common Name (CN)
		},

		// NotBefore is the start time of the certificate's validity.
		NotBefore: time.Now(),
		// NotAfter is the end time. We set it to 24 hours from now.
		NotAfter: time.Now().Add(24 * time.Hour),

		// KeyUsage defines the purpose of the public key contained in the certificate.
		KeyUsage: x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		// ExtKeyUsage indicates extended purposes (e.g., server/client authentication).
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},

		// BasicConstraintsValid indicates if this is a CA certificate.
		// Since this is a self-signed certificate, we set it to true.
		BasicConstraintsValid: true,
	}

	// 3. Create the certificate
	// x509.CreateCertificate creates a new certificate based on a template.
	// Since this is a self-signed certificate, the parent certificate is the template itself.
	// We use the public key from our generated private key.
	// The final argument is the private key used to sign the certificate.
	certBytes, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, err
	}

	// 4. Encode the certificate to the PEM format
	certPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	return certPEM, nil
}

func TestManageServiceAccountCABundle(t *testing.T) {
	cert1, err := generateTemporaryCertificate()
	require.NoError(t, err)

	cert2, err := generateTemporaryCertificate()
	require.NoError(t, err)

	tests := []struct {
		name               string
		existingConfigMaps []*corev1.ConfigMap
		expectedConfigMap  *corev1.ConfigMap
		expectedChanged    bool
	}{
		{
			name:               "create new serviceaccount-ca configmap when none exists",
			existingConfigMaps: []*corev1.ConfigMap{},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "serviceaccount-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-controller-manager",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": "",
				},
			},
			expectedChanged: true,
		},
		{
			name: "one source",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-server-ca",
						Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "serviceaccount-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-controller-manager",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "set annotations if missing",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "serviceaccount-ca",
						Namespace:   operatorclient.TargetNamespace,
						Annotations: map[string]string{},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-server-ca",
						Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "serviceaccount-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-controller-manager",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "annotations update",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serviceaccount-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							"foo": "bar",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-server-ca",
						Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "serviceaccount-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-controller-manager",
						"foo":                          "bar",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "update existing client-ca configmap when new source appears",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serviceaccount-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							annotations.OpenShiftComponent: "kube-controller-manager",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-server-ca",
						Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				// Add a new source that wasn't in the original bundle
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "default-ingress-cert",
						Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert2),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "serviceaccount-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-controller-manager",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1) + string(cert2),
				},
			},
			expectedChanged: true,
		},
		{
			name: "no changes required",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "serviceaccount-ca",
						Namespace: operatorclient.TargetNamespace,
						Annotations: map[string]string{
							annotations.OpenShiftComponent: "kube-controller-manager",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver-server-ca",
						Namespace: operatorclient.GlobalMachineSpecifiedConfigNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "serviceaccount-ca",
					Namespace: operatorclient.TargetNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent: "kube-controller-manager",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()

			// Create existing configmaps
			for _, cm := range test.existingConfigMaps {
				_, err := client.CoreV1().ConfigMaps(cm.Namespace).Create(context.Background(), cm, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			lister := &configMapLister{
				client:    client,
				namespace: "",
			}

			recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

			// Call the function under test
			resultConfigMap, changed, err := manageServiceAccountCABundle(context.Background(), lister, client.CoreV1(), recorder)

			// Assert error expectations
			require.NoError(t, err)

			// Assert change expectations
			require.Equal(t, test.expectedChanged, changed, "Expected changed=%v, got changed=%v", test.expectedChanged, changed)

			// Compare with expected configmap
			require.Equal(t, test.expectedConfigMap, resultConfigMap)

			// Verify the configmap exists in the cluster
			storedConfigMap, err := client.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(context.Background(), "serviceaccount-ca", metav1.GetOptions{})
			require.NoError(t, err)
			require.NotNil(t, storedConfigMap)

			// Ensure the returned configmap matches what's stored in the cluster
			require.Equal(t, storedConfigMap, resultConfigMap, "returned configmap should match stored configmap")

			// Verify events were recorded if changes were made
			if test.expectedChanged {
				events := recorder.Events()
				require.NotEmpty(t, events)
			}
		})
	}
}

func TestManageCSRCABundle(t *testing.T) {
	cert1, err := generateTemporaryCertificate()
	require.NoError(t, err)

	cert2, err := generateTemporaryCertificate()
	require.NoError(t, err)

	tests := []struct {
		name               string
		existingConfigMaps []*corev1.ConfigMap
		expectedConfigMap  *corev1.ConfigMap
		expectedChanged    bool
	}{
		{
			name:               "create new csr-controller-ca configmap when none exists",
			existingConfigMaps: []*corev1.ConfigMap{},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "csr-controller-ca",
					Namespace: operatorclient.OperatorNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent:   "kube-controller-manager",
						annotations.OpenShiftDescription: "CA to recognize the CSRs (both serving and client) signed by the kube-controller-manager.",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": "",
				},
			},
			expectedChanged: true,
		},
		{
			name: "one source",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-signer-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "csr-controller-ca",
					Namespace: operatorclient.OperatorNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent:   "kube-controller-manager",
						annotations.OpenShiftDescription: "CA to recognize the CSRs (both serving and client) signed by the kube-controller-manager.",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "set annotations if missing",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "csr-controller-ca",
						Namespace:   operatorclient.OperatorNamespace,
						Annotations: map[string]string{},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-signer-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "csr-controller-ca",
					Namespace: operatorclient.OperatorNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent:   "kube-controller-manager",
						annotations.OpenShiftDescription: "CA to recognize the CSRs (both serving and client) signed by the kube-controller-manager.",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "annotations update",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-controller-ca",
						Namespace: operatorclient.OperatorNamespace,
						Annotations: map[string]string{
							"foo": "bar",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-signer-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "csr-controller-ca",
					Namespace: operatorclient.OperatorNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent:   "kube-controller-manager",
						annotations.OpenShiftDescription: "CA to recognize the CSRs (both serving and client) signed by the kube-controller-manager.",
						"foo":                            "bar",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: true,
		},
		{
			name: "update existing client-ca configmap when new source appears",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-controller-ca",
						Namespace: operatorclient.OperatorNamespace,
						Annotations: map[string]string{
							annotations.OpenShiftComponent:   "kube-controller-manager",
							annotations.OpenShiftDescription: "CA to recognize the CSRs (both serving and client) signed by the kube-controller-manager.",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-signer-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				// Add a new source that wasn't in the original bundle
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-controller-signer-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert2),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "csr-controller-ca",
					Namespace: operatorclient.OperatorNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent:   "kube-controller-manager",
						annotations.OpenShiftDescription: "CA to recognize the CSRs (both serving and client) signed by the kube-controller-manager.",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1) + string(cert2),
				},
			},
			expectedChanged: true,
		},
		{
			name: "no changes required",
			existingConfigMaps: []*corev1.ConfigMap{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-controller-ca",
						Namespace: operatorclient.OperatorNamespace,
						Annotations: map[string]string{
							annotations.OpenShiftComponent:   "kube-controller-manager",
							annotations.OpenShiftDescription: "CA to recognize the CSRs (both serving and client) signed by the kube-controller-manager.",
						},
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "csr-signer-ca",
						Namespace: operatorclient.OperatorNamespace,
					},
					Data: map[string]string{
						"ca-bundle.crt": string(cert1),
					},
				},
			},
			expectedConfigMap: &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "csr-controller-ca",
					Namespace: operatorclient.OperatorNamespace,
					Annotations: map[string]string{
						annotations.OpenShiftComponent:   "kube-controller-manager",
						annotations.OpenShiftDescription: "CA to recognize the CSRs (both serving and client) signed by the kube-controller-manager.",
					},
				},
				Data: map[string]string{
					"ca-bundle.crt": string(cert1),
				},
			},
			expectedChanged: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()

			// Create existing configmaps
			for _, cm := range test.existingConfigMaps {
				_, err := client.CoreV1().ConfigMaps(cm.Namespace).Create(context.Background(), cm, metav1.CreateOptions{})
				require.NoError(t, err)
			}

			lister := &configMapLister{
				client:    client,
				namespace: "",
			}

			recorder := events.NewInMemoryRecorder("test", clock.RealClock{})

			// Call the function under test
			resultConfigMap, changed, err := ManageCSRCABundle(context.Background(), lister, client.CoreV1(), recorder)

			// Assert error expectations
			require.NoError(t, err)

			// Assert change expectations
			require.Equal(t, test.expectedChanged, changed, "Expected changed=%v, got changed=%v", test.expectedChanged, changed)

			// Compare with expected configmap
			require.Equal(t, test.expectedConfigMap, resultConfigMap)

			// Verify the configmap exists in the cluster
			storedConfigMap, err := client.CoreV1().ConfigMaps(operatorclient.OperatorNamespace).Get(context.Background(), "csr-controller-ca", metav1.GetOptions{})
			require.NoError(t, err)
			require.NotNil(t, storedConfigMap)

			// Ensure the returned configmap matches what's stored in the cluster
			require.Equal(t, storedConfigMap, resultConfigMap, "returned configmap should match stored configmap")

			// Verify events were recorded if changes were made
			if test.expectedChanged {
				events := recorder.Events()
				require.NotEmpty(t, events)
			}
		})
	}
}

func TestInt32sToStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []int32
		expected []string
	}{
		{
			name:     "empty slice",
			input:    []int32{},
			expected: []string{},
		},
		{
			name:     "single value",
			input:    []int32{29},
			expected: []string{"29"},
		},
		{
			name:     "multiple values",
			input:    []int32{29, 23, 24},
			expected: []string{"29", "23", "24"},
		},
		{
			name:     "with negative values",
			input:    []int32{-1, 0, 100},
			expected: []string{"-1", "0", "100"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := int32sToStrings(tt.input)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("int32sToStrings(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestManagePod_TLSGroupsInjection(t *testing.T) {
	tests := []struct {
		name                    string
		apiServer               *configv1.APIServer
		featureGateEnabled      bool
		expectedCurvePreference string
		expectCurveArg          bool
	}{
		{
			name: "TLS groups injected when feature gate enabled",
			apiServer: newAPIServerWithTLSGroups([]configv1.TLSGroup{
				configv1.TLSGroupX25519,
				configv1.TLSGroupSecP256r1,
				configv1.TLSGroupSecP384r1,
			}),
			featureGateEnabled:      true,
			expectedCurvePreference: "--tls-curve-preferences=29,23,24",
			expectCurveArg:          true,
		},
		{
			name: "TLS groups not injected when feature gate disabled",
			apiServer: newAPIServerWithTLSGroups([]configv1.TLSGroup{
				configv1.TLSGroupX25519,
				configv1.TLSGroupSecP256r1,
				configv1.TLSGroupSecP384r1,
			}),
		},
		{
			name:               "no TLS groups in profile",
			apiServer:          newAPIServerWithTLSGroups(nil),
			featureGateEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			// setup operator client with wrapper to support UpdateOperatorSpec
			fakeOperatorClient := &fakeOperatorClientWrapper{
				StaticPodOperatorClient: v1helpers.NewFakeStaticPodOperatorClient(
					&operatorv1.StaticPodOperatorSpec{
						OperatorSpec: operatorv1.OperatorSpec{},
					},
					&operatorv1.StaticPodOperatorStatus{
						OperatorStatus: operatorv1.OperatorStatus{},
					},
					nil,
					nil,
				),
			}

			// setup fake clients with all required resources
			fakeKubeClient, kubeInformersForNamespaces, configInformers := setupFakeClients(t, tt.apiServer)

			// create event recorder
			eventRecorder := events.NewInMemoryRecorder("", clock.RealClock{})

			// Create feature gate based on test case
			var featureGates featuregates.FeatureGateAccess
			if tt.featureGateEnabled {
				featureGates = featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{"TLSGroupPreferences", "ControllerManagerConfig"}, nil)
			} else {
				featureGates = featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{"ControllerManagerConfig"}, []configv1.FeatureGateName{"TLSGroupPreferences"})
			}

			// Create config observer - this registers event handlers with informers
			configObserver, err := configobservercontroller.NewConfigObserver(
				fakeOperatorClient,
				configInformers,
				kubeInformersForNamespaces,
				&fakeResourceSyncer{},
				featureGates,
				eventRecorder,
			)
			if err != nil {
				t.Fatalf("failed to create config observer: %v", err)
			}

			// Create a fake lister that returns the operator
			operatorLister := &fakeOperatorLister{}

			// Create target config controller
			targetConfigController := NewTargetConfigController(
				"test-image",
				"test-operator-image",
				"test-cluster-policy-controller-image",
				"test-tools-image",
				"0.0.1-snapshot",
				kubeInformersForNamespaces,
				fakeOperatorClient,
				operatorLister,
				fakeKubeClient,
				configInformers.Config().V1().Infrastructures(),
				eventRecorder,
			)

			// Start informers after controllers have registered their event handlers
			kubeInformersForNamespaces.Start(ctx.Done())
			configInformers.Start(ctx.Done())

			// Run config observer sync to update observed config in operator spec
			if err := configObserver.Sync(ctx, &fakeSyncContext{recorder: eventRecorder}); err != nil {
				t.Logf("WARNING: config observer sync returned error: %v", err)
			}

			// Run target config controller sync to trigger the full production code path
			if err := targetConfigController.Sync(ctx, &fakeSyncContext{recorder: eventRecorder}); err != nil {
				t.Fatalf("targetConfigController.Sync failed: %v", err)
			}

			// Read the generated ConfigMap from the fake kube client
			actualConfigMap, err := fakeKubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(ctx, "kube-controller-manager-pod", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get kube-controller-manager-pod ConfigMap: %v", err)
			}

			actualPod := decodeKCMPod(t, actualConfigMap)

			// Check container args for TLS curve preferences
			foundCurvePreference := false

			for _, arg := range actualPod.Spec.Containers[0].Args {
				if strings.Contains(arg, "--tls-curve-preferences=") {
					foundCurvePreference = true
					if tt.expectCurveArg {
						if !strings.Contains(arg, tt.expectedCurvePreference) {
							t.Errorf("Expected curve preference arg to contain %q, got %q", tt.expectedCurvePreference, arg)
						}
					} else {
						t.Errorf("Did not expect --tls-curve-preferences arg but found %q", arg)
					}
				}
			}

			if tt.expectCurveArg && !foundCurvePreference {
				t.Errorf("Expected to find --tls-curve-preferences arg but didn't")
			}
			if !tt.expectCurveArg && foundCurvePreference {
				t.Errorf("Did not expect to find --tls-curve-preferences arg but found one")
			}
		})
	}
}

func TestManageClusterPolicyControllerConfig_TLSGroupsInjection(t *testing.T) {
	tests := []struct {
		name                         string
		apiServer                    *configv1.APIServer
		featureGateEnabled           bool
		expectedCurvePreferences     []int32
		expectCurvePreferencesInYAML bool
	}{
		{
			name: "TLS groups converted to curve preferences when feature gate enabled",
			apiServer: newAPIServerWithTLSGroups([]configv1.TLSGroup{
				configv1.TLSGroupX25519,
				configv1.TLSGroupSecP256r1,
				configv1.TLSGroupSecP384r1,
			}),
			featureGateEnabled:           true,
			expectedCurvePreferences:     []int32{29, 23, 24},
			expectCurvePreferencesInYAML: true,
		},
		{
			name: "TLS groups not converted when feature gate disabled",
			apiServer: newAPIServerWithTLSGroups([]configv1.TLSGroup{
				configv1.TLSGroupX25519,
				configv1.TLSGroupSecP256r1,
				configv1.TLSGroupSecP384r1,
			}),
			featureGateEnabled:           false,
			expectCurvePreferencesInYAML: false,
		},
		{
			name:                         "no TLS groups in profile",
			apiServer:                    newAPIServerWithTLSGroups(nil),
			featureGateEnabled:           true,
			expectCurvePreferencesInYAML: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())
			defer cancel()

			// setup operator client with wrapper to support UpdateOperatorSpec
			fakeOperatorClient := &fakeOperatorClientWrapper{
				StaticPodOperatorClient: v1helpers.NewFakeStaticPodOperatorClient(
					&operatorv1.StaticPodOperatorSpec{
						OperatorSpec: operatorv1.OperatorSpec{},
					},
					&operatorv1.StaticPodOperatorStatus{
						OperatorStatus: operatorv1.OperatorStatus{},
					},
					nil,
					nil,
				),
			}

			// setup fake clients with all required resources
			fakeKubeClient, kubeInformersForNamespaces, configInformers := setupFakeClients(t, tt.apiServer)

			// create event recorder
			eventRecorder := events.NewInMemoryRecorder("", clock.RealClock{})

			// Create feature gate based on test case
			var featureGates featuregates.FeatureGateAccess
			if tt.featureGateEnabled {
				featureGates = featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{"TLSGroupPreferences", "ControllerManagerConfig"}, nil)
			} else {
				featureGates = featuregates.NewHardcodedFeatureGateAccess([]configv1.FeatureGateName{"ControllerManagerConfig"}, []configv1.FeatureGateName{"TLSGroupPreferences"})
			}

			// Create config observer - this registers event handlers with informers
			configObserver, err := configobservercontroller.NewConfigObserver(
				fakeOperatorClient,
				configInformers,
				kubeInformersForNamespaces,
				&fakeResourceSyncer{},
				featureGates,
				eventRecorder,
			)
			if err != nil {
				t.Fatalf("failed to create config observer: %v", err)
			}

			// Create a fake lister that returns the operator
			operatorLister := &fakeOperatorLister{}

			// Create target config controller
			targetConfigController := NewTargetConfigController(
				"test-image",
				"test-operator-image",
				"test-cluster-policy-controller-image",
				"test-tools-image",
				"0.0.1-snapshot",
				kubeInformersForNamespaces,
				fakeOperatorClient,
				operatorLister,
				fakeKubeClient,
				configInformers.Config().V1().Infrastructures(),
				eventRecorder,
			)

			// Start informers after controllers have registered their event handlers
			kubeInformersForNamespaces.Start(ctx.Done())
			configInformers.Start(ctx.Done())

			// Run config observer sync to update observed config in operator spec
			if err := configObserver.Sync(ctx, &fakeSyncContext{recorder: eventRecorder}); err != nil {
				t.Logf("WARNING: config observer sync returned error: %v", err)
			}

			// Run target config controller sync to trigger the full production code path
			if err := targetConfigController.Sync(ctx, &fakeSyncContext{recorder: eventRecorder}); err != nil {
				t.Fatalf("targetConfigController.Sync failed: %v", err)
			}

			// Read the generated ConfigMap from the fake kube client
			actualConfigMap, err := fakeKubeClient.CoreV1().ConfigMaps(operatorclient.TargetNamespace).Get(ctx, "cluster-policy-controller-config", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("failed to get cluster-policy-controller-config ConfigMap: %v", err)
			}

			// Parse the config.yaml from the ConfigMap
			configYAML, exists := actualConfigMap.Data["config.yaml"]
			if !exists {
				t.Fatal("config.yaml not found in ConfigMap")
			}

			var config map[string]interface{}
			if err := yaml.Unmarshal([]byte(configYAML), &config); err != nil {
				t.Fatalf("failed to unmarshal config.yaml: %v", err)
			}

			// Check for curvePreferences in servingInfo
			curvePrefs, curvePrefsFound, err := unstructured.NestedSlice(config, "servingInfo", "curvePreferences")
			if err != nil {
				t.Fatalf("error checking for curvePreferences: %v", err)
			}

			if tt.expectCurvePreferencesInYAML {
				if !curvePrefsFound {
					t.Errorf("Expected curvePreferences to be set in servingInfo but it wasn't found")
				} else {
					var curvePrefsInt32 []int32
					for _, v := range curvePrefs {
						// YAML unmarshaling can make numbers either int or float64
						switch val := v.(type) {
						case int:
							curvePrefsInt32 = append(curvePrefsInt32, int32(val))
						case float64:
							curvePrefsInt32 = append(curvePrefsInt32, int32(val))
						default:
							t.Fatalf("unexpected type for curve preference: %T", v)
						}
					}
					if !reflect.DeepEqual(curvePrefsInt32, tt.expectedCurvePreferences) {
						t.Errorf("Expected curvePreferences %v, got %v", tt.expectedCurvePreferences, curvePrefsInt32)
					}
				}

				// Verify that groups field is NOT in the final config (it should have been removed)
				_, groupsFound, err := unstructured.NestedStringSlice(config, "servingInfo", "groups")
				if err != nil {
					t.Fatalf("error checking for groups: %v", err)
				}
				if groupsFound {
					t.Errorf("groups field should not be present in final config (it's not a standard Kubernetes field)")
				}
			} else {
				if curvePrefsFound {
					t.Errorf("Did not expect curvePreferences to be set but found %v", curvePrefs)
				}
			}
		})
	}
}

func TestCreateCurvePreferencesOverride(t *testing.T) {
	tests := []struct {
		name                     string
		inputObservedConfig      map[string]interface{}
		expectedCurvePreferences []int32
		expectOverride           bool
		expectedError            string
	}{
		{
			name: "creates override with curve preferences from groups",
			inputObservedConfig: map[string]interface{}{
				"servingInfo": map[string]interface{}{
					"bindAddress": "127.0.0.1:10357",
					"groups":      []interface{}{"X25519", "secp256r1", "secp384r1"},
				},
			},
			expectedCurvePreferences: []int32{29, 23, 24},
			expectOverride:           true,
		},
		{
			name: "no groups field - no override",
			inputObservedConfig: map[string]interface{}{
				"servingInfo": map[string]interface{}{
					"bindAddress": "127.0.0.1:10357",
				},
			},
			expectOverride: false,
		},
		{
			name: "empty groups array - no override",
			inputObservedConfig: map[string]interface{}{
				"servingInfo": map[string]interface{}{
					"bindAddress": "127.0.0.1:10357",
					"groups":      []interface{}{},
				},
			},
			expectOverride: false,
		},
		{
			name: "unrecognized groups - error",
			inputObservedConfig: map[string]interface{}{
				"servingInfo": map[string]interface{}{
					"bindAddress": "127.0.0.1:10357",
					"groups":      []interface{}{"invalid-group"},
				},
			},
			expectedError: "unrecognized groups when reading curve preferences",
		},
		{
			name:           "empty observed config - no override",
			expectOverride: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var inputRaw []byte
			var err error
			if tt.inputObservedConfig != nil {
				inputRaw, err = json.Marshal(tt.inputObservedConfig)
				require.NoError(t, err)
			}

			overrideRaw, err := createCurvePreferencesOverride(inputRaw)

			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
				return
			}

			require.NoError(t, err)

			if !tt.expectOverride {
				require.Nil(t, overrideRaw)
				return
			}

			require.NotNil(t, overrideRaw)

			// Parse the override
			var override map[string]interface{}
			err = json.Unmarshal(overrideRaw, &override)
			require.NoError(t, err)

			// Check that it only contains servingInfo.curvePreferences
			curvePrefs, found, err := unstructured.NestedSlice(override, "servingInfo", "curvePreferences")
			require.NoError(t, err)
			require.True(t, found, "curvePreferences should be set in servingInfo")

			var curvePrefsInt32 []int32
			for _, v := range curvePrefs {
				// JSON unmarshaling makes numbers float64
				if f, ok := v.(float64); ok {
					curvePrefsInt32 = append(curvePrefsInt32, int32(f))
				} else {
					t.Fatalf("unexpected type for curve preference: %T", v)
				}
			}
			require.Equal(t, tt.expectedCurvePreferences, curvePrefsInt32)

			// Verify that groups is set to nil in the override to remove it from final config
			servingInfo, found, err := unstructured.NestedMap(override, "servingInfo")
			require.NoError(t, err)
			require.True(t, found)

			groupsValue, exists := servingInfo["groups"]
			require.True(t, exists, "groups field should exist in override")
			require.Nil(t, groupsValue, "groups should be nil to remove it from final config")
		})
	}
}
