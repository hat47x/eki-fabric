package trustbundle

import (
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrUnapproved is returned when a version change has no recorded approval for
// an issuer it adds or removes (ADR-0036 §2.2). The Control Plane's operator
// approves each change, and the record says who, when, and which version.
var ErrUnapproved = errors.New("trustbundle: version change has no approval")

// Operation is what an approval authorises.
type Operation string

const (
	// OperationAdd trusts a new issuer.
	OperationAdd Operation = "add"
	// OperationRemove stops trusting an issuer. It is not the same decision as
	// adding: removing cuts off the certificates that issuer signed.
	OperationRemove Operation = "remove"
)

// Approval is the record of one operator decision about one issuer in one
// version.
type Approval struct {
	Version       uint64
	Operation     Operation
	Issuer        contract.Digest
	ApprovedBy    string
	ApprovedAt    time.Time
	Justification string
}

// Validate reports whether the approval is complete enough to be an audit
// record.
func (a Approval) Validate() error {
	if a.Version == 0 {
		return fmt.Errorf("%w: approval has no version", ErrUnapproved)
	}
	if a.Operation != OperationAdd && a.Operation != OperationRemove {
		return fmt.Errorf("%w: approval has operation %q", ErrUnapproved, a.Operation)
	}
	if a.Issuer.IsZero() {
		return fmt.Errorf("%w: approval names no issuer", ErrUnapproved)
	}
	if a.ApprovedBy == "" || a.ApprovedAt.IsZero() {
		return fmt.Errorf("%w: approval has no approver or time", ErrUnapproved)
	}
	return nil
}

// CheckTransition reports whether every issuer that next adds or removes,
// compared with previous, has an approval at next's version with the matching
// operation. Approvals that name an issuer the version does not change are
// refused: they would make the audit trail disagree with the bundle.
func CheckTransition(previous, next Bundle, approvals []Approval) error {
	if err := next.Follows(previous); err != nil {
		return err
	}
	byIssuer := make(map[contract.Digest]Approval, len(approvals))
	for _, approval := range approvals {
		if err := approval.Validate(); err != nil {
			return err
		}
		if approval.Version != next.Version {
			return fmt.Errorf("%w: approval for version %d, change is %d", ErrUnapproved, approval.Version, next.Version)
		}
		byIssuer[approval.Issuer] = approval
	}
	before := issuerSet(previous)
	after := issuerSet(next)
	for issuer := range after {
		if _, held := before[issuer]; held {
			continue
		}
		approval, ok := byIssuer[issuer]
		if !ok || approval.Operation != OperationAdd {
			return fmt.Errorf("%w: version %d adds %s", ErrUnapproved, next.Version, issuer)
		}
	}
	for issuer := range before {
		if _, kept := after[issuer]; kept {
			continue
		}
		approval, ok := byIssuer[issuer]
		if !ok || approval.Operation != OperationRemove {
			return fmt.Errorf("%w: version %d removes %s", ErrUnapproved, next.Version, issuer)
		}
	}
	for issuer := range byIssuer {
		_, was := before[issuer]
		_, is := after[issuer]
		if was == is {
			return fmt.Errorf("%w: version %d does not change %s", ErrUnapproved, next.Version, issuer)
		}
	}
	return nil
}

func issuerSet(b Bundle) map[contract.Digest]struct{} {
	set := make(map[contract.Digest]struct{}, len(b.Issuers))
	for _, issuer := range b.Issuers {
		set[issuer.Fingerprint] = struct{}{}
	}
	return set
}
