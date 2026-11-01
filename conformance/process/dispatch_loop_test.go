package process

// The dispatch loop (#242) across a real process boundary: an eki-control
// process with the placement loop enabled moves a READY Task to an ASSIGNED
// Attempt against the enrolled Worker, and the Worker then fetches the signed
// Lease and WorkOrder over the mTLS Worker listener.
//
// This is the TEST PROFILE fetch, not the Worker Protocol (#198). A pass here
// shows that the loop runs inside the real binary, signs with the real PLC-1,
// LSE-1 and WOR-1 issuers, and that the issued material reaches the Worker it
// was issued to. It does not show that the Worker verifies them; the agent
// side does that.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/conformance/signedchain"
	"github.com/hat47x/eki-fabric/internal/control/service"
	"github.com/hat47x/eki-fabric/internal/control/signingkeys"
	"github.com/hat47x/eki-fabric/internal/core/attemptstate"
	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// writeSigningKeys writes the Control Plane's own signing keys file: one key
// per signing role, as an operator's key ceremony would produce.
func (e *mtlsEnv) writeSigningKeys(t *testing.T) string {
	t.Helper()
	path := e.dir + "/control-signing-keys.json"
	notBefore := time.Now().UTC().Add(-time.Minute)
	notAfter := notBefore.Add(24 * time.Hour)
	doc := signingkeys.File{TrustDomain: trustDomain}
	for _, role := range []authority.Role{authority.RolePlacementAuthorization, authority.RoleLease, authority.RoleWorkOrder} {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		must(t, err)
		doc.Keys = append(doc.Keys, signingkeys.Key{
			KeyID:            "key-" + string(role),
			Role:             string(role),
			Principal:        "control@" + trustDomain,
			Algorithm:        "EdDSA",
			PrivateKey:       base64.StdEncoding.EncodeToString(priv.Seed()),
			PublicKey:        base64.StdEncoding.EncodeToString(pub),
			SigningNotBefore: notBefore,
			SigningNotAfter:  notAfter,
			KeyPolicy:        "kp-test-1",
		})
	}
	raw, err := json.Marshal(doc)
	must(t, err)
	must(t, os.WriteFile(path, raw, 0o600))
	return path
}

// enableDispatch adds the placement loop to the config already written by
// writeConfig. It must run after writeConfig and before startControl.
func (e *mtlsEnv) enableDispatch(t *testing.T, keyFile string) {
	t.Helper()
	raw, err := os.ReadFile(e.cfgPath)
	must(t, err)
	var cfg service.Config
	must(t, json.Unmarshal(raw, &cfg))
	cfg.Dispatch = &service.DispatchConfig{
		SigningKeysFile:    keyFile,
		TrustDomain:        trustDomain,
		PlacementPrincipal: "control@" + trustDomain,
		LeasePrincipal:     "control@" + trustDomain,
		WorkOrderPrincipal: "control@" + trustDomain,
		KeyPolicyRevision:  "kp-test-1",
		Interval:           100 * time.Millisecond,
		LeaseLifetime:      time.Minute,
		PlacementValidity:  10 * time.Minute,
		ExecutorApproval: &service.ExecutorApprovalConfig{
			DecisionID:     "executor-approval-1",
			PayloadDigest:  contract.SHA256([]byte("executor approval payload")).Hex(),
			ExecutorDigest: contract.SHA256([]byte("registered executor")).Hex(),
		},
		Limits:  &signedchain.Limits,
		Sandbox: &signedchain.Sandbox,
	}
	raw, err = json.Marshal(cfg)
	must(t, err)
	must(t, os.WriteFile(e.cfgPath, raw, 0o600))
}

// seedReadyTask plants a READY Task straight into the State Store, the same
// state the Admission boundary leaves Tasks in.
func (e *mtlsEnv) seedReadyTask(t *testing.T) ids.TaskID {
	t.Helper()
	s := openState(t, e.controlDB)
	defer s.Close()
	id, err := ids.NewTaskID()
	must(t, err)
	task, err := taskstate.NewTask(id, contract.SHA256([]byte("task-ir")), time.Now().UTC().Add(-time.Minute))
	must(t, err)
	task.Phase = taskstate.TaskPhaseReady
	must(t, s.CreateTask(context.Background(), task))
	return id
}

// assignedAttempt polls the State Store until an Attempt of the Task is
// ASSIGNED, and returns it.
func (e *mtlsEnv) assignedAttempt(t *testing.T, taskID ids.TaskID) attemptstate.AttemptRecord {
	t.Helper()
	deadline := time.Now().Add(pollDeadline)
	for time.Now().Before(deadline) {
		s := openState(t, e.controlDB)
		attempts, err := s.ListAttemptsByTask(context.Background(), taskID)
		if err == nil {
			for _, a := range attempts {
				if a.Phase == attemptstate.AttemptPhaseDispatched {
					s.Close()
					return a
				}
			}
		}
		s.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no ASSIGNED Attempt appeared for %s within the deadline", taskID)
	return attemptstate.AttemptRecord{}
}

// waitForAssignments polls the TEST PROFILE fetch until the Worker can see at
// least want Assignments, and returns them.
//
// The dispatch loop commits the Attempt as ASSIGNED before it emits the signed
// WorkOrder, and the listing only carries an Attempt once both the Lease and
// the WorkOrder are EMITTED. A single fetch therefore races the loop; the
// Worker's own fetch loop polls for the same reason.
func (e *mtlsEnv) waitForAssignments(t *testing.T, w rawWorker, want int) []workerplane.Assignment {
	t.Helper()
	deadline := time.Now().Add(pollDeadline)
	var (
		status int
		body   string
		got    []workerplane.Assignment
	)
	for time.Now().Before(deadline) {
		status, got, body = e.fetchAssignments(t, w, testprofile.EncodeAssignmentsRequest(nil))
		if status == http.StatusOK && len(got) >= want {
			return got
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("fetch = %d %d assignments %s within the deadline", status, len(got), body)
	return nil
}

func TestDispatchLoopAssignsAReadyTaskAndTheWorkerFetchesItsLease(t *testing.T) {
	e := newMTLSEnv(t, 0)
	e.seedPolicyNow(t)
	e.writeConfig(t)
	keyFile := e.writeSigningKeys(t)
	e.enableDispatch(t, keyFile)
	taskID := e.seedReadyTask(t)

	e.startControl(t)

	// The Worker enrolls while the loop is already running: until then the
	// loop defers the Task, it does not fail it.
	w := e.rawEnroll(t, &x509.CertificateRequest{})

	attempt := e.assignedAttempt(t, taskID)
	if attempt.WorkerIdentity != w.identity {
		t.Fatalf("the Attempt went to %s, want the enrolled Worker %s", attempt.WorkerIdentity, w.identity)
	}
	if attempt.LeaseID == "" {
		t.Fatal("the ASSIGNED Attempt carries no Lease")
	}

	// The Worker the loop chose can fetch the Lease and WorkOrder issued for
	// its Attempt. The loop commits the Attempt as ASSIGNED before it emits
	// the WorkOrder, so the first fetch can legitimately come back empty;
	// poll like the Worker's own fetch loop does.
	got := e.waitForAssignments(t, w, 1)
	if got[0].AttemptID != attempt.ID.String() || got[0].LeaseID != attempt.LeaseID.String() {
		t.Fatalf("the Assignment names %s/%s, want the Attempt and Lease the loop issued", got[0].AttemptID, got[0].LeaseID)
	}
	if len(got[0].LeaseEnvelope) == 0 || len(got[0].WorkOrderEnvelope) == 0 {
		t.Fatal("the Assignment carries no signed Lease or WorkOrder")
	}

	// A Worker that holds no Attempt still gets nothing.
	other := e.rawEnroll(t, &x509.CertificateRequest{})
	status, none, _ := e.fetchAssignments(t, other, testprofile.EncodeAssignmentsRequest(nil))
	if status != http.StatusOK || len(none) != 0 {
		t.Fatalf("a Worker without an Attempt = %d %+v, want an empty list", status, none)
	}
}
