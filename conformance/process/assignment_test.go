package process

// The Control Plane side of assignment delivery (MVP #57) across a real process
// boundary. The signed Lease and WorkOrder are produced by the real PLC-1, LSE-1
// and WOR-1 code into the Control Plane's State Store; the eki-control process
// then serves them over the mTLS Worker listener to the Worker whose
// certificate asks.
//
// This is the TEST PROFILE (/test-profile/v1/assignments), not the Worker
// Protocol (#198). What a pass shows: the stored bytes reach only the Worker
// that holds the Attempt, unchanged, and a revoked Worker gets nothing. It does
// not show that the Worker verifies them; the agent side does that.

import (
	"bytes"
	"context"
	"crypto/x509"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// fetchAssignments posts the assignments request with the given client.
func (e *mtlsEnv) fetchAssignments(t *testing.T, w rawWorker, body []byte) (int, []workerplane.Assignment, string) {
	t.Helper()
	var cert = &w.cert
	if w.identity == "" {
		cert = nil
	}
	status, text := e.post(t, e.rawClient(t, cert), e.workerURL, testprofile.AssignmentsPath, testprofile.ContentType, "", body)
	if status != http.StatusOK {
		return status, nil, text
	}
	list, err := testprofile.DecodeAssignments([]byte(text))
	must(t, err)
	return status, list, text
}

func TestAssignmentsReachOnlyTheWorkerThatHoldsTheAttempt(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	e.startControl(t)

	a := e.rawEnroll(t, &x509.CertificateRequest{})
	b := e.rawEnroll(t, &x509.CertificateRequest{})
	idle := e.rawEnroll(t, &x509.CertificateRequest{})

	now := time.Now().UTC()
	auth := signedchain.New(t, now.Add(-2*time.Hour), 17)
	var placedA, placedB signedchain.Placed
	func() {
		s := openState(t, e.controlDB)
		defer s.Close()
		policy, err := s.LoadCurrentPolicy(context.Background())
		must(t, err)
		placedA = auth.Place(t, s, policy, 1, a.identity, now)
		placedB = auth.Place(t, s, policy, 2, b.identity, now)
	}()

	// A receives exactly the bytes stored for A, and nothing of B.
	status, got, body := e.fetchAssignments(t, a, testprofile.EncodeAssignmentsRequest())
	if status != http.StatusOK || len(got) != 1 {
		t.Fatalf("A: %d %d assignments %s", status, len(got), body)
	}
	if got[0].AttemptID != placedA.AttemptID.String() || got[0].LeaseID != placedA.LeaseID.String() ||
		!bytes.Equal(got[0].LeaseEnvelope, placedA.SignedLease) || !bytes.Equal(got[0].WorkOrderEnvelope, placedA.SignedWorkOrder) {
		t.Fatalf("A did not receive the stored bytes of its own Attempt: %+v", got[0])
	}
	if strings.Contains(body, placedB.AttemptID.String()) || strings.Contains(body, placedB.LeaseID.String()) {
		t.Fatal("A's response names B's Attempt or Lease")
	}

	// Asking again returns the same bytes: nothing was marked delivered, and
	// nothing was re-signed.
	status, again, _ := e.fetchAssignments(t, a, testprofile.EncodeAssignmentsRequest())
	if status != http.StatusOK || len(again) != 1 || !bytes.Equal(again[0].LeaseEnvelope, placedA.SignedLease) || !bytes.Equal(again[0].WorkOrderEnvelope, placedA.SignedWorkOrder) {
		t.Fatalf("second fetch = %d %+v", status, again)
	}

	// B receives B's.
	status, gotB, _ := e.fetchAssignments(t, b, testprofile.EncodeAssignmentsRequest())
	if status != http.StatusOK || len(gotB) != 1 || !bytes.Equal(gotB[0].WorkOrderEnvelope, placedB.SignedWorkOrder) || bytes.Equal(gotB[0].WorkOrderEnvelope, placedA.SignedWorkOrder) {
		t.Fatalf("B = %d %+v", status, gotB)
	}

	// A Worker that holds no Attempt gets an empty list, not someone else's.
	status, none, body := e.fetchAssignments(t, idle, testprofile.EncodeAssignmentsRequest())
	if status != http.StatusOK || len(none) != 0 {
		t.Fatalf("idle Worker = %d %s, want an empty list", status, body)
	}

	// A body that names another Worker is refused, not obeyed.
	status, _, _ = e.fetchAssignments(t, idle, []byte(`{"worker_identity":"`+a.identity+`"}`))
	if status != http.StatusBadRequest {
		t.Fatalf("a request naming another Worker = %d, want 400", status)
	}

	// No certificate: nothing.
	status, _, body = e.fetchAssignments(t, rawWorker{}, testprofile.EncodeAssignmentsRequest())
	if status != http.StatusUnauthorized || strings.Contains(body, placedA.AttemptID.String()) {
		t.Fatalf("no certificate = %d %s, want 401", status, body)
	}

	// A revoked Worker is refused on its next request, with no material.
	e.workerState(t, a.identity, "revoked")
	status, _, body = e.fetchAssignments(t, a, testprofile.EncodeAssignmentsRequest())
	if status != http.StatusUnauthorized || strings.Contains(body, placedA.AttemptID.String()) {
		t.Fatalf("revoked Worker = %d %s, want 401 and no material", status, body)
	}
	// B is unaffected.
	if status, gotB, _ := e.fetchAssignments(t, b, testprofile.EncodeAssignmentsRequest()); status != http.StatusOK || len(gotB) != 1 {
		t.Fatalf("B after A was revoked = %d %+v", status, gotB)
	}
}
