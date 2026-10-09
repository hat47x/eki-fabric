package providercontract_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/providercontract"
	"github.com/hat47x/eki-fabric/conformance/providercontract/fixture"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/resourceprovider"
)

func kubernetesLike(t testing.TB) providercontract.Fixture {
	t.Helper()
	m, err := fixture.NewKubernetesLike()
	if err != nil {
		t.Fatal(err)
	}
	return providercontract.Fixture{Adapter: m, Sim: m}
}

func slurmLike(t testing.TB) providercontract.Fixture {
	t.Helper()
	m, err := fixture.NewSlurmLike()
	if err != nil {
		t.Fatal(err)
	}
	return providercontract.Fixture{Adapter: m, Sim: m}
}

func retryNotDisableable(t testing.TB) providercontract.Fixture {
	t.Helper()
	m, err := fixture.NewRetryNotDisableable()
	if err != nil {
		t.Fatal(err)
	}
	return providercontract.Fixture{Adapter: m, Sim: m}
}

// TestKubernetesLikeFixture runs the shared contract on the Kubernetes-like fixture.
func TestKubernetesLikeFixture(t *testing.T) {
	providercontract.RunContract(t, kubernetesLike)
}

// TestSlurmLikeFixture runs the shared contract on the Slurm-like fixture.
func TestSlurmLikeFixture(t *testing.T) {
	providercontract.RunContract(t, slurmLike)
}

// TestRetryNotDisableableFixtureIsHonest runs the shared contract on the
// misconfigured fixture. It must pass, because the fixture declares its
// configuration truthfully. Rule (a) is what rejects it, as the next test shows.
func TestRetryNotDisableableFixtureIsHonest(t *testing.T) {
	providercontract.RunContract(t, retryNotDisableable)
}

// TestRetryNotDisableableFixtureIsRejectedByRuleA shows that a retry-sensitive task
// cannot be placed on the misconfigured fixture. It also shows what would happen if
// the rule were bypassed: the provider would run the work a second time inside the
// same attempt.
func TestRetryNotDisableableFixtureIsRejectedByRuleA(t *testing.T) {
	f := retryNotDisableable(t)
	decl := f.Adapter.Declare()

	sideEffect := resourceprovider.TaskProfile{SideEffect: true}
	if _, err := resourceprovider.PlaceWithRetryRule(sideEffect, decl); !errors.Is(err, resourceprovider.ErrRetryNotDisableable) {
		t.Fatalf("PlaceWithRetryRule(side effect) error = %v, want ErrRetryNotDisableable", err)
	}

	lease, err := ids.NewLeaseID()
	if err != nil {
		t.Fatal(err)
	}
	// The bypass: the submission keeps the provider default, as a placement that
	// skipped rule (a) would do.
	if _, err := f.Adapter.Allocate(context.Background(), resourceprovider.AllocationRequest{
		LeaseID: lease,
		Slots:   1,
		Retry:   resourceprovider.RetryProviderDefault,
	}); err != nil {
		t.Fatalf("bypass allocate error = %v", err)
	}
	if err := f.Sim.FailAttempt(lease); err != nil {
		t.Fatalf("FailAttempt() error = %v", err)
	}
	evs, err := f.Adapter.Events(context.Background(), lease, 0)
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	requeued := false
	for _, ev := range evs {
		if ev.Kind == resourceprovider.EventRequeued {
			requeued = true
		}
	}
	if !requeued {
		t.Fatal("the bypass did not show an internal re-execution; the rejection would not be justified")
	}
}

// TestDisableableFixturesPlaceSideEffectsWithRetryDisabled checks that rule (a)
// accepts the two documented-default fixtures and sets the retry setting explicitly.
func TestDisableableFixturesPlaceSideEffectsWithRetryDisabled(t *testing.T) {
	for name, newFixture := range map[string]providercontract.Factory{
		"kubernetes-like": kubernetesLike,
		"slurm-like":      slurmLike,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			setting, err := resourceprovider.PlaceWithRetryRule(resourceprovider.TaskProfile{SideEffect: true}, f.Adapter.Declare())
			if err != nil {
				t.Fatalf("PlaceWithRetryRule() error = %v", err)
			}
			if setting != resourceprovider.RetryDisabled {
				t.Fatalf("setting = %q, want %q", setting, resourceprovider.RetryDisabled)
			}
		})
	}
}

// TestKubernetesLikeRetryLimitMatchesDocumentedDefault checks that the fixture uses
// up the documented backoffLimit of 6 retries before the failure is final.
func TestKubernetesLikeRetryLimitMatchesDocumentedDefault(t *testing.T) {
	m, err := fixture.NewKubernetesLike()
	if err != nil {
		t.Fatal(err)
	}
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

	for i := 1; i <= 6; i++ {
		if err := m.FailAttempt(lease); err != nil {
			t.Fatalf("FailAttempt() %d error = %v", i, err)
		}
	}
	retries, err := m.Retries(lease)
	if err != nil {
		t.Fatal(err)
	}
	if retries != 6 {
		t.Fatalf("retries after six failures = %d, want 6", retries)
	}

	// The seventh failure is final.
	if err := m.FailAttempt(lease); err != nil {
		t.Fatalf("seventh FailAttempt() error = %v", err)
	}
	evs, err := m.Events(context.Background(), lease, 0)
	if err != nil {
		t.Fatal(err)
	}
	if last := evs[len(evs)-1]; last.Kind != resourceprovider.EventFailed {
		t.Fatalf("last event after the retry limit = %q, want failed", last.Kind)
	}
}
