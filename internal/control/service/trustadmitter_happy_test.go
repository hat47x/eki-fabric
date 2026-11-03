package service

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

func TestASignedVersionIsAdmittedWithItsApprovals(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	first := trustbundle.Bundle{
		Version:  1,
		Issuers:  []trustbundle.Issuer{{Fingerprint: contract.SHA256([]byte("ca1")), NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(365 * 24 * time.Hour)}},
		NotAfter: now.Add(365 * 24 * time.Hour),
	}
	signedFirst, err := trustbundle.Sign(first, "k1", private)
	if err != nil {
		t.Fatal(err)
	}
	store, err := trustbundle.NewStore(signedFirst)
	if err != nil {
		t.Fatal(err)
	}
	second := trustbundle.Bundle{
		Version: 2,
		Issuers: append(append([]trustbundle.Issuer(nil), first.Issuers...),
			trustbundle.Issuer{Fingerprint: contract.SHA256([]byte("ca2")), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour)}),
		NotAfter: now.Add(400 * 24 * time.Hour),
	}
	signedSecond, err := trustbundle.Sign(second, "k1", private)
	if err != nil {
		t.Fatal(err)
	}
	book := trustbundle.NewApprovalBook()
	if err := book.Add(trustbundle.Approval{
		Version: 2, Operation: trustbundle.OperationAdd, Issuer: second.Issuers[1].Fingerprint,
		ApprovedBy: "alice", ApprovedAt: now, Justification: "new authority",
	}); err != nil {
		t.Fatal(err)
	}
	admitter := &trustAdmitter{store: store, key: public, book: book, now: func() time.Time { return now.Add(time.Minute) }}

	raw, err := json.Marshal(map[string]any{
		"version":   2,
		"key_id":    "k1",
		"signature": base64.StdEncoding.EncodeToString(signedSecond.Signature),
		"not_after": second.NotAfter,
		"issuers": []map[string]any{
			{"fingerprint": second.Issuers[0].Fingerprint.Hex(), "not_before": second.Issuers[0].NotBefore, "not_after": second.Issuers[0].NotAfter},
			{"fingerprint": second.Issuers[1].Fingerprint.Hex(), "not_before": second.Issuers[1].NotBefore, "not_after": second.Issuers[1].NotAfter},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := admitter.AdmitSigned(2, raw); err != nil {
		t.Fatalf("AdmitSigned() error = %v", err)
	}
	current, err := store.Current()
	if err != nil || current.Bundle.Version != 2 {
		t.Fatalf("current = %+v, %v", current, err)
	}
	if got := book.Len(2); got != 0 {
		t.Fatalf("the book kept %d approvals after admission", got)
	}
}
