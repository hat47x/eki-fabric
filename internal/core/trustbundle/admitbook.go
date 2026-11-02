package trustbundle

import (
	"crypto/ed25519"
	"fmt"
	"time"
)

// AdmitFromBook admits next using the approvals the book collected for its
// version, and drops them once the version is stored (ADR-0036 §3, #531).
//
// The approvals are released only after the store accepted the version: an
// approval that failed a check must stay in the book, or the Operator's decision
// would disappear exactly when it was needed.
func (s *Store) AdmitFromBook(next Signed, key ed25519.PublicKey, book *ApprovalBook, now time.Time) error {
	if book == nil {
		return fmt.Errorf("%w: no approval book", ErrUnapproved)
	}
	if err := s.Admit(next, key, book.For(next.Bundle.Version), now); err != nil {
		return err
	}
	book.Release(next.Bundle.Version)
	return nil
}
