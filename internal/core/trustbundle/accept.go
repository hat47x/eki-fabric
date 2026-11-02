package trustbundle

import (
	"crypto/ed25519"
	"fmt"
	"time"
)

// AcceptSigned is what a Worker does when it receives a version: verify the
// signature with the key the version names, refuse anything that does not follow
// the version it holds, and ignore a duplicate of the version it already has
// (ADR-0036 §2.1).
//
// It is the receiving side of Admit: a Worker has no approvals to check, because
// it is not the party that decides who to trust; it only accepts what the
// Control Plane signed and what follows what it holds.
func (s *Store) AcceptSigned(next Signed, key ed25519.PublicKey, now time.Time) error {
	current, err := s.Current()
	if err != nil {
		return err
	}
	if err := next.Verify(key); err != nil {
		return err
	}
	switch {
	case next.Bundle.Version == current.Bundle.Version:
		if next.Bundle.Digest() != current.Bundle.Digest() {
			return fmt.Errorf("%w: version %d", ErrConfigDiverged, next.Bundle.Version)
		}
		return next.Bundle.Current(now)
	case next.Bundle.Version < current.Bundle.Version:
		// A Worker never goes back: an old version may have been revoked.
		return fmt.Errorf("%w: version %d after %d", ErrNotSuccessor, next.Bundle.Version, current.Bundle.Version)
	default:
		if err := next.Bundle.Follows(current.Bundle); err != nil {
			return err
		}
		if err := next.Bundle.Current(now); err != nil {
			return err
		}
		s.chain = append(s.chain, next)
		return nil
	}
}
