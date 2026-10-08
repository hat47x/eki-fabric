package grant

import (
	"fmt"
	"time"
)

// Request describes one lease the caller wants to admit against a grant.
//
// ActiveLeases and ActivePercent describe the leases of the same grant and
// resource class that are already running. The caller supplies them, because
// the package keeps no lease state. ActivePercent is the sum of the percents
// those leases hold. Together they let the check bound total use of the class
// rather than only the size of one lease.
type Request struct {
	ResourceClass ResourceClass
	Percent       int
	ActiveLeases  int
	ActivePercent int
}

// Admit decides whether req may start under g at the decision time at. It
// returns nil only when every check passes. Otherwise it returns an error that
// wraps one of the package's sentinel errors.
//
// The checks run in this order, and the first failure wins. A nil grant and a
// zero decision time are refused first, then a malformed request. After that,
// the grant must be active at at, the resource class must be granted, the
// active percent plus the requested percent must not exceed the class ceiling,
// and the active lease count must be below the lease cap, when one is set.
//
// at is the decision time, so the caller must pass the current time. A time
// before the revocation admits a lease, because the grant really was active
// then.
func Admit(g Grant, at time.Time, req Request) error {
	if !g.issued {
		return ErrNilGrant
	}
	if at.IsZero() {
		return fmt.Errorf("%w: decision time", ErrMissingField)
	}
	if err := checkRequest(req); err != nil {
		return err
	}
	if !g.ActiveAt(at) {
		return g.inactiveError(at)
	}
	ceiling, ok := g.Ceiling(req.ResourceClass)
	if !ok {
		return ErrResourceNotGranted
	}
	if req.ActivePercent+req.Percent > ceiling {
		return ErrCeilingExceeded
	}
	if g.maxLeases > 0 && req.ActiveLeases >= g.maxLeases {
		return ErrLeaseLimitReached
	}
	return nil
}

// checkRequest refuses a malformed request. It also refuses active counts that
// contradict each other, because a caller that miscounts must fail closed
// rather than admit against the wrong totals.
func checkRequest(req Request) error {
	if err := checkResourceClass(req.ResourceClass); err != nil {
		return err
	}
	if req.Percent < 1 || req.Percent > MaxPercent {
		return ErrPercentOutOfRange
	}
	if req.ActiveLeases < 0 || req.ActivePercent < 0 {
		return fmt.Errorf("%w: negative active count", ErrInvalidRequest)
	}
	if req.ActivePercent > MaxPercent {
		return ErrPercentOutOfRange
	}
	// Every lease holds at least one percent, so the percent total bounds the
	// lease count, and an empty set of leases holds no percent.
	if req.ActiveLeases > req.ActivePercent {
		return fmt.Errorf("%w: more leases than active percent", ErrInvalidRequest)
	}
	if (req.ActiveLeases == 0) != (req.ActivePercent == 0) {
		return fmt.Errorf("%w: lease count and active percent disagree", ErrInvalidRequest)
	}
	return nil
}
