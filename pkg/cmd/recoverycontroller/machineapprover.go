package recoverycontroller

import (
	"crypto/x509"
	"reflect"
	"strings"

	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// The checks in this file are copied as they are from openshift/cluster-machine-approver at
// f53c9f299a40cbf5fe49135dcdf7247547b3de23 (pkg/controller/node_client.go and
// pkg/controller/csr_check.go), so the client approver recognizes a kubelet client CSR from the
// node-bootstrapper credential exactly as machine-approver does. Only the doc comments were added;
// keep the code identical to the source.

// nodeBootstrapperUsername is the service account that kubelets use to request their first client
// certificate, and to request a new one once theirs has expired.
//
// Copied from machine-approver: https://github.com/openshift/cluster-machine-approver/blob/f53c9f299a40cbf5fe49135dcdf7247547b3de23/pkg/controller/csr_check.go#L37
const nodeBootstrapperUsername = "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper"

// nodeBootstrapperGroups are the groups of nodeBootstrapperUsername.
//
// Copied from machine-approver: https://github.com/openshift/cluster-machine-approver/blob/f53c9f299a40cbf5fe49135dcdf7247547b3de23/pkg/controller/csr_check.go#L50-L54
var nodeBootstrapperGroups = sets.NewString(
	"system:serviceaccounts:openshift-machine-config-operator",
	"system:serviceaccounts",
	"system:authenticated",
)

// isReqFromNodeBootstrapper reports whether req was submitted by nodeBootstrapperUsername with exactly
// nodeBootstrapperGroups, as a kubelet's bootstrap or re-bootstrap CSR is.
//
// Copied from machine-approver: https://github.com/openshift/cluster-machine-approver/blob/f53c9f299a40cbf5fe49135dcdf7247547b3de23/pkg/controller/csr_check.go#L492-L494
func isReqFromNodeBootstrapper(req *certificatesv1.CertificateSigningRequest) bool {
	return req.Spec.Username == nodeBootstrapperUsername && nodeBootstrapperGroups.Equal(sets.NewString(req.Spec.Groups...))
}

// hasExactUsages reports whether csr requests exactly the given usages, in any order. Machine-approver
// copied it from k8s.io/kubernetes/pkg/controller/certificates/approver/sarapprove.go.
//
// Copied from machine-approver: https://github.com/openshift/cluster-machine-approver/blob/f53c9f299a40cbf5fe49135dcdf7247547b3de23/pkg/controller/node_client.go#L11-L30
func hasExactUsages(csr *certificatesv1.CertificateSigningRequest, usages []certificatesv1.KeyUsage) bool {
	if len(usages) != len(csr.Spec.Usages) {
		return false
	}

	usageMap := map[certificatesv1.KeyUsage]struct{}{}
	for _, u := range usages {
		usageMap[u] = struct{}{}
	}

	for _, u := range csr.Spec.Usages {
		if _, ok := usageMap[u]; !ok {
			return false
		}
	}

	return true
}

// kubeletClientUsagesLegacy are the usages a kubelet with an RSA key requests for its client
// certificate.
//
// Copied from machine-approver: https://github.com/openshift/cluster-machine-approver/blob/f53c9f299a40cbf5fe49135dcdf7247547b3de23/pkg/controller/node_client.go#L32-L36
var kubeletClientUsagesLegacy = []certificatesv1.KeyUsage{
	certificatesv1.UsageKeyEncipherment,
	certificatesv1.UsageDigitalSignature,
	certificatesv1.UsageClientAuth,
}

// kubeletClientUsages are the usages a kubelet with any other key (for example ECDSA) requests for its
// client certificate.
//
// Copied from machine-approver: https://github.com/openshift/cluster-machine-approver/blob/f53c9f299a40cbf5fe49135dcdf7247547b3de23/pkg/controller/node_client.go#L38-L41
var kubeletClientUsages = []certificatesv1.KeyUsage{
	certificatesv1.UsageDigitalSignature,
	certificatesv1.UsageClientAuth,
}

// isNodeClientCert reports whether csr asks for a kubelet client certificate: organization exactly
// system:nodes, a system:node: common name, no DNS, email or IP SANs, and one of the kubelet client
// usage sets.
//
// Copied from machine-approver: https://github.com/openshift/cluster-machine-approver/blob/f53c9f299a40cbf5fe49135dcdf7247547b3de23/pkg/controller/node_client.go#L43-L57
func isNodeClientCert(csr *certificatesv1.CertificateSigningRequest, x509cr *x509.CertificateRequest) bool {
	if !reflect.DeepEqual([]string{"system:nodes"}, x509cr.Subject.Organization) {
		return false
	}
	if (len(x509cr.DNSNames) > 0) || (len(x509cr.EmailAddresses) > 0) || (len(x509cr.IPAddresses) > 0) {
		return false
	}
	if !hasExactUsages(csr, kubeletClientUsagesLegacy) && !hasExactUsages(csr, kubeletClientUsages) {
		return false
	}
	if !strings.HasPrefix(x509cr.Subject.CommonName, "system:node:") {
		return false
	}
	return true
}
