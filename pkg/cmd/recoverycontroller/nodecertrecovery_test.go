package recoverycontroller

import (
	"context"
	"encoding/pem"
	"testing"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	corev1listers "k8s.io/client-go/listers/core/v1"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

func TestParseCSRRequest(t *testing.T) {
	csr := newCSR(t, csrOpts{name: "csr", cn: "system:node:master-0"})
	if _, err := parseCSRRequest(csr); err != nil {
		t.Fatalf("valid request: unexpected error: %v", err)
	}

	block, _ := pem.Decode(csr.Spec.Request)
	wrongType := csr.DeepCopy()
	wrongType.Spec.Request = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})
	if _, err := parseCSRRequest(wrongType); err == nil {
		t.Fatal("PEM block of type CERTIFICATE: expected an error")
	}

	garbage := csr.DeepCopy()
	garbage.Spec.Request = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte("not DER")})
	if _, err := parseCSRRequest(garbage); err == nil {
		t.Fatal("unparseable DER: expected an error")
	}
}

func TestNodeCertRecoveryEnabled(t *testing.T) {
	otherNamespace := enabledToggle("true")
	otherNamespace.Namespace = operatorclient.TargetNamespace
	noKey := enabledToggle("true")
	noKey.Data = nil
	tests := []struct {
		name      string
		configMap *corev1.ConfigMap
		want      bool
	}{
		{name: "no ConfigMap"},
		{name: "enabled", configMap: enabledToggle("true"), want: true},
		{name: "not exactly true", configMap: enabledToggle("True")},
		{name: "false", configMap: enabledToggle("false")},
		{name: "no enabled key", configMap: noKey},
		{name: "in another namespace", configMap: otherNamespace},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			if tc.configMap != nil {
				_ = indexer.Add(tc.configMap)
			}
			got, err := nodeCertRecoveryEnabled(corev1listers.NewConfigMapLister(indexer))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("enabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCSRSignerValid(t *testing.T) {
	notBefore, notAfter := testNow.Add(-time.Hour), testNow.Add(time.Hour)
	garbage := signerSecret(t, notBefore, notAfter)
	garbage.Data[corev1.TLSCertKey] = []byte("not a certificate")
	tests := []struct {
		name   string
		secret *corev1.Secret
		now    time.Time
		want   bool
	}{
		{name: "no Secret", now: testNow},
		{name: "unparseable certificate", secret: garbage, now: testNow},
		{name: "not yet valid", secret: signerSecret(t, notBefore, notAfter), now: notBefore.Add(-time.Second)},
		{name: "valid from NotBefore", secret: signerSecret(t, notBefore, notAfter), now: notBefore, want: true},
		{name: "valid", secret: signerSecret(t, notBefore, notAfter), now: testNow, want: true},
		{name: "expired at NotAfter", secret: signerSecret(t, notBefore, notAfter), now: notAfter},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			if tc.secret != nil {
				_ = indexer.Add(tc.secret)
			}
			got, reason := csrSignerValid(corev1listers.NewSecretLister(indexer), tc.now)
			if got != tc.want {
				t.Fatalf("valid = %v (reason %q), want %v", got, reason, tc.want)
			}
			if got != (reason == "") {
				t.Fatalf("valid = %v with reason %q: a reason must be given exactly when it isn't valid", got, reason)
			}
		})
	}
}

func TestUsagesMatch(t *testing.T) {
	const (
		digitalSignature = certificatesv1.UsageDigitalSignature
		keyEncipherment  = certificatesv1.UsageKeyEncipherment
		clientAuth       = certificatesv1.UsageClientAuth
		serverAuth       = certificatesv1.UsageServerAuth
	)
	tests := []struct {
		name   string
		usages []certificatesv1.KeyUsage
		want   bool
	}{
		{name: "exactly the first set", usages: []certificatesv1.KeyUsage{digitalSignature, clientAuth}, want: true},
		{name: "exactly the second set", usages: []certificatesv1.KeyUsage{keyEncipherment, digitalSignature, clientAuth}, want: true},
		{name: "another order", usages: []certificatesv1.KeyUsage{clientAuth, digitalSignature}, want: true},
		{name: "subset", usages: []certificatesv1.KeyUsage{clientAuth}},
		{name: "superset", usages: []certificatesv1.KeyUsage{digitalSignature, clientAuth, serverAuth}},
		{name: "duplicate usage", usages: []certificatesv1.KeyUsage{digitalSignature, digitalSignature, clientAuth}},
		{name: "none", usages: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := usagesMatch(tc.usages, kubeletClientUsages, kubeletClientUsagesLegacy); got != tc.want {
				t.Fatalf("usagesMatch(%v) = %v, want %v", tc.usages, got, tc.want)
			}
		})
	}
}

func TestApproveCSR(t *testing.T) {
	approvedAt := testNow.Add(-time.Second)

	t.Run("adds an Approved condition through the approval subresource", func(t *testing.T) {
		csr := newCSR(t, csrOpts{name: "csr-1", cn: "system:node:master-0"})
		client := fake.NewSimpleClientset(csr)
		client.ClearActions()
		approved, err := approveCSR(context.Background(), client, csr, "master-0", "message", approvedAt)
		if err != nil || !approved {
			t.Fatalf("approved = %v, err = %v; want true, nil", approved, err)
		}
		if len(csr.Status.Conditions) != 0 {
			t.Fatal("the caller's CSR was modified")
		}
		actions := client.Actions()
		if len(actions) != 1 || actions[0].GetVerb() != "update" || actions[0].GetSubresource() != "approval" {
			t.Fatalf("actions %v, want one update of the approval subresource", actions)
		}
		got := actions[0].(clienttesting.UpdateAction).GetObject().(*certificatesv1.CertificateSigningRequest).Status.Conditions
		want := certificatesv1.CertificateSigningRequestCondition{
			Type:           certificatesv1.CertificateApproved,
			Status:         corev1.ConditionTrue,
			Reason:         KubeletCSRRecoveryApproveReason,
			Message:        "message",
			LastUpdateTime: metav1.NewTime(approvedAt),
		}
		// lastTransitionTime stays empty so kube-apiserver stamps it; the serving approver relies on that.
		if len(got) != 1 || got[0] != want {
			t.Fatalf("conditions %+v, want only %+v", got, want)
		}
	})

	t.Run("a CSR deleted meanwhile is ignored", func(t *testing.T) {
		csr := newCSR(t, csrOpts{name: "csr-gone", cn: "system:node:master-0"})
		approved, err := approveCSR(context.Background(), fake.NewSimpleClientset(), csr, "master-0", "message", approvedAt)
		if err != nil || approved {
			t.Fatalf("approved = %v, err = %v; want false, nil", approved, err)
		}
	})

	t.Run("a conflict is returned", func(t *testing.T) {
		csr := newCSR(t, csrOpts{name: "csr-1", cn: "system:node:master-0"})
		client := fake.NewSimpleClientset(csr)
		client.PrependReactor("update", "certificatesigningrequests", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "certificates.k8s.io", Resource: "certificatesigningrequests"}, csr.Name, nil)
		})
		approved, err := approveCSR(context.Background(), client, csr, "master-0", "message", approvedAt)
		if !apierrors.IsConflict(err) || approved {
			t.Fatalf("approved = %v, err = %v; want false and a Conflict", approved, err)
		}
	})
}
