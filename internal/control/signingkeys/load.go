// Package signingkeys loads the Control Plane's own signing keys for the
// PLC-1, LSE-1, and WOR-1 Authority roles from a local file, and fails closed.
//
// The file is the private counterpart of the Worker's trust anchor
// (internal/agent/trustanchor): the same key fields plus the Ed25519 seed of
// each signing key. Only the keys this process signs with belong here; a key
// that only verifies belongs in a trust anchor instead.
//
// This is the test profile's file format. How an operator provisions, rotates
// and revokes these keys, and where a production process keeps its private
// keys, is open (#78).
package signingkeys

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

// ErrInvalid means the signing key file is missing, unreadable or unusable.
var ErrInvalid = errors.New("invalid signing key file")

const maxFileBytes = 1 << 20

// File is the JSON document of the Control Plane's signing keys.
type File struct {
	TrustDomain string `json:"trust_domain"`
	Keys        []Key  `json:"keys"`
}

// Key is one Authority signing key.
type Key struct {
	KeyID     string `json:"key_id"`
	Role      string `json:"role"`
	Principal string `json:"principal"`
	Algorithm string `json:"algorithm"`
	// PrivateKey is the base64 of the raw Ed25519 seed.
	PrivateKey       string    `json:"private_key"`
	PublicKey        string    `json:"public_key"` // base64 of the raw Ed25519 public key
	SigningNotBefore time.Time `json:"signing_not_before"`
	SigningNotAfter  time.Time `json:"signing_not_after"`
	KeyPolicy        string    `json:"key_policy_revision"`
}

// Keys is a loaded, validated set of signing keys.
type Keys struct {
	Registry *decisiontrust.Registry
	Signer   *decisiontrust.ReferenceSoftwareSigner
}

// Load reads and validates the file. Every problem is an ErrInvalid.
func Load(path string) (*Keys, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: no signing key file is configured", ErrInvalid)
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

// Build validates a document and builds the registry and signer.
func Build(doc File) (*Keys, error) {
	switch {
	case doc.TrustDomain == "":
		return nil, fmt.Errorf("%w: trust_domain is required", ErrInvalid)
	case len(doc.Keys) == 0:
		return nil, fmt.Errorf("%w: no keys", ErrInvalid)
	}
	// All keys are registered relative to the earliest validity start, which
	// is also how the trust anchor builds its registry.
	keyTime := doc.Keys[0].SigningNotBefore
	for _, k := range doc.Keys {
		if k.SigningNotBefore.Before(keyTime) {
			keyTime = k.SigningNotBefore
		}
	}
	registry := decisiontrust.NewRegistry(keyTime)
	signer, err := decisiontrust.NewReferenceSoftwareSigner(registry)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	roles := map[authority.Role]bool{}
	for i, k := range doc.Keys {
		role := authority.Role(k.Role)
		if k.Algorithm != string(decisiontrust.AlgorithmEdDSA) {
			return nil, fmt.Errorf("%w: key %d: only EdDSA is supported", ErrInvalid, i)
		}
		seed, err := base64.StdEncoding.DecodeString(k.PrivateKey)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%w: key %d: private_key must be a base64 Ed25519 seed", ErrInvalid, i)
		}
		private := ed25519.NewKeyFromSeed(seed)
		pub, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil || len(pub) != ed25519.PublicKeySize || !bytes.Equal(pub, private.Public().(ed25519.PublicKey)) {
			return nil, fmt.Errorf("%w: key %d: public_key does not match private_key", ErrInvalid, i)
		}
		record, err := decisiontrust.NewKeyRecord(decisiontrust.KeyConfig{
			ID: decisiontrust.KeyID(k.KeyID), AuthorityRole: role, AuthorityPrincipal: k.Principal,
			TrustDomain: doc.TrustDomain, Algorithm: decisiontrust.AlgorithmEdDSA,
			KeyType: decisiontrust.KeyTypeOKP, Curve: decisiontrust.CurveEd25519, PublicKey: private.Public(),
			SigningNotBefore: k.SigningNotBefore, SigningNotAfter: k.SigningNotAfter, PolicyRevision: k.KeyPolicy,
		}, keyTime)
		if err != nil {
			return nil, fmt.Errorf("%w: key %d: %v", ErrInvalid, i, err)
		}
		for _, step := range []struct {
			name string
			do   func() error
		}{
			{"register", func() error { return registry.Register(record, keyTime) }},
			{"activate", func() error { return registry.Activate(record.ID(), keyTime) }},
			{"set primary", func() error { return registry.SetPrimary(record.ID(), keyTime) }},
			{"load private key", func() error { return signer.RegisterPrivateKey(record.ID(), private) }},
		} {
			if err := step.do(); err != nil {
				return nil, fmt.Errorf("%w: key %d: %s: %v", ErrInvalid, i, step.name, err)
			}
		}
		roles[role] = true
	}
	for _, role := range []authority.Role{authority.RolePlacementAuthorization, authority.RoleLease, authority.RoleWorkOrder} {
		if !roles[role] {
			return nil, fmt.Errorf("%w: a key for the %s role is required", ErrInvalid, role)
		}
	}
	return &Keys{Registry: registry, Signer: signer}, nil
}
