// Package trustanchor loads the Worker's trust anchor for signed Leases and
// WorkOrders from a local file, and fails closed.
//
// The anchor is the set of public keys the Worker accepts for the Lease and
// WorkOrder Authority roles, the Trust Bundle revision and the time it was last
// validated, the Policy revision the Worker accepts, and the limits that bound
// how old and how long-lived a signed decision may be. Nothing is taken from
// the Control Plane's delivery: a Worker that trusted keys delivered next to the
// bytes they sign would trust whoever sent them.
//
// This is the test profile's file format. How an operator provisions, rotates
// and revokes these keys, and who validates the bundle, is open (#78).
package trustanchor

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
)

// ErrInvalid means the trust anchor file is missing, unreadable or unusable.
var ErrInvalid = errors.New("invalid trust anchor")

const maxFileBytes = 1 << 20

// File is the JSON document of the trust anchor.
type File struct {
	TrustDomain    string `json:"trust_domain"`
	PolicyRevision string `json:"policy_revision"`
	BundleRevision uint64 `json:"bundle_revision"`
	// ValidatedAt is when the bundle was last validated. The verifier refuses
	// a bundle older than MaxBundleStalenessSeconds.
	ValidatedAt                time.Time `json:"validated_at"`
	MaxClockSkewSeconds        int64     `json:"max_clock_skew_seconds"`
	MaxBundleStalenessSeconds  int64     `json:"max_bundle_staleness_seconds"`
	MaxDecisionLifetimeSeconds int64     `json:"max_decision_lifetime_seconds"`
	Keys                       []Key     `json:"keys"`
}

// Key is one Authority public key.
type Key struct {
	KeyID            string    `json:"key_id"`
	Role             string    `json:"role"`
	Principal        string    `json:"principal"`
	Algorithm        string    `json:"algorithm"`
	PublicKey        string    `json:"public_key"` // base64 of the raw Ed25519 public key
	SigningNotBefore time.Time `json:"signing_not_before"`
	SigningNotAfter  time.Time `json:"signing_not_after"`
	KeyPolicy        string    `json:"key_policy_revision"`
}

// Anchor is a loaded, validated trust anchor.
type Anchor struct {
	Boundary       *decisiontrust.VerifierBoundary
	TrustDomain    string
	PolicyRevision string
	bundle         decisiontrust.TrustBundleMetadata
}

// TrustBundle returns the bundle metadata the verifiers check for freshness.
func (a *Anchor) TrustBundle() decisiontrust.TrustBundleMetadata { return a.bundle }

// Load reads and validates the file. Every problem is an ErrInvalid.
func Load(path string) (*Anchor, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: no trust anchor file is configured", ErrInvalid)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(raw) > maxFileBytes {
		return nil, fmt.Errorf("%w: file is too large", ErrInvalid)
	}
	var doc File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: trailing data", ErrInvalid)
	}
	return Build(doc)
}

// Build validates a document and builds the verifier boundary.
func Build(doc File) (*Anchor, error) {
	switch {
	case doc.TrustDomain == "" || doc.PolicyRevision == "":
		return nil, fmt.Errorf("%w: trust_domain and policy_revision are required", ErrInvalid)
	case doc.BundleRevision == 0 || doc.ValidatedAt.IsZero():
		return nil, fmt.Errorf("%w: bundle_revision and validated_at are required", ErrInvalid)
	case doc.MaxClockSkewSeconds <= 0 || doc.MaxBundleStalenessSeconds <= 0 || doc.MaxDecisionLifetimeSeconds <= 0:
		return nil, fmt.Errorf("%w: the clock skew, bundle staleness and decision lifetime limits are required", ErrInvalid)
	case len(doc.Keys) == 0:
		return nil, fmt.Errorf("%w: no keys", ErrInvalid)
	}
	keyTime := doc.Keys[0].SigningNotBefore
	for _, k := range doc.Keys {
		if k.SigningNotBefore.Before(keyTime) {
			keyTime = k.SigningNotBefore
		}
	}
	registry := decisiontrust.NewRegistry(keyTime)
	roles := map[authority.Role]bool{}
	for i, k := range doc.Keys {
		role := authority.Role(k.Role)
		if k.Algorithm != string(decisiontrust.AlgorithmEdDSA) {
			return nil, fmt.Errorf("%w: key %d: only EdDSA is supported", ErrInvalid, i)
		}
		pub, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: key %d: public_key must be a base64 Ed25519 public key", ErrInvalid, i)
		}
		record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
			ID: decisiontrust.KeyID(k.KeyID), AuthorityRole: role, AuthorityPrincipal: k.Principal,
			TrustDomain: doc.TrustDomain, Algorithm: decisiontrust.AlgorithmEdDSA,
			KeyType: decisiontrust.KeyTypeOKP, Curve: decisiontrust.CurveEd25519, PublicKey: ed25519.PublicKey(pub),
			SigningNotBefore: k.SigningNotBefore, SigningNotAfter: k.SigningNotAfter, PolicyRevision: k.KeyPolicy,
		}, keyTime)
		if err != nil {
			return nil, fmt.Errorf("%w: key %d: %v", ErrInvalid, i, err)
		}
		if err := registry.Register(record, keyTime); err != nil {
			return nil, fmt.Errorf("%w: key %d: %v", ErrInvalid, i, err)
		}
		if err := registry.Activate(record.ID(), keyTime); err != nil {
			return nil, fmt.Errorf("%w: key %d: %v", ErrInvalid, i, err)
		}
		roles[role] = true
	}
	if !roles[authority.RoleLease] || !roles[authority.RoleWorkOrder] {
		return nil, fmt.Errorf("%w: keys for both the LSE-1 and the WOR-1 roles are required", ErrInvalid)
	}
	lifetime := time.Duration(doc.MaxDecisionLifetimeSeconds) * time.Second
	policy, err := decisiontrust.NewVerificationPolicy(decisiontrust.VerificationPolicyConfig{
		MaxClockSkew:            time.Duration(doc.MaxClockSkewSeconds) * time.Second,
		MaxTrustBundleStaleness: time.Duration(doc.MaxBundleStalenessSeconds) * time.Second,
		AllowedAlgorithms:       []decisiontrust.Algorithm{decisiontrust.AlgorithmEdDSA},
		MaxDecisionLifetime: map[authority.DecisionKind]time.Duration{
			authority.DecisionLease:     lifetime,
			authority.DecisionWorkOrder: lifetime,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	boundary, err := decisiontrust.NewVerifierBoundary(registry, policy)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return &Anchor{
		Boundary: boundary, TrustDomain: doc.TrustDomain, PolicyRevision: doc.PolicyRevision,
		bundle: decisiontrust.TrustBundleMetadata{Revision: doc.BundleRevision, ValidatedAt: doc.ValidatedAt.UTC()},
	}, nil
}
