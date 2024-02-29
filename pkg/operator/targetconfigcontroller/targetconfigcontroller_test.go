package targetconfigcontroller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
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
			_, delay, changed, err := ManageCSRSigner(context.Background(), lister, client.CoreV1(), events.NewInMemoryRecorder("target-config-controller"))
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
