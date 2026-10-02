package recoverycontroller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	certificateslisters "k8s.io/client-go/listers/certificates/v1"
	coordinationlisters "k8s.io/client-go/listers/coordination/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/library-go/pkg/operator/events"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

type fakePassiveClock struct{ now time.Time }

func (f *fakePassiveClock) Now() time.Time                  { return f.now }
func (f *fakePassiveClock) Since(t time.Time) time.Duration { return f.now.Sub(t) }

var testNow = time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)

type csrOpts struct {
	name     string
	cn       string
	org      []string
	signer   string
	username string
	groups   []string
	usages   []certificatesv1.KeyUsage
	created  time.Time
	dnsNames []string
	ips      []net.IP
	emails   []string
	uris     []*url.URL
	request  []byte
	approved bool
	denied   bool
}

func newCSR(t *testing.T, o csrOpts) *certificatesv1.CertificateSigningRequest {
	t.Helper()
	if o.org == nil {
		o.org = []string{"system:nodes"}
	}
	if o.signer == "" {
		o.signer = kubeletClientSignerName
	}
	if o.username == "" {
		o.username = nodeBootstrapperUsername
	}
	if o.groups == nil {
		o.groups = []string{"system:serviceaccounts:openshift-machine-config-operator", "system:serviceaccounts", "system:authenticated"}
	}
	if o.usages == nil {
		o.usages = []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageClientAuth}
	}
	if o.created.IsZero() {
		o.created = testNow.Add(-time.Minute)
	}
	request := o.request
	if request == nil {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
			Subject:        pkix.Name{CommonName: o.cn, Organization: o.org},
			DNSNames:       o.dnsNames,
			IPAddresses:    o.ips,
			EmailAddresses: o.emails,
			URIs:           o.uris,
		}, key)
		if err != nil {
			t.Fatal(err)
		}
		request = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	}
	csr := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{Name: o.name, CreationTimestamp: metav1.NewTime(o.created)},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:    request,
			SignerName: o.signer,
			Username:   o.username,
			Groups:     o.groups,
			Usages:     o.usages,
		},
	}
	if o.approved {
		csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{Type: certificatesv1.CertificateApproved, Status: corev1.ConditionTrue, Reason: "KubectlApprove"})
	}
	if o.denied {
		csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{Type: certificatesv1.CertificateDenied, Status: corev1.ConditionTrue})
	}
	return csr
}

func TestRecoveryClientCSRNodeName(t *testing.T) {
	u, _ := url.Parse("spiffe://node")
	tests := []struct {
		name     string
		opts     csrOpts
		wantNode string
	}{
		{name: "valid", opts: csrOpts{cn: "system:node:master-0"}, wantNode: "master-0"},
		{name: "valid legacy usages", opts: csrOpts{cn: "system:node:worker-1", usages: []certificatesv1.KeyUsage{certificatesv1.UsageKeyEncipherment, certificatesv1.UsageDigitalSignature, certificatesv1.UsageClientAuth}}, wantNode: "worker-1"},
		{name: "serving signer", opts: csrOpts{cn: "system:node:master-0", signer: certificatesv1.KubeletServingSignerName}},
		{name: "wrong requester", opts: csrOpts{cn: "system:node:master-0", username: "system:node:master-0"}},
		{name: "missing group", opts: csrOpts{cn: "system:node:master-0", groups: []string{"system:serviceaccounts", "system:authenticated"}}},
		{name: "extra group", opts: csrOpts{cn: "system:node:master-0", groups: []string{"system:serviceaccounts:openshift-machine-config-operator", "system:serviceaccounts", "system:authenticated", "system:masters"}}},
		{name: "wrong organization", opts: csrOpts{cn: "system:node:master-0", org: []string{"system:masters"}}},
		{name: "extra organization", opts: csrOpts{cn: "system:node:master-0", org: []string{"system:nodes", "system:masters"}}},
		{name: "cn without prefix", opts: csrOpts{cn: "master-0"}},
		{name: "cn with empty node name", opts: csrOpts{cn: "system:node:"}},
		{name: "dns san", opts: csrOpts{cn: "system:node:master-0", dnsNames: []string{"master-0"}}},
		{name: "ip san", opts: csrOpts{cn: "system:node:master-0", ips: []net.IP{net.ParseIP("10.0.0.1")}}},
		{name: "email san", opts: csrOpts{cn: "system:node:master-0", emails: []string{"a@b.c"}}},
		{name: "uri san", opts: csrOpts{cn: "system:node:master-0", uris: []*url.URL{u}}},
		{name: "server auth usage", opts: csrOpts{cn: "system:node:master-0", usages: []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageClientAuth, certificatesv1.UsageServerAuth}}},
		{name: "missing client auth usage", opts: csrOpts{cn: "system:node:master-0", usages: []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature}}},
		{name: "duplicate usages", opts: csrOpts{cn: "system:node:master-0", usages: []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageClientAuth, certificatesv1.UsageClientAuth}}},
		{name: "garbage request", opts: csrOpts{cn: "system:node:master-0", request: []byte("not a pem")}},
		{name: "wrong pem type", opts: csrOpts{cn: "system:node:master-0", request: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.name = "csr"
			got, err := recoveryClientCSRNodeName(newCSR(t, tt.opts))
			if tt.wantNode == "" {
				if err == nil {
					t.Fatalf("expected no match, got node %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantNode {
				t.Fatalf("got node %q, want %q", got, tt.wantNode)
			}
		})
	}
}

type fixture struct {
	toggle *corev1.ConfigMap
	signer *corev1.Secret
	// leases are in the informer cache. liveLeases are in the API; nil means the same as leases.
	leases     []*coordinationv1.Lease
	liveLeases []*coordinationv1.Lease
	nodes      []string
	// deletingNodes exist in the API with a deletionTimestamp, held by a finalizer.
	deletingNodes []string
	csrs          []*certificatesv1.CertificateSigningRequest
	reactor       clienttesting.ReactionFunc
	leaseGetErr   error
	wasOnline     bool
}

func enabledToggle(value string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.GlobalUserSpecifiedConfigNamespace, Name: NodeClientCertRecoveryConfigMapName},
		Data:       map[string]string{"enabled": value},
	}
}

func signerSecret(t *testing.T, notBefore, notAfter time.Time) *corev1.Secret {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-signer"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: operatorclient.TargetNamespace, Name: "csr-signer"},
		Data:       map[string][]byte{"tls.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})},
	}
}

func validSigner(t *testing.T) *corev1.Secret {
	return signerSecret(t, testNow.Add(-2*time.Minute), testNow.Add(30*24*time.Hour))
}

func lease(node string, renew *time.Time) *coordinationv1.Lease {
	l := &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: nodeLeaseNamespace, Name: node}}
	if renew != nil {
		mt := metav1.NewMicroTime(*renew)
		l.Spec.RenewTime = &mt
	}
	return l
}

func ptrTime(t time.Time) *time.Time { return &t }

// staleHeartbeat is the last heartbeat before a long power-off.
var staleHeartbeat = testNow.Add(-50 * time.Hour)

func (f fixture) run(t *testing.T) (*fake.Clientset, events.InMemoryRecorder, *kubeletClientCSRApprover, error) {
	t.Helper()
	cmIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	secretIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	leaseIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	csrIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if f.toggle != nil {
		_ = cmIndexer.Add(f.toggle)
	}
	if f.signer != nil {
		_ = secretIndexer.Add(f.signer)
	}
	for _, l := range f.leases {
		_ = leaseIndexer.Add(l)
	}
	var objs []runtime.Object
	liveLeases := f.liveLeases
	if liveLeases == nil {
		liveLeases = f.leases
	}
	for _, l := range liveLeases {
		objs = append(objs, l)
	}
	for _, n := range f.nodes {
		objs = append(objs, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: n}})
	}
	for _, n := range f.deletingNodes {
		deleting := metav1.NewTime(testNow.Add(-time.Minute))
		objs = append(objs, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: n, DeletionTimestamp: &deleting, Finalizers: []string{"test/hold"}}})
	}
	for _, csr := range f.csrs {
		_ = csrIndexer.Add(csr)
		objs = append(objs, csr)
	}
	client := fake.NewSimpleClientset(objs...)
	if f.reactor != nil {
		client.PrependReactor("update", "certificatesigningrequests", f.reactor)
	}
	if f.leaseGetErr != nil {
		client.PrependReactor("get", "leases", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, f.leaseGetErr
		})
	}
	client.ClearActions()
	recorder := events.NewInMemoryRecorder("test", &fakePassiveClock{now: testNow})
	c := &kubeletClientCSRApprover{
		kubeClient:      client,
		csrLister:       certificateslisters.NewCertificateSigningRequestLister(csrIndexer),
		leaseLister:     coordinationlisters.NewLeaseLister(leaseIndexer),
		configMapLister: corev1listers.NewConfigMapLister(cmIndexer),
		secretLister:    corev1listers.NewSecretLister(secretIndexer),
		clock:           &fakePassiveClock{now: testNow},
		recorder:        recorder,
		enabled:         f.wasOnline,
	}
	err := c.sync(context.Background(), nil)
	return client, recorder, c, err
}

func approvals(t *testing.T, client *fake.Clientset) []string {
	t.Helper()
	var names []string
	for _, a := range client.Actions() {
		if a.GetVerb() != "update" || a.GetResource().Resource != "certificatesigningrequests" {
			continue
		}
		if a.GetSubresource() != "approval" {
			t.Fatalf("unexpected CSR update on subresource %q", a.GetSubresource())
		}
		csr := a.(clienttesting.UpdateAction).GetObject().(*certificatesv1.CertificateSigningRequest)
		var approved bool
		for _, c := range csr.Status.Conditions {
			if c.Type == certificatesv1.CertificateDenied {
				t.Fatalf("CSR %s was denied", csr.Name)
			}
			if c.Type == certificatesv1.CertificateApproved && c.Reason == KubeletCSRRecoveryApproveReason && c.Status == corev1.ConditionTrue {
				approved = true
			}
		}
		if !approved {
			t.Fatalf("CSR %s updated without an Approved condition from this controller", csr.Name)
		}
		names = append(names, csr.Name)
	}
	return names
}

func eventReasons(recorder events.InMemoryRecorder) []string {
	var reasons []string
	for _, e := range recorder.Events() {
		reasons = append(reasons, e.Reason)
	}
	return reasons
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestKubeletClientCSRApproverSync(t *testing.T) {
	afterPowerOn := testNow.Add(-3 * time.Minute)
	master0 := func(t *testing.T, name string, created time.Time) *certificatesv1.CertificateSigningRequest {
		return newCSR(t, csrOpts{name: name, cn: "system:node:master-0", created: created})
	}

	tests := []struct {
		name          string
		fixture       func(t *testing.T) fixture
		wantApproved  []string
		wantNoActions bool
		wantEvents    []string
		wantErr       bool
	}{
		{
			name: "toggle missing: no API calls",
			fixture: func(t *testing.T) fixture {
				return fixture{signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
			wantNoActions: true,
		},
		{
			name: "toggle not exactly true: no API calls",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("TRUE"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
			wantNoActions: true,
		},
		{
			name: "toggle false after being on: disabled event, no API calls",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("false"), wasOnline: true}
			},
			wantNoActions: true,
			wantEvents:    []string{"NodeCertRecoveryDisabled"},
		},
		{
			name: "enabled, nothing pending: enabled event only",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t)}
			},
			wantNoActions: true,
			wantEvents:    []string{"NodeCertRecoveryEnabled"},
		},
		{
			name: "happy path: every pending CSR of a stale node is approved",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{
						master0(t, "csr-c", testNow.Add(-1*time.Minute)),
						master0(t, "csr-a", testNow.Add(-31*time.Minute)),
						master0(t, "csr-b", testNow.Add(-16*time.Minute)),
					}}
			},
			wantApproved: []string{"csr-a", "csr-b", "csr-c"},
			wantEvents:   []string{"NodeCertRecoveryEnabled", "KubeletClientCSRApproved", "KubeletClientCSRApproved", "KubeletClientCSRApproved"},
		},
		{
			name: "two nodes: only the stale one is approved",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t),
					leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat), lease("worker-0", ptrTime(testNow.Add(-10*time.Second)))},
					nodes:  []string{"master-0", "worker-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{
						master0(t, "csr-m", afterPowerOn),
						newCSR(t, csrOpts{name: "csr-w", cn: "system:node:worker-0", created: afterPowerOn}),
					}}
			},
			wantApproved: []string{"csr-m"},
		},
		{
			name: "signer missing",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
			wantNoActions: true,
		},
		{
			name: "signer expired",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: signerSecret(t, testNow.Add(-48*time.Hour), testNow.Add(-24*time.Hour)),
					leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
			wantNoActions: true,
		},
		{
			name: "signer not yet valid",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: signerSecret(t, testNow.Add(time.Minute), testNow.Add(24*time.Hour)),
					leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
			wantNoActions: true,
		},
		{
			name: "heartbeat fresh",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", ptrTime(testNow.Add(-4*time.Minute)))}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
			wantNoActions: true,
		},
		{
			name: "heartbeat Lease missing",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
			wantNoActions: true,
		},
		{
			name: "no heartbeat ever sent",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", nil)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
			wantNoActions: true,
		},
		{
			name: "node missing: no approval",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
		},
		{
			name: "node being deleted: no approval",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)},
					deletingNodes: []string{"master-0"},
					csrs:          []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
		},
		{
			name: "heartbeat stale in the cache but fresh in the API: no approval",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), nodes: []string{"master-0"},
					leases:     []*coordinationv1.Lease{lease("master-0", ptrTime(testNow.Add(-6*time.Minute)))},
					liveLeases: []*coordinationv1.Lease{lease("master-0", ptrTime(testNow.Add(-time.Second)))},
					csrs:       []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
		},
		{
			name: "heartbeat stale in the cache but Lease gone from the API: no approval",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), nodes: []string{"master-0"},
					leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, liveLeases: []*coordinationv1.Lease{},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)}}
			},
		},
		{
			name: "live heartbeat Lease lookup fails: error, no approval",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs:        []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)},
					leaseGetErr: apierrors.NewServiceUnavailable("etcd unavailable")}
			},
			wantErr: true,
		},
		{
			name: "csr is compared against the API's last heartbeat, not the cache's",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), nodes: []string{"master-0"},
					leases:     []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)},
					liveLeases: []*coordinationv1.Lease{lease("master-0", ptrTime(testNow.Add(-40*time.Minute)))},
					csrs:       []*certificatesv1.CertificateSigningRequest{master0(t, "csr-old", testNow.Add(-time.Hour)), master0(t, "csr-new", afterPowerOn)}}
			},
			wantApproved: []string{"csr-new"},
		},
		{
			name: "csr created before the last heartbeat is skipped",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-old", staleHeartbeat.Add(-time.Hour)), master0(t, "csr-new", afterPowerOn)}}
			},
			wantApproved: []string{"csr-new"},
		},
		{
			name: "approved, denied and serving CSRs are skipped",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{
						newCSR(t, csrOpts{name: "csr-approved", cn: "system:node:master-0", created: afterPowerOn, approved: true}),
						newCSR(t, csrOpts{name: "csr-denied", cn: "system:node:master-0", created: afterPowerOn, denied: true}),
						newCSR(t, csrOpts{name: "csr-serving", cn: "system:node:master-0", created: afterPowerOn, signer: certificatesv1.KubeletServingSignerName, username: "system:node:master-0", groups: []string{"system:nodes", "system:authenticated"}}),
					}}
			},
			wantNoActions: true,
		},
		{
			name: "conflict on approval returns an error",
			fixture: func(t *testing.T) fixture {
				return fixture{toggle: enabledToggle("true"), signer: validSigner(t), leases: []*coordinationv1.Lease{lease("master-0", &staleHeartbeat)}, nodes: []string{"master-0"},
					csrs: []*certificatesv1.CertificateSigningRequest{master0(t, "csr-a", afterPowerOn)},
					reactor: func(action clienttesting.Action) (bool, runtime.Object, error) {
						return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "certificates.k8s.io", Resource: "certificatesigningrequests"}, "csr-a", nil)
					}}
			},
			wantApproved: []string{"csr-a"},
			wantErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, recorder, _, err := tt.fixture(t).run(t)
			if (err != nil) != tt.wantErr {
				t.Fatalf("sync error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantNoActions && len(client.Actions()) != 0 {
				t.Fatalf("expected no API calls, got %v", client.Actions())
			}
			if got := approvals(t, client); !equalStrings(got, tt.wantApproved) {
				t.Fatalf("approved %v, want %v", got, tt.wantApproved)
			}
			if tt.wantEvents != nil {
				if got := eventReasons(recorder); !equalStrings(got, tt.wantEvents) {
					t.Fatalf("events %v, want %v", got, tt.wantEvents)
				}
			}
		})
	}
}

func TestKubeletClientCSRApproverEnabledEventOncePerTransition(t *testing.T) {
	f := fixture{toggle: enabledToggle("true"), signer: validSigner(t)}
	_, recorder, c, err := f.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.sync(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := eventReasons(recorder); !equalStrings(got, []string{"NodeCertRecoveryEnabled"}) {
		t.Fatalf("events %v, want a single NodeCertRecoveryEnabled", got)
	}
}
