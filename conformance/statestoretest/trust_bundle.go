package statestoretest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// TrustBundleFactory builds an empty TrustBundleStore for one test.
type TrustBundleFactory func(t *testing.T) statestore.TrustBundleStore

// RunTrustBundleContract fixes what every TrustBundleStore must do, so the
// memory and SQLite implementations cannot drift (ADR-0036 §6, #451).
func RunTrustBundleContract(t *testing.T, newStore TrustBundleFactory) {
	t.Helper()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	record := func(version uint64, digest byte) statestore.TrustBundleRecord {
		return statestore.TrustBundleRecord{
			Version: version, NotAfter: now.Add(90 * 24 * time.Hour), KeyID: "control-key-1",
			Signature: []byte{1, 2, 3}, Digest: contract.SHA256([]byte{digest}), RecordedAt: now,
			Issuers: []statestore.TrustBundleIssuer{{Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour)}},
		}
	}
	approval := func(version uint64) statestore.TrustBundleApprovalRecord {
		return statestore.TrustBundleApprovalRecord{
			Version: version, Operation: "add", Issuer: contract.SHA256([]byte("ca")),
			ApprovedBy: "operator@example.org", ApprovedAt: now,
		}
	}

	t.Run("a stored version is the latest", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		if err := store.SaveTrustBundle(ctx, record(1, 1), []statestore.TrustBundleApprovalRecord{approval(1)}); err != nil {
			t.Fatalf("save 1: %v", err)
		}
		if err := store.SaveTrustBundle(ctx, record(2, 2), []statestore.TrustBundleApprovalRecord{approval(2)}); err != nil {
			t.Fatalf("save 2: %v", err)
		}
		latest, err := store.LatestTrustBundle(ctx)
		if err != nil {
			t.Fatalf("latest: %v", err)
		}
		if latest.Version != 2 || latest.Digest != record(2, 2).Digest {
			t.Fatalf("latest = %d %s, want 2", latest.Version, latest.Digest)
		}
		if len(latest.Issuers) != 1 {
			t.Fatalf("issuers = %d, want 1", len(latest.Issuers))
		}
	})

	t.Run("the same version with another digest is a conflict", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		if err := store.SaveTrustBundle(ctx, record(1, 1), nil); err != nil {
			t.Fatalf("save: %v", err)
		}
		if err := store.SaveTrustBundle(ctx, record(1, 9), nil); !errors.Is(err, statestore.ErrTrustBundleConflict) {
			t.Fatalf("error = %v, want %v", err, statestore.ErrTrustBundleConflict)
		}
	})

	t.Run("saving the same version and digest again is idempotent", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		for i := 0; i < 2; i++ {
			if err := store.SaveTrustBundle(ctx, record(1, 1), nil); err != nil {
				t.Fatalf("save %d: %v", i, err)
			}
		}
	})

	t.Run("an absent version is not found", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		if _, err := store.LoadTrustBundle(ctx, 1); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("load = %v, want %v", err, statestore.ErrNotFound)
		}
		if _, err := store.LatestTrustBundle(ctx); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("latest = %v, want %v", err, statestore.ErrNotFound)
		}
		if _, err := store.ListTrustBundleApprovals(ctx, 1); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("approvals = %v, want %v", err, statestore.ErrNotFound)
		}
	})

	t.Run("versions are listed newest first and the limit applies", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		for version := uint64(1); version <= 3; version++ {
			if err := store.SaveTrustBundle(ctx, record(version, byte(version)), nil); err != nil {
				t.Fatalf("save %d: %v", version, err)
			}
		}
		all, err := store.ListTrustBundles(ctx, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(all) != 3 || all[0].Version != 3 || all[2].Version != 1 {
			t.Fatalf("list = %+v, want 3,2,1", all)
		}
		limited, err := store.ListTrustBundles(ctx, 2)
		if err != nil {
			t.Fatalf("limited: %v", err)
		}
		if len(limited) != 2 || limited[0].Version != 3 {
			t.Fatalf("limited = %+v, want 3,2", limited)
		}
	})

	t.Run("approvals come back with their version", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		if err := store.SaveTrustBundle(ctx, record(1, 1), []statestore.TrustBundleApprovalRecord{approval(1), approval(1)}); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := store.ListTrustBundleApprovals(ctx, 1)
		if err != nil {
			t.Fatalf("approvals: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("approvals = %d, want 2", len(got))
		}
	})
}
