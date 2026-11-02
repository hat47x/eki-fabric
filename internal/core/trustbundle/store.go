package trustbundle

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"
)

// ErrNotInitialised is returned when a Store is used without a first version.
var ErrNotInitialised = errors.New("trustbundle: store has no current version")

// Store holds the versions this Control Plane has published, newest last, and
// the approvals that let each change through. It is the state ADR-0036 §2.2
// describes: a version is only added when its signature verifies and every
// issuer it adds or removes was approved.
//
// It is in-memory and does not persist; persistence is a later decision.
type Store struct {
	chain     []Signed
	approvals []Approval
}

// NewStore starts a store at the first version a deployment trusts (the
// out-of-band version of ADR-0036 §2.1). The signature is not checked here: the
// first version has no predecessor to check it against, so the deployment's
// configuration is what makes it trusted.
func NewStore(first Signed) (*Store, error) {
	if err := first.Bundle.Validate(); err != nil {
		return nil, err
	}
	if first.KeyID == "" {
		return nil, fmt.Errorf("%w: first version has no key id", ErrInvalidBundle)
	}
	return &Store{chain: []Signed{first}}, nil
}

// Current is the version a Worker should be using now.
func (s *Store) Current() (Signed, error) {
	if s == nil || len(s.chain) == 0 {
		return Signed{}, ErrNotInitialised
	}
	return s.chain[len(s.chain)-1], nil
}

// Approvals is a copy of the records kept so far, oldest first.
func (s *Store) Approvals() []Approval {
	if s == nil {
		return nil
	}
	return append([]Approval(nil), s.approvals...)
}

// Admit adds the next version. The signature must verify with the Control
// Plane key, the version must follow the current one, and every issuer the
// version adds or removes must carry an approval for that version.
func (s *Store) Admit(next Signed, key ed25519.PublicKey, approvals []Approval, now time.Time) error {
	current, err := s.Current()
	if err != nil {
		return err
	}
	if err := next.Verify(key); err != nil {
		return err
	}
	if err := CheckTransition(current.Bundle, next.Bundle, approvals); err != nil {
		return err
	}
	if err := next.Bundle.Current(now); err != nil {
		return err
	}
	s.chain = append(s.chain, next)
	s.approvals = append(s.approvals, approvals...)
	return nil
}

// Chain is a copy of the versions held, oldest first, for a Worker that has
// fallen behind and needs to follow the signatures forward.
func (s *Store) Chain() []Signed {
	if s == nil {
		return nil
	}
	return append([]Signed(nil), s.chain...)
}

// Append adds a version that was verified and stored earlier. It is for
// rebuilding a store from persisted history (#478); the production paths check
// what they add (Admit for the Control Plane, AcceptSigned for a Worker), so this
// method deliberately checks nothing.
func (s *Store) Append(signed Signed) {
	s.chain = append(s.chain, signed)
}
