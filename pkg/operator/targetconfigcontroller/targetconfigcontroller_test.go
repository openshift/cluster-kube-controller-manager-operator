package targetconfigcontroller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openshift/api/annotations"
	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/utils/clock"
)

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
		t.Fatalf(err.Error())
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
		t.Fatalf(err.Error())
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
		t.Fatalf(err.Error())
	}
	certs, err := x509.ParseCertificates(derBytes)
	if err != nil {
		t.Fatalf(err.Error())
	}
	if len(certs) != 1 {
		t.Fatalf(err.Error())
	}
	rootcaCert := certs[0]
	ca := &crypto.TLSCertificateConfig{
		Certs: []*x509.Certificate{rootcaCert},
		Key:   rootcaPrivateKey,
	}
	cert, key, err := ca.GetPEMBytes()
	if err != nil {
		t.Fatalf(err.Error())
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
