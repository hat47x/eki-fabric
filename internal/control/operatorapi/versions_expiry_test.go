package operatorapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// The answer says whether a version is about to expire, so the operator does not
// have to compare dates in their head (ADR-0036 §10.2, #685).
func TestTheHistorySaysWhenAVersionIsAboutToExpire(t *testing.T) {
	now := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	records := []statestore.TrustBundleRecord{
		{Version: 3, NotAfter: now.Add(31 * 24 * time.Hour), RecordedAt: now},
		{Version: 2, NotAfter: now.Add(29 * 24 * time.Hour), RecordedAt: now},
		{Version: 1, NotAfter: now.Add(-time.Hour), RecordedAt: now},
	}
	h := &Handler{
		bundles:  versionStore{records: records},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:      func() time.Time { return now },
		auditErr: func(Access) error { return nil },
	}
	rec := httptest.NewRecorder()
	h.versions(rec, httptest.NewRequest(http.MethodGet, VersionsPath, nil), operatorcert.Identity{Name: "alice", Role: operatorcert.RoleOperator})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Versions []struct {
			Version      uint64 `json:"version"`
			ExpiringSoon bool   `json:"expiring_soon"`
			Expired      bool   `json:"expired"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Versions[0].ExpiringSoon || body.Versions[0].Expired {
		t.Fatalf("a version with 31 days left was flagged: %+v", body.Versions[0])
	}
	if !body.Versions[1].ExpiringSoon || body.Versions[1].Expired {
		t.Fatalf("a version with 29 days left was not flagged as expiring: %+v", body.Versions[1])
	}
	if !body.Versions[2].Expired || body.Versions[2].ExpiringSoon {
		t.Fatalf("an expired version was not flagged as expired: %+v", body.Versions[2])
	}
}

// It is the same store the other operator routes read.
type versionStore struct {
	records []statestore.TrustBundleRecord
}

func (s versionStore) SaveTrustBundle(context.Context, statestore.TrustBundleRecord, []statestore.TrustBundleApprovalRecord) error {
	return nil
}

func (s versionStore) LatestTrustBundle(context.Context) (statestore.TrustBundleRecord, error) {
	return s.records[0], nil
}

func (s versionStore) LoadTrustBundle(context.Context, uint64) (statestore.TrustBundleRecord, error) {
	return statestore.TrustBundleRecord{}, statestore.ErrNotFound
}

func (s versionStore) ListTrustBundles(context.Context, int) ([]statestore.TrustBundleRecord, error) {
	return s.records, nil
}

func (s versionStore) ListTrustBundleApprovals(context.Context, uint64) ([]statestore.TrustBundleApprovalRecord, error) {
	return nil, nil
}
