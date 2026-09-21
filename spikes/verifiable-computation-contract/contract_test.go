package verifiable

import (
	"testing"
	"time"
)

type fixtureData struct {
	now       time.Time
	prover    KeyPair
	verifierK KeyPair
	verifier  *Verifier
	rp        *RelyingParty
	claim     ExecutionClaim
	output    []byte
	req       ResultRequirement
}

func fixture(t *testing.T, disclosure DisclosureProfile) fixtureData {
	t.Helper()
	prover, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	vk, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	policy := VerifierPolicy{
		Revision: 9, ResultTTL: 5 * time.Minute, StatementVersion: "eki.execution/v1",
		Profiles: map[string]ProofProfile{
			"mock.zkvm/v1": {SystemID: "mock.zkvm/v1", VerificationKey: prover.Public, DisclosureProfile: disclosure, MaxProofBytes: 4096},
		},
	}
	verifier, err := NewVerifier("proof-verifier.example", vk, policy)
	if err != nil {
		t.Fatal(err)
	}
	rp, err := NewRelyingParty(vk.Public)
	if err != nil {
		t.Fatal(err)
	}
	output := []byte("result bytes")
	claim := ExecutionClaim{
		StatementVersion: "eki.execution/v1", TaskID: "task-1", AttemptID: "attempt-1",
		ProgramDigest: "sha256:approved-program", InputDigest: "sha256:input-a", OutputDigest: DigestBytes(output),
	}
	req := ResultRequirement{
		TaskID: claim.TaskID, AttemptID: claim.AttemptID, ApprovedProgramDigest: claim.ProgramDigest,
		ExpectedInputDigest: claim.InputDigest, AcceptedProofSystems: map[string]bool{"mock.zkvm/v1": true},
		AcceptedVerifierID: "proof-verifier.example", MinVerifierPolicyRevision: 9,
	}
	return fixtureData{now: time.Unix(1_800_000_000, 0), prover: prover, verifierK: vk, verifier: verifier, rp: rp, claim: claim, output: output, req: req}
}

func verified(t *testing.T, f fixtureData, claim ExecutionClaim) Signed[VerificationResult] {
	t.Helper()
	proof, err := MockProof(f.prover, "mock.zkvm/v1", claim)
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.verifier.VerifyProof(f.now, proof)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestHappyPathBindsProgramInputOutputAndAttempt(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	r := verified(t, f, f.claim)
	if err := f.rp.Accept(f.now, f.req, r, f.output); err != nil {
		t.Fatal(err)
	}
}

func TestValidProofForUnapprovedProgramIsRejectedByRelyingParty(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	c := f.claim
	c.ProgramDigest = "sha256:malicious-but-provable-program"
	r := verified(t, f, c)
	if err := f.rp.Accept(f.now, f.req, r, f.output); err == nil {
		t.Fatal("proof correctness must not replace executor/program admission")
	}
}

func TestProofForDifferentInputCannotBeRebound(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	c := f.claim
	c.InputDigest = "sha256:other-input"
	r := verified(t, f, c)
	if err := f.rp.Accept(f.now, f.req, r, f.output); err == nil {
		t.Fatal("proof must remain bound to task input")
	}
}

func TestTamperedOutputIsRejectedAfterProofVerification(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	r := verified(t, f, f.claim)
	if err := f.rp.Accept(f.now, f.req, r, []byte("tampered result")); err == nil {
		t.Fatal("returned bytes must match proof-bound output digest")
	}
}

func TestWorkerCannotForgeVerificationResult(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	attacker, _ := NewKeyPair()
	fake := VerificationResult{
		TaskID: f.claim.TaskID, AttemptID: f.claim.AttemptID, ProgramDigest: f.claim.ProgramDigest,
		InputDigest: f.claim.InputDigest, OutputDigest: f.claim.OutputDigest, ProofSystemID: "mock.zkvm/v1",
		DisclosureProfile: DisclosurePublicIO, VerifierID: "proof-verifier.example", PolicyRevision: 9,
		VerifiedAt: f.now, ExpiresAt: f.now.Add(time.Minute),
	}
	signed, _ := sign(attacker, fake)
	if err := f.rp.Accept(f.now, f.req, signed, f.output); err == nil {
		t.Fatal("worker must not self-certify proof verification")
	}
}

func TestStalePolicyAndExpiredVerificationResultAreRejected(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	r := verified(t, f, f.claim)
	req := f.req
	req.MinVerifierPolicyRevision = 10
	if err := f.rp.Accept(f.now, req, r, f.output); err == nil {
		t.Fatal("stale proof verifier policy must fail")
	}
	if err := f.rp.Accept(f.now.Add(6*time.Minute), f.req, r, f.output); err == nil {
		t.Fatal("expired proof verification result must fail")
	}
}

func TestOrdinaryProofDoesNotSatisfyPrivateInputRequirement(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	r := verified(t, f, f.claim)
	req := f.req
	req.RequirePrivateInputProof = true
	if err := f.rp.Accept(f.now, req, r, f.output); err == nil {
		t.Fatal("verifiable computation must not be treated as zero knowledge by default")
	}
}

func TestPrivateInputProfileCanSatisfyPrivateInputRequirement(t *testing.T) {
	f := fixture(t, DisclosurePrivateInput)
	r := verified(t, f, f.claim)
	req := f.req
	req.RequirePrivateInputProof = true
	if err := f.rp.Accept(f.now, req, r, f.output); err != nil {
		t.Fatal(err)
	}
}

func TestDifferentProofSystemIsRejectedByRelyingParty(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	r := verified(t, f, f.claim)
	req := f.req
	req.AcceptedProofSystems = map[string]bool{"other.system/v2": true}
	if err := f.rp.Accept(f.now, req, r, f.output); err == nil {
		t.Fatal("proof system acceptance remains a relying-party policy decision")
	}
}

func TestProofArtifactMutationFailsVerifier(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	proof, err := MockProof(f.prover, "mock.zkvm/v1", f.claim)
	if err != nil {
		t.Fatal(err)
	}
	proof.Claim.OutputDigest = "sha256:changed-after-proof"
	if _, err := f.verifier.VerifyProof(f.now, proof); err == nil {
		t.Fatal("proof must bind exact execution claim")
	}
}

func TestStatementVersionMismatchFailsVerifier(t *testing.T) {
	f := fixture(t, DisclosurePublicIO)
	c := f.claim
	c.StatementVersion = "eki.execution/v2"
	proof, err := MockProof(f.prover, "mock.zkvm/v1", c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.verifier.VerifyProof(f.now, proof); err == nil {
		t.Fatal("proof statement schema/version must be policy-bound")
	}
}
