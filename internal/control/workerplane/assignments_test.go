package workerplane_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	server "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	spi "github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// fakeAssignments hands out whatever was stored for the principal it is given
// and records who asked.
type fakeAssignments struct {
	byWorker map[string][]spi.Assignment
	asked    []spi.Principal
}

func (f *fakeAssignments) ListAssignments(_ context.Context, p spi.Principal) ([]spi.Assignment, error) {
	f.asked = append(f.asked, p)
	return f.byWorker[p.WorkerIdentity], nil
}

func (f *mtlsFixture) assignments(t *testing.T, w enrolledWorker, c *http.Client, body []byte) (*http.Response, string) {
	t.Helper()
	if c == nil {
		c = f.client(&w.cert)
	}
	return f.post(t, c, testprofile.AssignmentsPath, testprofile.ContentType, "", body)
}

func TestMTLSAssignmentsAreTheCertificateWorkersOnly(t *testing.T) {
	f := newMTLSFixture(t)
	a, b := f.enroll(t, nil), f.enroll(t, nil)
	f.assign.byWorker[string(a.id)] = []spi.Assignment{{AttemptID: "atm_a", LeaseID: "lse_a", LeaseState: spi.LeaseStateCurrent, LeaseEnvelope: []byte("lease-for-a"), WorkOrderEnvelope: []byte("order-for-a")}}
	f.assign.byWorker[string(b.id)] = []spi.Assignment{{AttemptID: "atm_b", LeaseID: "lse_b", LeaseState: spi.LeaseStateCurrent, LeaseEnvelope: []byte("lease-for-b"), WorkOrderEnvelope: []byte("order-for-b")}}

	resp, body := f.assignments(t, a, nil, testprofile.EncodeAssignmentsRequest(nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assignments = %d %s", resp.StatusCode, body)
	}
	got, err := testprofile.DecodeAssignments([]byte(body))
	if err != nil || len(got) != 1 || !bytes.Equal(got[0].LeaseEnvelope, []byte("lease-for-a")) || !bytes.Equal(got[0].WorkOrderEnvelope, []byte("order-for-a")) {
		t.Fatalf("a received %+v, %v", got, err)
	}
	if strings.Contains(body, "base64") || len(f.assign.asked) != 1 || f.assign.asked[0].WorkerIdentity != string(a.id) || f.assign.asked[0].TrustDomain != "example.org" {
		t.Fatalf("the source was asked as %+v", f.assign.asked)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", resp.Header.Get("Cache-Control"))
	}

	respB, bodyB := f.assignments(t, b, nil, testprofile.EncodeAssignmentsRequest(nil))
	gotB, err := testprofile.DecodeAssignments([]byte(bodyB))
	if respB.StatusCode != http.StatusOK || err != nil || len(gotB) != 1 || !bytes.Equal(gotB[0].LeaseEnvelope, []byte("lease-for-b")) {
		t.Fatalf("b received %d %+v, %v", respB.StatusCode, gotB, err)
	}
}

func TestMTLSAssignmentsRefuseABodyThatNamesAWorker(t *testing.T) {
	f := newMTLSFixture(t)
	a, b := f.enroll(t, nil), f.enroll(t, nil)
	for _, body := range []string{
		`{"worker_identity":"` + string(b.id) + `"}`, `{"x":1}`, `[]`, ``, `{} {}`, `not json`,
	} {
		resp, _ := f.assignments(t, a, nil, []byte(body))
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q = %d, want 400", body, resp.StatusCode)
		}
	}
	if len(f.assign.asked) != 0 {
		t.Fatalf("the source was asked for a malformed request: %+v", f.assign.asked)
	}
	if resp, _ := f.post(t, f.client(&a.cert), testprofile.AssignmentsPath, "text/plain", "", []byte("{}")); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain = %d, want 415", resp.StatusCode)
	}
	big := bytes.Repeat([]byte(" "), 4096)
	if resp, _ := f.assignments(t, a, nil, append([]byte("{}"), big...)); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize body = %d, want 413", resp.StatusCode)
	}
}

func TestMTLSAssignmentsNeedAUsableCertificate(t *testing.T) {
	f := newMTLSFixture(t)
	w := f.enroll(t, nil)
	f.assign.byWorker[string(w.id)] = []spi.Assignment{{AttemptID: "atm_a", LeaseID: "lse_a", LeaseState: spi.LeaseStateCurrent, LeaseEnvelope: []byte("l"), WorkOrderEnvelope: []byte("o")}}

	if resp, _ := f.assignments(t, w, f.client(nil), testprofile.EncodeAssignmentsRequest(nil)); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a certificate = %d, want 401", resp.StatusCode)
	}
	if resp, _ := f.post(t, f.client(&w.cert), testprofile.AssignmentsPath, testprofile.ContentType, "Bearer "+strings.Repeat("t", 30), testprofile.EncodeAssignmentsRequest(nil)); resp.StatusCode != http.StatusOK {
		t.Fatalf("a bearer header must not matter next to a valid certificate, got %d", resp.StatusCode)
	}
	asked := len(f.assign.asked)
	if _, err := f.svc.SetWorkerState(context.Background(), f.admin, w.id, workerregistry.StateRevoked); err != nil {
		t.Fatal(err)
	}
	if resp, body := f.assignments(t, w, nil, testprofile.EncodeAssignmentsRequest(nil)); resp.StatusCode != http.StatusUnauthorized || strings.Contains(body, "lse_a") {
		t.Fatalf("revoked Worker = %d %s, want 401 and no material", resp.StatusCode, body)
	}
	if len(f.assign.asked) != asked {
		t.Fatal("a revoked Worker reached the assignment source")
	}
}

func TestMTLSAssignmentsRouteIsAbsentWithoutASource(t *testing.T) {
	f := newMTLSFixture(t)
	h, err := server.NewMTLSHandler(server.MTLSConfig{
		Claims: f.claims, Registry: f.svc, WorkerCA: f.caCert, TrustDomain: "example.org", Clock: f.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, testprofile.AssignmentsPath, strings.NewReader("{}"))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	// And a non-POST is refused where the route exists.
	rec = httptest.NewRecorder()
	f2 := newMTLSFixture(t)
	h2, _ := server.NewMTLSHandler(server.MTLSConfig{Claims: f2.claims, Assignments: f2.assign, Registry: f2.svc, WorkerCA: f2.caCert, TrustDomain: "example.org"})
	h2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, testprofile.AssignmentsPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", rec.Code)
	}
}

func TestAssignmentServiceNeedsAStore(t *testing.T) {
	if _, err := server.NewAssignmentService(nil); err == nil {
		t.Fatal("a nil store was accepted")
	}
}
