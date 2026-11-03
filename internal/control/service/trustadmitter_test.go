package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

func TestAdmittingADocumentWithTheWrongVersionIsRefused(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	admitter := &trustAdmitter{keys: []ed25519.PublicKey{public}, book: trustbundle.NewApprovalBook()}
	raw, err := json.Marshal(map[string]any{
		"version": 3, "key_id": "k1", "signature": base64.StdEncoding.EncodeToString([]byte("sig")),
		"not_after": time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := admitter.AdmitSigned(2, raw); err == nil {
		t.Fatal("AdmitSigned() accepted a document for another version")
	}
}

func TestADocumentWithoutASignatureIsRefused(t *testing.T) {
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	admitter := &trustAdmitter{keys: []ed25519.PublicKey{public}, book: trustbundle.NewApprovalBook()}
	raw, err := json.Marshal(map[string]any{"version": 2, "key_id": "k1", "not_after": time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := admitter.AdmitSigned(2, raw); err == nil {
		t.Fatal("AdmitSigned() accepted a document without a signature")
	}
}
