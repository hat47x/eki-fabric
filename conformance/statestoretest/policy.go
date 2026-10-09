package statestoretest

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type PolicyFactory func(t testing.TB) statestore.PolicyStore

type DurablePolicyFactory struct {
	Open func(t testing.TB, location string) (store statestore.PolicyStore, close func() error)
}

func RunPolicyStoreContract(t *testing.T, newStore PolicyFactory) {
	t.Helper()

	t.Run("uninitialized-fails-closed", func(t *testing.T) {
		store := newStore(t)
		if _, err := store.LoadCurrentPolicy(context.Background()); !errors.Is(err, statestore.ErrPolicyUnavailable) {
			t.Fatalf("LoadCurrentPolicy() error = %v, want ErrPolicyUnavailable", err)
		}
	})

	t.Run("bootstrap-and-monotonic-cas", func(t *testing.T) {
		store := newStore(t)
		p1 := mustActivePolicy(t, 1, "policy-1", policyNow)
		if err := store.CompareAndSwapPolicy(context.Background(), 0, p1); err != nil {
			t.Fatalf("bootstrap error = %v", err)
		}
		got, err := store.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		assertPolicyEqual(t, got, p1)

		p2 := mustActivePolicy(t, 2, "policy-2", policyNow.Add(time.Minute))
		if err := store.CompareAndSwapPolicy(context.Background(), 0, p2); !errors.Is(err, statestore.ErrPolicyChanged) {
			t.Fatalf("stale CAS error = %v, want ErrPolicyChanged", err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), p1.Epoch, p2); err != nil {
			t.Fatalf("advance policy error = %v", err)
		}
		got, err = store.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		assertPolicyEqual(t, got, p2)
	})

	t.Run("superseded-revision-cannot-return", func(t *testing.T) {
		store := newStore(t)
		p1 := mustActivePolicy(t, 1, "policy-1", policyNow)
		if err := store.CompareAndSwapPolicy(context.Background(), 0, p1); err != nil {
			t.Fatal(err)
		}
		p2 := mustActivePolicy(t, 2, "policy-2", policyNow.Add(time.Minute))
		if err := store.CompareAndSwapPolicy(context.Background(), p1.Epoch, p2); err != nil {
			t.Fatal(err)
		}
		rollback := mustActivePolicy(t, 3, p1.Revision, policyNow.Add(2*time.Minute))
		if err := store.CompareAndSwapPolicy(context.Background(), p2.Epoch, rollback); !errors.Is(err, statestore.ErrPolicyReactivation) {
			t.Fatalf("historical revision rollback error = %v, want ErrPolicyReactivation", err)
		}
		current, err := store.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		assertPolicyEqual(t, current, p2)
	})

	t.Run("revoke-and-no-reactivation", func(t *testing.T) {
		store := newStore(t)
		p1 := mustActivePolicy(t, 1, "policy-1", policyNow)
		if err := store.CompareAndSwapPolicy(context.Background(), 0, p1); err != nil {
			t.Fatal(err)
		}
		revoked, err := policystate.Revoke(p1, 2, policyNow.Add(time.Minute), "emergency revoke")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), p1.Epoch, revoked); err != nil {
			t.Fatalf("revoke error = %v", err)
		}

		reactivated := mustActivePolicy(t, 3, p1.Revision, policyNow.Add(2*time.Minute))
		if err := store.CompareAndSwapPolicy(context.Background(), revoked.Epoch, reactivated); !errors.Is(err, statestore.ErrPolicyReactivation) {
			t.Fatalf("reactivation error = %v, want ErrPolicyReactivation", err)
		}

		p2 := mustActivePolicy(t, 3, "policy-2", policyNow.Add(2*time.Minute))
		if err := store.CompareAndSwapPolicy(context.Background(), revoked.Epoch, p2); err != nil {
			t.Fatalf("activate replacement policy error = %v", err)
		}
	})

	t.Run("cancelled-context", func(t *testing.T) {
		store := newStore(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		p1 := mustActivePolicy(t, 1, "policy-1", policyNow)
		if err := store.CompareAndSwapPolicy(ctx, 0, p1); !errors.Is(err, context.Canceled) {
			t.Fatalf("CompareAndSwapPolicy() error = %v", err)
		}
		if _, err := store.LoadCurrentPolicy(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("LoadCurrentPolicy() error = %v", err)
		}
	})
}

func RunPolicyDurabilityContract(t *testing.T, factory DurablePolicyFactory) {
	t.Helper()
	t.Run("current-policy-survives-restart", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		p1 := mustActivePolicy(t, 1, "policy-1", policyNow)
		if err := store.CompareAndSwapPolicy(context.Background(), 0, p1); err != nil {
			t.Fatal(err)
		}
		revoked, err := policystate.Revoke(p1, 2, policyNow.Add(time.Minute), "revoke")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompareAndSwapPolicy(context.Background(), p1.Epoch, revoked); err != nil {
			t.Fatal(err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer closeReopened()
		got, err := reopened.LoadCurrentPolicy(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		assertPolicyEqual(t, got, revoked)

		p2 := mustActivePolicy(t, 3, "policy-2", policyNow.Add(2*time.Minute))
		if err := reopened.CompareAndSwapPolicy(context.Background(), revoked.Epoch, p2); err != nil {
			t.Fatalf("policy advance after restart error = %v", err)
		}

		rollback := mustActivePolicy(t, 4, p1.Revision, policyNow.Add(3*time.Minute))
		if err := reopened.CompareAndSwapPolicy(context.Background(), p2.Epoch, rollback); !errors.Is(err, statestore.ErrPolicyReactivation) {
			t.Fatalf("historical revision rollback after restart error = %v, want ErrPolicyReactivation", err)
		}
	})
}

var policyNow = time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)

func mustActivePolicy(t testing.TB, epoch uint64, revision string, at time.Time) policystate.State {
	t.Helper()
	state, err := policystate.NewActive(epoch, revision, at)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertPolicyEqual(t testing.TB, got, want policystate.State) {
	t.Helper()
	if got.Epoch != want.Epoch ||
		got.Revision != want.Revision ||
		got.Status != want.Status ||
		!got.ActivatedAt.Equal(want.ActivatedAt) ||
		!got.RevokedAt.Equal(want.RevokedAt) ||
		got.RevokeReason != want.RevokeReason {
		t.Fatalf("PolicyState mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}
