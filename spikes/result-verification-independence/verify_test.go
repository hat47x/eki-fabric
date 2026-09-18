package verify

import "testing"

func subject(worker, principal, domain string, managed bool) Subject {
	return Subject{WorkerID: worker, PrincipalID: principal, TrustDomain: domain, Managed: managed}
}

func TestNaiveQuorumCanBeCapturedBySybilWorkers(t *testing.T) {
	claims := []Claim{
		{Subject: subject("w1", "attacker", "public.example", false), Digest: "wrong"},
		{Subject: subject("w2", "attacker", "public.example", false), Digest: "wrong"},
		{Subject: subject("w3", "attacker", "public.example", false), Digest: "wrong"},
		{Subject: subject("w4", "honest", "other.example", false), Digest: "correct"},
	}
	got := naiveQuorum(claims, 3)
	if !got.Accepted || got.Digest != "wrong" {
		t.Fatalf("expected naive quorum to demonstrate Sybil capture, got %+v", got)
	}
}

func TestIndependentPrincipalPolicyRejectsSybilCount(t *testing.T) {
	claims := []Claim{
		{Subject: subject("w1", "attacker", "public.example", false), Digest: "wrong"},
		{Subject: subject("w2", "attacker", "public.example", false), Digest: "wrong"},
		{Subject: subject("w3", "attacker", "public.example", false), Digest: "wrong"},
		{Subject: subject("w4", "honest", "other.example", false), Digest: "correct"},
	}
	got := VerifyExact(claims, Policy{MinMatchingClaims: 2, MinDistinctPrincipals: 2, MinDistinctTrustDomains: 1})
	if got.Accepted {
		t.Fatalf("Sybil claims must not satisfy principal independence: %+v", got)
	}
}

func TestTwoIndependentPrincipalsCanAccept(t *testing.T) {
	claims := []Claim{
		{Subject: subject("w1", "p1", "a.example", false), Digest: "same"},
		{Subject: subject("w2", "p2", "b.example", false), Digest: "same"},
	}
	got := VerifyExact(claims, Policy{MinMatchingClaims: 2, MinDistinctPrincipals: 2, MinDistinctTrustDomains: 2})
	if !got.Accepted || got.Digest != "same" {
		t.Fatalf("expected acceptance, got %+v", got)
	}
}

func TestTrustDomainIndependenceCanBeRequired(t *testing.T) {
	claims := []Claim{
		{Subject: subject("w1", "p1", "same.example", false), Digest: "same"},
		{Subject: subject("w2", "p2", "same.example", false), Digest: "same"},
	}
	got := VerifyExact(claims, Policy{MinMatchingClaims: 2, MinDistinctPrincipals: 2, MinDistinctTrustDomains: 2})
	if got.Accepted {
		t.Fatalf("same trust domain must not satisfy domain independence: %+v", got)
	}
}

func TestManagedAnchorCanBeRequired(t *testing.T) {
	claims := []Claim{
		{Subject: subject("w1", "p1", "a.example", false), Digest: "same"},
		{Subject: subject("w2", "p2", "b.example", false), Digest: "same"},
	}
	p := Policy{MinMatchingClaims: 2, MinDistinctPrincipals: 2, MinDistinctTrustDomains: 2, RequireManagedAnchor: true}
	if got := VerifyExact(claims, p); got.Accepted {
		t.Fatalf("unmanaged-only set must not satisfy managed anchor: %+v", got)
	}
	claims = append(claims, Claim{Subject: subject("w3", "corp", "corp.example", true), Digest: "same"})
	if got := VerifyExact(claims, p); !got.Accepted {
		t.Fatalf("managed anchor should allow acceptance: %+v", got)
	}
}

func TestConflictingPassingDigestsRemainAmbiguous(t *testing.T) {
	claims := []Claim{
		{Subject: subject("a1", "a1", "a.example", false), Digest: "A"},
		{Subject: subject("a2", "a2", "b.example", false), Digest: "A"},
		{Subject: subject("b1", "b1", "c.example", false), Digest: "B"},
		{Subject: subject("b2", "b2", "d.example", false), Digest: "B"},
	}
	got := VerifyExact(claims, Policy{MinMatchingClaims: 2, MinDistinctPrincipals: 2, MinDistinctTrustDomains: 2})
	if got.Accepted || got.Reason == "" {
		t.Fatalf("conflicting passing groups must remain ambiguous: %+v", got)
	}
}

func TestDuplicateWorkerDoesNotCountTwice(t *testing.T) {
	claims := []Claim{
		{Subject: subject("w1", "p1", "a.example", false), Digest: "same"},
		{Subject: subject("w1", "p2", "b.example", false), Digest: "same"},
	}
	got := VerifyExact(claims, Policy{MinMatchingClaims: 2, MinDistinctPrincipals: 2, MinDistinctTrustDomains: 2})
	if got.Accepted {
		t.Fatalf("duplicate worker must invalidate candidate: %+v", got)
	}
}
