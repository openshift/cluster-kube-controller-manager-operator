package recoverycontroller

import (
	"context"
	"crypto/x509"
	"net"
	"net/url"
	"testing"
	"time"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	certificateslisters "k8s.io/client-go/listers/certificates/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	clienttesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/openshift/library-go/pkg/operator/events"
)

// recoveredAt is when the client approver approved the recovering node's client CSR in these tests.
var recoveredAt = testNow.Add(-3 * time.Minute)

func servingCSR(t *testing.T, name, node string, created time.Time, dnsNames []string, ips ...string) *certificatesv1.CertificateSigningRequest {
	t.Helper()
	return newCSR(t, csrOpts{
		name:     name,
		cn:       "system:node:" + node,
		signer:   kubeletServingSignerName,
		username: "system:node:" + node,
		groups:   []string{"system:nodes", "system:authenticated"},
		usages:   []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageServerAuth},
		created:  created,
		dnsNames: dnsNames,
		ips:      parseIPs(ips),
	})
}

// clientApproval is a node's client CSR approved at the given time with the given reason, as the
// client approver (KubeletCSRRecoveryApproveReason) or machine-approver ("NodeCSRApprove") leaves it:
// kube-apiserver stamps lastTransitionTime, and the approver sets lastUpdateTime.
func clientApproval(t *testing.T, name, node, reason string, at time.Time) *certificatesv1.CertificateSigningRequest {
	t.Helper()
	csr := newCSR(t, csrOpts{name: name, cn: "system:node:" + node, created: at.Add(-time.Minute)})
	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type: certificatesv1.CertificateApproved, Status: corev1.ConditionTrue, Reason: reason,
		LastUpdateTime: metav1.NewTime(at), LastTransitionTime: metav1.NewTime(at),
	})
	return csr
}

func externalDNS(a string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeExternalDNS, Address: a}
}

func internalIP(a string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: a}
}
func hostname(a string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeHostName, Address: a}
}
func internalDNS(a string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeInternalDNS, Address: a}
}

func testNode(name string, addresses ...corev1.NodeAddress) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}, Status: corev1.NodeStatus{Addresses: addresses}}
}

// testMachine returns a Machine linked to node nodeRef; an empty nodeRef leaves it unlinked.
func testMachine(name, nodeRef string, addresses ...corev1.NodeAddress) *machinev1beta1.Machine {
	m := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{Namespace: machineAPINamespace, Name: name},
		Status:     machinev1beta1.MachineStatus{Addresses: addresses},
	}
	if nodeRef != "" {
		m.Status.NodeRef = &corev1.ObjectReference{Kind: "Node", Name: nodeRef}
	}
	return m
}

func testEgressIP(name, node, ip string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]interface{}{
		"status": map[string]interface{}{"items": []interface{}{map[string]interface{}{"node": node, "egressIP": ip}}},
	}}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: egressIPGVR.Group, Version: egressIPGVR.Version, Kind: "EgressIP"})
	u.SetName(name)
	return u
}

// servingFixture is the cluster a serving approver sync sees.
type servingFixture struct {
	toggle *corev1.ConfigMap
	signer *corev1.Secret
	// clientCSRs are only in the client approver's informer cache, which the gate reads.
	clientCSRs []*certificatesv1.CertificateSigningRequest
	// servingCSRs are in both the serving CSR cache and the API.
	servingCSRs []*certificatesv1.CertificateSigningRequest
	nodes       []*corev1.Node
	machines    []*machinev1beta1.Machine
	egressIPs   []*unstructured.Unstructured
	// machineListErr, egressIPListErr and nodeListErr are returned by every list of that resource.
	machineListErr  error
	egressIPListErr error
	nodeListErr     error
	// approveErr is returned by every CSR approval.
	approveErr error
	// clockOffset moves the controller's clock away from testNow, which kube-apiserver's timestamps use.
	clockOffset time.Duration
}

// agentCluster is two recovering control-plane nodes of an agent-installed bare-metal cluster: the Nodes
// report their hostnames, the Machines only IPs. master-0 has one pending serving CSR.
func agentCluster(t *testing.T) servingFixture {
	return servingFixture{
		toggle: enabledToggle("true"),
		signer: validSigner(t),
		clientCSRs: []*certificatesv1.CertificateSigningRequest{
			clientApproval(t, "client-0", "master-0", KubeletCSRRecoveryApproveReason, recoveredAt),
			clientApproval(t, "client-1", "master-1", KubeletCSRRecoveryApproveReason, recoveredAt),
		},
		servingCSRs: []*certificatesv1.CertificateSigningRequest{
			servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80"),
		},
		nodes: []*corev1.Node{
			testNode("master-0", internalIP("192.168.111.80"), hostname("master-0")),
			testNode("master-1", internalIP("192.168.111.81"), hostname("master-1")),
		},
		machines: []*machinev1beta1.Machine{
			testMachine("ostest-master-0", "master-0", internalIP("192.168.111.80")),
			testMachine("ostest-master-1", "master-1", internalIP("192.168.111.81")),
		},
	}
}

func (f servingFixture) run(t *testing.T) (*fake.Clientset, *dynamicfake.FakeDynamicClient, events.InMemoryRecorder, error) {
	t.Helper()
	cmIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	secretIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	clientCSRIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	servingCSRIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if f.toggle != nil {
		_ = cmIndexer.Add(f.toggle)
	}
	if f.signer != nil {
		_ = secretIndexer.Add(f.signer)
	}
	for _, csr := range f.clientCSRs {
		_ = clientCSRIndexer.Add(csr)
	}
	var objs []runtime.Object
	for _, csr := range f.servingCSRs {
		_ = servingCSRIndexer.Add(csr)
		objs = append(objs, csr)
	}
	for _, n := range f.nodes {
		objs = append(objs, n)
	}
	client := fake.NewSimpleClientset(objs...)
	if f.approveErr != nil {
		client.PrependReactor("update", "certificatesigningrequests", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, f.approveErr
		})
	}
	if f.nodeListErr != nil {
		client.PrependReactor("list", "nodes", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, f.nodeListErr
		})
	}

	var dynObjs []runtime.Object
	for _, m := range f.machines {
		content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(m)
		if err != nil {
			t.Fatal(err)
		}
		u := &unstructured.Unstructured{Object: content}
		u.SetGroupVersionKind(machinev1beta1.GroupVersion.WithKind("Machine"))
		dynObjs = append(dynObjs, u)
	}
	for _, e := range f.egressIPs {
		dynObjs = append(dynObjs, e)
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{machineGVR: "MachineList", egressIPGVR: "EgressIPList"}, dynObjs...)
	if f.machineListErr != nil {
		dynamicClient.PrependReactor("list", "machines", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, f.machineListErr
		})
	}
	if f.egressIPListErr != nil {
		dynamicClient.PrependReactor("list", "egressips", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, f.egressIPListErr
		})
	}
	client.ClearActions()
	dynamicClient.ClearActions()

	recorder := events.NewInMemoryRecorder("test", &fakePassiveClock{now: testNow})
	c := &kubeletServingCSRApprover{
		kubeClient:       client,
		dynamicClient:    dynamicClient,
		servingCSRLister: certificateslisters.NewCertificateSigningRequestLister(servingCSRIndexer),
		clientCSRLister:  certificateslisters.NewCertificateSigningRequestLister(clientCSRIndexer),
		configMapLister:  corev1listers.NewConfigMapLister(cmIndexer),
		secretLister:     corev1listers.NewSecretLister(secretIndexer),
		clock:            &fakePassiveClock{now: testNow.Add(f.clockOffset)},
		recorder:         recorder,
	}
	err := c.sync(context.Background(), nil)
	return client, dynamicClient, recorder, err
}

func TestKubeletServingCSRApproverSync(t *testing.T) {
	tests := []struct {
		name    string
		fixture func(t *testing.T) servingFixture
		// wantNoActions requires that the sync made no kube or dynamic API call at all.
		wantNoActions bool
		wantApproved  []string
		wantEvents    []string
		wantErr       bool
	}{
		// The normal cases: the controller must stay idle and not even list anything.
		{
			name: "toggle off: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.toggle = nil
				return f
			},
			wantNoActions: true,
		},
		{
			name: "routine rotation, no client approval: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.clientCSRs = nil
				return f
			},
			wantNoActions: true,
		},
		{
			name: "new node whose client CSR machine-approver approved: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.clientCSRs = []*certificatesv1.CertificateSigningRequest{clientApproval(t, "client-0", "master-0", "NodeCSRApprove", recoveredAt)}
				return f
			},
			wantNoActions: true,
		},
		{
			name: "recovery approval condition not True: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				for _, csr := range f.clientCSRs {
					csr.Status.Conditions[0].Status = corev1.ConditionFalse
				}
				return f
			},
			wantNoActions: true,
		},
		{
			name: "serving CSR created more than the recovery window after the approval: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.clientCSRs = []*certificatesv1.CertificateSigningRequest{
					clientApproval(t, "client-0", "master-0", KubeletCSRRecoveryApproveReason, testNow.Add(-recoveryWindow-3*time.Minute)),
				}
				return f
			},
			wantNoActions: true,
		},
		{
			// The window is measured between kube-apiserver's timestamps, so the controller's clock can't
			// open it either.
			name: "controller clock far behind, serving CSR created after the window: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.clientCSRs = []*certificatesv1.CertificateSigningRequest{
					clientApproval(t, "client-0", "master-0", KubeletCSRRecoveryApproveReason, testNow.Add(-recoveryWindow-3*time.Minute)),
				}
				f.clockOffset = -recoveryWindow
				return f
			},
			wantNoActions: true,
		},
		{
			name: "serving CSR created before the recovery approval: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", recoveredAt.Add(-time.Second), []string{"master-0"}, "192.168.111.80"),
				}
				return f
			},
			wantNoActions: true,
		},
		{
			name: "serving CSRs already approved or denied: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				approved := servingCSR(t, "serving-approved", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80")
				approved.Status.Conditions = []certificatesv1.CertificateSigningRequestCondition{{Type: certificatesv1.CertificateApproved, Status: corev1.ConditionTrue, Reason: "NodeCSRApprove"}}
				denied := servingCSR(t, "serving-denied", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80")
				denied.Status.Conditions = []certificatesv1.CertificateSigningRequestCondition{{Type: certificatesv1.CertificateDenied, Status: corev1.ConditionTrue}}
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{approved, denied}
				return f
			},
			wantNoActions: true,
		},
		{
			name: "csr-signer not valid: no API calls",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.signer = signerSecret(t, testNow.Add(-48*time.Hour), testNow.Add(-24*time.Hour))
				return f
			},
			wantNoActions: true,
		},

		// Recovering nodes whose CSRs must still be left alone.
		{
			name: "AWS-style Machine covers every name and IP: left for machine-approver, no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				addresses := []corev1.NodeAddress{internalIP("10.0.1.5"), internalDNS("ip-10-0-1-5.ec2.internal"), hostname("ip-10-0-1-5")}
				f.nodes = []*corev1.Node{testNode("master-0", addresses...)}
				f.machines = []*machinev1beta1.Machine{testMachine("aws-master-0", "master-0", addresses...)}
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"ip-10-0-1-5.ec2.internal", "ip-10-0-1-5"}, "10.0.1.5"),
				}
				return f
			},
		},
		{
			name: "node has no Machine: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.machines = []*machinev1beta1.Machine{testMachine("ostest-master-1", "master-1", internalIP("192.168.111.81"))}
				return f
			},
		},
		{
			name: "Machine API missing: no approval, no error",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.machineListErr = apierrors.NewNotFound(machineGVR.GroupResource(), "")
				return f
			},
		},
		{
			name: "Node being deleted: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				deleting := metav1.NewTime(testNow.Add(-time.Minute))
				f.nodes[0].DeletionTimestamp = &deleting
				f.nodes[0].Finalizers = []string{"test/hold"}
				return f
			},
		},
		{
			name: "DNS name that isn't the node's: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "api.example.com"}, "192.168.111.80"),
				}
				return f
			},
		},
		{
			name: "IP that isn't the node's: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "10.9.9.9"),
				}
				return f
			},
		},
		{
			name: "node reports another node's IP and asks for it: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, internalIP("192.168.111.81"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "192.168.111.81"),
				}
				return f
			},
		},
		{
			name: "node reports another node's hostname and asks for it: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, hostname("master-1"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "master-1"}, "192.168.111.80"),
				}
				return f
			},
		},

		{
			name: "two Machines claim the node, one covering every SAN: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.machines = append(f.machines, testMachine("ostest-master-0-b", "master-0", internalIP("192.168.111.80"), hostname("master-0")))
				return f
			},
		},
		{
			name: "Machine has no addresses yet: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.machines[0].Status.Addresses = nil
				return f
			},
		},
		{
			name: "no Node for the requester: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes = f.nodes[1:]
				return f
			},
		},
		{
			name: "CSR created between an older and the latest recovery approval: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.clientCSRs = append(f.clientCSRs, clientApproval(t, "client-0-old", "master-0", KubeletCSRRecoveryApproveReason, recoveredAt.Add(-30*time.Minute)))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", recoveredAt.Add(-10*time.Minute), []string{"master-0"}, "192.168.111.80"),
				}
				return f
			},
		},
		// The cross-node guard, one kind of record at a time.
		{
			name: "IP recorded only on another Node: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[1].Status.Addresses = append(f.nodes[1].Status.Addresses, internalIP("192.168.111.181"))
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, internalIP("192.168.111.181"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "192.168.111.181"),
				}
				return f
			},
		},
		{
			name: "IP recorded only on a Machine without a Node: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.machines = append(f.machines, testMachine("ostest-worker-provisioning", "", internalIP("192.168.111.150")))
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, internalIP("192.168.111.150"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "192.168.111.150"),
				}
				return f
			},
		},
		{
			name: "another node's name, not among its addresses: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[1].Status.Addresses = []corev1.NodeAddress{internalIP("192.168.111.81")}
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, hostname("master-1"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "master-1"}, "192.168.111.80"),
				}
				return f
			},
		},
		{
			name: "hostname recorded only as another Node's DNS address: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[1].Status.Addresses = append(f.nodes[1].Status.Addresses, externalDNS("m1.example.test"))
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, externalDNS("m1.example.test"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "m1.example.test"}, "192.168.111.80"),
				}
				return f
			},
		},
		{
			name: "egress IP of a node whose Node is gone: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes = f.nodes[:1]
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, internalIP("192.168.111.200"))
				f.egressIPs = []*unstructured.Unstructured{testEgressIP("egress-a", "master-1", "192.168.111.200")}
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "192.168.111.200"),
				}
				return f
			},
		},
		{
			name: "another Machine's nodeRef name whose Node is gone: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				// master-1's Node was deleted; its Machine, linked to it and listing only an IP, remains.
				f.nodes = f.nodes[:1]
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, hostname("master-1"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "master-1"}, "192.168.111.80"),
				}
				return f
			},
		},
		{
			name: "node name of an egress assignment with no Node: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, hostname("worker-2"))
				f.egressIPs = []*unstructured.Unstructured{testEgressIP("egress-a", "worker-2", "192.168.111.210")}
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "worker-2"}, "192.168.111.80"),
				}
				return f
			},
		},
		{
			name: "IP recorded as another Node's hostname-type address: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[1].Status.Addresses = append(f.nodes[1].Status.Addresses, hostname("192.168.111.181"))
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, internalIP("192.168.111.181"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "192.168.111.181"),
				}
				return f
			},
		},
		{
			name: "another node's IP asked for as a DNS name: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, hostname("192.168.111.81"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "192.168.111.81"}, "192.168.111.80"),
				}
				return f
			},
		},
		{
			name: "wildcard DNS name that covers another node's hostname: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[1].Status.Addresses = append(f.nodes[1].Status.Addresses, internalDNS("master-1.example.test"))
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, internalDNS("*.example.test"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "*.example.test"}, "192.168.111.80"),
				}
				return f
			},
		},

		// Recovering nodes that machine-approver can't approve.
		{
			name: "agent bare metal, IP-only Machine and Node hostname: approved",
			fixture: func(t *testing.T) servingFixture {
				return agentCluster(t)
			},
			wantApproved: []string{"serving-0"},
			wantEvents:   []string{"KubeletServingCSRApproved"},
		},
		{
			name: "several pending CSRs of a recovering node: all approved, oldest first",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-b", "master-0", testNow.Add(-1*time.Minute), []string{"master-0"}, "192.168.111.80"),
					servingCSR(t, "serving-a", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80"),
					servingCSR(t, "serving-c", "master-1", recoveredAt, []string{"master-1"}, "192.168.111.81"),
				}
				return f
			},
			wantApproved: []string{"serving-a", "serving-b", "serving-c"},
		},
		{
			name: "egress IP assigned to the node: approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.egressIPs = []*unstructured.Unstructured{testEgressIP("egress-a", "master-0", "192.168.111.200")}
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "192.168.111.200"),
				}
				return f
			},
			wantApproved: []string{"serving-0"},
		},
		{
			name: "node reports an egress IP assigned to another node and asks for it: no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, internalIP("192.168.111.200"))
				f.egressIPs = []*unstructured.Unstructured{testEgressIP("egress-a", "master-1", "192.168.111.200")}
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "192.168.111.200"),
				}
				return f
			},
		},
		{
			name: "new Node IP after a site move, Machine IP stale: approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[0].Status.Addresses = []corev1.NodeAddress{internalIP("10.20.0.80"), hostname("master-0")}
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "10.20.0.80"),
				}
				return f
			},
			wantApproved: []string{"serving-0"},
		},
		{
			name: "hostname only on the Machine, node name not on it: approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[0].Status.Addresses = []corev1.NodeAddress{internalIP("192.168.111.80")}
				f.machines[0].Status.Addresses = []corev1.NodeAddress{internalIP("192.168.111.80"), internalDNS("master-0.lab.example")}
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0", "master-0.lab.example"}, "192.168.111.80"),
				}
				return f
			},
			wantApproved: []string{"serving-0"},
		},
		{
			name: "controller clock ahead of kube-apiserver's: gate uses the apiserver's time, approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				// The approver's own clock (lastUpdateTime) ran 2 minutes ahead of kube-apiserver's
				// (lastTransitionTime); the serving CSR came 1 minute after the approval by kube-apiserver's clock.
				f.clientCSRs[0].Status.Conditions[0].LastUpdateTime = metav1.NewTime(recoveredAt.Add(2 * time.Minute))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", recoveredAt.Add(time.Minute), []string{"master-0"}, "192.168.111.80"),
				}
				return f
			},
			wantApproved: []string{"serving-0"},
		},
		{
			// Before the window was measured between kube-apiserver's timestamps, a controller clock this far
			// ahead dropped the node as no longer recovering.
			name: "controller clock 2h ahead of kube-apiserver's: approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.clockOffset = 2 * time.Hour
				return f
			},
			wantApproved: []string{"serving-0"},
		},
		{
			name: "serving CSR created just inside the recovery window: approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", recoveredAt.Add(recoveryWindow), []string{"master-0"}, "192.168.111.80"),
				}
				return f
			},
			wantApproved: []string{"serving-0"},
		},
		{
			// An IP-type address that doesn't parse is indexed as a name and blocks nothing.
			name: "another Machine with an IP-type address that doesn't parse: approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.machines[1].Status.Addresses = append(f.machines[1].Status.Addresses, internalIP("192.168.111.081"))
				return f
			},
			wantApproved: []string{"serving-0"},
		},
		{
			name: "EgressIP API missing: approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.egressIPListErr = apierrors.NewNotFound(egressIPGVR.GroupResource(), "")
				return f
			},
			wantApproved: []string{"serving-0"},
		},
		{
			// The accepted risk: an address no node records, such as a virtual IP, can't be checked
			// against anything. Changing this should be a deliberate decision.
			name: "self-reported IP that no node records: approved",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodes[0].Status.Addresses = append(f.nodes[0].Status.Addresses, internalIP("192.168.111.5"))
				f.servingCSRs = []*certificatesv1.CertificateSigningRequest{
					servingCSR(t, "serving-0", "master-0", testNow.Add(-2*time.Minute), []string{"master-0"}, "192.168.111.80", "192.168.111.5"),
				}
				return f
			},
			wantApproved: []string{"serving-0"},
		},

		// Errors are returned so the sync is retried.
		{
			name: "Machine list fails: error, no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.machineListErr = apierrors.NewServiceUnavailable("etcd unavailable")
				return f
			},
			wantErr: true,
		},
		{
			name: "EgressIP list fails: error, no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.egressIPListErr = apierrors.NewServiceUnavailable("etcd unavailable")
				return f
			},
			wantErr: true,
		},
		{
			// An undecodable assignment could hide another node's egress IP from sanOwnedByOtherNode.
			name: "EgressIP status.items entry not an object: error, no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				egressIP := testEgressIP("egress-a", "master-1", "192.168.111.200")
				egressIP.Object["status"] = map[string]interface{}{"items": []interface{}{"master-1=192.168.111.200"}}
				f.egressIPs = []*unstructured.Unstructured{egressIP}
				return f
			},
			wantErr: true,
		},
		{
			name: "EgressIP assignment with a non-string egressIP: error, no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				egressIP := testEgressIP("egress-a", "master-1", "192.168.111.200")
				egressIP.Object["status"] = map[string]interface{}{"items": []interface{}{map[string]interface{}{"node": "master-1", "egressIP": int64(3232263112)}}}
				f.egressIPs = []*unstructured.Unstructured{egressIP}
				return f
			},
			wantErr: true,
		},
		{
			name: "EgressIP assignment whose egressIP doesn't parse: error, no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.egressIPs = []*unstructured.Unstructured{testEgressIP("egress-a", "master-1", "192.168.111.200 ")}
				return f
			},
			wantErr: true,
		},
		{
			name: "EgressIP assignment with no node: error, no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.egressIPs = []*unstructured.Unstructured{testEgressIP("egress-a", "", "192.168.111.200")}
				return f
			},
			wantErr: true,
		},
		{
			name: "Node list fails: error, no approval",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.nodeListErr = apierrors.NewServiceUnavailable("etcd unavailable")
				return f
			},
			wantErr: true,
		},
		{
			name: "conflict on approval: error",
			fixture: func(t *testing.T) servingFixture {
				f := agentCluster(t)
				f.approveErr = apierrors.NewConflict(schema.GroupResource{Group: "certificates.k8s.io", Resource: "certificatesigningrequests"}, "serving-0", nil)
				return f
			},
			// approvals() records the attempt, which failed.
			wantApproved: []string{"serving-0"},
			wantErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, dynamicClient, recorder, err := tt.fixture(t).run(t)
			if (err != nil) != tt.wantErr {
				t.Fatalf("sync error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantNoActions && (len(client.Actions()) != 0 || len(dynamicClient.Actions()) != 0) {
				t.Fatalf("expected no API calls, got %v and %v", client.Actions(), dynamicClient.Actions())
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

func TestServingCSRNodeName(t *testing.T) {
	u, _ := url.Parse("spiffe://node")
	valid := func() csrOpts {
		return csrOpts{
			cn:       "system:node:master-0",
			signer:   kubeletServingSignerName,
			username: "system:node:master-0",
			groups:   []string{"system:nodes", "system:authenticated"},
			usages:   []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageServerAuth},
			dnsNames: []string{"master-0"},
			ips:      []net.IP{net.ParseIP("192.168.111.80")},
		}
	}
	tests := []struct {
		name     string
		change   func(o *csrOpts)
		wantNode string
	}{
		{name: "valid", change: func(o *csrOpts) {}, wantNode: "master-0"},
		{name: "valid legacy usages", change: func(o *csrOpts) {
			o.usages = []certificatesv1.KeyUsage{certificatesv1.UsageKeyEncipherment, certificatesv1.UsageDigitalSignature, certificatesv1.UsageServerAuth}
		}, wantNode: "master-0"},
		{name: "valid with only an IP", change: func(o *csrOpts) { o.dnsNames = nil }, wantNode: "master-0"},
		{name: "extra group is allowed", change: func(o *csrOpts) { o.groups = append(o.groups, "system:extra") }, wantNode: "master-0"},
		{name: "client signer", change: func(o *csrOpts) { o.signer = kubeletClientSignerName }},
		{name: "requester is not a node", change: func(o *csrOpts) { o.username = nodeBootstrapperUsername }},
		{name: "requester with empty node name", change: func(o *csrOpts) { o.username = "system:node:"; o.cn = "system:node:" }},
		{name: "missing system:nodes group", change: func(o *csrOpts) { o.groups = []string{"system:authenticated"} }},
		{name: "common name of another node", change: func(o *csrOpts) { o.cn = "system:node:master-1" }},
		{name: "wrong organization", change: func(o *csrOpts) { o.org = []string{"system:masters"} }},
		{name: "extra organization", change: func(o *csrOpts) { o.org = []string{"system:nodes", "system:masters"} }},
		{name: "client auth usage", change: func(o *csrOpts) {
			o.usages = []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageServerAuth, certificatesv1.UsageClientAuth}
		}},
		{name: "missing server auth usage", change: func(o *csrOpts) { o.usages = []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature} }},
		{name: "duplicate usages", change: func(o *csrOpts) {
			o.usages = []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageServerAuth, certificatesv1.UsageServerAuth}
		}},
		{name: "email san", change: func(o *csrOpts) { o.emails = []string{"a@b.c"} }},
		{name: "uri san", change: func(o *csrOpts) { o.uris = []*url.URL{u} }},
		{name: "no dns or ip san", change: func(o *csrOpts) { o.dnsNames = nil; o.ips = nil }},
		{name: "uppercase hostname is allowed", change: func(o *csrOpts) { o.dnsNames = []string{"Master-0"} }, wantNode: "master-0"},
		{name: "fully qualified hostname is allowed", change: func(o *csrOpts) { o.dnsNames = []string{"ip-10-0-1-5.ec2.internal"} }, wantNode: "master-0"},
		{name: "bare wildcard", change: func(o *csrOpts) { o.dnsNames = []string{"*"} }},
		{name: "wildcard subdomain", change: func(o *csrOpts) { o.dnsNames = []string{"*.apps.example.com"} }},
		{name: "trailing dot", change: func(o *csrOpts) { o.dnsNames = []string{"master-1."} }},
		{name: "empty label", change: func(o *csrOpts) { o.dnsNames = []string{"master..example"} }},
		{name: "IPv4 address as a DNS name", change: func(o *csrOpts) { o.dnsNames = []string{"192.168.111.81"} }},
		{name: "garbage request", change: func(o *csrOpts) { o.request = []byte("not a pem") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := valid()
			tt.change(&o)
			o.name = "csr"
			got, _, err := servingCSRNodeName(newCSR(t, o))
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

// TestMachineCoversSANs checks the copy of machine-approver's Machine-path rule
// (authorizeServingCertWithMachine): true exactly where machine-approver approves.
func TestMachineCoversSANs(t *testing.T) {
	aws := []corev1.NodeAddress{internalIP("10.0.1.5"), internalDNS("ip-10-0-1-5.ec2.internal."), hostname("ip-10-0-1-5")}
	tests := []struct {
		name      string
		addresses []corev1.NodeAddress
		dnsNames  []string
		ips       []string
		egress    []string
		want      bool
	}{
		{name: "every name and IP is a Machine address", addresses: aws, dnsNames: []string{"ip-10-0-1-5.ec2.internal", "ip-10-0-1-5"}, ips: []string{"10.0.1.5"}, want: true},
		{name: "DNS names compare case-insensitively without the trailing dot", addresses: aws, dnsNames: []string{"IP-10-0-1-5.EC2.INTERNAL"}, want: true},
		{name: "IP that is an egress IP", addresses: aws, ips: []string{"10.0.1.5", "10.0.1.200"}, egress: []string{"10.0.1.200"}, want: true},
		{name: "no Machine addresses yet", dnsNames: []string{"ip-10-0-1-5"}, want: false},
		{name: "agent: hostname missing from an IP-only Machine", addresses: []corev1.NodeAddress{internalIP("192.168.111.80")}, dnsNames: []string{"master-0"}, ips: []string{"192.168.111.80"}, want: false},
		{name: "IP not a Machine address", addresses: aws, ips: []string{"10.0.1.6"}, want: false},
		{name: "IP only present as a hostname-type address", addresses: []corev1.NodeAddress{hostname("10.0.1.5")}, ips: []string{"10.0.1.5"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := newCSR(t, csrOpts{name: "csr", cn: "system:node:n", dnsNames: tt.dnsNames, ips: parseIPs(tt.ips)})
			_, parsed, err := servingCSRNodeName(&certificatesv1.CertificateSigningRequest{Spec: certificatesv1.CertificateSigningRequestSpec{
				SignerName: kubeletServingSignerName, Username: "system:node:n", Groups: []string{"system:nodes", "system:authenticated"},
				Usages: []certificatesv1.KeyUsage{certificatesv1.UsageDigitalSignature, certificatesv1.UsageServerAuth}, Request: request.Spec.Request,
			}})
			if err != nil {
				t.Fatal(err)
			}
			got := machineCoversSANs(tt.addresses, parsed, parseIPs(tt.egress))
			if got != tt.want {
				t.Fatalf("machineCoversSANs() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSanOwnedByOtherNode checks the cross-node guard on its own, including requests that
// servingCSRNodeName refuses earlier.
func TestSanOwnedByOtherNode(t *testing.T) {
	a := newRecoveryAddresses()
	a.addNode(testNode("master-0", internalIP("192.168.111.80"), hostname("master-0")))
	a.addNode(testNode("master-1", internalIP("192.168.111.81"), hostname("master-1"), internalDNS("Master-1.Lab.Example.")))
	a.addMachine(machineRecordOf(t, testMachine("ostest-master-0", "master-0", internalIP("192.168.111.80"))))
	a.addMachine(machineRecordOf(t, testMachine("ostest-master-2", "master-2", internalIP("192.168.111.82"))))
	a.addMachine(machineRecordOf(t, testMachine("ostest-worker-new", "", internalIP("192.168.111.150"), internalIP(" 192.168.111.151 "))))
	a.addNode(testNode("worker-5", internalIP("192.168.111.95")))
	a.addNode(testNode("worker-6", internalIP("fd00:0:0:0:0:0:0:6")))
	a.addEgressIP("worker-3", net.ParseIP("192.168.111.210"))
	a.addEgressIP("master-0", net.ParseIP("192.168.111.200"))

	tests := []struct {
		name     string
		dnsNames []string
		ips      []string
		wantErr  bool
	}{
		{name: "the node's own name and addresses", dnsNames: []string{"master-0"}, ips: []string{"192.168.111.80", "192.168.111.200"}},
		{name: "an address nobody records", ips: []string{"192.168.111.5"}},
		{name: "another Node's name, any case", dnsNames: []string{"MASTER-1"}, wantErr: true},
		{name: "another Node's DNS address, trailing dot ignored", dnsNames: []string{"master-1.lab.example"}, wantErr: true},
		{name: "another Node's IP as a DNS name", dnsNames: []string{"192.168.111.81"}, wantErr: true},
		{name: "another Node's IP", ips: []string{"192.168.111.81"}, wantErr: true},
		{name: "another Node's name that is none of its addresses and has no Machine", dnsNames: []string{"worker-5"}, wantErr: true},
		{name: "another Node's IPv6 address recorded in long form", ips: []string{"fd00::6"}, wantErr: true},
		{name: "a Machine's nodeRef name without a Node", dnsNames: []string{"master-2"}, wantErr: true},
		{name: "a Machine's IP without a Node", ips: []string{"192.168.111.82"}, wantErr: true},
		{name: "an unlinked Machine's IP", ips: []string{"192.168.111.150"}, wantErr: true},
		{name: "an unlinked Machine's IP recorded with spaces", ips: []string{"192.168.111.151"}, wantErr: true},
		{name: "an egress node name without a Node", dnsNames: []string{"worker-3"}, wantErr: true},
		{name: "an egress IP of another node", ips: []string{"192.168.111.210"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := a.sanOwnedByOtherNode("master-0", &x509.CertificateRequest{DNSNames: tt.dnsNames, IPAddresses: parseIPs(tt.ips)})
			if (err != nil) != tt.wantErr {
				t.Fatalf("sanOwnedByOtherNode() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// machineRecordOf decodes m the way loadRecoveryAddresses decodes the Machines it lists.
func machineRecordOf(t *testing.T, m *machinev1beta1.Machine) *machineRecord {
	t.Helper()
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(m)
	if err != nil {
		t.Fatal(err)
	}
	record := &machineRecord{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(content, record); err != nil {
		t.Fatal(err)
	}
	return record
}

func parseIPs(ips []string) []net.IP {
	var parsed []net.IP
	for _, ip := range ips {
		parsed = append(parsed, net.ParseIP(ip))
	}
	return parsed
}

// orderedCSRLister lists CSRs in a fixed order, unlike the indexer-backed lister.
type orderedCSRLister struct {
	certificateslisters.CertificateSigningRequestLister
	csrs []*certificatesv1.CertificateSigningRequest
}

func (l orderedCSRLister) List(labels.Selector) ([]*certificatesv1.CertificateSigningRequest, error) {
	return l.csrs, nil
}

func TestRecoveredNodesLatestApprovalWins(t *testing.T) {
	older := clientApproval(t, "client-old", "master-0", KubeletCSRRecoveryApproveReason, recoveredAt.Add(-30*time.Minute))
	latest := clientApproval(t, "client-new", "master-0", KubeletCSRRecoveryApproveReason, recoveredAt)
	for _, order := range [][]*certificatesv1.CertificateSigningRequest{{older, latest}, {latest, older}} {
		c := &kubeletServingCSRApprover{clientCSRLister: orderedCSRLister{csrs: order}}
		recovered, err := c.recoveredNodes()
		if err != nil {
			t.Fatal(err)
		}
		if got := recovered["master-0"]; !got.Equal(recoveredAt) {
			t.Fatalf("listed %s first: recovered at %s, want the latest approval %s", order[0].Name, got, recoveredAt)
		}
	}
}
