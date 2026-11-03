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

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type versionsStore struct {
	records []statestore.TrustBundleRecord
}

func (s versionsStore) SaveTrustBundle(context.Context, statestore.TrustBundleRecord, []statestore.TrustBundleApprovalRecord) error {
	return nil
}

func (s versionsStore) LatestTrustBundle(context.Context) (statestore.TrustBundleRecord, error) {
	return s.records[0], nil
}

func (s versionsStore) LoadTrustBundle(context.Context, uint64) (statestore.TrustBundleRecord, error) {
	return statestore.TrustBundleRecord{}, statestore.ErrNotFound
}

func (s versionsStore) ListTrustBundles(context.Context, int) ([]statestore.TrustBundleRecord, error) {
	return s.records, nil
}

func (s versionsStore) ListTrustBundleApprovals(context.Context, uint64) ([]statestore.TrustBundleApprovalRecord, error) {
	return nil, nil
}

var _ statestore.TrustBundleStore = versionsStore{}

func TestTheVersionHistoryCarriesBothKeyFields(t *testing.T) {
	at := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	store := versionsStore{records: []statestore.TrustBundleRecord{
		{Version: 2, NotAfter: at, RecordedAt: at, KeyID: "old-name", VerifiedKey: "abcdef"},
		{Version: 1, NotAfter: at, RecordedAt: at, KeyID: "old-name"},
	}}
	h := &Handler{
		bundles:  store,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:      func() time.Time { return at },
		auditErr: func(Access) error { return nil },
	}
	rec := httptest.NewRecorder()
	h.versions(rec, httptest.NewRequest(http.MethodGet, VersionsPath, nil), operatorcert.Identity{Name: "alice", Role: operatorcert.RoleOperator})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Versions []struct {
			Version     uint64 `json:"version"`
			KeyID       string `json:"key_id"`
			VerifiedKey string `json:"verified_key"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(body.Versions))
	}
	if body.Versions[0].VerifiedKey != "abcdef" {
		t.Fatalf("verified_key = %q, want the fingerprint", body.Versions[0].VerifiedKey)
	}
	if body.Versions[1].VerifiedKey != "" {
		t.Fatalf("verified_key = %q for a version recorded before the field: it must stay empty, not be guessed", body.Versions[1].VerifiedKey)
	}
}

var _ = contract.Digest{}
