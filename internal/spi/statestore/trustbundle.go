package statestore

import (
	"context"
	"errors"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrTrustBundleConflict is returned when a version is written twice with
// different content. Versions are append-only: a version number identifies one
// decision, and rewriting it would erase what Workers were told.
var ErrTrustBundleConflict = errors.New("statestore: trust bundle version already recorded")

// TrustBundleRecord is one published version of the Control Plane's trust
// bundle (ADR-0036). The signature travels with it so a Worker can verify the
// version without asking the Control Plane again.
type TrustBundleRecord struct {
	Version   uint64
	NotAfter  time.Time
	KeyID     string
	Signature []byte
	// Digest is the value the signature covers. It is stored because it is the
	// identity of the version: two records with the same number but different
	// digests are a conflict, not an update.
	Digest contract.Digest
	// Issuers are the authorities this version trusts.
	Issuers []TrustBundleIssuer
	// RecordedAt is when the Control Plane stored the version.
	RecordedAt time.Time
}

// TrustBundleIssuer is one authority inside a version.
type TrustBundleIssuer struct {
	Fingerprint contract.Digest
	NotBefore   time.Time
	NotAfter    time.Time
}

// TrustBundleApprovalRecord is the audit record of one operator decision that
// let a version add or remove an issuer (ADR-0036 §2.2).
type TrustBundleApprovalRecord struct {
	Version       uint64
	Operation     string
	Issuer        contract.Digest
	ApprovedBy    string
	ApprovedAt    time.Time
	Justification string
}

// TrustBundleStore holds the published versions and their approvals. The two
// are written together: a version without its approvals, or an approval for a
// version that is not stored, is a state the Control Plane must not be able to
// produce.
type TrustBundleStore interface {
	// SaveTrustBundle records a version and the approvals that allowed it in
	// one transaction. It returns ErrTrustBundleConflict when the version
	// exists with a different digest.
	SaveTrustBundle(ctx context.Context, record TrustBundleRecord, approvals []TrustBundleApprovalRecord) error
	// LatestTrustBundle returns the newest version, or ErrNotFound.
	LatestTrustBundle(ctx context.Context) (TrustBundleRecord, error)
	// LoadTrustBundle returns one version, or ErrNotFound.
	LoadTrustBundle(ctx context.Context, version uint64) (TrustBundleRecord, error)
	// ListTrustBundles returns versions up to limit, newest first.
	ListTrustBundles(ctx context.Context, limit int) ([]TrustBundleRecord, error)
	// ListTrustBundleApprovals returns the approvals of one version, oldest first.
	ListTrustBundleApprovals(ctx context.Context, version uint64) ([]TrustBundleApprovalRecord, error)
}
