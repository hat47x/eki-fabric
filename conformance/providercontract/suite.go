// Package providercontract is the shared contract that every resource provider
// adapter must pass. An adapter's test calls RunContract with a Factory that
// returns a fresh fixture. The contract checks allocation, event reporting, the
// declared retry behaviour, and the five rules of package resourceprovider.
//
// The suite runs against in-memory fixtures only (see the fixture subpackage). A
// pass shows that an adapter follows the contract under simulation. It does not
// show that a real Kubernetes or Slurm installation behaves the same way. Issue #67
// stays open until the adapter runs against real clusters.
package providercontract

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resourceprovider"
)

// Simulator drives the underlying system that a fixture stands for. A real adapter
// cannot be driven this way, so only fixtures implement it.
type Simulator interface {
	CompleteAttempt(lease ids.LeaseID) error
	FailAttempt(lease ids.LeaseID) error
	PreemptAttempt(lease ids.LeaseID) error
}

// Fixture is the adapter under test, together with the means to drive its underlying system.
type Fixture struct {
	Adapter resourceprovider.ProviderAdapter
	Sim     Simulator
}

// Factory returns a fresh, empty fixture for one subtest.
type Factory func(t testing.TB) Fixture

// RunContract checks the behaviour that every ProviderAdapter must have. Each
// subtest uses a fresh fixture.
func RunContract(t *testing.T, newFixture Factory) {
	t.Helper()

	t.Run("declaration-is-valid", func(t *testing.T) {
		f := newFixture(t)
		if err := resourceprovider.ValidateDeclaration(f.Adapter.Declare()); err != nil {
			t.Fatalf("declaration is not valid: %v", err)
		}
	})

	t.Run("declaration-is-stable", func(t *testing.T) {
		f := newFixture(t)
		if !reflect.DeepEqual(f.Adapter.Declare(), f.Adapter.Declare()) {
			t.Fatal("Declare() returned different values on two calls")
		}
	})

	t.Run("allocate-returns-requested-slots", func(t *testing.T) {
		f := newFixture(t)
		lease := mustLease(t)
		alloc, err := f.Adapter.Allocate(context.Background(), resourceprovider.AllocationRequest{
			LeaseID: lease,
			Slots:   3,
			Retry:   resourceprovider.RetryProviderDefault,
		})
		if err != nil {
			t.Fatalf("Allocate() error = %v", err)
		}
		if alloc.LeaseID != lease {
			t.Fatalf("allocation lease = %q, want %q", alloc.LeaseID, lease)
		}
		if len(alloc.Slots) != 3 {
			t.Fatalf("allocated %d slots, want 3", len(alloc.Slots))
		}
		seen := make(map[resourceprovider.SlotRef]bool)
		for _, s := range alloc.Slots {
			if s == "" || seen[s] {
				t.Fatalf("slot %q is empty or repeated", s)
			}
			seen[s] = true
		}
	})

	t.Run("allocate-rejects-invalid-request", func(t *testing.T) {
		good := mustLease(t)
		cases := []struct {
			name string
			req  resourceprovider.AllocationRequest
		}{
			{name: "malformed-lease", req: resourceprovider.AllocationRequest{LeaseID: ids.LeaseID("not-a-lease"), Slots: 1, Retry: resourceprovider.RetryProviderDefault}},
			{name: "zero-slots", req: resourceprovider.AllocationRequest{LeaseID: good, Slots: 0, Retry: resourceprovider.RetryProviderDefault}},
			{name: "too-many-slots", req: resourceprovider.AllocationRequest{LeaseID: good, Slots: resourceprovider.MaxSlotsPerAllocation + 1, Retry: resourceprovider.RetryProviderDefault}},
			{name: "unknown-retry-setting", req: resourceprovider.AllocationRequest{LeaseID: good, Slots: 1, Retry: "maybe"}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := newFixture(t)
				_, err := f.Adapter.Allocate(context.Background(), tc.req)
				if !errors.Is(err, resourceprovider.ErrInvalidRequest) {
					t.Fatalf("Allocate() error = %v, want ErrInvalidRequest", err)
				}
			})
		}
	})

	t.Run("allocate-rejects-duplicate-lease", func(t *testing.T) {
		f := newFixture(t)
		alloc := mustAllocate(t, f, 1, resourceprovider.RetryProviderDefault)
		_, err := f.Adapter.Allocate(context.Background(), resourceprovider.AllocationRequest{
			LeaseID: alloc.LeaseID,
			Slots:   1,
			Retry:   resourceprovider.RetryProviderDefault,
		})
		if !errors.Is(err, resourceprovider.ErrLeaseAlreadyAllocated) {
			t.Fatalf("second Allocate() error = %v, want ErrLeaseAlreadyAllocated", err)
		}
	})

	t.Run("allocate-honours-cancelled-context", func(t *testing.T) {
		f := newFixture(t)
		lease := mustLease(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := f.Adapter.Allocate(ctx, resourceprovider.AllocationRequest{
			LeaseID: lease,
			Slots:   1,
			Retry:   resourceprovider.RetryProviderDefault,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Allocate() error = %v, want context.Canceled", err)
		}
		if _, err := f.Adapter.Events(context.Background(), lease, 0); !errors.Is(err, resourceprovider.ErrUnknownLease) {
			t.Fatalf("Events() after cancelled allocate error = %v, want ErrUnknownLease", err)
		}
	})

	t.Run("events-are-lease-scoped-and-ordered", func(t *testing.T) {
		f := newFixture(t)
		a := mustAllocate(t, f, 1, resourceprovider.RetryProviderDefault)
		b := mustAllocate(t, f, 1, resourceprovider.RetryProviderDefault)

		if err := f.Sim.CompleteAttempt(a.LeaseID); err != nil {
			t.Fatalf("CompleteAttempt() error = %v", err)
		}

		evA := mustEvents(t, f, a.LeaseID, 0)
		wantA := []resourceprovider.EventKind{resourceprovider.EventStarted, resourceprovider.EventCompleted}
		checkSequence(t, evA, a.LeaseID, wantA)

		evB := mustEvents(t, f, b.LeaseID, 0)
		checkSequence(t, evB, b.LeaseID, []resourceprovider.EventKind{resourceprovider.EventStarted})

		resumed := mustEvents(t, f, a.LeaseID, 1)
		checkSequence(t, resumed, a.LeaseID, []resourceprovider.EventKind{resourceprovider.EventCompleted})
		if got := mustEvents(t, f, a.LeaseID, 2); len(got) != 0 {
			t.Fatalf("events after the last sequence = %d, want 0", len(got))
		}
	})

	t.Run("completion-is-a-claim-not-a-result", func(t *testing.T) {
		f := newFixture(t)
		alloc := mustAllocate(t, f, 1, resourceprovider.RetryProviderDefault)
		if err := f.Sim.CompleteAttempt(alloc.LeaseID); err != nil {
			t.Fatalf("CompleteAttempt() error = %v", err)
		}
		evs := mustEvents(t, f, alloc.LeaseID, 0)
		last := evs[len(evs)-1]

		claim, ok := resourceprovider.ClaimFromEvent(last)
		if !ok || claim.Outcome != resourceprovider.ClaimSucceeded {
			t.Fatalf("ClaimFromEvent(completed) = %+v, %v, want succeeded claim", claim, ok)
		}
		d, err := resourceprovider.Classify(last.Kind)
		if err != nil {
			t.Fatalf("Classify() error = %v", err)
		}
		if d != resourceprovider.DispositionClaimed {
			t.Fatalf("disposition = %q, want %q", d, resourceprovider.DispositionClaimed)
		}
	})

	t.Run("preemption-is-a-reclaim-not-a-failure", func(t *testing.T) {
		f := newFixture(t)
		alloc := mustAllocate(t, f, 1, resourceprovider.RetryProviderDefault)
		if err := f.Sim.PreemptAttempt(alloc.LeaseID); err != nil {
			t.Fatalf("PreemptAttempt() error = %v", err)
		}
		evs := mustEvents(t, f, alloc.LeaseID, 0)
		if !hasKind(evs, resourceprovider.EventPreempted) {
			t.Fatal("preemption was not reported as a preempted event")
		}
		if hasKind(evs, resourceprovider.EventFailed) {
			t.Fatal("preemption was reported as a failed event")
		}
		d, err := resourceprovider.Classify(resourceprovider.EventPreempted)
		if err != nil {
			t.Fatalf("Classify() error = %v", err)
		}
		if d != resourceprovider.DispositionReclaim || d.CountsAsAttemptFailure() {
			t.Fatalf("preempted disposition = %q, want reclaim that is not an attempt failure", d)
		}
	})

	t.Run("declared-default-retry-matches-observed", func(t *testing.T) {
		f := newFixture(t)
		decl := f.Adapter.Declare()
		alloc := mustAllocate(t, f, 1, resourceprovider.RetryProviderDefault)
		if err := f.Sim.FailAttempt(alloc.LeaseID); err != nil {
			t.Fatalf("FailAttempt() error = %v", err)
		}
		evs := mustEvents(t, f, alloc.LeaseID, 0)
		requeued := hasKind(evs, resourceprovider.EventRequeued)
		failed := hasKind(evs, resourceprovider.EventFailed)

		if decl.Retry.OnByDefault {
			if !requeued || failed {
				t.Fatalf("declared on by default, but a failure was not retried inside the attempt (requeued=%v failed=%v)", requeued, failed)
			}
			return
		}
		if requeued || !failed {
			t.Fatalf("declared off by default, but a failure was retried (requeued=%v failed=%v)", requeued, failed)
		}
	})

	t.Run("disable-per-submission-is-honoured-or-refused", func(t *testing.T) {
		f := newFixture(t)
		decl := f.Adapter.Declare()
		if decl.Retry.DisableablePerSubmission {
			alloc := mustAllocate(t, f, 1, resourceprovider.RetryDisabled)
			if err := f.Sim.FailAttempt(alloc.LeaseID); err != nil {
				t.Fatalf("FailAttempt() error = %v", err)
			}
			evs := mustEvents(t, f, alloc.LeaseID, 0)
			if hasKind(evs, resourceprovider.EventRequeued) {
				t.Fatal("retry was disabled for the submission, but the failure was retried")
			}
			if !hasKind(evs, resourceprovider.EventFailed) {
				t.Fatal("retry was disabled, and the failure was not reported as failed")
			}
			return
		}

		lease := mustLease(t)
		_, err := f.Adapter.Allocate(context.Background(), resourceprovider.AllocationRequest{
			LeaseID: lease,
			Slots:   1,
			Retry:   resourceprovider.RetryDisabled,
		})
		if !errors.Is(err, resourceprovider.ErrRetryNotDisableable) {
			t.Fatalf("Allocate(RetryDisabled) error = %v, want ErrRetryNotDisableable", err)
		}
		if _, err := f.Adapter.Events(context.Background(), lease, 0); !errors.Is(err, resourceprovider.ErrUnknownLease) {
			t.Fatalf("Events() after refused allocate error = %v, want ErrUnknownLease", err)
		}
	})

	t.Run("placement-rule-a-matches-declared-retry", func(t *testing.T) {
		f := newFixture(t)
		decl := f.Adapter.Declare()

		sensitive := []resourceprovider.TaskProfile{
			{SideEffect: true, IdempotencyBasis: true},
			{SideEffect: true},
			{IdempotencyBasis: false},
		}
		for _, task := range sensitive {
			setting, err := resourceprovider.PlaceWithRetryRule(task, decl)
			if !decl.Retry.DisableablePerSubmission {
				if !errors.Is(err, resourceprovider.ErrRetryNotDisableable) {
					t.Fatalf("PlaceWithRetryRule(%+v) error = %v, want ErrRetryNotDisableable", task, err)
				}
				continue
			}
			if err != nil || setting != resourceprovider.RetryDisabled {
				t.Fatalf("PlaceWithRetryRule(%+v) = %q, %v, want disabled", task, setting, err)
			}
			// The setting that placement returns must be accepted by the adapter.
			if _, err := f.Adapter.Allocate(context.Background(), resourceprovider.AllocationRequest{
				LeaseID: mustLease(t),
				Slots:   1,
				Retry:   setting,
			}); err != nil {
				t.Fatalf("Allocate() with placement setting error = %v", err)
			}
		}

		setting, err := resourceprovider.PlaceWithRetryRule(resourceprovider.TaskProfile{IdempotencyBasis: true}, decl)
		if err != nil || setting != resourceprovider.RetryProviderDefault {
			t.Fatalf("PlaceWithRetryRule(idempotent) = %q, %v, want provider default", setting, err)
		}
	})

	t.Run("managed-declaration-does-not-raise-assurance", func(t *testing.T) {
		f := newFixture(t)
		decl := f.Adapter.Declare()

		if resourceprovider.ManagedForPlacement(decl, resourceprovider.AdministratorPolicy{}) {
			t.Fatal("a declaration alone made the provider managed for placement")
		}
		named, err := resourceprovider.NewAdministratorPolicy(decl.Provider)
		if err != nil {
			t.Fatalf("NewAdministratorPolicy() error = %v", err)
		}
		if !resourceprovider.ManagedForPlacement(decl, named) {
			t.Fatal("an administrator policy naming the provider did not make it managed")
		}
		other, err := resourceprovider.NewAdministratorPolicy("some-other-provider")
		if err != nil {
			t.Fatalf("NewAdministratorPolicy() error = %v", err)
		}
		if resourceprovider.ManagedForPlacement(decl, other) {
			t.Fatal("a policy naming another provider made this provider managed")
		}
	})

	t.Run("credential-is-scoped-to-one-provider", func(t *testing.T) {
		f := newFixture(t)
		decl := f.Adapter.Declare()

		if err := resourceprovider.ValidateCredentialScope(decl.Credential, decl.Provider); err != nil {
			t.Fatalf("declared credential is not scoped to its provider: %v", err)
		}
		if err := resourceprovider.ValidateCredentialScope(decl.Credential, "some-other-provider"); !errors.Is(err, resourceprovider.ErrCredentialScope) {
			t.Fatalf("credential accepted for another provider, error = %v", err)
		}
		wide := decl.Credential
		wide.ClusterWide = true
		if err := resourceprovider.ValidateCredentialScope(wide, decl.Provider); !errors.Is(err, resourceprovider.ErrCredentialScope) {
			t.Fatalf("cluster-wide credential accepted, error = %v", err)
		}
	})
}

func mustLease(t testing.TB) ids.LeaseID {
	t.Helper()
	lease, err := ids.NewLeaseID()
	if err != nil {
		t.Fatalf("NewLeaseID() error = %v", err)
	}
	return lease
}

func mustAllocate(t testing.TB, f Fixture, slots int, retry resourceprovider.RetrySetting) resourceprovider.Allocation {
	t.Helper()
	alloc, err := f.Adapter.Allocate(context.Background(), resourceprovider.AllocationRequest{
		LeaseID: mustLease(t),
		Slots:   slots,
		Retry:   retry,
	})
	if err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}
	return alloc
}

func mustEvents(t testing.TB, f Fixture, lease ids.LeaseID, after uint64) []resourceprovider.Event {
	t.Helper()
	evs, err := f.Adapter.Events(context.Background(), lease, after)
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	return evs
}

// checkSequence verifies that evs belongs to lease, that the sequence numbers are
// contiguous and start after the cursor, and that the kinds match want in order.
func checkSequence(t testing.TB, evs []resourceprovider.Event, lease ids.LeaseID, want []resourceprovider.EventKind) {
	t.Helper()
	if len(evs) != len(want) {
		t.Fatalf("got %d events, want %d", len(evs), len(want))
	}
	for i, ev := range evs {
		if ev.LeaseID != lease {
			t.Fatalf("event %d belongs to lease %q, want %q", i, ev.LeaseID, lease)
		}
		if ev.Kind != want[i] {
			t.Fatalf("event %d kind = %q, want %q", i, ev.Kind, want[i])
		}
		if i > 0 && ev.Sequence != evs[i-1].Sequence+1 {
			t.Fatalf("event %d sequence = %d, not contiguous after %d", i, ev.Sequence, evs[i-1].Sequence)
		}
	}
}

func hasKind(evs []resourceprovider.Event, kind resourceprovider.EventKind) bool {
	for _, ev := range evs {
		if ev.Kind == kind {
			return true
		}
	}
	return false
}
