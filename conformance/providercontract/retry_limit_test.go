package providercontract_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/providercontract/fixture"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resourceprovider"
)

// startLease allocates one lease with the provider default retry setting.
func startLease(t *testing.T, m *fixture.Memory) ids.LeaseID {
	t.Helper()
	lease, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Allocate(context.Background(), resourceprovider.AllocationRequest{
		LeaseID: lease,
		Slots:   1,
		Retry:   resourceprovider.RetryProviderDefault,
	}); err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}
	return lease
}

// eventKinds returns the kinds of the lease's events in order.
func eventKinds(t *testing.T, m *fixture.Memory, lease ids.LeaseID) []resourceprovider.EventKind {
	t.Helper()
	evs, err := m.Events(context.Background(), lease, 0)
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	kinds := make([]resourceprovider.EventKind, 0, len(evs))
	for _, ev := range evs {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

// TestKubernetesBackoffLimitZeroGivesNoRetry shows that backoffLimit 0 can be
// expressed. The first failure is then final, with no internal re-execution.
// Whether a real Job with backoffLimit 0 behaves this way is not verified (see the
// fixture's doc comment and the README).
func TestKubernetesBackoffLimitZeroGivesNoRetry(t *testing.T) {
	m, err := fixture.NewKubernetesLikeWithBackoffLimit(0)
	if err != nil {
		t.Fatalf("NewKubernetesLikeWithBackoffLimit(0) error = %v", err)
	}
	lease := startLease(t, m)
	if err := m.FailAttempt(lease); err != nil {
		t.Fatalf("FailAttempt() error = %v", err)
	}

	want := []resourceprovider.EventKind{resourceprovider.EventStarted, resourceprovider.EventFailed}
	if got := eventKinds(t, m, lease); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v (no requeue for backoffLimit 0)", got, want)
	}
	if retries, err := m.Retries(lease); err != nil || retries != 0 {
		t.Fatalf("Retries() = %d, %v, want 0, nil", retries, err)
	}
	if err := m.FailAttempt(lease); !errors.Is(err, fixture.ErrLeaseFinished) {
		t.Fatalf("FailAttempt() after the final failure error = %v, want ErrLeaseFinished", err)
	}
}

// TestKubernetesBackoffLimitCountsRetries checks the exact event sequence for several
// limits. With limit n, the lease has n internal re-executions, and then the next
// failure is final.
func TestKubernetesBackoffLimitCountsRetries(t *testing.T) {
	for _, limit := range []int{0, 1, 2, 6} {
		m, err := fixture.NewKubernetesLikeWithBackoffLimit(limit)
		if err != nil {
			t.Fatalf("NewKubernetesLikeWithBackoffLimit(%d) error = %v", limit, err)
		}
		lease := startLease(t, m)

		want := []resourceprovider.EventKind{resourceprovider.EventStarted}
		for range limit {
			if err := m.FailAttempt(lease); err != nil {
				t.Fatalf("limit %d: FailAttempt() error = %v", limit, err)
			}
			want = append(want, resourceprovider.EventRequeued, resourceprovider.EventStarted)
		}
		if err := m.FailAttempt(lease); err != nil {
			t.Fatalf("limit %d: final FailAttempt() error = %v", limit, err)
		}
		want = append(want, resourceprovider.EventFailed)

		if got := eventKinds(t, m, lease); !slices.Equal(got, want) {
			t.Fatalf("limit %d: events = %v, want %v", limit, got, want)
		}
		if retries, err := m.Retries(lease); err != nil || retries != limit {
			t.Fatalf("limit %d: Retries() = %d, %v, want %d, nil", limit, retries, err, limit)
		}
	}
}

// TestUnboundedRetryIsNotZeroLimit shows that a fixture with no count limit (nil) keeps
// retrying past any fixed number. It must not behave like a limit of 0. Slurm-like
// fixtures use this form.
func TestUnboundedRetryIsNotZeroLimit(t *testing.T) {
	m, err := fixture.NewSlurmLike()
	if err != nil {
		t.Fatalf("NewSlurmLike() error = %v", err)
	}
	lease := startLease(t, m)
	const failures = 10
	for range failures {
		if err := m.FailAttempt(lease); err != nil {
			t.Fatalf("FailAttempt() error = %v", err)
		}
	}
	if retries, err := m.Retries(lease); err != nil || retries != failures {
		t.Fatalf("Retries() = %d, %v, want %d, nil", retries, err, failures)
	}
	if kinds := eventKinds(t, m, lease); slices.Contains(kinds, resourceprovider.EventFailed) {
		t.Fatalf("an unbounded fixture reported a final failure: %v", kinds)
	}
}

// TestNegativeRetryLimitIsRefused checks that a negative count is an invalid
// configuration, not a way to request unlimited retries.
func TestNegativeRetryLimitIsRefused(t *testing.T) {
	n := -1
	_, err := fixture.New(fixture.Config{
		Declaration: onByDefaultDeclaration("fixture-negative-limit"),
		RetryLimit:  &n,
	})
	if err == nil {
		t.Fatal("New() with a negative retry limit = nil error, want an error")
	}
}

// TestRetryLimitIsCopiedAtConstruction checks that the fixture keeps the limit it was
// built with. A later change to the caller's variable must not change the fixture.
func TestRetryLimitIsCopiedAtConstruction(t *testing.T) {
	n := 0
	m, err := fixture.New(fixture.Config{
		Declaration: onByDefaultDeclaration("fixture-copied-limit"),
		RetryLimit:  &n,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	n = 6

	lease := startLease(t, m)
	if err := m.FailAttempt(lease); err != nil {
		t.Fatalf("FailAttempt() error = %v", err)
	}
	want := []resourceprovider.EventKind{resourceprovider.EventStarted, resourceprovider.EventFailed}
	if got := eventKinds(t, m, lease); !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v: the fixture followed the caller's later change", got, want)
	}
}

// onByDefaultDeclaration returns a valid declaration with automatic retry on by default
// and not disableable per submission. It is used only for fixture construction tests.
func onByDefaultDeclaration(provider resourceprovider.ProviderID) resourceprovider.Declaration {
	return resourceprovider.Declaration{
		Provider: provider,
		Retry:    resourceprovider.RetryDeclaration{OnByDefault: true, DisableablePerSubmission: false},
		Credential: resourceprovider.CredentialScope{
			Providers: []resourceprovider.ProviderID{provider},
			Partition: "eki-test",
		},
	}
}
