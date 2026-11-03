package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

// The path an approval takes to become a version (#545): an Operator approves the
// issuer, the Administrator admits the signed version, and the approval is spent.
// The store being empty of that approval afterwards is what makes "approved once"
// mean "used once" rather than "standing permission".
func TestAnApprovalBecomesAVersionAndIsSpent(t *testing.T) {
	ctx := context.Background()
	state := memory.NewTrustBundles()
	book := trustbundle.NewApprovalBook()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	first := bundleFor(1, now, contract.SHA256([]byte("ca-1")))
	if err := trustbundle.Save(ctx, state, sign(t, first, private), nil, "", now); err != nil {
		t.Fatal(err)
	}

	admitter := startAdmitter(ctx, state, book, []string{hex.EncodeToString(public)}, log)
	if admitter == nil {
		t.Fatal("startAdmitter() hid the route with a stored version and a key")
	}

	second := bundleFor(2, now.Add(time.Hour), contract.SHA256([]byte("ca-1")), contract.SHA256([]byte("ca-2")))
	added := second.Issuers[1].Fingerprint

	// Without the approval the version is refused, and the reason is the missing one.
	if err := admitter.AdmitSigned(2, []byte(wireDocument(t, sign(t, second, private)))); err == nil {
		t.Fatal("a version adding an issuer was admitted without an approval")
	}

	// The Operator approves that issuer; the next attempt goes through.
	if err := book.Add(trustbundle.Approval{Version: 2, Operation: trustbundle.OperationAdd, Issuer: added, ApprovedBy: "alice", ApprovedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := admitter.AdmitSigned(2, []byte(wireDocument(t, sign(t, second, private)))); err != nil {
		t.Fatalf("AdmitSigned() error = %v, want the approved version to be admitted", err)
	}
	if got := book.Len(2); got != 0 {
		t.Fatalf("the approval book still holds %d approvals for version 2: it must be spent", got)
	}
	record, err := state.LatestTrustBundle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if record.Version != 2 {
		t.Fatalf("stored version = %d, want 2", record.Version)
	}
}

func bundleFor(version uint64, notAfter time.Time, issuers ...contract.Digest) trustbundle.Bundle {
	bundle := trustbundle.Bundle{Version: version, NotAfter: notAfter}
	for _, issuer := range issuers {
		bundle.Issuers = append(bundle.Issuers, trustbundle.Issuer{
			Fingerprint: issuer, NotBefore: notAfter.Add(-24 * time.Hour), NotAfter: notAfter,
		})
	}
	return bundle
}

func sign(t *testing.T, bundle trustbundle.Bundle, private ed25519.PrivateKey) trustbundle.Signed {
	t.Helper()
	signed, err := trustbundle.Sign(bundle, "control-key-1", private)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// wireDocument is the shape the Administrator's tool sends: the version fields plus
// the base64 signature (the contract in spec/external/operator-api.md).
func wireDocument(t *testing.T, signed trustbundle.Signed) []byte {
	t.Helper()
	type issuer struct {
		Fingerprint string    `json:"fingerprint"`
		NotBefore   time.Time `json:"not_before"`
		NotAfter    time.Time `json:"not_after"`
	}
	doc := struct {
		Version   uint64    `json:"version"`
		KeyID     string    `json:"key_id"`
		Signature string    `json:"signature"`
		NotAfter  time.Time `json:"not_after"`
		Issuers   []issuer  `json:"issuers"`
	}{Version: signed.Bundle.Version, KeyID: signed.KeyID, Signature: base64.StdEncoding.EncodeToString(signed.Signature), NotAfter: signed.Bundle.NotAfter}
	for _, in := range signed.Bundle.Issuers {
		doc.Issuers = append(doc.Issuers, issuer{Fingerprint: in.Fingerprint.Hex(), NotBefore: in.NotBefore, NotAfter: in.NotAfter})
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
