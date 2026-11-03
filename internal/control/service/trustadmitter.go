package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// trustAdmitter is the Administrator side of ADR-0038 §6: it reads a signed
// version, checks it with the public key the Control Plane holds, and admits it
// with the approvals the Operator API collected.
//
// The Control Plane never sees a private key, so a compromised process can refuse
// a version but cannot create one.
type trustAdmitter struct {
	store *trustbundle.Store
	// keys are the Administrator's public keys. More than one during a handover
	// (ADR-0038 §7): a signature that any of them verifies is accepted, so the
	// change of person does not create a window where nothing verifies.
	keys []ed25519.PublicKey
	book *trustbundle.ApprovalBook
	// state persists the version once it has been admitted. Nil keeps it in memory
	// only, which a test may want but a deployment must not.
	state statestore.TrustBundleStore
	// log records which key verified the version. The key itself is not stored with
	// the version (ADR-0038 §7): the log is what an investigation reads.
	log *slog.Logger
	// verifiedKey is the fingerprint of the key that actually verified the last
	// admitted version. It is a fact, unlike the signer's key_id claim (ADR-0038 §8).
	verifiedKey string
	now         func() time.Time
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
	approvals := a.book.For(signed.Bundle.Version)
	at := now().UTC()
	if err := a.admit(signed, at); err != nil {
		return err
	}
	if a.log != nil {
		// Which key verified the version is not stored with it (ADR-0038 §7); the
		// log is where an investigation looks, so the key id goes here.
		a.log.Info("trust bundle admitted", "version", signed.Bundle.Version, "key_id", signed.KeyID, "verified_key", a.verifiedKey)
	}
	if a.state == nil {
		return nil
	}
	// The version is admitted; now make it survive a restart. A failure here is
	// reported rather than swallowed: an admission forgotten on the next start
	// would let the same version through twice.
	if err := trustbundle.Save(context.Background(), a.state, signed, approvals, a.verifiedKey, at); err != nil {
		return fmt.Errorf("trustadmitter: save version %d: %w", signed.Bundle.Version, err)
	}
	return nil
}

// admit lets the store check the version against each configured key and stops at
// the first that verifies. A failed attempt changes nothing (the store releases
// approvals only on success), so trying the next key is safe.
func (a *trustAdmitter) admit(signed trustbundle.Signed, at time.Time) error {
	var last error
	for _, key := range a.keys {
		err := a.store.AdmitFromBook(signed, key, a.book, at)
		if err == nil {
			a.verifiedKey = fingerprint(key)
			return nil
		}
		last = err
	}
	if last == nil {
		// No key at all means nothing can be checked. Say that rather than accept,
		// and rather than return a nil error for a version nobody verified.
		return errors.New("trustadmitter: no administrator key is configured")
	}
	return fmt.Errorf("trustadmitter: no configured key verifies version %d: %w", signed.Bundle.Version, last)
}

// fingerprint names a public key by what it is, not by what the signer called it.
// It is the value an investigation can compare against a configured key.
func fingerprint(key ed25519.PublicKey) string {
	sum := sha256.Sum256(key)
	return hex.EncodeToString(sum[:])
}
