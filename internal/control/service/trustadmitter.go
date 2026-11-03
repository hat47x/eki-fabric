package service

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

// trustAdmitter is the Administrator side of ADR-0038 §6: it reads a signed
// version, checks it with the public key the Control Plane holds, and admits it
// with the approvals the Operator API collected.
//
// The Control Plane never sees a private key, so a compromised process can refuse
// a version but cannot create one.
type trustAdmitter struct {
	store *trustbundle.Store
	key   ed25519.PublicKey
	book  *trustbundle.ApprovalBook
	now   func() time.Time
}

// signedDocument is the wire form of a signed version. It names every field
// explicitly instead of reusing a storage record: the wire format is a contract
// with the Administrator's tool, and a storage change must not silently change it.
type signedDocument struct {
	Version     uint64    `json:"version"`
	KeyID       string    `json:"key_id"`
	Signature   string    `json:"signature"`
	NotAfter    time.Time `json:"not_after"`
	TrustDomain string    `json:"trust_domain"`
	Issuers     []struct {
		Fingerprint string    `json:"fingerprint"`
		NotBefore   time.Time `json:"not_before"`
		NotAfter    time.Time `json:"not_after"`
	} `json:"issuers"`
}

// AdmitSigned implements operatorapi.Admitter.
func (a *trustAdmitter) AdmitSigned(version uint64, raw []byte) error {
	if a == nil || a.store == nil || a.book == nil {
		return fmt.Errorf("trustadmitter: not configured")
	}
	var doc signedDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("trustadmitter: the document is not a signed version: %v", err)
	}
	if doc.Version != version {
		return fmt.Errorf("trustadmitter: the document is version %d, the request says %d", doc.Version, version)
	}
	if doc.KeyID == "" {
		return fmt.Errorf("trustadmitter: key_id is required")
	}
	signature, err := base64.StdEncoding.DecodeString(doc.Signature)
	if err != nil || len(signature) == 0 {
		return fmt.Errorf("trustadmitter: signature must be base64")
	}
	bundle := trustbundle.Bundle{Version: doc.Version, NotAfter: doc.NotAfter}
	for _, issuer := range doc.Issuers {
		fingerprint, err := contract.ParseSHA256(issuer.Fingerprint)
		if err != nil {
			return fmt.Errorf("trustadmitter: issuer fingerprint: %v", err)
		}
		bundle.Issuers = append(bundle.Issuers, trustbundle.Issuer{
			Fingerprint: fingerprint, NotBefore: issuer.NotBefore, NotAfter: issuer.NotAfter,
		})
	}
	signed := trustbundle.Signed{Bundle: bundle, KeyID: doc.KeyID, Signature: signature}
	now := time.Now
	if a.now != nil {
		now = a.now
	}
	return a.store.AdmitFromBook(signed, a.key, a.book, now().UTC())
}
