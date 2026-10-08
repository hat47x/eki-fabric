package grant

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// Bounds on caller-supplied input. A value outside a bound is refused, never
// truncated.
const (
	// MaxIdentityBytes bounds a provider, beneficiary, issuer, or revoker name.
	MaxIdentityBytes = 128
	// MaxResourceClassBytes bounds one resource class name.
	MaxResourceClassBytes = 64
	// MaxResourceClasses bounds the number of resource classes in one grant.
	MaxResourceClasses = 16
	// MaxGrantWindow bounds the validity window of one grant.
	MaxGrantWindow = 366 * 24 * time.Hour
	// MaxPercent is the largest percent a ceiling, request, or active total may hold.
	MaxPercent = 100

	// maxLeaseLimit is MaxPercent because each active lease holds at least one
	// percent, so a single class can never have more than 100 active leases.
	maxLeaseLimit = MaxPercent
)

// Identity names a provider, a beneficiary, an issuer, or a revoker. The
// package checks only the shape of a name. Authentication is the caller's job.
type Identity string

// ResourceClass names one kind of compute, for example a class of GPU.
type ResourceClass string

// Purpose is the closed vocabulary of reasons a grant may be issued for.
type Purpose string

const (
	PurposePublicResearch Purpose = "public-research"
	PurposeOpenSource     Purpose = "open-source"
	PurposePublicInterest Purpose = "public-interest"
)

// Valid reports whether p belongs to the closed purpose vocabulary.
func (p Purpose) Valid() bool {
	switch p {
	case PurposePublicResearch, PurposeOpenSource, PurposePublicInterest:
		return true
	}
	return false
}

// ReasonCategory is the closed vocabulary of reasons a provider may record
// when it revokes a grant. A category says why, not who is to blame.
type ReasonCategory string

const (
	ReasonCapacityReclaim  ReasonCategory = "capacity-reclaim"
	ReasonPolicyChange     ReasonCategory = "policy-change"
	ReasonProviderWithdraw ReasonCategory = "provider-withdrawal"
	ReasonSecurityIncident ReasonCategory = "security-incident"
)

// Valid reports whether r belongs to the closed reason vocabulary.
func (r ReasonCategory) Valid() bool {
	switch r {
	case ReasonCapacityReclaim, ReasonPolicyChange, ReasonProviderWithdraw, ReasonSecurityIncident:
		return true
	}
	return false
}

// ResourceCeiling caps how much of one resource class a grant may use at the
// same time, as a percent of that class.
type ResourceCeiling struct {
	Class   ResourceClass
	Percent int
}

// Spec is the input to Issue. Every field is required except
// MaxConcurrentLeases, where zero means no lease-count cap was set.
type Spec struct {
	Provider            Identity
	Beneficiary         Identity
	Ceilings            []ResourceCeiling
	IssuedAt            time.Time
	ExpiresAt           time.Time
	MaxConcurrentLeases int
	Purpose             Purpose
	PolicyRevision      contract.Digest
}

// Revocation records that a provider stopped a grant. It takes effect at
// EffectiveAt and the grant admits no lease from that moment on.
type Revocation struct {
	Reason      ReasonCategory
	EffectiveAt time.Time
}

// Grant is an issued Compute Grant. Its zero value is a nil grant. Only Issue
// creates a usable Grant, and Grant is immutable after that: Revoke returns a
// copy rather than changing the receiver.
type Grant struct {
	issued      bool
	provider    Identity
	beneficiary Identity
	ceilings    []ResourceCeiling
	issuedAt    time.Time
	expiresAt   time.Time
	maxLeases   int
	purpose     Purpose
	policy      contract.Digest
	revoked     bool
	revocation  Revocation
}

// Issue builds a grant from spec. The issuer must be the provider that spec
// names. A beneficiary cannot issue a grant to itself, and a provider cannot
// name itself as its own beneficiary.
func Issue(issuer Identity, spec Spec) (Grant, error) {
	if err := checkIdentity("issuer", issuer); err != nil {
		return Grant{}, err
	}
	if err := checkIdentity("provider", spec.Provider); err != nil {
		return Grant{}, err
	}
	if err := checkIdentity("beneficiary", spec.Beneficiary); err != nil {
		return Grant{}, err
	}
	if spec.Provider == spec.Beneficiary || issuer == spec.Beneficiary {
		return Grant{}, ErrSelfGrant
	}
	if issuer != spec.Provider {
		return Grant{}, ErrIssuerMismatch
	}
	ceilings, err := normalizeCeilings(spec.Ceilings)
	if err != nil {
		return Grant{}, err
	}
	issuedAt, expiresAt, err := checkWindow(spec.IssuedAt, spec.ExpiresAt)
	if err != nil {
		return Grant{}, err
	}
	if spec.MaxConcurrentLeases < 0 || spec.MaxConcurrentLeases > maxLeaseLimit {
		return Grant{}, ErrInvalidLeaseLimit
	}
	if !spec.Purpose.Valid() {
		return Grant{}, ErrUnknownPurpose
	}
	if spec.PolicyRevision.IsZero() {
		return Grant{}, fmt.Errorf("%w: policy revision", ErrMissingField)
	}
	return Grant{
		issued:      true,
		provider:    spec.Provider,
		beneficiary: spec.Beneficiary,
		ceilings:    ceilings,
		issuedAt:    issuedAt,
		expiresAt:   expiresAt,
		maxLeases:   spec.MaxConcurrentLeases,
		purpose:     spec.Purpose,
		policy:      spec.PolicyRevision,
	}, nil
}

// Provider returns the provider the grant names.
func (g Grant) Provider() Identity { return g.provider }

// Beneficiary returns the beneficiary the grant names.
func (g Grant) Beneficiary() Identity { return g.beneficiary }

// Ceilings returns a copy of the per-class ceilings, sorted by class name.
func (g Grant) Ceilings() []ResourceCeiling { return slices.Clone(g.ceilings) }

// IssuedAt returns the start of the validity window, in UTC.
func (g Grant) IssuedAt() time.Time { return g.issuedAt }

// ExpiresAt returns the end of the validity window, which is exclusive, in UTC.
func (g Grant) ExpiresAt() time.Time { return g.expiresAt }

// MaxConcurrentLeases returns the lease-count cap. Zero means no cap was set,
// and the percent ceilings still apply.
func (g Grant) MaxConcurrentLeases() int { return g.maxLeases }

// Purpose returns the grant's purpose tag.
func (g Grant) Purpose() Purpose { return g.purpose }

// PolicyRevision returns the digest of the policy revision the grant was issued under.
func (g Grant) PolicyRevision() contract.Digest { return g.policy }

// Revocation returns the revocation record, and false if the grant was never revoked.
func (g Grant) Revocation() (Revocation, bool) {
	if !g.revoked {
		return Revocation{}, false
	}
	return g.revocation, true
}

// Ceiling returns the percent ceiling for class, and false if the grant does
// not cover that class.
func (g Grant) Ceiling(class ResourceClass) (int, bool) {
	for _, c := range g.ceilings {
		if c.Class == class {
			return c.Percent, true
		}
	}
	return 0, false
}

// ActiveAt reports whether the grant was active at the instant at. A grant is
// active from IssuedAt up to ExpiresAt, exclusive, and stops being active at
// the revocation's effective time. Querying an earlier time still answers
// whether the grant was active then.
func (g Grant) ActiveAt(at time.Time) bool {
	if !g.issued || at.IsZero() {
		return false
	}
	if at.Before(g.issuedAt) || !at.Before(g.expiresAt) {
		return false
	}
	if g.revoked && !at.Before(g.revocation.EffectiveAt) {
		return false
	}
	return true
}

// inactiveError names why the grant is not active at at, without echoing times.
func (g Grant) inactiveError(at time.Time) error {
	switch {
	case at.Before(g.issuedAt):
		return fmt.Errorf("%w: not yet issued", ErrGrantNotActive)
	case !at.Before(g.expiresAt):
		return fmt.Errorf("%w: expired", ErrGrantNotActive)
	default:
		return fmt.Errorf("%w: revoked", ErrGrantNotActive)
	}
}

func checkIdentity(field string, v Identity) error {
	s := string(v)
	if s == "" {
		return fmt.Errorf("%w: %s", ErrMissingField, field)
	}
	if len(s) > MaxIdentityBytes {
		return fmt.Errorf("%w: %s", ErrTooLong, field)
	}
	for i := 0; i < len(s); i++ {
		// Printable ASCII without spaces. Control characters and non-ASCII
		// bytes are refused rather than escaped.
		if s[i] < 0x21 || s[i] > 0x7e {
			return fmt.Errorf("%w: %s", ErrInvalidField, field)
		}
	}
	return nil
}

func checkResourceClass(c ResourceClass) error {
	s := string(c)
	if s == "" {
		return fmt.Errorf("%w: resource class", ErrMissingField)
	}
	if len(s) > MaxResourceClassBytes {
		return fmt.Errorf("%w: resource class", ErrTooLong)
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		lowerOrDigit := (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
		if i == 0 && !lowerOrDigit {
			return fmt.Errorf("%w: resource class", ErrInvalidField)
		}
		if i > 0 && !lowerOrDigit && b != '.' && b != '_' && b != '-' {
			return fmt.Errorf("%w: resource class", ErrInvalidField)
		}
	}
	return nil
}

// normalizeCeilings copies the ceilings, sorts them by class, and rejects an
// empty list, too many classes, duplicate classes, and percents outside 1..100.
func normalizeCeilings(in []ResourceCeiling) ([]ResourceCeiling, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("%w: resource classes", ErrMissingField)
	}
	if len(in) > MaxResourceClasses {
		return nil, ErrTooManyResourceClasses
	}
	out := slices.Clone(in)
	for _, c := range out {
		if err := checkResourceClass(c.Class); err != nil {
			return nil, err
		}
		if c.Percent < 1 || c.Percent > MaxPercent {
			return nil, ErrPercentOutOfRange
		}
	}
	slices.SortFunc(out, func(a, b ResourceCeiling) int {
		return cmp.Compare(a.Class, b.Class)
	})
	for i := 1; i < len(out); i++ {
		if out[i].Class == out[i-1].Class {
			return nil, ErrDuplicateResourceClass
		}
	}
	return out, nil
}

// checkWindow returns both times in UTC without a monotonic reading, so that
// equal instants compare and encode the same way.
func checkWindow(issuedAt, expiresAt time.Time) (time.Time, time.Time, error) {
	if issuedAt.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: issued at", ErrMissingField)
	}
	if expiresAt.IsZero() {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: expires at", ErrMissingField)
	}
	i, e := normalizeTime(issuedAt), normalizeTime(expiresAt)
	if !e.After(i) {
		return time.Time{}, time.Time{}, ErrInvalidWindow
	}
	if e.Sub(i) > MaxGrantWindow {
		return time.Time{}, time.Time{}, ErrWindowTooLong
	}
	return i, e, nil
}

func normalizeTime(t time.Time) time.Time {
	return t.Round(0).UTC()
}
