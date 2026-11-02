package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// SaveTrustBundle records a version and its approvals in one transaction
// (ADR-0036 §6): a version without its approvals, or approvals for a version
// that is not stored, is a state this method cannot produce.
func (s *Store) SaveTrustBundle(ctx context.Context, record statestore.TrustBundleRecord, approvals []statestore.TrustBundleApprovalRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin trust bundle write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var stored string
	err = tx.QueryRowContext(ctx, `SELECT digest FROM trust_bundles WHERE version = ?`, int64(record.Version)).Scan(&stored)
	switch {
	case err == nil:
		if stored != digestText(record.Digest) {
			return statestore.ErrTrustBundleConflict
		}
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read trust bundle %d: %w", record.Version, err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO trust_bundles (version, not_after_ns, key_id, signature, digest, recorded_at_ns) VALUES (?, ?, ?, ?, ?, ?)`,
		int64(record.Version), record.NotAfter.UTC().UnixNano(), record.KeyID, record.Signature, digestText(record.Digest), record.RecordedAt.UTC().UnixNano(),
	); err != nil {
		return fmt.Errorf("insert trust bundle %d: %w", record.Version, err)
	}
	for _, issuer := range record.Issuers {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO trust_bundle_issuers (version, fingerprint, not_before_ns, not_after_ns) VALUES (?, ?, ?, ?)`,
			int64(record.Version), digestText(issuer.Fingerprint), issuer.NotBefore.UTC().UnixNano(), issuer.NotAfter.UTC().UnixNano(),
		); err != nil {
			return fmt.Errorf("insert trust bundle %d issuer: %w", record.Version, err)
		}
	}
	for _, approval := range approvals {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO trust_bundle_approvals (version, operation, issuer, approved_by, approved_at_ns, justification) VALUES (?, ?, ?, ?, ?, ?)`,
			int64(approval.Version), approval.Operation, digestText(approval.Issuer), approval.ApprovedBy, approval.ApprovedAt.UTC().UnixNano(), approval.Justification,
		); err != nil {
			return fmt.Errorf("insert trust bundle %d approval: %w", record.Version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit trust bundle %d: %w", record.Version, err)
	}
	return nil
}

// LatestTrustBundle returns the newest version.
func (s *Store) LatestTrustBundle(ctx context.Context) (statestore.TrustBundleRecord, error) {
	var newest sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(version) FROM trust_bundles`).Scan(&newest); err != nil {
		return statestore.TrustBundleRecord{}, fmt.Errorf("read latest trust bundle: %w", err)
	}
	if !newest.Valid {
		return statestore.TrustBundleRecord{}, statestore.ErrNotFound
	}
	return s.LoadTrustBundle(ctx, uint64(newest.Int64))
}

// LoadTrustBundle returns one version.
func (s *Store) LoadTrustBundle(ctx context.Context, version uint64) (statestore.TrustBundleRecord, error) {
	record, err := s.loadTrustBundle(ctx, s.db, version)
	if err != nil {
		return statestore.TrustBundleRecord{}, err
	}
	return record, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) loadTrustBundle(ctx context.Context, q queryer, version uint64) (statestore.TrustBundleRecord, error) {
	var (
		notAfter   int64
		keyID      string
		signature  []byte
		digest     string
		recordedAt int64
	)
	err := q.QueryRowContext(ctx,
		`SELECT not_after_ns, key_id, signature, digest, recorded_at_ns FROM trust_bundles WHERE version = ?`, int64(version),
	).Scan(&notAfter, &keyID, &signature, &digest, &recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return statestore.TrustBundleRecord{}, statestore.ErrNotFound
	}
	if err != nil {
		return statestore.TrustBundleRecord{}, fmt.Errorf("read trust bundle %d: %w", version, err)
	}
	record := statestore.TrustBundleRecord{
		Version: version, NotAfter: nanosToTime(notAfter), KeyID: keyID, Signature: signature,
		RecordedAt: nanosToTime(recordedAt),
	}
	parsedDigest, err := contract.ParseSHA256(digest)
	if err != nil {
		return statestore.TrustBundleRecord{}, fmt.Errorf("read trust bundle %d digest: %w", version, err)
	}
	record.Digest = parsedDigest
	rows, err := q.QueryContext(ctx,
		`SELECT fingerprint, not_before_ns, not_after_ns FROM trust_bundle_issuers WHERE version = ? ORDER BY fingerprint`, int64(version))
	if err != nil {
		return statestore.TrustBundleRecord{}, fmt.Errorf("read trust bundle %d issuers: %w", version, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			fingerprint string
			notBefore   int64
			issuerAfter int64
		)
		if err := rows.Scan(&fingerprint, &notBefore, &issuerAfter); err != nil {
			return statestore.TrustBundleRecord{}, fmt.Errorf("scan trust bundle %d issuer: %w", version, err)
		}
		var issuer statestore.TrustBundleIssuer
		if parsed, err := contract.ParseSHA256(fingerprint); err != nil {
			return statestore.TrustBundleRecord{}, fmt.Errorf("read trust bundle %d issuer fingerprint: %w", version, err)
		} else {
			issuer.Fingerprint = parsed
		}
		issuer.NotBefore, issuer.NotAfter = nanosToTime(notBefore), nanosToTime(issuerAfter)
		record.Issuers = append(record.Issuers, issuer)
	}
	if err := rows.Err(); err != nil {
		return statestore.TrustBundleRecord{}, fmt.Errorf("read trust bundle %d issuers: %w", version, err)
	}
	return record, nil
}

// ListTrustBundles returns versions newest first, up to limit.
func (s *Store) ListTrustBundles(ctx context.Context, limit int) ([]statestore.TrustBundleRecord, error) {
	query := `SELECT version FROM trust_bundles ORDER BY version DESC`
	args := []any{}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list trust bundles: %w", err)
	}
	defer rows.Close()
	var versions []uint64
	for rows.Next() {
		var version uint64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan trust bundle version: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list trust bundles: %w", err)
	}
	out := make([]statestore.TrustBundleRecord, 0, len(versions))
	for _, version := range versions {
		record, err := s.LoadTrustBundle(ctx, version)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

// ListTrustBundleApprovals returns one version's approvals, oldest first.
func (s *Store) ListTrustBundleApprovals(ctx context.Context, version uint64) ([]statestore.TrustBundleApprovalRecord, error) {
	if _, err := s.LoadTrustBundle(ctx, version); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT version, operation, issuer, approved_by, approved_at_ns, justification FROM trust_bundle_approvals WHERE version = ? ORDER BY id`, int64(version))
	if err != nil {
		return nil, fmt.Errorf("list trust bundle %d approvals: %w", version, err)
	}
	defer rows.Close()
	var out []statestore.TrustBundleApprovalRecord
	for rows.Next() {
		var (
			record     statestore.TrustBundleApprovalRecord
			issuer     string
			approvedAt int64
		)
		if err := rows.Scan(&record.Version, &record.Operation, &issuer, &record.ApprovedBy, &approvedAt, &record.Justification); err != nil {
			return nil, fmt.Errorf("scan trust bundle %d approval: %w", version, err)
		}
		record.ApprovedAt = nanosToTime(approvedAt)
		if parsed, err := contract.ParseSHA256(issuer); err != nil {
			return nil, fmt.Errorf("read trust bundle %d approval issuer: %w", version, err)
		} else {
			record.Issuer = parsed
		}
		out = append(out, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list trust bundle %d approvals: %w", version, err)
	}
	return out, nil
}

var _ statestore.TrustBundleStore = (*Store)(nil)

func nanosToTime(nanos int64) time.Time { return time.Unix(0, nanos).UTC() }

// digestText is the stored form: the hex digest without the algorithm prefix,
// which ParseSHA256 reads back.
func digestText(d contract.Digest) string {
	return strings.TrimPrefix(d.String(), "sha256:")
}
