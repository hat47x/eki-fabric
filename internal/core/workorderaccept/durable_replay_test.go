package workorderaccept

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	replaysqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/replayguard/sqlite"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
)

func TestSignedWorkOrderReplayIsRejectedAfterGuardRestart(t *testing.T) {
	fx := newFixture(t)
	path := filepath.Join(t.TempDir(), "worker-nonces.db")
	open := func() (*replaysqlite.Guard, *Service) {
		t.Helper()
		guard, err := replaysqlite.Open(context.Background(), path, fx.service.clock, 1)
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
	req := fx.request(nil)
	bad := req
	bad.Raw = append([]byte(nil), req.Raw...)
	bad.Raw[len(bad.Raw)-1] ^= 1
	if admission, err := service.Accept(context.Background(), bad); err == nil || !admission.IsZero() {
		t.Fatalf("tampered WorkOrder admitted: %v", err)
	}
	if admission, err := service.Accept(context.Background(), req); err != nil || admission.IsZero() {
		t.Fatalf("valid WorkOrder failed after rejected signature: %v", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	guard, service = open()
	if admission, err := service.Accept(context.Background(), req); !errors.Is(err, ErrReplay) || !admission.IsZero() {
		t.Fatalf("restart replay = %v, %v", admission, err)
	}
	other := fx.request(func(cfg *decisionpayload.WorkOrderConfig) { cfg.Nonce = bytes.Repeat([]byte{99}, 16) })
	if admission, err := service.Accept(context.Background(), other); !errors.Is(err, ErrReplayUnknown) || !errors.Is(err, replaysqlite.ErrGuardCapacity) || !admission.IsZero() {
		t.Fatalf("full durable guard = %v, %v", admission, err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	if admission, err := service.Accept(context.Background(), other); !errors.Is(err, ErrFenceUnknown) || !admission.IsZero() {
		t.Fatalf("closed durable guard = %v, %v", admission, err)
	}
}
