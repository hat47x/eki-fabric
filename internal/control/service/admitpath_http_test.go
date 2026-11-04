package service

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/control/operatorapi"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

// approverAdapter is the shape the Operator route calls: the book is where the
// approval lands, and the route is what a real Operator talks to.
type approverAdapter struct{ book *trustbundle.ApprovalBook }

func (a approverAdapter) Approve(approval trustbundle.Approval) error { return a.book.Add(approval) }

// The whole path over HTTP, with the roles and the audit that a deployment has:
// the Operator approves, the Administrator admits, and the approval is spent
// (#545).
func TestTheOperatorAndAdministratorRoutesCarryAnApprovalIntoAVersion(t *testing.T) {
	ctx := context.Background()
	state := memory.NewTrustBundles()
	book := trustbundle.NewApprovalBook()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// startAdmitter judges currentness with the real clock, so the fixture must be
	// relative to it. A fixed date made these tests fail once the date passed.
	now := time.Now().UTC().Truncate(time.Second)

	first := bundleFor(1, now, contract.SHA256([]byte("ca-1")))
	if err := trustbundle.Save(ctx, state, sign(t, first, private), nil, "", now); err != nil {
		t.Fatal(err)
	}
	admitter := startAdmitter(ctx, state, book, []string{hex.EncodeToString(public)}, log)
	if admitter == nil {
		t.Fatal("the admission route stayed hidden with a stored version and a key")
	}

	// The same person holds both roles in this test, one at a time: the routes check
	// the role the certificate carries, and a certificate carries exactly one.
	identity := operatorcert.Identity{Name: "alice", Role: operatorcert.RoleOperator}
	handler := operatorapi.New(operatorapi.Config{
		Authenticate: func(*http.Request) (operatorcert.Identity, error) { return identity, nil },
		Approver:     approverAdapter{book: book},
		Admitter:     admitter,
		Book:         book,
		Bundles:      state,
		Logger:       log,
		Now:          func() time.Time { return now },
		AuditErr:     func(operatorapi.Access) error { return nil },
	})

	second := bundleFor(2, now.Add(time.Hour), contract.SHA256([]byte("ca-1")), contract.SHA256([]byte("ca-2")))
	added := second.Issuers[1].Fingerprint

	// Without the approval the Administrator's route refuses with a conflict.
	if code, body := postJSON(t, handler, operatorapi.AdmitPath, mustJSON(t, map[string]any{"version": 2, "signed": base64.StdEncoding.EncodeToString(wireDocument(t, sign(t, second, private)))})); code == http.StatusCreated {
		t.Fatalf("the version was admitted before any approval: %d %s", code, body)
	}

	approve := map[string]any{"version": 2, "operation": "add", "issuer": added.Hex(), "approved_by": "alice", "justification": "add the second CA"}
	if code, body := postJSON(t, handler, operatorapi.Path, mustJSON(t, approve)); code != http.StatusCreated {
		t.Fatalf("approve = %d %s", code, body)
	}

	identity.Role = operatorcert.RoleAdministrator
	document := map[string]any{"version": 2, "signed": base64.StdEncoding.EncodeToString(wireDocument(t, sign(t, second, private)))}
	if code, body := postJSON(t, handler, operatorapi.AdmitPath, mustJSON(t, document)); code != http.StatusCreated {
		t.Fatalf("admin admit = %d %s", code, body)
	}
	if got := book.Len(2); got != 0 {
		t.Fatalf("the approval book still holds %d approvals for version 2", got)
	}
	record, err := state.LatestTrustBundle(ctx)
	if err != nil || record.Version != 2 {
		t.Fatalf("stored version = %d, %v; want 2", record.Version, err)
	}
}

func postJSON(t *testing.T, h http.Handler, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	out, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
