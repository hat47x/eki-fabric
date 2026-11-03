package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// trailStore is the smallest store that can answer the one question startAdmitter
// asks: which version is current.
type trailStore struct {
	latest statestore.TrustBundleRecord
	err    error
}

func (s trailStore) SaveTrustBundle(context.Context, statestore.TrustBundleRecord, []statestore.TrustBundleApprovalRecord) error {
	return nil
}

func (s trailStore) LatestTrustBundle(context.Context) (statestore.TrustBundleRecord, error) {
	return s.latest, s.err
}

func (s trailStore) LoadTrustBundle(context.Context, uint64) (statestore.TrustBundleRecord, error) {
	return statestore.TrustBundleRecord{}, statestore.ErrNotFound
}

func (s trailStore) ListTrustBundles(context.Context, int) ([]statestore.TrustBundleRecord, error) {
	return nil, nil
}

var _ statestore.TrustBundleStore = trailStore{}

func (s trailStore) ListTrustBundleApprovals(context.Context, uint64) ([]statestore.TrustBundleApprovalRecord, error) {
	return nil, nil
}

func TestWithoutAStoredVersionTheRouteStaysHidden(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	admitter := startAdmitter(context.Background(), trailStore{err: statestore.ErrNotFound}, trustbundle.NewApprovalBook(), []string{hex.EncodeToString(public)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if admitter != nil {
		t.Fatal("startAdmitter returned an admitter without a version to succeed")
	}
	_ = private
}

func TestWithAStoredVersionAndAKeyTheRouteOpens(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bundle := trustbundle.Bundle{
		Version:  1,
		Issuers:  []trustbundle.Issuer{{Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}},
		NotAfter: now.Add(24 * time.Hour),
	}
	signed, err := trustbundle.Sign(bundle, "control-key-1", private)
	if err != nil {
		t.Fatal(err)
	}
	store := trailStore{latest: trustbundle.ToRecord(signed, now)}
	if _, err := trustbundle.FromRecord(store.latest); err != nil {
		t.Fatalf("the fixture record does not read back: %v", err)
	}
	var logs bytes.Buffer
	admitter := startAdmitter(context.Background(), store, trustbundle.NewApprovalBook(), []string{hex.EncodeToString(public)}, slog.New(slog.NewTextHandler(&logs, nil)))
	if admitter == nil {
		t.Fatalf("startAdmitter hid the route with a version and a key in place; it said: %s", logs.String())
	}
}
