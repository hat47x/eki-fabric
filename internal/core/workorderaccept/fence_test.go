package workorderaccept

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	replaysqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/replayguard/sqlite"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// newAttempt is a second Attempt of the fixture's Task: the Attempt a
// re-placement issues with a higher fence epoch.
var newAttempt = ids.AttemptID("atm_00000000000000000000000000000002")

// superseded builds the WorkOrder of the older Attempt: signed, correct, and
// inside its Lease window, but behind a fence the Worker already accepted.
func (f *fixture) superseded() Request {
	f.t.Helper()
	req := f.request(func(cfg *decisionpayload.WorkOrderConfig) {
		cfg.DecisionID = "wor-decision-002"
		cfg.WorkOrderID = "work-order-002"
		cfg.AttemptID = newAttempt
		cfg.Nonce = bytes.Repeat([]byte{0x5b}, 16)
	})
	req.Lease.AttemptID = newAttempt
	req.Lease.FenceEpoch = f.lease.FenceEpoch - 1
	req.Placement.AttemptID = newAttempt
	return req
}

func TestASupersededFenceIsRefusedAndKeepsTheNonceUnspent(t *testing.T) {
	fx := newFixture(t)
	if admission, err := fx.service.Accept(context.Background(), fx.request(nil)); err != nil || admission.IsZero() {
		t.Fatalf("accept = %v, %v", admission, err)
	}
	task := fx.lease.TaskID.String()
	if got := fx.guard.fences[task]; got != fx.lease.FenceEpoch {
		t.Fatalf("fence record = %d, want %d", got, fx.lease.FenceEpoch)
	}
	spent := len(fx.guard.claimed)

	// The stale Attempt arrives while its Lease is still valid: the fence
	// refuses it, and the refusal claims nothing.
	if admission, err := fx.service.Accept(context.Background(), fx.superseded()); !errors.Is(err, ErrSuperseded) || !admission.IsZero() {
		t.Fatalf("stale fence = %v, %v", admission, err)
	}
	if len(fx.guard.claimed) != spent {
		t.Fatalf("the refusal spent a nonce: %d claims", len(fx.guard.claimed))
	}

	// A re-issued WorkOrder for the accepted Attempt keeps its fence and is
	// accepted with a fresh nonce.
	reissued := fx.request(func(cfg *decisionpayload.WorkOrderConfig) {
		cfg.DecisionID = "wor-decision-003"
		cfg.Nonce = bytes.Repeat([]byte{0x5c}, 16)
	})
	if admission, err := fx.service.Accept(context.Background(), reissued); err != nil || admission.IsZero() {
		t.Fatalf("re-issue = %v, %v", admission, err)
	}

	// A further re-placement of the same Task advances the fence.
	replaced := fx.request(func(cfg *decisionpayload.WorkOrderConfig) {
		cfg.DecisionID = "wor-decision-004"
		cfg.WorkOrderID = "work-order-004"
		cfg.AttemptID = newAttempt
		cfg.Nonce = bytes.Repeat([]byte{0x5d}, 16)
	})
	replaced.Lease.AttemptID = newAttempt
	replaced.Lease.FenceEpoch = fx.lease.FenceEpoch + 1
	replaced.Placement.AttemptID = newAttempt
	if admission, err := fx.service.Accept(context.Background(), replaced); err != nil || admission.IsZero() {
		t.Fatalf("replacement = %v, %v", admission, err)
	}
	if got := fx.guard.fences[task]; got != fx.lease.FenceEpoch+1 {
		t.Fatalf("fence record = %d, want %d", got, fx.lease.FenceEpoch+1)
	}
	// The stale Attempt stays refused after the fence moved on.
	if admission, err := fx.service.Accept(context.Background(), fx.superseded()); !errors.Is(err, ErrSuperseded) || !admission.IsZero() {
		t.Fatalf("stale fence after replacement = %v, %v", admission, err)
	}
}

func TestPreviewLeavesTheFenceAndTheNonceUnclaimed(t *testing.T) {
	fx := newFixture(t)
	if admission, err := fx.service.Preview(context.Background(), fx.request(nil)); err != nil || admission.IsZero() {
		t.Fatalf("preview = %v, %v", admission, err)
	}
	if len(fx.guard.fences) != 0 || len(fx.guard.claimed) != 0 {
		t.Fatalf("preview claimed: fences %v, nonces %v", fx.guard.fences, fx.guard.claimed)
	}
	// Accept after Preview claims both: the preview did not spend anything.
	if admission, err := fx.service.Accept(context.Background(), fx.request(nil)); err != nil || admission.IsZero() {
		t.Fatalf("accept = %v, %v", admission, err)
	}
	if len(fx.guard.fences) != 1 || len(fx.guard.claimed) != 1 {
		t.Fatalf("accept claimed: fences %v, nonces %v", fx.guard.fences, fx.guard.claimed)
	}
}

func TestSupersededFenceStaysRefusedAfterGuardRestart(t *testing.T) {
	fx := newFixture(t)
	path := filepath.Join(t.TempDir(), "worker-nonces.db")
	open := func() (*replaysqlite.Guard, *Service) {
		t.Helper()
		guard, err := replaysqlite.Open(context.Background(), path, fx.service.clock, 8)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = guard.Close() })
		service, err := New(Config{Verifier: fx.service.verifier, Replay: guard, Clock: fx.service.clock})
		if err != nil {
			t.Fatal(err)
		}
		return guard, service
	}
	guard, service := open()
	if admission, err := service.Accept(context.Background(), fx.request(nil)); err != nil || admission.IsZero() {
		t.Fatalf("accept = %v, %v", admission, err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	guard, service = open()
	// The fence record survives the restart: the older Attempt is refused
	// without ever having had its nonce claimed.
	if admission, err := service.Accept(context.Background(), fx.superseded()); !errors.Is(err, ErrSuperseded) || !admission.IsZero() {
		t.Fatalf("stale fence after restart = %v, %v", admission, err)
	}
	if admission, err := service.Accept(context.Background(), fx.request(nil)); !errors.Is(err, ErrReplay) || !admission.IsZero() {
		t.Fatalf("replay after restart = %v, %v", admission, err)
	}
}
