package recoverycontroller

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"reflect"
	"sort"
	"strings"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	certificateslisters "k8s.io/client-go/listers/certificates/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

const (
	kubeletServingSignerName = certificatesv1.KubeletServingSignerName

	// recoveryWindow is how long after the client approver approved a node's client CSR the node's new
	// serving CSRs still count as part of the recovery. It's measured from the approval to the serving
	// CSR's creation, both kube-apiserver timestamps, so the controller's clock plays no part. Kubelets
	// submit their serving CSR within a minute of getting a client certificate and resubmit about every
	// 15 minutes while it stays Pending, so an hour allows several attempts (for example while a node's
	// addresses catch up after a site move). It can't usefully be longer: kube-controller-manager's CSR
	// cleaner deletes approved CSRs after an hour, and with them the record of the recovery.
	recoveryWindow = time.Hour

	// machineAPINamespace holds the Machines that machine-approver matches serving CSRs against.
	machineAPINamespace = "openshift-machine-api"

	kubeletServingCSRApproverName = "KubeletServingCSRRecoveryApprover"
)

var (
	// machineGVR and egressIPGVR are read with the dynamic client, only while a node is recovering,
	// so KCMO needs neither their typed clients nor always-on informers for them. Either API may be
	// missing: a cluster without the Machine API has no Machines, and one without OVN-Kubernetes has
	// no egress IPs.
	machineGVR  = schema.GroupVersionResource{Group: "machine.openshift.io", Version: "v1beta1", Resource: "machines"}
	egressIPGVR = schema.GroupVersionResource{Group: "k8s.ovn.org", Version: "v1", Resource: "egressips"}

	nodeServingGroups          = sets.New[string](nodeGroup, "system:authenticated")
	kubeletServingUsages       = sets.New[certificatesv1.KeyUsage](certificatesv1.UsageDigitalSignature, certificatesv1.UsageServerAuth)
	kubeletServingUsagesLegacy = sets.New[certificatesv1.KeyUsage](
		certificatesv1.UsageKeyEncipherment,
		certificatesv1.UsageDigitalSignature,
		certificatesv1.UsageServerAuth,
	)
)

// kubeletServingCSRApprover approves kubelet serving CSRs of nodes that kubeletClientCSRApprover just
// recovered, when machine-approver can't. After a build-and-ship power-off every kubelet serving
// certificate has expired too, and each kubelet asks for a new one as soon as it has a client
// certificate again. machine-approver approves it only if every name and IP in the request is an
// address of the node's Machine. On agent-installed bare metal the Machines list only IP addresses, so
// the kubelet's hostname never matches, and the fallback of comparing with the current serving
// certificate can't work because that certificate has expired. Until someone approves the CSRs,
// kube-apiserver can't reach any kubelet: no logs, exec, debug or metrics.
//
// It runs next to kubeletClientCSRApprover in the kube-controller-manager static pod, behind the same
// opt-in toggle (NodeClientCertRecoveryConfigMapName), and never denies anything. It acts only when all
// of these hold:
//   - the node is recovering: the client approver approved one of its client CSRs, and this CSR was
//     created after that, within recoveryWindow. Routine serving rotation, new nodes and reboots never
//     get a client approval from it, so this controller leaves them to machine-approver.
//   - the node has a Machine. Nodes without one (SNO, UPI, platform: none) are out of scope.
//   - machine-approver would reject the CSR. If the Machine covers every name and IP, the CSR is left
//     for machine-approver, so this controller only fills the gap.
//   - every name and IP is this node's (its name, or an address of its Node or Machine, or one of its
//     egress IPs), and none is recorded for anyone else: another node's name, or any address of another
//     Node, Machine or egress assignment. The records of other nodes can be stale during a recovery
//     (for example after a site move that reuses IPs), so this is only as good as them; see
//     sanOwnedByOtherNode.
type kubeletServingCSRApprover struct {
	kubeClient       kubernetes.Interface
	dynamicClient    dynamic.Interface
	servingCSRLister certificateslisters.CertificateSigningRequestLister
	clientCSRLister  certificateslisters.CertificateSigningRequestLister
	configMapLister  corev1listers.ConfigMapLister
	secretLister     corev1listers.SecretLister
	clock            clock.PassiveClock
	recorder         events.Recorder
}

// servingCandidate is a pending serving CSR that passed the content checks, with its parsed request.
type servingCandidate struct {
	csr     *certificatesv1.CertificateSigningRequest
	request *x509.CertificateRequest
}

// NewKubeletServingCSRApproverInformers returns an informer factory for kubelet serving CSRs only.
func NewKubeletServingCSRApproverInformers(kubeClient kubernetes.Interface) informers.SharedInformerFactory {
	return informers.NewSharedInformerFactoryWithOptions(kubeClient, 0,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("spec.signerName", kubeletServingSignerName).String()
		}))
}

// NewKubeletServingCSRApprover returns the controller. clientCSRInformers is the client approver's
// factory (NewKubeletClientCSRApproverInformers): its CSRs record which nodes it recovered.
func NewKubeletServingCSRApprover(
	kubeClient kubernetes.Interface,
	dynamicClient dynamic.Interface,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	clientCSRInformers informers.SharedInformerFactory,
	servingCSRInformers informers.SharedInformerFactory,
	eventRecorder events.Recorder,
	clock clock.PassiveClock,
) factory.Controller {
	servingCSRInformer := servingCSRInformers.Certificates().V1().CertificateSigningRequests()
	clientCSRInformer := clientCSRInformers.Certificates().V1().CertificateSigningRequests()
	configMapInformer := kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().ConfigMaps()
	secretInformer := kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets()

	c := &kubeletServingCSRApprover{
		kubeClient:       kubeClient,
		dynamicClient:    dynamicClient,
		servingCSRLister: servingCSRInformer.Lister(),
		clientCSRLister:  clientCSRInformer.Lister(),
		configMapLister:  configMapInformer.Lister(),
		secretLister:     secretInformer.Lister(),
		clock:            clock,
		recorder:         eventRecorder.WithComponentSuffix("kubelet-serving-csr-recovery-approver"),
	}

	// Sync on serving CSR changes, on the toggle ConfigMap, and every resyncInterval. Client CSRs and
	// csr-signer don't trigger a sync: a recovered kubelet submits its serving CSR after its client CSR
	// was approved, which already required a valid csr-signer. Registering them bare only makes the
	// controller wait for their caches.
	return factory.New().
		WithInformers(servingCSRInformer.Informer()).
		WithBareInformers(clientCSRInformer.Informer(), secretInformer.Informer()).
		WithFilteredEventsInformers(factory.NamesFilter(NodeClientCertRecoveryConfigMapName), configMapInformer.Informer()).
		ResyncEvery(resyncInterval).
		WithSync(c.sync).
		ToController(kubeletServingCSRApproverName, c.recorder)
}

// sync approves pending kubelet serving CSRs of recovering nodes that machine-approver can't approve.
// Every step below must pass; a CSR that fails one is left for the next sync, machine-approver or a
// human. Steps 1-4 make no API calls, so on a cluster that isn't recovering this controller only reads
// its caches.
func (c *kubeletServingCSRApprover) sync(ctx context.Context, _ factory.SyncContext) error {
	// 1. Toggle. The client approver reports on/off changes; here it only gates.
	enabled, err := nodeCertRecoveryEnabled(c.configMapLister)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	// 2. Collect pending CSRs shaped like a kubelet asking for its own serving certificate, grouped by
	// node.
	csrs, err := c.servingCSRLister.List(labels.Everything())
	if err != nil {
		return err
	}
	candidates := map[string][]servingCandidate{}
	for _, csr := range csrs {
		if isApprovedOrDenied(csr) {
			continue
		}
		nodeName, request, err := servingCSRNodeName(csr)
		if err != nil {
			klog.V(4).Infof("Ignoring CSR %s: %v", csr.Name, err)
			continue
		}
		candidates[nodeName] = append(candidates[nodeName], servingCandidate{csr: csr, request: request})
	}
	if len(candidates) == 0 {
		return nil
	}

	// 3. Recovery gate: keep only CSRs of nodes the client approver recovered, created at or after that
	// approval and within recoveryWindow of it. Both times are kube-apiserver's, so a controller clock
	// that is off right after the power-on can't end the window early or late. Anything else is routine
	// rotation or a new node, which is machine-approver's, so it's logged only at V(4).
	recovered, err := c.recoveredNodes()
	if err != nil {
		return err
	}
	for nodeName, nodeCSRs := range candidates {
		recoveredAt, ok := recovered[nodeName]
		if !ok {
			klog.V(4).Infof("Not considering %d pending serving CSRs for node %s: the node isn't recovering", len(nodeCSRs), nodeName)
			delete(candidates, nodeName)
			continue
		}
		var kept []servingCandidate
		for _, candidate := range nodeCSRs {
			// Compared at second precision: both times are stored with it.
			created := candidate.csr.CreationTimestamp.Time
			if created.Before(recoveredAt) || created.Sub(recoveredAt) > recoveryWindow {
				klog.V(4).Infof("Not considering CSR %s for node %s: created at %s, not within %s after its client certificate was recovered at %s",
					candidate.csr.Name, nodeName, created.UTC().Format(time.RFC3339), recoveryWindow, recoveredAt.UTC().Format(time.RFC3339))
				continue
			}
			kept = append(kept, candidate)
		}
		if len(kept) == 0 {
			delete(candidates, nodeName)
			continue
		}
		candidates[nodeName] = kept
	}
	if len(candidates) == 0 {
		return nil
	}

	// 4. Approve nothing until csr-signer is valid; kube-controller-manager can't sign otherwise.
	if ok, reason := csrSignerValid(c.secretLister, c.clock.Now()); !ok {
		klog.Infof("Not approving pending serving CSRs of %d recovering nodes yet: %s", len(candidates), reason)
		return nil
	}

	// 5. Read every node's records: Machines, Nodes and egress IPs, live, so a stalled watch can't
	// hide a name or address that another node owns.
	addresses, err := c.loadRecoveryAddresses(ctx)
	if err != nil {
		return err
	}

	// 6. Per node: first what depends only on the node, then each CSR, oldest first. Approve only what
	// machine-approver won't and what belongs to this node alone.
	var errs []error
	for _, nodeName := range sets.List(sets.KeySet(candidates)) {
		nodeCSRs := candidates[nodeName]
		node, reason := addresses.servingNodeFor(nodeName)
		if node == nil {
			klog.Infof("Not approving %d serving CSRs for node %s: %s", len(nodeCSRs), nodeName, reason)
			continue
		}
		sort.Slice(nodeCSRs, func(i, j int) bool {
			return nodeCSRs[i].csr.CreationTimestamp.Before(&nodeCSRs[j].csr.CreationTimestamp)
		})
		for _, candidate := range nodeCSRs {
			reason, deferred := addresses.servingCSRSkipReason(node, candidate.request)
			if deferred {
				// Expected on platforms whose Machines list every name (AWS); repeats until machine-approver acts.
				klog.V(2).Infof("Leaving serving CSR %s for node %s to machine-approver: %s", candidate.csr.Name, nodeName, reason)
				continue
			}
			if reason != "" {
				klog.Infof("Not approving serving CSR %s for node %s: %s", candidate.csr.Name, nodeName, reason)
				continue
			}
			if err := c.approve(ctx, candidate.csr, nodeName, recovered[nodeName]); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

// recoveredNodes returns, per node, when the client approver last approved one of its client CSRs.
// It reads the approvals from the client CSRs themselves, so a restart or a leader handoff doesn't
// forget a recovery, and `oc get csr` shows why a node's serving CSR was approved.
//
// The time is the condition's lastTransitionTime, which kube-apiserver stamps because approveCSR leaves
// it empty. sync compares it only with serving CSRs' creationTimestamp, which kube-apiserver also
// stamps, so a controller clock that is off after a long power-off can't hide a serving CSR or move the
// recovery window. lastUpdateTime, set from this controller's clock, is the fallback.
func (c *kubeletServingCSRApprover) recoveredNodes() (map[string]time.Time, error) {
	csrs, err := c.clientCSRLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	recovered := map[string]time.Time{}
	for _, csr := range csrs {
		var approvedAt time.Time
		for _, condition := range csr.Status.Conditions {
			if condition.Type == certificatesv1.CertificateApproved && condition.Status == corev1.ConditionTrue && condition.Reason == KubeletCSRRecoveryApproveReason {
				approvedAt = condition.LastTransitionTime.Time
				if approvedAt.IsZero() {
					approvedAt = condition.LastUpdateTime.Time
				}
			}
		}
		if approvedAt.IsZero() {
			continue
		}
		// The client approver approved only CSRs this accepts, so it recovers the node name.
		nodeName, err := recoveryClientCSRNodeName(csr)
		if err != nil {
			continue
		}
		if approvedAt.After(recovered[nodeName]) {
			recovered[nodeName] = approvedAt
		}
	}
	return recovered, nil
}

// loadRecoveryAddresses reads and indexes every Machine, Node and egress IP. It runs only while a node
// is recovering, so the live lists are rare.
func (c *kubeletServingCSRApprover) loadRecoveryAddresses(ctx context.Context) (*recoveryAddresses, error) {
	addresses := newRecoveryAddresses()

	machineList, err := c.dynamicClient.Resource(machineGVR).Namespace(machineAPINamespace).List(ctx, metav1.ListOptions{})
	switch {
	case isMissingAPI(err):
	case err != nil:
		return nil, fmt.Errorf("failed to list Machines: %w", err)
	default:
		for i := range machineList.Items {
			machine := &machineRecord{}
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(machineList.Items[i].Object, machine); err != nil {
				return nil, fmt.Errorf("failed to decode Machine %s: %w", machineList.Items[i].GetName(), err)
			}
			addresses.addMachine(machine)
		}
	}

	nodeList, err := c.kubeClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list Nodes: %w", err)
	}
	for i := range nodeList.Items {
		addresses.addNode(&nodeList.Items[i])
	}

	egressIPList, err := c.dynamicClient.Resource(egressIPGVR).List(ctx, metav1.ListOptions{})
	switch {
	case isMissingAPI(err):
	case err != nil:
		return nil, fmt.Errorf("failed to list EgressIPs: %w", err)
	default:
		for i := range egressIPList.Items {
			if err := addEgressIPs(addresses, &egressIPList.Items[i]); err != nil {
				return nil, err
			}
		}
	}
	return addresses, nil
}

// addEgressIPs adds an EgressIP's assignments (status.items[].node and .egressIP) to addresses. Cloud
// providers report a node's egress IPs (secondary NIC IPs) as Node addresses, so the kubelet can ask for
// them; machine-approver accepts them too (getNodeEgressIPs). An assignment it can't decode, or whose
// node is empty or whose IP doesn't parse, is an error rather than skipped: sanOwnedByOtherNode would
// otherwise treat that IP as no other node's.
func addEgressIPs(addresses *recoveryAddresses, egressIP *unstructured.Unstructured) error {
	items, _, err := unstructured.NestedSlice(egressIP.Object, "status", "items")
	if err != nil {
		return fmt.Errorf("failed to decode EgressIP %s: %w", egressIP.GetName(), err)
	}
	for _, item := range items {
		assignment, ok := item.(map[string]interface{})
		if !ok {
			return fmt.Errorf("failed to decode EgressIP %s: status.items entry is %T", egressIP.GetName(), item)
		}
		nodeName, _, err := unstructured.NestedString(assignment, "node")
		if err != nil {
			return fmt.Errorf("failed to decode EgressIP %s: %w", egressIP.GetName(), err)
		}
		address, _, err := unstructured.NestedString(assignment, "egressIP")
		if err != nil {
			return fmt.Errorf("failed to decode EgressIP %s: %w", egressIP.GetName(), err)
		}
		ip := net.ParseIP(address)
		if nodeName == "" || ip == nil {
			return fmt.Errorf("failed to decode EgressIP %s: assignment of egress IP %q to node %q", egressIP.GetName(), address, nodeName)
		}
		addresses.addEgressIP(nodeName, ip)
	}
	return nil
}

// isMissingAPI reports whether err means the resource's API isn't served on this cluster: kube-apiserver
// answers a list of an unserved resource with NotFound.
func isMissingAPI(err error) bool {
	return apierrors.IsNotFound(err)
}

// approve approves csr and reports it in the log and as an Event.
func (c *kubeletServingCSRApprover) approve(ctx context.Context, csr *certificatesv1.CertificateSigningRequest, nodeName string, recoveredAt time.Time) error {
	message := fmt.Sprintf("Approved by the kube-controller-manager cert-recovery-controller: node %s got a new client certificate from it at %s, every requested name and IP is an address of the node's Node or Machine and of no other node, and %s/%s is enabled",
		nodeName, recoveredAt.UTC().Format(time.RFC3339), operatorclient.GlobalUserSpecifiedConfigNamespace, NodeClientCertRecoveryConfigMapName)
	approved, err := approveCSR(ctx, c.kubeClient, csr, nodeName, message, c.clock.Now())
	if !approved || err != nil {
		return err
	}
	klog.Infof("Approved serving CSR %s for node %s (client certificate recovered at %s)", csr.Name, nodeName, recoveredAt.UTC().Format(time.RFC3339))
	c.recorder.Eventf("KubeletServingCSRApproved", "Approved kubelet serving CSR %s for node %s: its client certificate was recovered at %s", csr.Name, nodeName, recoveredAt.UTC().Format(time.RFC3339))
	return nil
}

// machineRecord is the part of a Machine the checks use. It's decoded the way machine-approver decodes
// Machines (machinehandler.Machine), so an unexpected value elsewhere in a Machine can't block every
// approval.
type machineRecord struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            struct {
		NodeRef   *corev1.ObjectReference `json:"nodeRef,omitempty"`
		Addresses []corev1.NodeAddress    `json:"addresses,omitempty"`
	} `json:"status,omitempty"`
}

// recoveryAddresses is a snapshot of every Machine, Node and egress IP record the serving CSR checks
// compare against.
type recoveryAddresses struct {
	// machinesForNode indexes Machines with a status.nodeRef by node name, as machine-approver finds a
	// node's Machine (FindMatchingMachineFromNodeRef).
	machinesForNode map[string][]*machineRecord
	nodes           map[string]*corev1.Node
	// egressIPs are the egress IPs assigned to each node name, including names with no Node.
	egressIPs map[string][]net.IP
	// owners maps every identity recorded anywhere (see identityKey) to whom it's recorded for: a node
	// name, or "Machine <name>" for a Machine linked to no node. The identities are node names (of
	// Nodes, of Machines' nodeRefs and of egress assignments) and every Node, Machine and egress address,
	// whatever its type. sanOwnedByOtherNode looks requested names and IPs up here.
	owners map[string]sets.Set[string]
}

func newRecoveryAddresses() *recoveryAddresses {
	return &recoveryAddresses{
		machinesForNode: map[string][]*machineRecord{},
		nodes:           map[string]*corev1.Node{},
		egressIPs:       map[string][]net.IP{},
		owners:          map[string]sets.Set[string]{},
	}
}

// addMachine records a Machine. A Machine linked to no node belongs to nobody, so its addresses are
// never the requester's.
func (a *recoveryAddresses) addMachine(machine *machineRecord) {
	owner := "Machine " + machine.Name
	if ref := machine.Status.NodeRef; ref != nil && ref.Name != "" {
		owner = ref.Name
		a.machinesForNode[ref.Name] = append(a.machinesForNode[ref.Name], machine)
		a.addIdentity(ref.Name, owner)
	}
	for _, address := range machine.Status.Addresses {
		a.addIdentity(address.Address, owner)
	}
}

func (a *recoveryAddresses) addNode(node *corev1.Node) {
	a.nodes[node.Name] = node
	a.addIdentity(node.Name, node.Name)
	for _, address := range node.Status.Addresses {
		a.addIdentity(address.Address, node.Name)
	}
}

func (a *recoveryAddresses) addEgressIP(nodeName string, ip net.IP) {
	a.egressIPs[nodeName] = append(a.egressIPs[nodeName], ip)
	a.addIdentity(nodeName, nodeName)
	a.addIdentity(ip.String(), nodeName)
}

func (a *recoveryAddresses) addIdentity(identity, owner string) {
	key := identityKey(identity)
	if key == "" {
		return
	}
	if a.owners[key] == nil {
		a.owners[key] = sets.New[string]()
	}
	a.owners[key].Insert(owner)
}

// identityKey normalizes a name or address so that every spelling of one identity compares equal: an IP
// in its canonical form, so that an IP written as a DNS name still matches it, and anything else
// lowercased without a trailing dot, as DNS names compare. Surrounding spaces are dropped first. A value
// that still isn't an IP is kept as a name rather than rejected, even in an IP-type address: Node and
// Machine addresses come from the kubelet, cloud providers and machine-api, one odd value mustn't block
// every recovery, and machine-approver compares such values as strings too.
func identityKey(value string) string {
	value = strings.TrimSpace(value)
	if ip := net.ParseIP(value); ip != nil {
		return ip.String()
	}
	return strings.ToLower(strings.TrimSuffix(value, "."))
}

// servingNode is a recovering node's own records, which each of its serving CSRs is checked against.
type servingNode struct {
	name      string
	node      *corev1.Node
	machine   *machineRecord
	egressIPs []net.IP
}

// servingNodeFor returns the records of node nodeName, or nil and why none of its serving CSRs may be
// approved now. What it checks depends only on the node, so sync calls it once per node.
func (a *recoveryAddresses) servingNodeFor(nodeName string) (*servingNode, string) {
	machines := a.machinesForNode[nodeName]
	switch len(machines) {
	case 0:
		return nil, "the node has no Machine"
	case 1:
	default:
		// machine-approver takes the first match in list order, so neither side can tell which Machine
		// it checks. Leave the CSRs alone.
		return nil, fmt.Sprintf("%d Machines claim the node", len(machines))
	}
	machine := machines[0]
	node := a.nodes[nodeName]
	if node == nil {
		return nil, "no such Node"
	}
	if node.DeletionTimestamp != nil {
		return nil, "Node is being deleted"
	}
	if len(machine.Status.Addresses) == 0 {
		// machine-approver retries until the Machine has addresses; approving now could race it.
		return nil, fmt.Sprintf("Machine %s has no addresses yet", machine.Name)
	}
	return &servingNode{name: nodeName, node: node, machine: machine, egressIPs: a.egressIPs[nodeName]}, ""
}

// servingCSRSkipReason returns why the serving CSR request from node n must not be approved, or "" if it
// may be. deferred is true when the reason is that machine-approver will approve it.
func (a *recoveryAddresses) servingCSRSkipReason(n *servingNode, request *x509.CertificateRequest) (reason string, deferred bool) {
	if machineCoversSANs(n.machine.Status.Addresses, request, n.egressIPs) {
		return fmt.Sprintf("Machine %s covers every requested name and IP", n.machine.Name), true
	}
	if err := sansMatchNode(n.node, n.machine, n.egressIPs, request); err != nil {
		return err.Error(), false
	}
	if err := a.sanOwnedByOtherNode(n.name, request); err != nil {
		return err.Error(), false
	}
	return "", false
}

// servingCSRNodeName returns the node name and the parsed request if csr is a kubelet asking for its
// own serving certificate. It mirrors machine-approver's validateCSRContents, but is stricter: the
// organization must be exactly system:nodes, the usages exactly the kubelet's, and the request must
// name at least one DNS name or IP and nothing else, with every DNS name a plain hostname and not an IP.
func servingCSRNodeName(csr *certificatesv1.CertificateSigningRequest) (string, *x509.CertificateRequest, error) {
	// The node asks for itself, authenticated by its own client certificate.
	if csr.Spec.SignerName != kubeletServingSignerName {
		return "", nil, fmt.Errorf("signer is %q", csr.Spec.SignerName)
	}
	nodeName := strings.TrimPrefix(csr.Spec.Username, nodeUserPrefix)
	if !strings.HasPrefix(csr.Spec.Username, nodeUserPrefix) || len(nodeName) == 0 {
		return "", nil, fmt.Errorf("requested by %q", csr.Spec.Username)
	}
	if !sets.New(csr.Spec.Groups...).IsSuperset(nodeServingGroups) {
		return "", nil, fmt.Errorf("groups %v don't include %v", csr.Spec.Groups, sets.List(nodeServingGroups))
	}
	if !usagesMatch(csr.Spec.Usages, kubeletServingUsages, kubeletServingUsagesLegacy) {
		return "", nil, fmt.Errorf("unexpected usages %v", csr.Spec.Usages)
	}
	// The x509 request names the same node (O=system:nodes, CN=system:node:<name>) and only DNS names and IPs.
	request, err := parseCSRRequest(csr)
	if err != nil {
		return "", nil, err
	}
	if request.Subject.CommonName != csr.Spec.Username {
		return "", nil, fmt.Errorf("common name %q doesn't match the requester %q", request.Subject.CommonName, csr.Spec.Username)
	}
	if !reflect.DeepEqual([]string{nodeGroup}, request.Subject.Organization) {
		return "", nil, fmt.Errorf("organization is %v", request.Subject.Organization)
	}
	if len(request.EmailAddresses) > 0 || len(request.URIs) > 0 {
		return "", nil, fmt.Errorf("request has email or URI subject alternative names")
	}
	if len(request.DNSNames) == 0 && len(request.IPAddresses) == 0 {
		return "", nil, fmt.Errorf("request has no DNS or IP subject alternative names")
	}
	// A kubelet only asks for its own hostnames, and puts a hostname that is an IP among the IP SANs
	// (addressesToHostnamesAndIPs). Anything else is refused: a wildcard, which also matches other
	// nodes' names; a trailing dot or an empty label; or an IP written as a DNS name, which TLS clients
	// never match but which mustn't name another node's IP either.
	for _, name := range request.DNSNames {
		if net.ParseIP(name) != nil {
			return "", nil, fmt.Errorf("DNS name %q is an IP address", name)
		}
		if errs := validation.IsDNS1123Subdomain(strings.ToLower(name)); len(errs) > 0 {
			return "", nil, fmt.Errorf("DNS name %q is not a hostname: %s", name, strings.Join(errs, "; "))
		}
	}
	return nodeName, request, nil
}

// machineCoversSANs reports whether machine-approver will approve the request on its Machine path, given
// the Machine's addresses. It's a copy of the acceptance rule in machine-approver's
// authorizeServingCertWithMachine as of its "Always validate EgressIPs" change (main, 5.1 and later):
// the Machine has addresses, every DNS name is one of its hostname-type addresses, and every IP is one of
// its IP addresses (compared as strings, as there) or one of the node's egress IPs. Older machine-approver
// branches don't accept egress IPs here. Keep it in step with machine-approver: if it says no where
// machine-approver says yes, both approve and the CI assert reports it.
func machineCoversSANs(machineAddresses []corev1.NodeAddress, request *x509.CertificateRequest, egressIPs []net.IP) bool {
	if len(machineAddresses) == 0 {
		return false
	}
	for _, name := range request.DNSNames {
		if name == "" {
			continue
		}
		if !dnsNameInAddresses(name, machineAddresses) {
			return false
		}
	}
	for _, ip := range request.IPAddresses {
		if len(ip) == 0 {
			continue
		}
		found := false
		for _, address := range machineAddresses {
			if isIPAddressType(address.Type) && ip.String() == address.Address {
				found = true
				break
			}
		}
		if !found && !ipIn(ip, egressIPs) {
			return false
		}
	}
	return true
}

// sansMatchNode checks that every name and IP in the request belongs to the node. A DNS name must be
// the node name or a hostname-type address of its Node or Machine: on agent-installed bare metal only
// the Node has the hostname. An IP must be an IP address of its Node or Machine, or one of its egress
// IPs. Node addresses are reported by the kubelet itself, so this alone doesn't stop a node from asking
// for an address that isn't its own; sanOwnedByOtherNode and the recovery gate narrow that.
func sansMatchNode(node *corev1.Node, machine *machineRecord, egressIPs []net.IP, request *x509.CertificateRequest) error {
	for _, name := range request.DNSNames {
		if strings.EqualFold(name, node.Name) || dnsNameInAddresses(name, node.Status.Addresses) || dnsNameInAddresses(name, machine.Status.Addresses) {
			continue
		}
		return fmt.Errorf("DNS name %q is neither the node name nor a hostname of Node %s or Machine %s", name, node.Name, machine.Name)
	}
	for _, ip := range request.IPAddresses {
		if ipInAddresses(ip, node.Status.Addresses) || ipInAddresses(ip, machine.Status.Addresses) || ipIn(ip, egressIPs) {
			continue
		}
		return fmt.Errorf("IP %s is not an address of Node %s or Machine %s, nor one of its egress IPs", ip, node.Name, machine.Name)
	}
	return nil
}

// sanOwnedByOtherNode returns an error if a name or IP in the request is recorded for anyone other than
// nodeName: another node's name (of a Node, a Machine's nodeRef or an egress assignment), any address of
// another Node or of a Machine that isn't this node's, whatever its type, or an egress IP assigned to
// another node name. Names, Machines and egress IPs count whether or not that node's Node still exists.
// A node's addresses are self-reported, so without this a node could get a serving certificate for
// another node's name or IP, which is why upstream Kubernetes doesn't auto-approve serving CSRs.
//
// It only knows what is recorded now. During a recovery other nodes' records can be stale: Node status
// until their kubelets post it again, and bare-metal Machine addresses indefinitely. After a site move
// that reuses IPs, a node can therefore still get an IP that another node now uses. Addresses no node
// records, such as virtual IPs, aren't covered either.
func (a *recoveryAddresses) sanOwnedByOtherNode(nodeName string, request *x509.CertificateRequest) error {
	for _, name := range request.DNSNames {
		if others := a.otherOwners(name, nodeName); len(others) > 0 {
			return fmt.Errorf("DNS name %q is recorded for %s", name, strings.Join(others, ", "))
		}
	}
	for _, ip := range request.IPAddresses {
		if others := a.otherOwners(ip.String(), nodeName); len(others) > 0 {
			return fmt.Errorf("IP %s is recorded for %s", ip, strings.Join(others, ", "))
		}
	}
	return nil
}

// otherOwners returns, sorted, whom other than nodeName the identity is recorded for.
func (a *recoveryAddresses) otherOwners(identity, nodeName string) []string {
	return sets.List(a.owners[identityKey(identity)].Clone().Delete(nodeName))
}

// dnsNameInAddresses reports whether name is one of the hostname-type addresses, compared the way
// machine-approver does: case-insensitively, ignoring a trailing dot on the address.
func dnsNameInAddresses(name string, addresses []corev1.NodeAddress) bool {
	for _, address := range addresses {
		switch address.Type {
		case corev1.NodeHostName, corev1.NodeInternalDNS, corev1.NodeExternalDNS:
			if strings.EqualFold(name, strings.TrimSuffix(address.Address, ".")) {
				return true
			}
		}
	}
	return false
}

func isIPAddressType(addressType corev1.NodeAddressType) bool {
	return addressType == corev1.NodeInternalIP || addressType == corev1.NodeExternalIP
}

// ipInAddresses reports whether ip is one of the IP-type addresses.
func ipInAddresses(ip net.IP, addresses []corev1.NodeAddress) bool {
	for _, address := range addresses {
		if isIPAddressType(address.Type) && ip.Equal(net.ParseIP(address.Address)) {
			return true
		}
	}
	return false
}

func ipIn(ip net.IP, ips []net.IP) bool {
	for _, candidate := range ips {
		if ip.Equal(candidate) {
			return true
		}
	}
	return false
}
