package grant

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestDigestIsStableAcrossInputOrderAndTimeZone(t *testing.T) {
	base := mustIssue(t, testSpec()).Digest()
	if base.IsZero() || base.Algorithm() != "sha256" {
		t.Fatalf("Digest() = %q, want a sha256 digest", base.String())
	}
	if again := mustIssue(t, testSpec()).Digest(); again != base {
		t.Error("the same fields produced different digests")
	}

	reordered := testSpec()
	reordered.Ceilings = []ResourceCeiling{
		{Class: "cpu-class-b", Percent: 25},
		{Class: "gpu-class-a", Percent: 40},
	}
	if got := mustIssue(t, reordered).Digest(); got != base {
		t.Error("the order of the ceiling list changed the digest")
	}

	zoned := testSpec()
	zone := time.FixedZone("UTC+9", 9*3600)
	zoned.IssuedAt = testIssuedAt.In(zone)
	zoned.ExpiresAt = zoned.ExpiresAt.In(zone)
	if got := mustIssue(t, zoned).Digest(); got != base {
		t.Error("the same instants in another time zone changed the digest")
	}
}

func TestDigestChangesWithEachField(t *testing.T) {
	base := mustIssue(t, testSpec()).Digest()
	cases := []struct {
		name   string
		mutate func(*Spec)
	}{
		{"provider", func(s *Spec) { s.Provider = "provider-beta" }},
		{"beneficiary", func(s *Spec) { s.Beneficiary = "beneficiary-other" }},
		{"resource class name", func(s *Spec) { s.Ceilings[0].Class = "gpu-class-z" }},
		{"resource class percent", func(s *Spec) { s.Ceilings[0].Percent = 41 }},
		{"additional resource class", func(s *Spec) {
			s.Ceilings = append(s.Ceilings, ResourceCeiling{Class: "memory-class-c", Percent: 5})
		}},
		{"removed resource class", func(s *Spec) { s.Ceilings = s.Ceilings[:1] }},
		{"issuance time", func(s *Spec) { s.IssuedAt = testIssuedAt.Add(time.Second) }},
		{"expiry time", func(s *Spec) { s.ExpiresAt = s.ExpiresAt.Add(time.Second) }},
		{"lease cap changed", func(s *Spec) { s.MaxConcurrentLeases = 5 }},
		{"lease cap removed", func(s *Spec) { s.MaxConcurrentLeases = 0 }},
		{"purpose", func(s *Spec) { s.Purpose = PurposeOpenSource }},
		{"policy revision", func(s *Spec) { s.PolicyRevision = contract.SHA256([]byte("policy-revision-2")) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := testSpec()
			tc.mutate(&spec)
			if got := mustIssue(t, spec).Digest(); got == base {
				t.Errorf("changing %s did not change the digest", tc.name)
			}
		})
	}
}

func TestDigestLengthPrefixPreventsBoundaryCollision(t *testing.T) {
	a := testSpec()
	a.Provider, a.Beneficiary = "ab", "c"
	b := testSpec()
	b.Provider, b.Beneficiary = "a", "bc"
	if mustIssue(t, a).Digest() == mustIssue(t, b).Digest() {
		t.Error("shifting bytes between provider and beneficiary kept the digest")
	}
}

func TestDigestIgnoresRevocation(t *testing.T) {
	g := mustIssue(t, testSpec())
	before := g.Digest()
	var l Ledger
	checkErr(t, l.Revoke(g, testProvider, ReasonCapacityReclaim, testIssuedAt.Add(time.Hour)), nil)
	if g.Digest() != before {
		t.Error("revoking a grant changed its digest")
	}
	if mustIssue(t, testSpec()).Digest() != before {
		t.Error("an identically issued grant has a different digest")
	}
}

func TestZeroGrantHasZeroDigest(t *testing.T) {
	if d := (Grant{}).Digest(); !d.IsZero() {
		t.Errorf("zero grant digest = %q, want zero", d.String())
	}
}
