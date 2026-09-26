package recoverycontroller

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	certificateslisters "k8s.io/client-go/listers/certificates/v1"
	coordinationlisters "k8s.io/client-go/listers/coordination/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/crypto"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

const (
	// NodeClientCertRecoveryConfigMapName is the opt-in toggle. When the ConfigMap exists in
	// openshift-config with data.enabled == "true", the recovery controller approves kubelet
	// client CSRs for nodes that can no longer authenticate (see kubeletClientCSRApprover).
	NodeClientCertRecoveryConfigMapName = "node-client-cert-recovery"
	nodeClientCertRecoveryEnabledKey    = "enabled"

	// KubeletClientCSRRecoveryApproveReason is set on the Approved condition of CSRs approved by this controller.
	KubeletClientCSRRecoveryApproveReason = "KCMRecoveryApprove"

	kubeletClientSignerName  = certificatesv1.KubeAPIServerClientKubeletSignerName
	nodeBootstrapperUsername = "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper"
	nodeUserPrefix           = "system:node:"
	nodeGroup                = "system:nodes"
	nodeLeaseNamespace       = corev1.NamespaceNodeLease

	// heartbeatStaleAfter is how long a node's kubelet must have gone without a heartbeat before the
	// node is considered unable to authenticate. The heartbeat is the kubelet renewing the node's Lease
	// in kube-node-lease every ~10s; it is unrelated to leader-election Leases. A healthy node never
	// gets here.
	heartbeatStaleAfter = 5 * time.Minute
	// resyncInterval re-evaluates heartbeat staleness, which changes with time rather than with events.
	// After a power-off the heartbeats are already stale, and new CSRs and the csr-signer update trigger
	// a sync directly. Under a minute, library-go emits a FastControllerResync warning on every start.
	resyncInterval = time.Minute

	kubeletClientCSRApproverName = "KubeletClientCSRRecoveryApprover"
)

var (
	nodeBootstrapperGroups = sets.New[string](
		"system:serviceaccounts:openshift-machine-config-operator",
		"system:serviceaccounts",
		"system:authenticated",
	)
	kubeletClientUsages = sets.New[certificatesv1.KeyUsage](
		certificatesv1.UsageDigitalSignature,
		certificatesv1.UsageClientAuth,
	)
	kubeletClientUsagesLegacy = sets.New[certificatesv1.KeyUsage](
		certificatesv1.UsageKeyEncipherment,
		certificatesv1.UsageDigitalSignature,
		certificatesv1.UsageClientAuth,
	)
)

// kubeletClientCSRApprover approves kubelet client CSRs after a cluster was powered off long enough
// for every kubelet client certificate to expire (for example, shipped before the first rotation of the
// 24h install-time signer). Kubelets then re-bootstrap with the node-bootstrapper credential, and
// nothing approves their CSRs: machine-approver only handles new nodes and runs as a Deployment, which
// can't be scheduled until a kubelet authenticates. This controller runs in the kube-controller-manager
// static pod, so it doesn't depend on any kubelet.
//
// A node needs a new client certificate when its kubelet has stopped sending heartbeats, that is, stopped
// renewing the node's Lease in kube-node-lease. Node conditions can't tell: see nodeNeedsClientCert.
//
// It is opt-in (NodeClientCertRecoveryConfigMapName) and approve-only: it never denies, so a human or
// machine-approver can still act on anything it leaves alone.
type kubeletClientCSRApprover struct {
	kubeClient      kubernetes.Interface
	csrLister       certificateslisters.CertificateSigningRequestLister
	leaseLister     coordinationlisters.LeaseLister
	configMapLister corev1listers.ConfigMapLister
	secretLister    corev1listers.SecretLister
	clock           clock.PassiveClock
	recorder        events.Recorder

	// enabled is the toggle state seen by the last sync, so on/off changes are logged and reported
	// once. It needs no lock: the controller runs with a single worker.
	enabled bool
}

// NewKubeletClientCSRApproverInformers returns the informer factories the approver needs beyond
// kubeInformersForNamespaces: kubelet client CSRs only, and node heartbeat Leases only.
func NewKubeletClientCSRApproverInformers(kubeClient kubernetes.Interface) (csrInformers, leaseInformers informers.SharedInformerFactory) {
	csrInformers = informers.NewSharedInformerFactoryWithOptions(kubeClient, 0,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fields.OneTermEqualSelector("spec.signerName", kubeletClientSignerName).String()
		}))
	leaseInformers = informers.NewSharedInformerFactoryWithOptions(kubeClient, 0, informers.WithNamespace(nodeLeaseNamespace))
	return csrInformers, leaseInformers
}

func NewKubeletClientCSRApprover(
	kubeClient kubernetes.Interface,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	csrInformers informers.SharedInformerFactory,
	leaseInformers informers.SharedInformerFactory,
	eventRecorder events.Recorder,
	clock clock.PassiveClock,
) factory.Controller {
	csrInformer := csrInformers.Certificates().V1().CertificateSigningRequests()
	leaseInformer := leaseInformers.Coordination().V1().Leases()
	configMapInformer := kubeInformersForNamespaces.InformersFor(operatorclient.GlobalUserSpecifiedConfigNamespace).Core().V1().ConfigMaps()
	secretInformer := kubeInformersForNamespaces.InformersFor(operatorclient.TargetNamespace).Core().V1().Secrets()

	c := &kubeletClientCSRApprover{
		kubeClient:      kubeClient,
		csrLister:       csrInformer.Lister(),
		leaseLister:     leaseInformer.Lister(),
		configMapLister: configMapInformer.Lister(),
		secretLister:    secretInformer.Lister(),
		clock:           clock,
		recorder:        eventRecorder.WithComponentSuffix("kubelet-client-csr-recovery-approver"),
	}

	// Sync on CSR changes, on the toggle ConfigMap and csr-signer Secret, and every resyncInterval.
	return factory.New().
		WithInformers(csrInformer.Informer()).
		// Heartbeats (node Lease updates) don't trigger a sync: each kubelet sends one every ~10s, and
		// staleness is the absence of one, so there is no event to react to. Registering the informer
		// bare only makes the controller wait for its cache; staleness is re-checked on resync.
		WithBareInformers(leaseInformer.Informer()).
		WithFilteredEventsInformers(factory.NamesFilter(NodeClientCertRecoveryConfigMapName), configMapInformer.Informer()).
		WithFilteredEventsInformers(factory.NamesFilter("csr-signer"), secretInformer.Informer()).
		ResyncEvery(resyncInterval).
		WithSync(c.sync).
		ToController(kubeletClientCSRApproverName, c.recorder)
}

// sync approves pending kubelet client CSRs of nodes that lost their credentials. Every step
// below must pass; a CSR that fails one is left for the next sync or for a human.
func (c *kubeletClientCSRApprover) sync(ctx context.Context, _ factory.SyncContext) error {
	// 1. Toggle: log and emit an Event only when it flips on or off (sync runs at least every
	// minute, so not on every sync). While off, stop here with no API calls.
	enabled, err := c.toggleEnabled()
	if err != nil {
		return err
	}
	if enabled != c.enabled {
		c.enabled = enabled
		if enabled {
			klog.Infof("Kubelet client CSR recovery approval is enabled by %s/%s", operatorclient.GlobalUserSpecifiedConfigNamespace, NodeClientCertRecoveryConfigMapName)
			c.recorder.Eventf("NodeCertRecoveryEnabled", "Kubelet client CSR recovery approval enabled by ConfigMap %s/%s", operatorclient.GlobalUserSpecifiedConfigNamespace, NodeClientCertRecoveryConfigMapName)
		} else {
			klog.Infof("Kubelet client CSR recovery approval is disabled")
			c.recorder.Eventf("NodeCertRecoveryDisabled", "Kubelet client CSR recovery approval disabled")
		}
	}
	if !enabled {
		return nil
	}

	// 2. Collect pending CSRs shaped like a kubelet re-bootstrapping with the
	// node-bootstrapper credential, grouped by the node they ask to become.
	csrs, err := c.csrLister.List(labels.Everything())
	if err != nil {
		return err
	}
	candidates := map[string][]*certificatesv1.CertificateSigningRequest{}
	for _, csr := range csrs {
		if isApprovedOrDenied(csr) {
			continue
		}
		nodeName, err := recoveryClientCSRNodeName(csr)
		if err != nil {
			klog.V(4).Infof("Ignoring CSR %s: %v", csr.Name, err)
			continue
		}
		candidates[nodeName] = append(candidates[nodeName], csr)
	}
	if len(candidates) == 0 {
		return nil
	}

	// 3. Approve nothing until csr-signer is valid again; otherwise kube-controller-manager can't
	// sign what we approve and backs off.
	now := c.clock.Now()
	if ok, reason := c.signerValid(now); !ok {
		klog.Infof("Not approving %d pending kubelet client CSRs yet: %s", countCSRs(candidates), reason)
		return nil
	}

	// 4. Per node: continue only if its kubelet has stopped sending heartbeats and the Node exists and
	// isn't being deleted. An error for one node doesn't stop the others; they're returned together
	// so the sync is retried.
	nodeNames := sets.List(sets.KeySet(candidates))
	var errs []error
	for _, nodeName := range nodeNames {
		lastHeartbeat, reason, err := c.nodeNeedsClientCert(ctx, nodeName, now)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if reason != "" {
			klog.Infof("Not approving %d pending client CSRs for node %s: %s", len(candidates[nodeName]), nodeName, reason)
			continue
		}
		// 5. Approve the node's CSRs oldest first.
		nodeCSRs := candidates[nodeName]
		sort.Slice(nodeCSRs, func(i, j int) bool { return nodeCSRs[i].CreationTimestamp.Before(&nodeCSRs[j].CreationTimestamp) })
		for _, csr := range nodeCSRs {
			// Skip CSRs created before the node's last heartbeat: the node could still authenticate
			// then, so its kubelet had no reason to re-bootstrap. Such a CSR came from something
			// else, and approving it now would hand that requester the identity.
			if !csr.CreationTimestamp.Time.After(lastHeartbeat) {
				klog.Infof("Not approving CSR %s for node %s: created at %s, before the node's last heartbeat at %s",
					csr.Name, nodeName, csr.CreationTimestamp.UTC().Format(time.RFC3339), lastHeartbeat.UTC().Format(time.RFC3339))
				continue
			}
			if err := c.approve(ctx, csr, nodeName, now.Sub(lastHeartbeat)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

func (c *kubeletClientCSRApprover) toggleEnabled() (bool, error) {
	cm, err := c.configMapLister.ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Get(NodeClientCertRecoveryConfigMapName)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return cm.Data[nodeClientCertRecoveryEnabledKey] == "true", nil
}

// signerValid reports whether the csr-signer that kube-controller-manager signs with is currently valid.
// After a long power-off the kubelets submit CSRs minutes before this controller regenerates the expired
// signer; approving earlier makes kube-controller-manager fail to sign and back off for up to ~16 minutes.
func (c *kubeletClientCSRApprover) signerValid(now time.Time) (bool, string) {
	secret, err := c.secretLister.Secrets(operatorclient.TargetNamespace).Get("csr-signer")
	if err != nil {
		return false, fmt.Sprintf("cannot read %s/csr-signer: %v", operatorclient.TargetNamespace, err)
	}
	certs, err := crypto.CertsFromPEM(secret.Data[corev1.TLSCertKey])
	if err != nil || len(certs) == 0 {
		return false, fmt.Sprintf("cannot parse %s/csr-signer: %v", operatorclient.TargetNamespace, err)
	}
	if now.Before(certs[0].NotBefore) || !now.Before(certs[0].NotAfter) {
		return false, fmt.Sprintf("%s/csr-signer is not valid now (valid %s to %s)", operatorclient.TargetNamespace,
			certs[0].NotBefore.UTC().Format(time.RFC3339), certs[0].NotAfter.UTC().Format(time.RFC3339))
	}
	return true, ""
}

// nodeNeedsClientCert returns the time of the node's last heartbeat when the Node exists, isn't being
// deleted, and its kubelet hasn't sent a heartbeat for heartbeatStaleAfter. Otherwise it returns a
// non-empty reason.
// Node conditions can't be used: while no kubelet can authenticate, the network-node-identity webhook
// can't run either and rejects every nodes/status update, so Nodes stay Ready=True.
func (c *kubeletClientCSRApprover) nodeNeedsClientCert(ctx context.Context, nodeName string, now time.Time) (time.Time, string, error) {
	// Check the cache first: on a healthy cluster every heartbeat is fresh, so this returns without
	// an API call.
	lease, err := c.leaseLister.Leases(nodeLeaseNamespace).Get(nodeName)
	if apierrors.IsNotFound(err) {
		return time.Time{}, "node has no heartbeat Lease", nil
	}
	if err != nil {
		return time.Time{}, "", err
	}
	if _, reason := heartbeatStaleSince(lease, now); reason != "" {
		return time.Time{}, reason, nil
	}
	// Confirm with a live read: a stalled Lease watch would make a healthy node's heartbeat look stale
	// in the cache.
	lease, err = c.kubeClient.CoordinationV1().Leases(nodeLeaseNamespace).Get(ctx, nodeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return time.Time{}, "node has no heartbeat Lease", nil
	}
	if err != nil {
		return time.Time{}, "", err
	}
	lastHeartbeat, reason := heartbeatStaleSince(lease, now)
	if reason != "" {
		return time.Time{}, reason, nil
	}
	// A leftover Lease isn't enough; the Node must still exist, and not be on its way out. A kubelet that
	// hasn't registered since it started re-creates its Node, so approving one that is being deleted could
	// bring it back.
	node, err := c.kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return time.Time{}, "no such Node", nil
	}
	if err != nil {
		return time.Time{}, "", err
	}
	if node.DeletionTimestamp != nil {
		return time.Time{}, "Node is being deleted", nil
	}
	return lastHeartbeat, "", nil
}

// heartbeatStaleSince returns the time of the node's last heartbeat (the Lease's renewTime) if it is at
// least heartbeatStaleAfter old. Otherwise it returns a non-empty reason.
func heartbeatStaleSince(lease *coordinationv1.Lease, now time.Time) (time.Time, string) {
	if lease.Spec.RenewTime == nil {
		return time.Time{}, "node has never sent a heartbeat"
	}
	lastHeartbeat := lease.Spec.RenewTime.Time
	if age := now.Sub(lastHeartbeat); age < heartbeatStaleAfter {
		return time.Time{}, fmt.Sprintf("node sent a heartbeat %s ago", age.Round(time.Second))
	}
	return lastHeartbeat, ""
}

// approve adds an Approved condition through the approval subresource. A CSR deleted in the
// meantime is ignored. Any other error, including a Conflict, is returned so the sync is retried,
// and the retry skips the CSR if someone else approved or denied it meanwhile.
func (c *kubeletClientCSRApprover) approve(ctx context.Context, csr *certificatesv1.CertificateSigningRequest, nodeName string, heartbeatAge time.Duration) error {
	csr = csr.DeepCopy()
	message := fmt.Sprintf("Approved by the kube-controller-manager cert-recovery-controller: node %s has not sent a heartbeat for %s and %s/%s is enabled",
		nodeName, heartbeatAge.Round(time.Second), operatorclient.GlobalUserSpecifiedConfigNamespace, NodeClientCertRecoveryConfigMapName)
	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:           certificatesv1.CertificateApproved,
		Status:         corev1.ConditionTrue,
		Reason:         KubeletClientCSRRecoveryApproveReason,
		Message:        message,
		LastUpdateTime: metav1.NewTime(c.clock.Now()),
	})
	_, err := c.kubeClient.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, csr.Name, csr, metav1.UpdateOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("failed to approve CSR %s for node %s: %w", csr.Name, nodeName, err)
	}
	klog.Infof("Approved CSR %s for node %s (no heartbeat for %s)", csr.Name, nodeName, heartbeatAge.Round(time.Second))
	c.recorder.Eventf("KubeletClientCSRApproved", "Approved kubelet client CSR %s for node %s: it has not sent a heartbeat for %s", csr.Name, nodeName, heartbeatAge.Round(time.Second))
	return nil
}

// recoveryClientCSRNodeName returns the node name if csr is a kubelet client CSR submitted through the
// node-bootstrapper credential, as a kubelet does when it re-bootstraps. The checks mirror
// cluster-machine-approver's isNodeClientCert and isReqFromNodeBootstrapper.
func recoveryClientCSRNodeName(csr *certificatesv1.CertificateSigningRequest) (string, error) {
	// The request came through the node-bootstrapper credential, for the kubelet client signer and usages.
	if csr.Spec.SignerName != kubeletClientSignerName {
		return "", fmt.Errorf("signer is %q", csr.Spec.SignerName)
	}
	if csr.Spec.Username != nodeBootstrapperUsername {
		return "", fmt.Errorf("requested by %q", csr.Spec.Username)
	}
	if !nodeBootstrapperGroups.Equal(sets.New(csr.Spec.Groups...)) {
		return "", fmt.Errorf("unexpected groups %v", csr.Spec.Groups)
	}
	usages := sets.New(csr.Spec.Usages...)
	if len(usages) != len(csr.Spec.Usages) || (!usages.Equal(kubeletClientUsages) && !usages.Equal(kubeletClientUsagesLegacy)) {
		return "", fmt.Errorf("unexpected usages %v", csr.Spec.Usages)
	}
	// The x509 request asks for exactly a node identity (O=system:nodes, CN=system:node:<name>), no SANs.
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return "", fmt.Errorf("request is not a PEM-encoded CERTIFICATE REQUEST")
	}
	x509cr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("cannot parse request: %w", err)
	}
	if !reflect.DeepEqual([]string{nodeGroup}, x509cr.Subject.Organization) {
		return "", fmt.Errorf("organization is %v", x509cr.Subject.Organization)
	}
	if len(x509cr.DNSNames) > 0 || len(x509cr.EmailAddresses) > 0 || len(x509cr.IPAddresses) > 0 || len(x509cr.URIs) > 0 {
		return "", fmt.Errorf("request has subject alternative names")
	}
	nodeName := strings.TrimPrefix(x509cr.Subject.CommonName, nodeUserPrefix)
	if !strings.HasPrefix(x509cr.Subject.CommonName, nodeUserPrefix) || len(nodeName) == 0 {
		return "", fmt.Errorf("common name is %q", x509cr.Subject.CommonName)
	}
	return nodeName, nil
}

func isApprovedOrDenied(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certificatesv1.CertificateApproved || condition.Type == certificatesv1.CertificateDenied {
			return true
		}
	}
	return false
}

func countCSRs(byNode map[string][]*certificatesv1.CertificateSigningRequest) int {
	n := 0
	for _, csrs := range byNode {
		n += len(csrs)
	}
	return n
}
