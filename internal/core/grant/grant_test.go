package grant

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var testIssuedAt = time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)

const (
	testProvider    Identity = "provider-alpha"
	testBeneficiary Identity = "beneficiary-research"
	testDay                  = 24 * time.Hour
)

func testPolicy(label string) contract.Digest {
	return contract.SHA256([]byte(label))
}

func testSpec() Spec {
	return Spec{
		Provider:    testProvider,
		Beneficiary: testBeneficiary,
		Ceilings: []ResourceCeiling{
			{Class: "gpu-class-a", Percent: 40},
			{Class: "cpu-class-b", Percent: 25},
		},
		IssuedAt:            testIssuedAt,
		ExpiresAt:           testIssuedAt.Add(30 * testDay),
		MaxConcurrentLeases: 4,
		Purpose:             PurposePublicResearch,
		PolicyRevision:      testPolicy("policy-revision-1"),
	}
}

func mustIssue(t *testing.T, spec Spec) Grant {
	t.Helper()
	g, err := Issue(spec.Provider, spec)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	return g
}

// checkErr fails the test unless got matches want. A nil want requires got to be nil.
func checkErr(t *testing.T, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("error = %v, want nil", got)
		}
		return
	}
	if got == nil {
		t.Fatalf("error = nil, want %v", want)
	}
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestIssueBindsEveryField(t *testing.T) {
	spec := testSpec()
	g := mustIssue(t, spec)

	if g.Provider() != spec.Provider {
		t.Errorf("Provider() = %q, want %q", g.Provider(), spec.Provider)
	}
	if g.Beneficiary() != spec.Beneficiary {
		t.Errorf("Beneficiary() = %q, want %q", g.Beneficiary(), spec.Beneficiary)
	}
	if got, ok := g.Ceiling("gpu-class-a"); !ok || got != 40 {
		t.Errorf("Ceiling(gpu-class-a) = %d, %t; want 40, true", got, ok)
	}
	if got, ok := g.Ceiling("cpu-class-b"); !ok || got != 25 {
		t.Errorf("Ceiling(cpu-class-b) = %d, %t; want 25, true", got, ok)
	}
	if _, ok := g.Ceiling("memory-class-c"); ok {
		t.Error("Ceiling(memory-class-c) reports a class the grant does not cover")
	}
	if !g.IssuedAt().Equal(spec.IssuedAt) {
		t.Errorf("IssuedAt() = %v, want %v", g.IssuedAt(), spec.IssuedAt)
	}
	if !g.ExpiresAt().Equal(spec.ExpiresAt) {
		t.Errorf("ExpiresAt() = %v, want %v", g.ExpiresAt(), spec.ExpiresAt)
	}
	if g.MaxConcurrentLeases() != 4 {
		t.Errorf("MaxConcurrentLeases() = %d, want 4", g.MaxConcurrentLeases())
	}
	if g.Purpose() != PurposePublicResearch {
		t.Errorf("Purpose() = %q, want %q", g.Purpose(), PurposePublicResearch)
	}
	if g.PolicyRevision() != spec.PolicyRevision {
		t.Error("PolicyRevision() does not match the spec")
	}
	if !g.inWindow(spec.IssuedAt) {
		t.Error("a new grant is not active at its issuance instant")
	}

	ceilings := g.Ceilings()
	if len(ceilings) != 2 || ceilings[0].Class != "cpu-class-b" || ceilings[1].Class != "gpu-class-a" {
		t.Errorf("Ceilings() = %v, want two entries sorted by class", ceilings)
	}
}

func TestIssueCopiesInputsAndHidesInternalState(t *testing.T) {
	spec := testSpec()
	g := mustIssue(t, spec)

	spec.Ceilings[0].Percent = 100
	if got, _ := g.Ceiling("cpu-class-b"); got != 25 {
		t.Errorf("changing the caller's slice altered the grant: ceiling = %d", got)
	}

	ceilings := g.Ceilings()
	ceilings[0].Percent = 100
	if got, _ := g.Ceiling("cpu-class-b"); got != 25 {
		t.Errorf("changing a returned ceiling altered the grant: ceiling = %d", got)
	}
}

func TestIssueAcceptsBoundaryValues(t *testing.T) {
	manyClasses := make([]ResourceCeiling, 0, MaxResourceClasses)
	for i := 0; i < MaxResourceClasses; i++ {
		manyClasses = append(manyClasses, ResourceCeiling{Class: ResourceClass(fmt.Sprintf("class-%02d", i)), Percent: 1})
	}
	cases := []struct {
		name   string
		mutate func(*Spec)
	}{
		{"percent 1", func(s *Spec) { s.Ceilings = []ResourceCeiling{{Class: "gpu-class-a", Percent: 1}} }},
		{"percent 100", func(s *Spec) { s.Ceilings = []ResourceCeiling{{Class: "gpu-class-a", Percent: 100}} }},
		{"no lease cap", func(s *Spec) { s.MaxConcurrentLeases = 0 }},
		{"lease cap 100", func(s *Spec) { s.MaxConcurrentLeases = 100 }},
		{"window of exactly 366 days", func(s *Spec) { s.ExpiresAt = s.IssuedAt.Add(MaxGrantWindow) }},
		{"identity of exactly 128 bytes", func(s *Spec) {
			s.Provider = Identity(strings.Repeat("p", MaxIdentityBytes))
		}},
		{"resource class of exactly 64 bytes", func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: ResourceClass(strings.Repeat("c", MaxResourceClassBytes)), Percent: 10}}
		}},
		{"sixteen resource classes", func(s *Spec) { s.Ceilings = manyClasses }},
		{"class with dot, underscore, dash and digits", func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "gpu.class_a-2", Percent: 10}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := testSpec()
			tc.mutate(&spec)
			if _, err := Issue(spec.Provider, spec); err != nil {
				t.Fatalf("Issue() error = %v, want nil", err)
			}
		})
	}
}

func TestIssueRefusesInvalidSpec(t *testing.T) {
	tooManyClasses := make([]ResourceCeiling, 0, MaxResourceClasses+1)
	for i := 0; i <= MaxResourceClasses; i++ {
		tooManyClasses = append(tooManyClasses, ResourceCeiling{Class: ResourceClass(fmt.Sprintf("class-%02d", i)), Percent: 1})
	}
	cases := []struct {
		name   string
		issuer Identity // empty means the spec's provider
		mutate func(*Spec)
		want   error
	}{
		{name: "empty provider", mutate: func(s *Spec) { s.Provider = "" }, want: ErrMissingField},
		{name: "empty beneficiary", mutate: func(s *Spec) { s.Beneficiary = "" }, want: ErrMissingField},
		{name: "provider with a space", mutate: func(s *Spec) { s.Provider = "provider alpha" }, want: ErrInvalidField},
		{name: "beneficiary with non-ASCII bytes", mutate: func(s *Spec) {
			s.Beneficiary = "beneficiary-\xc3\xa9"
		}, want: ErrInvalidField},
		{name: "beneficiary with a control byte", mutate: func(s *Spec) {
			s.Beneficiary = "beneficiary\x7f"
		}, want: ErrInvalidField},
		{name: "identity of 129 bytes", mutate: func(s *Spec) {
			s.Provider = Identity(strings.Repeat("p", MaxIdentityBytes+1))
		}, want: ErrTooLong},
		{name: "beneficiary equals provider", mutate: func(s *Spec) {
			s.Beneficiary = s.Provider
		}, want: ErrSelfGrant},
		{name: "beneficiary issues to itself", issuer: testBeneficiary, mutate: func(s *Spec) {}, want: ErrSelfGrant},
		{name: "third party issues for the provider", issuer: "other-party", mutate: func(s *Spec) {}, want: ErrIssuerMismatch},
		{name: "beneficiary issues a child grant naming the provider", issuer: testBeneficiary, mutate: func(s *Spec) {
			s.Beneficiary = "sub-consumer-gamma"
		}, want: ErrIssuerMismatch},
		{name: "zero issuance time", mutate: func(s *Spec) { s.IssuedAt = time.Time{} }, want: ErrMissingField},
		{name: "zero expiry time", mutate: func(s *Spec) { s.ExpiresAt = time.Time{} }, want: ErrMissingField},
		{name: "reversed window", mutate: func(s *Spec) {
			s.ExpiresAt = s.IssuedAt.Add(-time.Hour)
		}, want: ErrInvalidWindow},
		{name: "empty window", mutate: func(s *Spec) { s.ExpiresAt = s.IssuedAt }, want: ErrInvalidWindow},
		{name: "window of 366 days and one second", mutate: func(s *Spec) {
			s.ExpiresAt = s.IssuedAt.Add(MaxGrantWindow + time.Second)
		}, want: ErrWindowTooLong},
		{name: "no ceilings", mutate: func(s *Spec) { s.Ceilings = nil }, want: ErrMissingField},
		{name: "seventeen resource classes", mutate: func(s *Spec) { s.Ceilings = tooManyClasses }, want: ErrTooManyResourceClasses},
		{name: "duplicate resource class", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "gpu-class-a", Percent: 10}, {Class: "gpu-class-a", Percent: 20}}
		}, want: ErrDuplicateResourceClass},
		{name: "percent zero", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "gpu-class-a", Percent: 0}}
		}, want: ErrPercentOutOfRange},
		{name: "percent 101", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "gpu-class-a", Percent: 101}}
		}, want: ErrPercentOutOfRange},
		{name: "percent negative", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "gpu-class-a", Percent: -5}}
		}, want: ErrPercentOutOfRange},
		{name: "empty resource class", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "", Percent: 10}}
		}, want: ErrMissingField},
		{name: "uppercase resource class", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "GPU", Percent: 10}}
		}, want: ErrInvalidField},
		{name: "resource class starting with a dash", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "-gpu", Percent: 10}}
		}, want: ErrInvalidField},
		{name: "resource class with a space", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: "gpu a", Percent: 10}}
		}, want: ErrInvalidField},
		{name: "resource class of 65 bytes", mutate: func(s *Spec) {
			s.Ceilings = []ResourceCeiling{{Class: ResourceClass(strings.Repeat("c", MaxResourceClassBytes+1)), Percent: 10}}
		}, want: ErrTooLong},
		{name: "lease cap negative", mutate: func(s *Spec) { s.MaxConcurrentLeases = -1 }, want: ErrInvalidLeaseLimit},
		{name: "lease cap above 100", mutate: func(s *Spec) { s.MaxConcurrentLeases = 101 }, want: ErrInvalidLeaseLimit},
		{name: "unknown purpose", mutate: func(s *Spec) { s.Purpose = "for-profit" }, want: ErrUnknownPurpose},
		{name: "empty purpose", mutate: func(s *Spec) { s.Purpose = "" }, want: ErrUnknownPurpose},
		{name: "zero policy revision", mutate: func(s *Spec) { s.PolicyRevision = contract.Digest{} }, want: ErrMissingField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := testSpec()
			tc.mutate(&spec)
			issuer := spec.Provider
			if tc.issuer != "" {
				issuer = tc.issuer
			}
			_, err := Issue(issuer, spec)
			checkErr(t, err, tc.want)
		})
	}
}
