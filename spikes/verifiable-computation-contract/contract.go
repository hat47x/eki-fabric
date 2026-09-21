package verifiable

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type KeyPair struct {
	Public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func NewKeyPair() (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, err
	}
	return KeyPair{Public: pub, private: priv}, nil
}

type Signed[T any] struct {
	Payload   T      `json:"payload"`
	Signature []byte `json:"signature"`
}

func canonical(v any) ([]byte, error) { return json.Marshal(v) }

func sign[T any](key KeyPair, payload T) (Signed[T], error) {
	b, err := canonical(payload)
	if err != nil {
		return Signed[T]{}, err
	}
	return Signed[T]{Payload: payload, Signature: ed25519.Sign(key.private, b)}, nil
}

func Verify[T any](pub ed25519.PublicKey, signed Signed[T]) bool {
	b, err := canonical(signed.Payload)
	return err == nil && ed25519.Verify(pub, b, signed.Signature)
}

func DigestBytes(content []byte) string {
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestJSON(v any) (string, error) {
	b, err := canonical(v)
	if err != nil {
		return "", err
	}
	return DigestBytes(b), nil
}

type DisclosureProfile string

const (
	DisclosurePublicIO     DisclosureProfile = "public-io"
	DisclosurePrivateInput DisclosureProfile = "private-input"
)

type ExecutionClaim struct {
	StatementVersion string `json:"statement_version"`
	TaskID           string `json:"task_id"`
	AttemptID        string `json:"attempt_id"`
	ProgramDigest    string `json:"program_digest"`
	InputDigest      string `json:"input_digest"`
	OutputDigest     string `json:"output_digest"`
}

func validateClaim(c ExecutionClaim) error {
	if c.StatementVersion == "" || c.TaskID == "" || c.AttemptID == "" || c.ProgramDigest == "" || c.InputDigest == "" || c.OutputDigest == "" {
		return errors.New("complete execution claim is required")
	}
	return nil
}

type ProofArtifact struct {
	ProofSystemID string         `json:"proof_system_id"`
	Claim         ExecutionClaim `json:"claim"`
	ProofBytes    []byte         `json:"proof_bytes"`
}

// MockProof is intentionally only a contract-test stand-in for a real zkVM/proof system.
// A production adapter must replace this signature check with native proof verification.
func MockProof(prover KeyPair, systemID string, claim ExecutionClaim) (ProofArtifact, error) {
	if systemID == "" {
		return ProofArtifact{}, errors.New("proof system id is required")
	}
	if err := validateClaim(claim); err != nil {
		return ProofArtifact{}, err
	}
	b, err := canonical(claim)
	if err != nil {
		return ProofArtifact{}, err
	}
	return ProofArtifact{ProofSystemID: systemID, Claim: claim, ProofBytes: ed25519.Sign(prover.private, b)}, nil
}

type ProofProfile struct {
	SystemID          string
	VerificationKey   ed25519.PublicKey
	DisclosureProfile DisclosureProfile
	MaxProofBytes     int
}

type VerifierPolicy struct {
	Revision         uint64
	ResultTTL        time.Duration
	StatementVersion string
	Profiles         map[string]ProofProfile
}

type VerificationResult struct {
	TaskID            string            `json:"task_id"`
	AttemptID         string            `json:"attempt_id"`
	ProgramDigest     string            `json:"program_digest"`
	InputDigest       string            `json:"input_digest"`
	OutputDigest      string            `json:"output_digest"`
	ProofSystemID     string            `json:"proof_system_id"`
	DisclosureProfile DisclosureProfile `json:"disclosure_profile"`
	ProofDigest       string            `json:"proof_digest"`
	VerifierID        string            `json:"verifier_id"`
	PolicyRevision    uint64            `json:"policy_revision"`
	VerifiedAt        time.Time         `json:"verified_at"`
	ExpiresAt         time.Time         `json:"expires_at"`
}

type Verifier struct {
	ID     string
	key    KeyPair
	policy VerifierPolicy
}

func NewVerifier(id string, key KeyPair, policy VerifierPolicy) (*Verifier, error) {
	if id == "" || policy.Revision == 0 || policy.ResultTTL <= 0 || policy.StatementVersion == "" || len(policy.Profiles) == 0 {
		return nil, errors.New("complete verifier configuration is required")
	}
	for id, p := range policy.Profiles {
		if id == "" || p.SystemID != id || len(p.VerificationKey) != ed25519.PublicKeySize || p.MaxProofBytes <= 0 {
			return nil, fmt.Errorf("invalid proof profile %q", id)
		}
		if p.DisclosureProfile != DisclosurePublicIO && p.DisclosureProfile != DisclosurePrivateInput {
			return nil, fmt.Errorf("invalid disclosure profile for %q", id)
		}
	}
	return &Verifier{ID: id, key: key, policy: policy}, nil
}

func (v *Verifier) VerifyProof(now time.Time, proof ProofArtifact) (Signed[VerificationResult], error) {
	profile, ok := v.policy.Profiles[proof.ProofSystemID]
	if !ok {
		return Signed[VerificationResult]{}, errors.New("proof system is not allowed by verifier policy")
	}
	if err := validateClaim(proof.Claim); err != nil {
		return Signed[VerificationResult]{}, err
	}
	if proof.Claim.StatementVersion != v.policy.StatementVersion {
		return Signed[VerificationResult]{}, errors.New("execution claim statement version is not allowed")
	}
	if len(proof.ProofBytes) == 0 || len(proof.ProofBytes) > profile.MaxProofBytes {
		return Signed[VerificationResult]{}, errors.New("proof size is outside verifier policy")
	}
	claimBytes, err := canonical(proof.Claim)
	if err != nil {
		return Signed[VerificationResult]{}, err
	}
	if !ed25519.Verify(profile.VerificationKey, claimBytes, proof.ProofBytes) {
		return Signed[VerificationResult]{}, errors.New("proof verification failed")
	}
	proofDigest, err := digestJSON(proof)
	if err != nil {
		return Signed[VerificationResult]{}, err
	}
	r := VerificationResult{
		TaskID: proof.Claim.TaskID, AttemptID: proof.Claim.AttemptID,
		ProgramDigest: proof.Claim.ProgramDigest, InputDigest: proof.Claim.InputDigest, OutputDigest: proof.Claim.OutputDigest,
		ProofSystemID: proof.ProofSystemID, DisclosureProfile: profile.DisclosureProfile,
		ProofDigest: proofDigest, VerifierID: v.ID, PolicyRevision: v.policy.Revision,
		VerifiedAt: now, ExpiresAt: now.Add(v.policy.ResultTTL),
	}
	return sign(v.key, r)
}

type ResultRequirement struct {
	TaskID                    string
	AttemptID                 string
	ApprovedProgramDigest     string
	ExpectedInputDigest       string
	AcceptedProofSystems      map[string]bool
	AcceptedVerifierID        string
	MinVerifierPolicyRevision uint64
	RequirePrivateInputProof  bool
}

type RelyingParty struct {
	verifierPub ed25519.PublicKey
}

func NewRelyingParty(verifierPub ed25519.PublicKey) (*RelyingParty, error) {
	if len(verifierPub) != ed25519.PublicKeySize {
		return nil, errors.New("verifier trust root is required")
	}
	return &RelyingParty{verifierPub: append(ed25519.PublicKey(nil), verifierPub...)}, nil
}

func (rp *RelyingParty) Accept(now time.Time, req ResultRequirement, result Signed[VerificationResult], output []byte) error {
	if req.TaskID == "" || req.AttemptID == "" || req.ApprovedProgramDigest == "" || req.ExpectedInputDigest == "" || len(req.AcceptedProofSystems) == 0 || req.AcceptedVerifierID == "" || req.MinVerifierPolicyRevision == 0 {
		return errors.New("complete result requirement is required")
	}
	if !Verify(rp.verifierPub, result) {
		return errors.New("verification result is not signed by pinned verifier authority")
	}
	r := result.Payload
	if r.TaskID != req.TaskID || r.AttemptID != req.AttemptID {
		return errors.New("proof verification result is not bound to requested task attempt")
	}
	if r.ProgramDigest != req.ApprovedProgramDigest {
		return errors.New("proof covers a program that is not approved for this task")
	}
	if r.InputDigest != req.ExpectedInputDigest {
		return errors.New("proof covers different task input")
	}
	if r.OutputDigest != DigestBytes(output) {
		return errors.New("returned output does not match proof-bound output digest")
	}
	if !req.AcceptedProofSystems[r.ProofSystemID] {
		return errors.New("proof system is not accepted by relying-party policy")
	}
	if r.VerifierID != req.AcceptedVerifierID {
		return errors.New("unexpected proof verifier identity")
	}
	if r.PolicyRevision < req.MinVerifierPolicyRevision {
		return errors.New("proof verification result uses stale verifier policy")
	}
	if !r.ExpiresAt.After(now) {
		return errors.New("proof verification result is expired")
	}
	if req.RequirePrivateInputProof && r.DisclosureProfile != DisclosurePrivateInput {
		return errors.New("proof profile does not satisfy private-input requirement")
	}
	return nil
}
