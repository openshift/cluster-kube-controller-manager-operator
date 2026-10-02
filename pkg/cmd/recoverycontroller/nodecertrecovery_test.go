package recoverycontroller

import (
	"encoding/pem"
	"testing"
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
