// Package grantledgertest is the contract that every durable grant ledger must
// pass. Each adapter calls RunContract from its tests, and an adapter that keeps
// revocations in a location also calls RunDurabilityContract.
package grantledgertest

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/grant"
)

// Ledger is the part of a grant ledger adapter that the contract checks.
type Ledger interface {
	Revoke(ctx context.Context, g grant.Grant, actor grant.Identity, reason grant.ReasonCategory, effectiveAt time.Time) error
	Admit(g grant.Grant, at time.Time, req grant.Request) error
}

// Factory returns a new, empty Ledger for one subtest. Any cleanup the adapter
// needs is registered by the factory itself.
type Factory func(t testing.TB) Ledger

// DurableFactory opens the Ledger stored at location. After closeLedger is
// called, opening the same location again must show every revocation that was
// committed before the close.
type DurableFactory struct {
	Open func(t testing.TB, location string) (ledger Ledger, closeLedger func() error)
}

const (
	provider    grant.Identity = "provider-alpha"
	beneficiary grant.Identity = "beneficiary-research"
)

const day = 24 * time.Hour

var (
	issuedAt     = time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	revokeAt     = issuedAt.Add(10 * day)
	admitReq     = grant.Request{ResourceClass: "gpu-class-a", Percent: 1}
	revokeReason = grant.ReasonCapacityReclaim
)

// RunContract checks the behavior that every Ledger must have. Each subtest uses
// a fresh Ledger.
func RunContract(t *testing.T, newLedger Factory) {
	t.Helper()

	t.Run("first-revocation-is-stored", func(t *testing.T) {
		l := newLedger(t)
		g := mustIssue(t, "policy-a")

		mustRevoke(t, l, g, provider, revokeReason, revokeAt)
		assertAdmit(t, l, g, revokeAt.Add(-time.Nanosecond), nil)
		assertAdmit(t, l, g, revokeAt, grant.ErrGrantNotActive)
	})

	t.Run("second-revocation-of-same-grant-is-refused", func(t *testing.T) {
		l := newLedger(t)
		g := mustIssue(t, "policy-a")

		mustRevoke(t, l, g, provider, revokeReason, revokeAt)
		expectRevoke(t, l, g, provider, grant.ReasonPolicyChange, revokeAt.Add(time.Hour), grant.ErrAlreadyRevoked)
		expectRevoke(t, l, g, provider, revokeReason, revokeAt.Add(-time.Hour), grant.ErrAlreadyRevoked)
		assertAdmit(t, l, g, revokeAt.Add(-time.Nanosecond), nil)
		assertAdmit(t, l, g, revokeAt, grant.ErrGrantNotActive)
	})

	t.Run("identically-issued-grant-shares-the-revocation", func(t *testing.T) {
		l := newLedger(t)
		g := mustIssue(t, "policy-a")
		reissued := mustIssue(t, "policy-a")

		mustRevoke(t, l, g, provider, revokeReason, revokeAt)
		expectRevoke(t, l, reissued, provider, grant.ReasonPolicyChange, issuedAt.Add(time.Hour), grant.ErrAlreadyRevoked)
		assertAdmit(t, l, reissued, revokeAt, grant.ErrGrantNotActive)
	})

	t.Run("effective-before-issuance-is-refused-and-stores-nothing", func(t *testing.T) {
		l := newLedger(t)
		g := mustIssue(t, "policy-a")

		expectRevoke(t, l, g, provider, revokeReason, issuedAt.Add(-time.Second), grant.ErrRevocationBeforeIssue)
		assertAdmit(t, l, g, issuedAt, nil)

		// Nothing was stored, so a valid revocation of the same grant still succeeds.
		mustRevoke(t, l, g, provider, revokeReason, revokeAt)
		assertAdmit(t, l, g, revokeAt, grant.ErrGrantNotActive)
	})

	t.Run("beneficiary-cannot-revoke", func(t *testing.T) {
		l := newLedger(t)
		g := mustIssue(t, "policy-a")

		expectRevoke(t, l, g, beneficiary, revokeReason, revokeAt, grant.ErrRevokerMismatch)
		assertAdmit(t, l, g, revokeAt, nil)
	})

	t.Run("nil-and-zero-grant-are-refused", func(t *testing.T) {
		l := newLedger(t)

		expectRevoke(t, l, grant.Grant{}, provider, revokeReason, revokeAt, grant.ErrNilGrant)
		assertAdmitError(t, l, grant.Grant{}, revokeAt, grant.ErrNilGrant)
	})

	t.Run("admit-refuses-at-or-after-effective-and-admits-before", func(t *testing.T) {
		l := newLedger(t)
		g := mustIssue(t, "policy-a")

		assertAdmit(t, l, g, issuedAt, nil)
		mustRevoke(t, l, g, provider, revokeReason, revokeAt)
		assertAdmit(t, l, g, issuedAt, nil)
		assertAdmit(t, l, g, revokeAt.Add(-time.Nanosecond), nil)
		assertAdmit(t, l, g, revokeAt, grant.ErrGrantNotActive)
		assertAdmit(t, l, g, revokeAt.Add(time.Hour), grant.ErrGrantNotActive)
	})
}

// RunDurabilityContract checks that committed revocations survive a close and a
// reopen of the same location, and that concurrent revocations of one grant have
// exactly one winner.
func RunDurabilityContract(t *testing.T, factory DurableFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableFactory.Open is nil")
	}

	t.Run("revocations-survive-close-and-reopen", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "grant-ledger.db")
		g := mustIssue(t, "policy-a")
		reissued := mustIssue(t, "policy-a")
		other := mustIssue(t, "policy-b")

		l, closeL := factory.Open(t, location)
		mustRevoke(t, l, g, provider, revokeReason, revokeAt)
		if err := closeL(); err != nil {
			t.Fatalf("close first ledger: %v", err)
		}

		reopened, closeReopened := factory.Open(t, location)
		t.Cleanup(func() {
			if err := closeReopened(); err != nil {
				t.Errorf("close reopened ledger: %v", err)
			}
		})

		assertAdmit(t, reopened, g, revokeAt.Add(-time.Nanosecond), nil)
		assertAdmit(t, reopened, g, revokeAt, grant.ErrGrantNotActive)
		assertAdmit(t, reopened, reissued, revokeAt.Add(time.Hour), grant.ErrGrantNotActive)
		assertAdmit(t, reopened, other, revokeAt.Add(time.Hour), nil)
		expectRevoke(t, reopened, g, provider, revokeReason, revokeAt.Add(time.Hour), grant.ErrAlreadyRevoked)
		expectRevoke(t, reopened, reissued, provider, grant.ReasonPolicyChange, revokeAt.Add(2*time.Hour), grant.ErrAlreadyRevoked)
	})

	t.Run("concurrent-revoke-of-one-grant-has-one-winner", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "grant-ledger.db")
		l, closeL := factory.Open(t, location)
		copies := []grant.Grant{mustIssue(t, "policy-race"), mustIssue(t, "policy-race")}
		const racers = 16

		// Each racer attempts a different effective time, so the stored time can
		// be checked against the winner.
		results := raceRevoke(t, racers, func(i int) (Ledger, grant.Grant, time.Time) {
			return l, copies[i%2], revokeAt.Add(time.Duration(i) * time.Minute)
		})
		winner := singleWinner(t, results)
		if err := closeL(); err != nil {
			t.Fatalf("close ledger: %v", err)
		}

		reopened, closeReopened := factory.Open(t, location)
		t.Cleanup(func() {
			if err := closeReopened(); err != nil {
				t.Errorf("close reopened ledger: %v", err)
			}
		})
		winnerAt := revokeAt.Add(time.Duration(winner) * time.Minute)
		assertAdmit(t, reopened, copies[0], winnerAt.Add(-time.Nanosecond), nil)
		assertAdmit(t, reopened, copies[1], winnerAt, grant.ErrGrantNotActive)
	})

	t.Run("concurrent-revoke-across-handles-has-one-winner", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "grant-ledger.db")
		first, closeFirst := factory.Open(t, location)
		second, closeSecond := factory.Open(t, location)
		t.Cleanup(func() {
			if err := closeFirst(); err != nil {
				t.Errorf("close first handle: %v", err)
			}
			if err := closeSecond(); err != nil {
				t.Errorf("close second handle: %v", err)
			}
		})
		g := mustIssue(t, "policy-race")
		handles := []Ledger{first, second}
		const racers = 16

		results := raceRevoke(t, racers, func(i int) (Ledger, grant.Grant, time.Time) {
			return handles[i%2], g, revokeAt.Add(time.Duration(i) * time.Minute)
		})
		winner := singleWinner(t, results)
		winnerAt := revokeAt.Add(time.Duration(winner) * time.Minute)

		// A handle that lost a race rebuilds its state from the stored rows, so
		// both handles now agree on the winning revocation.
		assertAdmit(t, first, g, winnerAt.Add(-time.Nanosecond), nil)
		assertAdmit(t, first, g, winnerAt, grant.ErrGrantNotActive)
		assertAdmit(t, second, g, winnerAt.Add(-time.Nanosecond), nil)
		assertAdmit(t, second, g, winnerAt, grant.ErrGrantNotActive)
	})
}

// raceRevoke starts racers goroutines that call Revoke at the same moment and
// returns each call's error, indexed by racer.
func raceRevoke(t *testing.T, racers int, attempt func(i int) (Ledger, grant.Grant, time.Time)) []error {
	t.Helper()
	start := make(chan struct{})
	results := make([]error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, g, at := attempt(i)
			<-start
			results[i] = l.Revoke(context.Background(), g, provider, revokeReason, at)
		}()
	}
	close(start)
	wg.Wait()
	return results
}

// singleWinner checks that exactly one racer succeeded and that every other racer
// was refused with ErrAlreadyRevoked. It returns the winner's index.
func singleWinner(t *testing.T, results []error) int {
	t.Helper()
	winner := -1
	for i, err := range results {
		switch {
		case err == nil:
			if winner >= 0 {
				t.Fatalf("racers %d and %d both revoked the grant", winner, i)
			}
			winner = i
		case errors.Is(err, grant.ErrAlreadyRevoked):
		default:
			t.Errorf("racer %d error = %v, want nil or ErrAlreadyRevoked", i, err)
		}
	}
	if winner < 0 {
		t.Fatal("no racer revoked the grant")
	}
	return winner
}

// mustIssue returns a grant for the fixed fixture. Two calls with the same policy
// label return grants with the same digest.
func mustIssue(t testing.TB, policy string) grant.Grant {
	t.Helper()
	spec := grant.Spec{
		Provider:    provider,
		Beneficiary: beneficiary,
		Ceilings: []grant.ResourceCeiling{
			{Class: "gpu-class-a", Percent: 40},
			{Class: "cpu-class-b", Percent: 25},
		},
		IssuedAt:            issuedAt,
		ExpiresAt:           issuedAt.Add(30 * day),
		MaxConcurrentLeases: 4,
		Purpose:             grant.PurposePublicResearch,
		PolicyRevision:      contract.SHA256([]byte(policy)),
	}
	g, err := grant.Issue(provider, spec)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	return g
}

func mustRevoke(t testing.TB, l Ledger, g grant.Grant, actor grant.Identity, reason grant.ReasonCategory, at time.Time) {
	t.Helper()
	if err := l.Revoke(context.Background(), g, actor, reason, at); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
}

func expectRevoke(t testing.TB, l Ledger, g grant.Grant, actor grant.Identity, reason grant.ReasonCategory, at time.Time, want error) {
	t.Helper()
	expectError(t, l.Revoke(context.Background(), g, actor, reason, at), want)
}

func assertAdmit(t testing.TB, l Ledger, g grant.Grant, at time.Time, want error) {
	t.Helper()
	if want == nil {
		if err := l.Admit(g, at, admitReq); err != nil {
			t.Fatalf("Admit(%s) error = %v, want nil", at.Format(time.RFC3339Nano), err)
		}
		return
	}
	assertAdmitError(t, l, g, at, want)
}

func assertAdmitError(t testing.TB, l Ledger, g grant.Grant, at time.Time, want error) {
	t.Helper()
	expectError(t, l.Admit(g, at, admitReq), want)
}

func expectError(t testing.TB, err, want error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		return
	}
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
