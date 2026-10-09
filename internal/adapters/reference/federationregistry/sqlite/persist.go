package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/federation"
)

// The write helpers below run inside the transaction of Store.commit. Each one
// writes only the rows that a change touches. An update checks the values that
// the in-memory state expects to find, and it fails unless exactly one row
// matches, so a change based on stale memory is never written.

// insertDomain stores a new federation: the domain row, its guard row, its
// purposes, and the keys of its first bundle.
func insertDomain(tx *sql.Tx, link federation.Link, bundle federation.Bundle, signature []byte) error {
	domain := link.Domain().String()
	if _, err := tx.Exec(`INSERT INTO federation_domains
		(domain, authority_key, bundle_sequence, bundle_validated_at, bundle_signature, revoked, revoked_at)
		VALUES (?, ?, ?, ?, ?, 0, NULL)`,
		domain, []byte(link.AuthorityKey()), int64(bundle.Sequence()), formatTime(bundle.ValidatedAt()), signature); err != nil {
		return fmt.Errorf("store federation of %s: %w", domain, err)
	}
	if _, err := tx.Exec(`INSERT INTO federation_sequence_guard (domain, highest_sequence) VALUES (?, ?)`,
		domain, int64(bundle.Sequence())); err != nil {
		return fmt.Errorf("store sequence guard of %s: %w", domain, err)
	}
	for _, p := range link.Purposes() {
		if _, err := tx.Exec(`INSERT INTO federation_link_purposes (domain, purpose) VALUES (?, ?)`, domain, string(p)); err != nil {
			return fmt.Errorf("store purpose %s of %s: %w", p, domain, err)
		}
	}
	return insertKeys(tx, domain, bundle.Keys())
}

// updateBundle stores an accepted offer. prev is the binding before the offer,
// and post is the binding after it. A higher sequence replaces the keys and moves
// the guard. The same sequence only moves the validated-at time forward.
func updateBundle(tx *sql.Tx, prev, post federation.Binding, signature []byte) error {
	domain := post.Domain().String()
	res, err := tx.Exec(`UPDATE federation_domains
		SET bundle_sequence = ?, bundle_validated_at = ?, bundle_signature = ?
		WHERE domain = ? AND bundle_sequence = ? AND bundle_validated_at = ?`,
		int64(post.Sequence()), formatTime(post.ValidatedAt()), signature,
		domain, int64(prev.Sequence()), formatTime(prev.ValidatedAt()))
	if err != nil {
		return fmt.Errorf("store bundle of %s: %w", domain, err)
	}
	if err := expectOneRow(res, "bundle of "+domain); err != nil {
		return err
	}
	if post.Sequence() == prev.Sequence() {
		return nil
	}

	res, err = tx.Exec(`UPDATE federation_sequence_guard SET highest_sequence = ?
		WHERE domain = ? AND highest_sequence = ?`,
		int64(post.Sequence()), domain, int64(prev.Sequence()))
	if err != nil {
		return fmt.Errorf("store sequence guard of %s: %w", domain, err)
	}
	if err := expectOneRow(res, "sequence guard of "+domain); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM federation_bundle_keys WHERE domain = ?`, domain); err != nil {
		return fmt.Errorf("replace keys of %s: %w", domain, err)
	}
	return insertKeys(tx, domain, post.Bundle().Keys())
}

// markRevoked stores a revocation. It matches only a domain that is not revoked.
func markRevoked(tx *sql.Tx, domain federation.TrustDomain, at time.Time) error {
	res, err := tx.Exec(`UPDATE federation_domains SET revoked = 1, revoked_at = ?
		WHERE domain = ? AND revoked = 0`,
		formatTime(at), domain.String())
	if err != nil {
		return fmt.Errorf("store revocation of %s: %w", domain, err)
	}
	return expectOneRow(res, "revocation of "+domain.String())
}

func insertKeys(tx *sql.Tx, domain string, keys []federation.BundleKey) error {
	for _, k := range keys {
		if _, err := tx.Exec(`INSERT INTO federation_bundle_keys (domain, key_id, public_key) VALUES (?, ?, ?)`,
			domain, k.ID, []byte(k.PublicKey)); err != nil {
			return fmt.Errorf("store key %q of %s: %w", k.ID, domain, err)
		}
	}
	return nil
}

// expectOneRow fails unless the statement changed exactly one row. Zero rows
// means that the stored state differs from what the caller expected.
func expectOneRow(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n != 1 {
		return fmt.Errorf("%s: %d rows matched, want 1; the stored state differs from memory", what, n)
	}
	return nil
}

// readSnapshot reads every stored row in one transaction and returns them as a
// core snapshot. The core checks the snapshot when it is restored.
func readSnapshot(ctx context.Context, db *sql.DB) (federation.Snapshot, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return federation.Snapshot{}, fmt.Errorf("read federation registry: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	purposes, err := readPurposes(ctx, tx)
	if err != nil {
		return federation.Snapshot{}, err
	}
	keys, err := readKeys(ctx, tx)
	if err != nil {
		return federation.Snapshot{}, err
	}
	entries, err := readDomains(ctx, tx, purposes, keys)
	if err != nil {
		return federation.Snapshot{}, err
	}
	return federation.Snapshot{Entries: entries}, nil
}

func readDomains(ctx context.Context, tx *sql.Tx, purposes map[string][]federation.Purpose, keys map[string][]federation.BundleKey) ([]federation.DomainSnapshot, error) {
	rows, err := tx.QueryContext(ctx, `SELECT d.domain, d.authority_key, d.bundle_sequence, d.bundle_validated_at,
			d.bundle_signature, d.revoked, d.revoked_at, COALESCE(g.highest_sequence, 0)
		FROM federation_domains AS d
		LEFT JOIN federation_sequence_guard AS g ON g.domain = d.domain
		ORDER BY d.domain`)
	if err != nil {
		return nil, fmt.Errorf("read federation domains: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []federation.DomainSnapshot
	for rows.Next() {
		var (
			domain, validatedAt string
			authorityKey        []byte
			signature           []byte
			sequence, highest   int64
			revoked             int64
			revokedAt           sql.NullString
		)
		if err := rows.Scan(&domain, &authorityKey, &sequence, &validatedAt, &signature, &revoked, &revokedAt, &highest); err != nil {
			return nil, fmt.Errorf("read federation domain: %w", err)
		}
		if sequence < 0 || highest < 0 {
			return nil, fmt.Errorf("federation domain %q has a negative sequence", domain)
		}
		if revoked != 0 && revoked != 1 {
			return nil, fmt.Errorf("federation domain %q has revoked flag %d", domain, revoked)
		}
		validated, err := parseTime(validatedAt)
		if err != nil {
			return nil, fmt.Errorf("federation domain %q: %w", domain, err)
		}
		var revokedTime time.Time
		if revokedAt.Valid {
			if revokedTime, err = parseTime(revokedAt.String); err != nil {
				return nil, fmt.Errorf("federation domain %q: %w", domain, err)
			}
		}
		out = append(out, federation.DomainSnapshot{
			Domain:          domain,
			AuthorityKey:    authorityKey,
			Purposes:        purposes[domain],
			HighestSequence: uint64(highest),
			Bundle: federation.BundleRecord{
				Domain:             domain,
				Sequence:           uint64(sequence),
				ValidatedAt:        validated,
				Keys:               keys[domain],
				AuthoritySignature: signature,
			},
			Revoked:   revoked == 1,
			RevokedAt: revokedTime,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read federation domains: %w", err)
	}
	return out, nil
}

func readPurposes(ctx context.Context, tx *sql.Tx) (map[string][]federation.Purpose, error) {
	rows, err := tx.QueryContext(ctx, `SELECT domain, purpose FROM federation_link_purposes ORDER BY domain, purpose`)
	if err != nil {
		return nil, fmt.Errorf("read federation purposes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string][]federation.Purpose)
	for rows.Next() {
		var domain, purpose string
		if err := rows.Scan(&domain, &purpose); err != nil {
			return nil, fmt.Errorf("read federation purpose: %w", err)
		}
		out[domain] = append(out[domain], federation.Purpose(purpose))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read federation purposes: %w", err)
	}
	return out, nil
}

func readKeys(ctx context.Context, tx *sql.Tx) (map[string][]federation.BundleKey, error) {
	rows, err := tx.QueryContext(ctx, `SELECT domain, key_id, public_key FROM federation_bundle_keys ORDER BY domain, key_id`)
	if err != nil {
		return nil, fmt.Errorf("read federation keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string][]federation.BundleKey)
	for rows.Next() {
		var domain, id string
		var publicKey []byte
		if err := rows.Scan(&domain, &id, &publicKey); err != nil {
			return nil, fmt.Errorf("read federation key: %w", err)
		}
		out[domain] = append(out[domain], federation.BundleKey{ID: id, PublicKey: publicKey})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read federation keys: %w", err)
	}
	return out, nil
}

// formatTime stores a time as UTC with nanosecond precision. Parsing it back gives
// the same instant, so the stored state restores to the same values.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("stored time %q: %w", s, err)
	}
	return t, nil
}
