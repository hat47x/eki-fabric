package attestation

import (
	"testing"
	"time"
)

type fixtureData struct {
	now         time.Time
	attester    KeyPair
	verifierKey KeyPair
	verifier    *Verifier
	rp          *RelyingParty
	evidence    Evidence
}

func fixture(t *testing.T) fixtureData {
	t.Helper()
	att, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	vk, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	policy := VerifierPolicy{Revision: 7, MaxEvidenceAge: 2 * time.Minute, AllowedEnvironmentClasses: map[string]bool{"confidential-vm": true}, AllowedMeasurements: map[string]bool{"m-good": true}}
	v, err := NewVerifier("verifier.example", vk, []EvidenceKey{{AttesterID: "worker-1", TrustDomain: "provider.example", KeyID: "ak1", Public: att.Public}}, policy, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rp, err := NewRelyingParty(vk.Public)
	if err != nil {
		t.Fatal(err)
	}
	e := Evidence{AttesterID: "worker-1", TrustDomain: "provider.example", EnvironmentClass: "confidential-vm", Measurement: "m-good", Nonce: "nonce-1", CollectedAt: now.Add(-10 * time.Second)}
	return fixtureData{now: now, attester: att, verifierKey: vk, verifier: v, rp: rp, evidence: e}
}

func verifyHappy(t *testing.T, f fixtureData, nonce string, e Evidence) Signed[AttestationResult] {
	t.Helper()
	env, err := SignEvidence(f.attester, "ak1", e)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.verifier.Appraise(f.now, nonce, env)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHappyPathSeparatesEvidenceVerificationFromPlacementDecision(t *testing.T) {
	f := fixture(t)
	r := verifyHappy(t, f, "nonce-1", f.evidence)
	req := PlacementRequirement{WorkerID: "worker-1", TrustDomain: "provider.example", RequiredEnvironmentClass: "confidential-vm", AcceptedVerifierID: "verifier.example", MinVerifierPolicyRevision: 7}
	if err := f.rp.AuthorizePlacement(f.now, req, r); err != nil {
		t.Fatal(err)
	}
}

func TestGoodVerifierResultDoesNotAuthorizeDifferentEnvironmentRequirement(t *testing.T) {
	f := fixture(t)
	r := verifyHappy(t, f, "nonce-1", f.evidence)
	req := PlacementRequirement{WorkerID: "worker-1", TrustDomain: "provider.example", RequiredEnvironmentClass: "gpu-confidential", AcceptedVerifierID: "verifier.example", MinVerifierPolicyRevision: 7}
	if err := f.rp.AuthorizePlacement(f.now, req, r); err == nil {
		t.Fatal("relying party policy must make the application-specific decision")
	}
}

func TestEvidenceFromDifferentWorkerCannotBeRebound(t *testing.T) {
	f := fixture(t)
	r := verifyHappy(t, f, "nonce-1", f.evidence)
	req := PlacementRequirement{WorkerID: "worker-2", TrustDomain: "provider.example", RequiredEnvironmentClass: "confidential-vm", AcceptedVerifierID: "verifier.example", MinVerifierPolicyRevision: 7}
	if err := f.rp.AuthorizePlacement(f.now, req, r); err == nil {
		t.Fatal("attestation result must remain bound to worker identity")
	}
}

func TestWrongTrustDomainIsRejectedByVerifier(t *testing.T) {
	f := fixture(t)
	e := f.evidence
	e.TrustDomain = "other.example"
	env, _ := SignEvidence(f.attester, "ak1", e)
	if _, err := f.verifier.Appraise(f.now, "nonce-1", env); err == nil {
		t.Fatal("trust-domain substitution must fail")
	}
}

func TestNonceMismatchAndReplayAreRejected(t *testing.T) {
	f := fixture(t)
	env, _ := SignEvidence(f.attester, "ak1", f.evidence)
	if _, err := f.verifier.Appraise(f.now, "other-nonce", env); err == nil {
		t.Fatal("nonce mismatch must fail")
	}
	if _, err := f.verifier.Appraise(f.now, "nonce-1", env); err != nil {
		t.Fatal(err)
	}
	if _, err := f.verifier.Appraise(f.now, "nonce-1", env); err == nil {
		t.Fatal("nonce replay must fail")
	}
}

func TestStaleEvidenceIsRejected(t *testing.T) {
	f := fixture(t)
	e := f.evidence
	e.CollectedAt = f.now.Add(-3 * time.Minute)
	env, _ := SignEvidence(f.attester, "ak1", e)
	if _, err := f.verifier.Appraise(f.now, "nonce-1", env); err == nil {
		t.Fatal("stale evidence must fail")
	}
}

func TestBadMeasurementIsRejectedByVerifierPolicy(t *testing.T) {
	f := fixture(t)
	e := f.evidence
	e.Measurement = "m-compromised"
	env, _ := SignEvidence(f.attester, "ak1", e)
	if _, err := f.verifier.Appraise(f.now, "nonce-1", env); err == nil {
		t.Fatal("unknown measurement must fail")
	}
}

func TestWorkerCannotForgeAttestationResult(t *testing.T) {
	f := fixture(t)
	fake := AttestationResult{AttesterID: "worker-1", TrustDomain: "provider.example", EnvironmentClass: "confidential-vm", VerifierID: "verifier.example", PolicyRevision: 7, VerifiedAt: f.now, ExpiresAt: f.now.Add(time.Minute)}
	signed, _ := sign(f.attester, fake)
	req := PlacementRequirement{WorkerID: "worker-1", TrustDomain: "provider.example", RequiredEnvironmentClass: "confidential-vm", AcceptedVerifierID: "verifier.example", MinVerifierPolicyRevision: 7}
	if err := f.rp.AuthorizePlacement(f.now, req, signed); err == nil {
		t.Fatal("worker must not self-certify attestation result")
	}
}

func TestExpiredResultAndStalePolicyAreRejectedByRelyingParty(t *testing.T) {
	f := fixture(t)
	r := verifyHappy(t, f, "nonce-1", f.evidence)
	req := PlacementRequirement{WorkerID: "worker-1", TrustDomain: "provider.example", RequiredEnvironmentClass: "confidential-vm", AcceptedVerifierID: "verifier.example", MinVerifierPolicyRevision: 8}
	if err := f.rp.AuthorizePlacement(f.now, req, r); err == nil {
		t.Fatal("stale verifier policy must fail")
	}
	req.MinVerifierPolicyRevision = 7
	if err := f.rp.AuthorizePlacement(f.now.Add(6*time.Minute), req, r); err == nil {
		t.Fatal("expired attestation result must fail")
	}
}

func TestRawEvidenceDoesNotReachRelyingPartyContract(t *testing.T) {
	f := fixture(t)
	r := verifyHappy(t, f, "nonce-1", f.evidence)
	if r.Payload.EvidenceDigest == "" {
		t.Fatal("normalized result should retain evidence provenance digest")
	}
	// AttestationResult intentionally exposes no raw measurement field.
}
