package trustbundle

import (
	"context"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// ToRecord is the storage form of a signed version. It is a pure conversion: the
// store decides nothing about the bundle, so an encoding mistake here cannot
// change who is trusted as long as the digest round-trips.
func ToRecord(signed Signed, recordedAt time.Time) statestore.TrustBundleRecord {
	record := statestore.TrustBundleRecord{
		Version: signed.Bundle.Version, NotAfter: signed.Bundle.NotAfter, KeyID: signed.KeyID,
		Signature: append([]byte(nil), signed.Signature...), Digest: signed.Bundle.Digest(),
		RecordedAt: recordedAt,
	}
	for _, issuer := range signed.Bundle.Issuers {
		record.Issuers = append(record.Issuers, statestore.TrustBundleIssuer{
			Fingerprint: issuer.Fingerprint, NotBefore: issuer.NotBefore, NotAfter: issuer.NotAfter,
		})
	}
	return record
}

// FromRecord rebuilds a signed version from storage. The digest stored with the
// record must still describe the bundle: a mismatch means the row was altered,
// and the version cannot be trusted.
func FromRecord(record statestore.TrustBundleRecord) (Signed, error) {
	bundle := Bundle{Version: record.Version, NotAfter: record.NotAfter}
	for _, issuer := range record.Issuers {
		bundle.Issuers = append(bundle.Issuers, Issuer{
			Fingerprint: issuer.Fingerprint, NotBefore: issuer.NotBefore, NotAfter: issuer.NotAfter,
		})
	}
	if got := bundle.Digest(); got != record.Digest {
		return Signed{}, fmt.Errorf("%w: version %d digest %s, stored %s", ErrBadSignature, record.Version, got, record.Digest)
	}
	return Signed{Bundle: bundle, KeyID: record.KeyID, Signature: append([]byte(nil), record.Signature...)}, nil
}

// ToApprovalRecord is the storage form of one approval.
func ToApprovalRecord(approval Approval) statestore.TrustBundleApprovalRecord {
	return statestore.TrustBundleApprovalRecord{
		Version: approval.Version, Operation: string(approval.Operation), Issuer: approval.Issuer,
		ApprovedBy: approval.ApprovedBy, ApprovedAt: approval.ApprovedAt, Justification: approval.Justification,
	}
}

// FromApprovalRecord reads one approval back. An unknown operation is refused
// rather than guessed: the audit record must mean what it says.
func FromApprovalRecord(record statestore.TrustBundleApprovalRecord) (Approval, error) {
	operation := Operation(record.Operation)
	if operation != OperationAdd && operation != OperationRemove {
		return Approval{}, fmt.Errorf("%w: operation %q", ErrUnapproved, record.Operation)
	}
	return Approval{
		Version: record.Version, Operation: operation, Issuer: record.Issuer,
		ApprovedBy: record.ApprovedBy, ApprovedAt: record.ApprovedAt, Justification: record.Justification,
	}, nil
}

// Load rebuilds the in-memory Store from what the Control Plane persisted. The
// versions come back newest first from the store; the chain is oldest first.
func Load(ctx context.Context, stored statestore.TrustBundleStore) (*Store, error) {
	records, err := stored.ListTrustBundles(ctx, 0)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrNotInitialised
	}
	chain := make([]Signed, 0, len(records))
	approvals := make([]Approval, 0)
	for i := len(records) - 1; i >= 0; i-- {
		signed, err := FromRecord(records[i])
		if err != nil {
			return nil, err
		}
		chain = append(chain, signed)
		records, err := stored.ListTrustBundleApprovals(ctx, records[i].Version)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			approval, err := FromApprovalRecord(record)
			if err != nil {
				return nil, err
			}
			approvals = append(approvals, approval)
		}
	}
	return &Store{chain: chain, approvals: approvals}, nil
}

// Save writes one version and its approvals through the store. The store's own
// transaction is what makes the pair atomic; this function only converts.
func Save(ctx context.Context, stored statestore.TrustBundleStore, signed Signed, approvals []Approval, verifiedKey string, now time.Time) error {
	records := make([]statestore.TrustBundleApprovalRecord, 0, len(approvals))
	for _, approval := range approvals {
		records = append(records, ToApprovalRecord(approval))
	}
	record := ToRecord(signed, now)
	// verifiedKey names the key that actually checked the signature, which is a fact;
	// the record's KeyID is what the signer called it (ADR-0038 §8).
	record.VerifiedKey = verifiedKey
	return stored.SaveTrustBundle(ctx, record, records)
}
