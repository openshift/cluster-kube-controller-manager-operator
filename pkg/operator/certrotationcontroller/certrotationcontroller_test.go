package certrotationcontroller

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	corev1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
	clocktesting "k8s.io/utils/clock/testing"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

// TestCSRSigningCertRotationWithFakeClock proves the Clock fields added by
// https://github.com/openshift/library-go/pull/2570 drive the CSR signing cert
// rotation (mirroring the CSRSigningCert controller construction) without
// real time passing: one fake clock is set on all four components, the clock
// is advanced past 75% of the target's validity, and the next sync rotates.
func TestCSRSigningCertRotationWithFakeClock(t *testing.T) {
	fakeClock := clocktesting.NewFakePassiveClock(time.Now().Truncate(time.Second))

	secretIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	configMapIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	client := kubefake.NewSimpleClientset()
	recorder := events.NewInMemoryRecorder("test", fakeClock)

	namespace := operatorclient.OperatorNamespace

	signer := certrotation.RotatedSigningCASecret{
		Namespace: namespace,
		// this is not a typo, this is the signer of the signer
		Name: "csr-signer-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-controller-manager",
		},
		Validity:        24 * time.Hour,
		Refresh:         12 * time.Hour,
		CertificateName: "kube-controller-manager.csr-signer-signer",
		Lister:          corev1listers.NewSecretLister(secretIndexer),
		Client:          client.CoreV1(),
		EventRecorder:   recorder,
		Clock:           fakeClock,
	}

	caBundle := certrotation.CABundleConfigMap{
		Namespace: namespace,
		Name:      "csr-controller-signer-ca",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-controller-manager",
		},
		Lister:        corev1listers.NewConfigMapLister(configMapIndexer),
		Client:        client.CoreV1(),
		EventRecorder: recorder,
		Clock:         fakeClock,
	}

	target := certrotation.RotatedSelfSignedCertKeySecret{
		Namespace: namespace,
		Name:      "csr-signer",
		AdditionalAnnotations: certrotation.AdditionalAnnotations{
			JiraComponent: "kube-controller-manager",
		},
		// Refresh of half the validity makes the refresh window open at 50%;
		// the test advances past 75% so both the refresh rule and the 80% rule
		// are behind and rotation is unambiguous.
		Validity:        time.Hour,
		Refresh:         30 * time.Minute,
		CertificateName: "kube-controller-manager.csr-signer",
		CertCreator: &certrotation.SignerRotation{
			SignerName: "kube-csr-signer",
			Clock:      fakeClock,
		},
		Lister:        corev1listers.NewSecretLister(secretIndexer),
		Client:        client.CoreV1(),
		EventRecorder: recorder,
		Clock:         fakeClock,
	}

	sync := func() (*corev1.Secret, error) {
		ca, _, err := signer.EnsureSigningCertKeyPair(context.TODO())
		if err != nil {
			return nil, err
		}
		caBundleCerts, err := caBundle.EnsureConfigMapCABundle(context.TODO(), ca, namespace+"/csr-signer-signer")
		if err != nil {
			return nil, err
		}
		secret, err := target.EnsureTargetCertKeyPair(context.TODO(), ca, caBundleCerts)
		if err != nil {
			return nil, err
		}
		// mirror the informers feeding the written objects back to the listers
		signerSecret, err := client.CoreV1().Secrets(namespace).Get(context.TODO(), "csr-signer-signer", metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if err := secretIndexer.Add(signerSecret); err != nil {
			return nil, err
		}
		caBundleConfigMap, err := client.CoreV1().ConfigMaps(namespace).Get(context.TODO(), "csr-controller-signer-ca", metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if err := configMapIndexer.Add(caBundleConfigMap); err != nil {
			return nil, err
		}
		if err := secretIndexer.Add(secret); err != nil {
			return nil, err
		}
		return secret, nil
	}

	secret, err := sync()
	if err != nil {
		t.Fatal(err)
	}

	// Advance past 75% of the target's 1h validity.
	fakeClock.SetTime(fakeClock.Now().Add(45 * time.Minute))

	client.ClearActions()
	newSecret, err := sync()
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(newSecret.Data["tls.crt"], secret.Data["tls.crt"]) {
		t.Error("expected the certificate to rotate after advancing the clock")
	}
	secretUpdated := false
	for _, action := range client.Actions() {
		if action.Matches("update", "secrets") {
			secretUpdated = true
		}
	}
	if !secretUpdated {
		t.Errorf("expected secret to get updated, but only found actions: %v", client.Actions())
	}

	// The new certificate must be stamped at the fake clock's time, proving
	// generation (not just the rotation decision) honors the injected clock.
	newCA, err := crypto.GetCAFromBytes(newSecret.Data["tls.crt"], newSecret.Data["tls.key"])
	if err != nil {
		t.Fatal(err)
	}
	wantNotBefore := fakeClock.Now().Add(-time.Second)
	if !newCA.Config.Certs[0].NotBefore.Equal(wantNotBefore) {
		t.Errorf("rotated NotBefore = %v, want %v (generation must use the fake clock)", newCA.Config.Certs[0].NotBefore, wantNotBefore)
	}

	// Steady state: without advancing the clock, another sync must not rotate.
	client.ClearActions()
	steadySecret, err := sync()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(steadySecret.Data["tls.crt"], newSecret.Data["tls.crt"]) {
		t.Error("expected the certificate to be unchanged in steady state")
	}
	for _, action := range client.Actions() {
		if action.Matches("update", "secrets") || action.Matches("create", "secrets") {
			t.Errorf("expected no secret writes in steady state, found: %v", action)
		}
	}
}
