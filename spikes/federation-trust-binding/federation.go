package federation

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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

func Verify[T any](pub ed25519.PublicKey, s Signed[T]) bool {
	b, err := canonical(s.Payload)
	if err != nil {
		return false
	}
	return ed25519.Verify(pub, b, s.Signature)
}

type DomainKey struct {
	KeyID     string            `json:"key_id"`
	PublicKey ed25519.PublicKey `json:"public_key"`
}

type BundleUpdate struct {
	TrustDomain string      `json:"trust_domain"`
	Version     uint64      `json:"version"`
	Keys        []DomainKey `json:"keys"`
}

type Identity struct {
	TrustDomain string    `json:"trust_domain"`
	Subject     string    `json:"subject"`
	KeyID       string    `json:"key_id"`
	Expires     time.Time `json:"expires"`
}

type Bundle struct {
	Version uint64
	Keys    map[string]ed25519.PublicKey
}

type Store struct {
	adminPub ed25519.PublicKey
	bundles  map[string]Bundle
}

func NewStore(adminPub ed25519.PublicKey) *Store {
	return &Store{adminPub: append(ed25519.PublicKey(nil), adminPub...), bundles: map[string]Bundle{}}
}

func validateDomain(domain string) error {
	if domain == "" {
		return errors.New("trust domain is required")
	}
	return nil
}

func validateUpdate(u BundleUpdate) error {
	if err := validateDomain(u.TrustDomain); err != nil {
		return err
	}
	if u.Version == 0 {
		return errors.New("bundle version must be positive")
	}
	if len(u.Keys) == 0 {
		return errors.New("bundle must contain at least one key")
	}

	seen := map[string]struct{}{}
	for _, k := range u.Keys {
		if k.KeyID == "" || len(k.PublicKey) != ed25519.PublicKeySize {
			return errors.New("valid key id and Ed25519 public key are required")
		}
		if _, ok := seen[k.KeyID]; ok {
			return fmt.Errorf("duplicate key id %q", k.KeyID)
		}
		seen[k.KeyID] = struct{}{}
	}
	return nil
}

func (s *Store) Apply(update Signed[BundleUpdate]) error {
	if !Verify(s.adminPub, update) {
		return errors.New("bundle update is not signed by the pinned federation administrator")
	}

	u := update.Payload
	if err := validateUpdate(u); err != nil {
		return err
	}
	if current, ok := s.bundles[u.TrustDomain]; ok && u.Version <= current.Version {
		return fmt.Errorf("bundle version rollback or replay: current=%d incoming=%d", current.Version, u.Version)
	}

	keys := make(map[string]ed25519.PublicKey, len(u.Keys))
	for _, k := range u.Keys {
		keys[k.KeyID] = append(ed25519.PublicKey(nil), k.PublicKey...)
	}
	s.bundles[u.TrustDomain] = Bundle{Version: u.Version, Keys: keys}
	return nil
}

func (s *Store) Domains() []string {
	out := make([]string, 0, len(s.bundles))
	for domain := range s.bundles {
		out = append(out, domain)
	}
	sort.Strings(out)
	return out
}

func (s *Store) VerifyIdentity(expectedDomain string, identity Signed[Identity], now time.Time) error {
	if err := validateDomain(expectedDomain); err != nil {
		return err
	}
	p := identity.Payload
	if p.TrustDomain != expectedDomain {
		return errors.New("identity trust domain does not match expected namespace")
	}
	if p.Subject == "" || p.KeyID == "" {
		return errors.New("subject and key id are required")
	}
	if !p.Expires.After(now) {
		return errors.New("identity is expired")
	}

	bundle, ok := s.bundles[expectedDomain]
	if !ok {
		return errors.New("trust domain is not federated")
	}
	pub, ok := bundle.Keys[p.KeyID]
	if !ok {
		return errors.New("identity key is not in the bound trust-domain bundle")
	}
	if !Verify(pub, identity) {
		return errors.New("identity signature verification failed")
	}
	return nil
}

func SignBundleUpdate(admin KeyPair, u BundleUpdate) (Signed[BundleUpdate], error) {
	if err := validateUpdate(u); err != nil {
		return Signed[BundleUpdate]{}, err
	}
	return sign(admin, u)
}

func SignIdentity(issuer KeyPair, domain, keyID, subject string, expires time.Time) (Signed[Identity], error) {
	if err := validateDomain(domain); err != nil {
		return Signed[Identity]{}, err
	}
	if keyID == "" || subject == "" || expires.IsZero() {
		return Signed[Identity]{}, errors.New("key id, subject, and expiry are required")
	}
	return sign(issuer, Identity{TrustDomain: domain, KeyID: keyID, Subject: subject, Expires: expires})
}
