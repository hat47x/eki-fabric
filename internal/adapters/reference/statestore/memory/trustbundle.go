package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// TrustBundles is the in-memory TrustBundleStore (ADR-0036 §6). Versions are
// append-only and each is written together with the approvals that allowed it,
// under one lock, so a reader never sees a version without its approvals.
type TrustBundles struct {
	mu        sync.Mutex
	versions  map[uint64]statestore.TrustBundleRecord
	approvals map[uint64][]statestore.TrustBundleApprovalRecord
}

// NewTrustBundles returns an empty store.
func NewTrustBundles() *TrustBundles {
	return &TrustBundles{
		versions:  make(map[uint64]statestore.TrustBundleRecord),
		approvals: make(map[uint64][]statestore.TrustBundleApprovalRecord),
	}
}

// SaveTrustBundle records a version and its approvals together.
func (t *TrustBundles) SaveTrustBundle(ctx context.Context, record statestore.TrustBundleRecord, approvals []statestore.TrustBundleApprovalRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if existing, ok := t.versions[record.Version]; ok {
		if existing.Digest != record.Digest {
			return statestore.ErrTrustBundleConflict
		}
		return nil
	}
	record.Issuers = append([]statestore.TrustBundleIssuer(nil), record.Issuers...)
	t.versions[record.Version] = record
	t.approvals[record.Version] = append([]statestore.TrustBundleApprovalRecord(nil), approvals...)
	return nil
}

// LatestTrustBundle returns the newest version.
func (t *TrustBundles) LatestTrustBundle(ctx context.Context) (statestore.TrustBundleRecord, error) {
	if err := ctx.Err(); err != nil {
		return statestore.TrustBundleRecord{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.versions) == 0 {
		return statestore.TrustBundleRecord{}, statestore.ErrNotFound
	}
	var newest uint64
	for version := range t.versions {
		if version > newest {
			newest = version
		}
	}
	return copyRecord(t.versions[newest]), nil
}

// LoadTrustBundle returns one version.
func (t *TrustBundles) LoadTrustBundle(ctx context.Context, version uint64) (statestore.TrustBundleRecord, error) {
	if err := ctx.Err(); err != nil {
		return statestore.TrustBundleRecord{}, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	record, ok := t.versions[version]
	if !ok {
		return statestore.TrustBundleRecord{}, statestore.ErrNotFound
	}
	return copyRecord(record), nil
}

// ListTrustBundles returns versions newest first, up to limit.
func (t *TrustBundles) ListTrustBundles(ctx context.Context, limit int) ([]statestore.TrustBundleRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	versions := make([]uint64, 0, len(t.versions))
	for version := range t.versions {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	if limit > 0 && len(versions) > limit {
		versions = versions[:limit]
	}
	out := make([]statestore.TrustBundleRecord, 0, len(versions))
	for _, version := range versions {
		out = append(out, copyRecord(t.versions[version]))
	}
	return out, nil
}

// ListTrustBundleApprovals returns one version's approvals, oldest first.
func (t *TrustBundles) ListTrustBundleApprovals(ctx context.Context, version uint64) ([]statestore.TrustBundleApprovalRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.versions[version]; !ok {
		return nil, statestore.ErrNotFound
	}
	return append([]statestore.TrustBundleApprovalRecord(nil), t.approvals[version]...), nil
}

func copyRecord(record statestore.TrustBundleRecord) statestore.TrustBundleRecord {
	record.Issuers = append([]statestore.TrustBundleIssuer(nil), record.Issuers...)
	record.Signature = append([]byte(nil), record.Signature...)
	return record
}
