package recoverycontroller

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"

	"github.com/openshift/library-go/pkg/crypto"

	"github.com/openshift/cluster-kube-controller-manager-operator/pkg/operator/operatorclient"
)

// This file holds the kubelet client CSR recovery approver's generic parts: the opt-in toggle, the
// csr-signer check, parsing a CSR's request, and how a CSR is approved.

const (
	// NodeCertRecoveryConfigMapName is the opt-in toggle. When the ConfigMap exists in
	// openshift-config with data.enabled == "true", the recovery controller approves kubelet
	// client CSRs for nodes that can no longer authenticate (see kubeletClientCSRApprover). The
	// approver sets enabled to "false" autoDisableAfter after it saw the toggle turned on.
	NodeCertRecoveryConfigMapName = "node-cert-recovery"
	nodeCertRecoveryEnabledKey    = "enabled"

	// autoDisableAfter is how long the toggle stays on. After a power-off it's counted from the
	// power-on, so nodes powered on later at the same site still recover; in CI every node recovered
	// within minutes.
	autoDisableAfter = 24 * time.Hour

	// KubeletCSRRecoveryApproveReason is set on the Approved condition of CSRs this controller approves.
	KubeletCSRRecoveryApproveReason = "KCMRecoveryApprove"

	nodeUserPrefix = "system:node:"

	// resyncInterval re-evaluates what changes with time rather than with events: heartbeat staleness,
	// and csr-signer becoming valid again after a power-on. Otherwise a sync is triggered by the toggle
	// and by the approver's CSRs. Under a minute, library-go emits a FastControllerResync warning on
	// every start.
	resyncInterval = time.Minute
)

// nodeCertRecoveryToggle returns the toggle ConfigMap, or nil if there is none.
func nodeCertRecoveryToggle(configMapLister corev1listers.ConfigMapLister) (*corev1.ConfigMap, error) {
	cm, err := configMapLister.ConfigMaps(operatorclient.GlobalUserSpecifiedConfigNamespace).Get(NodeCertRecoveryConfigMapName)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return cm, err
}

// toggleEnabled reports whether the toggle ConfigMap has enabled == "true". Anything else, including
// a missing ConfigMap, means off.
func toggleEnabled(cm *corev1.ConfigMap) bool {
	return cm != nil && cm.Data[nodeCertRecoveryEnabledKey] == "true"
}

// nodeCertRecoveryEnabled reports whether the toggle ConfigMap exists and has enabled == "true".
func nodeCertRecoveryEnabled(configMapLister corev1listers.ConfigMapLister) (bool, error) {
	cm, err := nodeCertRecoveryToggle(configMapLister)
	if err != nil {
		return false, err
	}
	return toggleEnabled(cm), nil
}

// csrSignerValid reports whether the csr-signer that kube-controller-manager signs with is currently valid.
// After a long power-off the kubelets submit CSRs minutes before this controller regenerates the expired
// signer; approving earlier makes kube-controller-manager fail to sign and back off for up to ~16 minutes.
func csrSignerValid(secretLister corev1listers.SecretLister, now time.Time) (bool, string) {
	secret, err := secretLister.Secrets(operatorclient.TargetNamespace).Get("csr-signer")
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

// approveCSR adds an Approved condition with KubeletCSRRecoveryApproveReason through the approval
// subresource, and reports whether it did. A CSR deleted in the meantime is ignored. Any other error,
// including a Conflict, is returned so the sync is retried, and the retry skips the CSR if someone
// else approved or denied it meanwhile.
func approveCSR(ctx context.Context, kubeClient kubernetes.Interface, csr *certificatesv1.CertificateSigningRequest, nodeName, message string, now time.Time) (bool, error) {
	csr = csr.DeepCopy()
	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:           certificatesv1.CertificateApproved,
		Status:         corev1.ConditionTrue,
		Reason:         KubeletCSRRecoveryApproveReason,
		Message:        message,
		LastUpdateTime: metav1.NewTime(now),
	})
	_, err := kubeClient.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, csr.Name, csr, metav1.UpdateOptions{})
	switch {
	case apierrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("failed to approve CSR %s for node %s: %w", csr.Name, nodeName, err)
	}
	return true, nil
}

func isApprovedOrDenied(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certificatesv1.CertificateApproved || condition.Type == certificatesv1.CertificateDenied {
			return true
		}
	}
	return false
}

// parseCSRRequest returns the x509 certificate request in csr, which must be a single PEM-encoded
// CERTIFICATE REQUEST.
func parseCSRRequest(csr *certificatesv1.CertificateSigningRequest) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(csr.Spec.Request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("request is not a PEM-encoded CERTIFICATE REQUEST")
	}
	request, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cannot parse request: %w", err)
	}
	return request, nil
}
