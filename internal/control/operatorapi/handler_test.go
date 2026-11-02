package operatorapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/operatorcert"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

type fakeApprover struct {
	got trustbundle.Approval
	err error
}

func (f *fakeApprover) Approve(a trustbundle.Approval) error {
	f.got = a
	return f.err
}

func operatorHandler(t *testing.T, id operatorcert.Identity, approver *fakeApprover, audit *[]Access) *Handler {
	t.Helper()
	return New(Config{
		Authenticate: func(*http.Request) (operatorcert.Identity, error) { return id, nil },
		Approver:     approver,
		Audit:        func(a Access) { *audit = append(*audit, a) },
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func approvalBody(t *testing.T, approvedBy string) string {
	t.Helper()
	issuer := contract.SHA256([]byte("ca")).Hex()
	raw, err := json.Marshal(map[string]any{
		"version": 2, "operation": "add", "issuer": issuer,
		"approved_by": approvedBy, "justification": "new authority",
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func post(h *Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(body))
	h.ServeHTTP(rec, req)
	return rec
}

func TestAnOperatorApprovesWithTheCertificateName(t *testing.T) {
	approver := &fakeApprover{}
	var audit []Access
	h := operatorHandler(t, operatorcert.Identity{Name: "alice", Role: operatorcert.RoleOperator}, approver, &audit)
	rec := post(h, approvalBody(t, "alice"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if approver.got.ApprovedBy != "alice" || approver.got.Version != 2 {
		t.Fatalf("approval = %+v", approver.got)
	}
	if len(audit) != 1 || audit[0].Outcome != OutcomeApproved {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestAnAdministratorCannotApprove(t *testing.T) {
	var audit []Access
	h := operatorHandler(t, operatorcert.Identity{Name: "bob", Role: operatorcert.RoleAdministrator}, &fakeApprover{}, &audit)
	if rec := post(h, approvalBody(t, "bob")); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(audit) != 1 || audit[0].Outcome != OutcomeRefused {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestABodyThatNamesAnotherOperatorIsRefused(t *testing.T) {
	var audit []Access
	h := operatorHandler(t, operatorcert.Identity{Name: "alice", Role: operatorcert.RoleOperator}, &fakeApprover{}, &audit)
	if rec := post(h, approvalBody(t, "mallory")); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestWithoutAnOperatorCATheRouteDoesNotExist(t *testing.T) {
	h := New(Config{})
	if rec := post(h, approvalBody(t, "alice")); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if h.Enabled() {
		t.Fatal("the handler reports itself enabled")
	}
}

func TestAnApprovalTheGuardRefusesIsReported(t *testing.T) {
	approver := &fakeApprover{err: trustbundle.ErrUnapproved}
	var audit []Access
	h := operatorHandler(t, operatorcert.Identity{Name: "alice", Role: operatorcert.RoleOperator}, approver, &audit)
	if rec := post(h, approvalBody(t, "alice")); rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	if len(audit) != 1 || audit[0].Outcome != OutcomeRefused {
		t.Fatalf("audit = %+v", audit)
	}
}
