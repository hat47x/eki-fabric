package service

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// A store that answers whatever the test wants, including a failure.
type bundleStoreFake struct {
	record statestore.TrustBundleRecord
	err    error
}

func (f bundleStoreFake) SaveTrustBundle(context.Context, statestore.TrustBundleRecord, []statestore.TrustBundleApprovalRecord) error {
	return nil
}

func (f bundleStoreFake) LatestTrustBundle(context.Context) (statestore.TrustBundleRecord, error) {
	return f.record, f.err
}

func (f bundleStoreFake) LoadTrustBundle(context.Context, uint64) (statestore.TrustBundleRecord, error) {
	return statestore.TrustBundleRecord{}, statestore.ErrNotFound
}

func (f bundleStoreFake) ListTrustBundles(context.Context, int) ([]statestore.TrustBundleRecord, error) {
	return nil, nil
}

func (f bundleStoreFake) ListTrustBundleApprovals(context.Context, uint64) ([]statestore.TrustBundleApprovalRecord, error) {
	return nil, nil
}

var _ statestore.TrustBundleStore = bundleStoreFake{}

// No stored version is the compatibility mode the operator chose. A version that
// cannot be read is not that: treating it as "none" would skip the version check
// while the operator believes it is on (#467).
func TestAnUnreadableVersionIsNotTheSameAsNoVersion(t *testing.T) {
	_, ok, err := currentTrustBundle(bundleStoreFake{err: statestore.ErrNotFound})()
	if err != nil || ok {
		t.Fatalf("missing version: ok = %v, err = %v; want false, nil", ok, err)
	}

	failure := errors.New("disk gone")
	_, ok, err = currentTrustBundle(bundleStoreFake{err: failure})()
	if err == nil {
		t.Fatal("a store failure was reported as no version")
	}
	if ok {
		t.Fatal("a store failure claimed a version")
	}
	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want it to wrap %v", err, failure)
	}
}

func TestAnUnusableRecordIsReportedAsAFailure(t *testing.T) {
	// A record with no signature cannot be decoded; that is a failure, not "none".
	_, ok, err := currentTrustBundle(bundleStoreFake{record: statestore.TrustBundleRecord{Version: 3}})()
	if err == nil || ok {
		t.Fatalf("ok = %v, err = %v; want a failure", ok, err)
	}
}
