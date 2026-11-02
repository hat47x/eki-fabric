package trustbundle

import (
	"fmt"
	"sort"
	"sync"
)

// ApprovalBook collects the approvals that apply to versions not yet written. An
// Operator approves an operation on the next version; the version itself is
// signed and stored later, and the approvals travel with it (ADR-0036 §3).
//
// The book is in memory on purpose. An approval that was never used to store a
// version has no effect, so losing it on restart loses nothing that mattered: the
// Operator approves again.
type ApprovalBook struct {
	mu      sync.Mutex
	byKey   map[string]Approval
	byVer   map[uint64][]string
	sealing map[string]bool
}

// NewApprovalBook returns an empty book.
func NewApprovalBook() *ApprovalBook {
	return &ApprovalBook{byKey: map[string]Approval{}, byVer: map[uint64][]string{}}
}

func approvalKey(a Approval) string {
	return fmt.Sprintf("%d|%s|%s|%s", a.Version, a.Operation, a.Issuer.String(), a.ApprovedBy)
}

// Add records an approval. It refuses an incomplete one, and it refuses a second
// approval of the same operation on the same issuer for the same version by the
// same Operator: repeating a decision is not a second decision, and keeping both
// would make the record look like two people agreed.
func (b *ApprovalBook) Add(approval Approval) error {
	if err := approval.Validate(); err != nil {
		return err
	}
	key := approvalKey(approval)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, seen := b.byKey[key]; seen {
		return fmt.Errorf("%w: version %d already has this approval from %s", ErrDuplicateApproval, approval.Version, approval.ApprovedBy)
	}
	b.byKey[key] = approval
	b.byVer[approval.Version] = append(b.byVer[approval.Version], key)
	return nil
}

// For returns the approvals recorded for a version, oldest first by approval
// time. The returned slice is a copy: a caller cannot change the book by sorting
// it.
func (b *ApprovalBook) For(version uint64) []Approval {
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := b.byVer[version]
	out := make([]Approval, 0, len(keys))
	for _, key := range keys {
		out = append(out, b.byKey[key])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ApprovedAt.Before(out[j].ApprovedAt) })
	return out
}

// Release drops the approvals for a version once they have been stored with it.
// Keeping them would attach last version's decisions to a later one.
func (b *ApprovalBook) Release(version uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, key := range b.byVer[version] {
		delete(b.byKey, key)
	}
	delete(b.byVer, version)
}

// Len reports how many approvals are held for a version.
func (b *ApprovalBook) Len(version uint64) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.byVer[version])
}

// ErrDuplicateApproval means the same Operator already approved the same
// operation on the same issuer for that version.
var ErrDuplicateApproval = fmt.Errorf("trustbundle: duplicate approval")
