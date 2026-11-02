package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var tbNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func trustRecord(version uint64, digest byte) statestore.TrustBundleRecord {
	return statestore.TrustBundleRecord{
		Version: version, NotAfter: tbNow.Add(90 * 24 * time.Hour), KeyID: "control-key-1",
		Signature: []byte{1, 2, 3}, Digest: contract.SHA256([]byte{digest}), RecordedAt: tbNow,
		Issuers: []statestore.TrustBundleIssuer{{Fingerprint: contract.SHA256([]byte("ca")), NotBefore: tbNow, NotAfter: tbNow.Add(365 * 24 * time.Hour)}},
	}
}

func approvalRecord(version uint64) statestore.TrustBundleApprovalRecord {
	return statestore.TrustBundleApprovalRecord{
		Version: version, Operation: "add", Issuer: contract.SHA256([]byte("ca")),
		ApprovedBy: "operator@example.org", ApprovedAt: tbNow,
	}
}

func TestAStoredVersionIsTheLatest(t *testing.T) {
	ctx := context.Background()
	store := NewTrustBundles()
	if err := store.SaveTrustBundle(ctx, trustRecord(1, 1), []statestore.TrustBundleApprovalRecord{approvalRecord(1)}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrustBundle(ctx, trustRecord(2, 2), []statestore.TrustBundleApprovalRecord{approvalRecord(2)}); err != nil {
		t.Fatal(err)
	}
	latest, err := store.LatestTrustBundle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Version != 2 {
		t.Fatalf("latest = %d, want 2", latest.Version)
	}
}

func TestTheSameVersionWithAnotherDigestIsAConflict(t *testing.T) {
	ctx := context.Background()
	store := NewTrustBundles()
	if err := store.SaveTrustBundle(ctx, trustRecord(1, 1), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrustBundle(ctx, trustRecord(1, 9), nil); !errors.Is(err, statestore.ErrTrustBundleConflict) {
		t.Fatalf("error = %v, want %v", err, statestore.ErrTrustBundleConflict)
	}
}

func TestSavingTheSameVersionAgainWithTheSameDigestIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := NewTrustBundles()
	if err := store.SaveTrustBundle(ctx, trustRecord(1, 1), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTrustBundle(ctx, trustRecord(1, 1), nil); err != nil {
		t.Fatalf("second save error = %v", err)
	}
}

func TestAnAbsentVersionIsNotFound(t *testing.T) {
	ctx := context.Background()
	store := NewTrustBundles()
	if _, err := store.LoadTrustBundle(ctx, 1); !errors.Is(err, statestore.ErrNotFound) {
		t.Fatalf("load error = %v, want %v", err, statestore.ErrNotFound)
	}
	if _, err := store.LatestTrustBundle(ctx); !errors.Is(err, statestore.ErrNotFound) {
		t.Fatalf("latest error = %v, want %v", err, statestore.ErrNotFound)
	}
	if _, err := store.ListTrustBundleApprovals(ctx, 1); !errors.Is(err, statestore.ErrNotFound) {
		t.Fatalf("approvals error = %v, want %v", err, statestore.ErrNotFound)
	}
}

func TestListReturnsNewestFirstAndHonoursTheLimit(t *testing.T) {
	ctx := context.Background()
	store := NewTrustBundles()
	for version := uint64(1); version <= 3; version++ {
		if err := store.SaveTrustBundle(ctx, trustRecord(version, byte(version)), nil); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.ListTrustBundles(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Version != 3 || all[2].Version != 1 {
		t.Fatalf("list = %+v, want 3,2,1", all)
	}
	limited, err := store.ListTrustBundles(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].Version != 3 {
		t.Fatalf("limited = %+v, want 3,2", limited)
	}
}

func TestApprovalsComeBackWithTheirVersion(t *testing.T) {
	ctx := context.Background()
	store := NewTrustBundles()
	approvals := []statestore.TrustBundleApprovalRecord{approvalRecord(1), approvalRecord(1)}
	if err := store.SaveTrustBundle(ctx, trustRecord(1, 1), approvals); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListTrustBundleApprovals(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("approvals = %d, want 2", len(got))
	}
}
