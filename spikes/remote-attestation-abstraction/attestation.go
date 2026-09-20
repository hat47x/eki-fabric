package attestation

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
func sign[T any](k KeyPair, p T) (Signed[T], error) {
	b, err := canonical(p)
	if err != nil {
		return Signed[T]{}, err
	}
	return Signed[T]{Payload: p, Signature: ed25519.Sign(k.private, b)}, nil
}
func Verify[T any](pub ed25519.PublicKey, s Signed[T]) bool {
	b, err := canonical(s.Payload)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, b, s.Signature)
}

func digestJSON(v any) (string, error) {
	b, err := canonical(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

type Evidence struct {
	AttesterID       string    `json:"attester_id"`
	TrustDomain      string    `json:"trust_domain"`
	EnvironmentClass string    `json:"environment_class"`
	Measurement      string    `json:"measurement"`
	Nonce            string    `json:"nonce"`
	CollectedAt      time.Time `json:"collected_at"`
}

type EvidenceEnvelope struct {
	KeyID  string           `json:"key_id"`
	Signed Signed[Evidence] `json:"signed"`
}

type EvidenceKey struct {
	AttesterID  string
	TrustDomain string
	KeyID       string
	Public      ed25519.PublicKey
}

type VerifierPolicy struct {
	Revision                  uint64
	MaxEvidenceAge            time.Duration
	AllowedEnvironmentClasses map[string]bool
	AllowedMeasurements       map[string]bool
}

type AttestationResult struct {
	AttesterID       string    `json:"attester_id"`
	TrustDomain      string    `json:"trust_domain"`
	EnvironmentClass string    `json:"environment_class"`
	EvidenceDigest   string    `json:"evidence_digest"`
	VerifierID       string    `json:"verifier_id"`
	PolicyRevision   uint64    `json:"policy_revision"`
	VerifiedAt       time.Time `json:"verified_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}

type Verifier struct {
	ID           string
	key          KeyPair
	evidenceKeys map[string]EvidenceKey
	policy       VerifierPolicy
	usedNonces   map[string]bool
	resultTTL    time.Duration
}

func NewVerifier(id string, key KeyPair, keys []EvidenceKey, policy VerifierPolicy, resultTTL time.Duration) (*Verifier, error) {
	if id == "" || policy.Revision == 0 || policy.MaxEvidenceAge <= 0 || resultTTL <= 0 {
		return nil, errors.New("verifier id, policy revision, freshness window, and result TTL are required")
	}
	m := map[string]EvidenceKey{}
	for _, k := range keys {
		if k.AttesterID == "" || k.TrustDomain == "" || k.KeyID == "" || len(k.Public) != ed25519.PublicKeySize {
			return nil, errors.New("complete attester evidence key binding is required")
		}
		idx := k.AttesterID + "\x00" + k.KeyID
		if _, ok := m[idx]; ok {
			return nil, fmt.Errorf("duplicate evidence key binding %q", idx)
		}
		m[idx] = k
	}
	return &Verifier{ID: id, key: key, evidenceKeys: m, policy: policy, usedNonces: map[string]bool{}, resultTTL: resultTTL}, nil
}

func (v *Verifier) Appraise(now time.Time, expectedNonce string, env EvidenceEnvelope) (Signed[AttestationResult], error) {
	if expectedNonce == "" {
		return Signed[AttestationResult]{}, errors.New("fresh challenge nonce is required")
	}
	e := env.Signed.Payload
	binding, ok := v.evidenceKeys[e.AttesterID+"\x00"+env.KeyID]
	if !ok {
		return Signed[AttestationResult]{}, errors.New("attester evidence key is not registered")
	}
	if binding.TrustDomain != e.TrustDomain {
		return Signed[AttestationResult]{}, errors.New("evidence trust-domain binding mismatch")
	}
	if !Verify(binding.Public, env.Signed) {
		return Signed[AttestationResult]{}, errors.New("evidence signature verification failed")
	}
	if e.Nonce != expectedNonce {
		return Signed[AttestationResult]{}, errors.New("evidence nonce does not match challenge")
	}
	nonceKey := e.AttesterID + "\x00" + e.Nonce
	if v.usedNonces[nonceKey] {
		return Signed[AttestationResult]{}, errors.New("evidence nonce replay detected")
	}
	if e.CollectedAt.After(now) || now.Sub(e.CollectedAt) > v.policy.MaxEvidenceAge {
		return Signed[AttestationResult]{}, errors.New("evidence is outside freshness window")
	}
	if !v.policy.AllowedEnvironmentClasses[e.EnvironmentClass] {
		return Signed[AttestationResult]{}, errors.New("environment class does not satisfy verifier policy")
	}
	if !v.policy.AllowedMeasurements[e.Measurement] {
		return Signed[AttestationResult]{}, errors.New("measurement does not satisfy verifier policy")
	}
	dg, err := digestJSON(e)
	if err != nil {
		return Signed[AttestationResult]{}, err
	}
	v.usedNonces[nonceKey] = true
	r := AttestationResult{AttesterID: e.AttesterID, TrustDomain: e.TrustDomain, EnvironmentClass: e.EnvironmentClass, EvidenceDigest: dg, VerifierID: v.ID, PolicyRevision: v.policy.Revision, VerifiedAt: now, ExpiresAt: now.Add(v.resultTTL)}
	return sign(v.key, r)
}

type PlacementRequirement struct {
	WorkerID                  string
	TrustDomain               string
	RequiredEnvironmentClass  string
	AcceptedVerifierID        string
	MinVerifierPolicyRevision uint64
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

func (rp *RelyingParty) AuthorizePlacement(now time.Time, req PlacementRequirement, result Signed[AttestationResult]) error {
	if req.WorkerID == "" || req.TrustDomain == "" || req.RequiredEnvironmentClass == "" || req.AcceptedVerifierID == "" || req.MinVerifierPolicyRevision == 0 {
		return errors.New("complete placement attestation requirement is required")
	}
	if !Verify(rp.verifierPub, result) {
		return errors.New("attestation result is not signed by the pinned verifier")
	}
	r := result.Payload
	if r.AttesterID != req.WorkerID || r.TrustDomain != req.TrustDomain {
		return errors.New("attestation result is not bound to the requested worker identity")
	}
	if r.VerifierID != req.AcceptedVerifierID {
		return errors.New("unexpected verifier identity")
	}
	if r.PolicyRevision < req.MinVerifierPolicyRevision {
		return errors.New("attestation result uses stale verifier policy")
	}
	if !r.ExpiresAt.After(now) {
		return errors.New("attestation result is expired")
	}
	if r.EnvironmentClass != req.RequiredEnvironmentClass {
		return errors.New("attested environment does not satisfy placement requirement")
	}
	return nil
}

func SignEvidence(k KeyPair, keyID string, e Evidence) (EvidenceEnvelope, error) {
	if keyID == "" || e.AttesterIDOHK\ÝÛXZ[OHK[\ÛY[Û\ÜÈOHKYX\Ý\[Y[OHKÛÙHOHKÛÛXÝY]\Ö\Ê
HÂ]\]Y[ÙQ[[Ü^ßK\ÜË]ÊÛÛ\]H]Y[ÙHY[È\H\]Z\YBB\Ë\HÚYÛËJBZY\OH[ÂB\]\]Y[ÙQ[[Ü^ßK\_B\]\]Y[ÙQ[[Ü^ÒÙ^RQÙ^RQÚYÛYßK[B