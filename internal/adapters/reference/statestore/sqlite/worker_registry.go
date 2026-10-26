package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

const selectCredential = `
SELECT credential_id, secret_sha256, trust_domain, issued_by, issued_at_ns, expires_at_ns, state,
       consumed_worker, consumed_at_ns, revoked_by, revoked_at_ns
FROM enrollment_credentials`

const selectWorker = `
SELECT worker_identity, trust_domain, state, revision, credential_id, state_changed_by,
       cert_serial, cert_not_after_ns, created_at_ns, updated_at_ns
FROM workers`

func (s *Store) IssueEnrollmentCredential(ctx context.Context, c workerregistry.Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.Validate(); err != nil {
		return err
	}
	if c.State != workerregistry.CredentialIssued {
		return fmt.Errorf("%w: a new credential is issued", workerregistry.ErrInvalidCredential)
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO enrollment_credentials (credential_id, secret_sha256, trust_domain, issued_by, issued_at_ns, expires_at_ns, state)
VALUES (?, ?, ?, ?, ?, ?, 'issued')`,
		string(c.ID), c.SecretSHA256.Hex(), c.TrustDomain, c.IssuedBy,
		c.IssuedAt.UTC().UnixNano(), c.ExpiresAt.UTC().UnixNano())
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: enrollment credential %s", statestore.ErrAlreadyExists, c.ID)
		}
		return fmt.Errorf("issue enrollment credential %s: %w", c.ID, err)
	}
	return nil
}

func (s *Store) LoadEnrollmentCredential(ctx context.Context, id ids.EnrollmentCredentialID) (workerregistry.Credential, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Credential{}, err
	}
	c, err := scanCredential(s.db.QueryRowContext(ctx, selectCredential+` WHERE credential_id = ?`, string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return workerregistry.Credential{}, fmt.Errorf("%w: enrollment credential %s", statestore.ErrNotFound, id)
	}
	return c, err
}

func (s *Store) RevokeEnrollmentCredential(ctx context.Context, id ids.EnrollmentCredentialID, actor string, at time.Time) (workerregistry.Credential, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Credential{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workerregistry.Credential{}, fmt.Errorf("revoke enrollment credential %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanCredential(tx.QueryRowContext(ctx, selectCredential+` WHERE credential_id = ?`, string(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return workerregistry.Credential{}, fmt.Errorf("%w: enrollment credential %s", statestore.ErrNotFound, id)
	}
	if err != nil {
		return workerregistry.Credential{}, err
	}
	next, err := current.Revoke(actor, at)
	if err != nil {
		return workerregistry.Credential{}, err
	}
	if next.State == current.State {
		return current, nil
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE enrollment_credentials SET state = 'revoked', revoked_by = ?, revoked_at_ns = ?
WHERE credential_id = ? AND state = 'issued'`,
		next.RevokedBy, next.RevokedAt.UTC().UnixNano(), string(id)); err != nil {
		return workerregistry.Credential{}, fmt.Errorf("revoke enrollment credential %s: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return workerregistry.Credential{}, fmt.Errorf("revoke enrollment credential %s: commit: %w", id, err)
	}
	return next, nil
}

func (s *Store) ConsumeEnrollmentCredential(ctx context.Context, req statestore.ConsumeEnrollment) (workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Worker{}, err
	}
	// Every transaction is BEGIN IMMEDIATE (see dsn), so concurrent consumers
	// run one after another: the second reads the credential as consumed and is
	// refused.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workerregistry.Worker{}, fmt.Errorf("consume enrollment credential: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	credential, err := scanCredential(tx.QueryRowContext(ctx, selectCredential+` WHERE secret_sha256 = ?`, req.SecretSHA256.Hex()))
	if errors.Is(err, sql.ErrNoRows) {
		return workerregistry.Worker{}, workerregistry.NewRefusal(workerregistry.ReasonUnknown)
	}
	if err != nil {
		return workerregistry.Worker{}, err
	}
	if err := credential.CheckUsable(req.SecretSHA256, req.TrustDomain, req.At); err != nil {
		return workerregistry.Worker{}, err
	}
	spent, err := credential.Consume(req.Identity, req.At)
	if err != nil {
		return workerregistry.Worker{}, err
	}
	worker, err := workerregistry.NewWorker(req.Identity, credential, spent.ConsumedAt)
	if err != nil {
		return workerregistry.Worker{}, err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO workers (worker_identity, trust_domain, state, revision, credential_id, state_changed_by, created_at_ns, updated_at_ns)
VALUES (?, ?, 'registered', 1, ?, '', ?, ?)`,
		string(worker.Identity), worker.TrustDomain, string(credential.ID),
		worker.CreatedAt.UnixNano(), worker.UpdatedAt.UnixNano()); err != nil {
		if isUniqueViolation(err) {
			return workerregistry.Worker{}, fmt.Errorf("%w: worker identity %s", statestore.ErrAlreadyExists, worker.Identity)
		}
		return workerregistry.Worker{}, fmt.Errorf("consume enrollment credential %s: insert worker: %w", credential.ID, err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE enrollment_credentials SET state = 'consumed', consumed_worker = ?, consumed_at_ns = ?
WHERE credential_id = ? AND state = 'issued'`,
		string(worker.Identity), spent.ConsumedAt.UnixNano(), string(credential.ID)); err != nil {
		return workerregistry.Worker{}, fmt.Errorf("consume enrollment credential %s: mark consumed: %w", credential.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return workerregistry.Worker{}, fmt.Errorf("consume enrollment credential %s: commit: %w", credential.ID, err)
	}
	return worker, nil
}

func (s *Store) LoadWorker(ctx context.Context, identity ids.WorkerIdentity) (workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Worker{}, err
	}
	w, err := scanWorker(s.db.QueryRowContext(ctx, selectWorker+` WHERE worker_identity = ?`, string(identity)))
	if errors.Is(err, sql.ErrNoRows) {
		return workerregistry.Worker{}, fmt.Errorf("%w: worker %s", statestore.ErrNotFound, identity)
	}
	return w, err
}

func (s *Store) TransitionWorker(ctx context.Context, identity ids.WorkerIdentity, to workerregistry.State, actor string, at time.Time) (workerregistry.Worker, error) {
	return s.updateWorker(ctx, identity, func(w workerregistry.Worker) (workerregistry.Worker, error) {
		return w.Transition(to, actor, at)
	})
}

func (s *Store) RecordWorkerCertificate(ctx context.Context, identity ids.WorkerIdentity, cert workerregistry.Certificate, at time.Time) (workerregistry.Worker, error) {
	return s.updateWorker(ctx, identity, func(w workerregistry.Worker) (workerregistry.Worker, error) {
		return w.WithCertificate(cert, at)
	})
}

// updateWorker reads, changes and writes one Worker in a transaction. The
// change is computed by the core rules, so both adapters share them.
func (s *Store) updateWorker(ctx context.Context, identity ids.WorkerIdentity, change func(workerregistry.Worker) (workerregistry.Worker, error)) (workerregistry.Worker, error) {
	if err := ctx.Err(); err != nil {
		return workerregistry.Worker{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workerregistry.Worker{}, fmt.Errorf("update worker %s: %w", identity, err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := scanWorker(tx.QueryRowContext(ctx, selectWorker+` WHERE worker_identity = ?`, string(identity)))
	if errors.Is(err, sql.ErrNoRows) {
		return workerregistry.Worker{}, fmt.Errorf("%w: worker %s", statestore.ErrNotFound, identity)
	}
	if err != nil {
		return workerregistry.Worker{}, err
	}
	next, err := change(current)
	if err != nil {
		return workerregistry.Worker{}, err
	}
	if next.Revision == current.Revision {
		return current, nil
	}

	var serial, notAfter any
	if next.Certificate.Serial != "" {
		serial = next.Certificate.Serial
		notAfter = next.Certificate.NotAfter.UnixNano()
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE workers
SET state = ?, revision = ?, state_changed_by = ?, cert_serial = ?, cert_not_after_ns = ?, updated_at_ns = ?
WHERE worker_identity = ? AND revision = ?`,
		string(next.State), int64(next.Revision), next.StateChangedBy, serial, notAfter, next.UpdatedAt.UnixNano(),
		string(identity), int64(current.Revision)); err != nil {
		return workerregistry.Worker{}, fmt.Errorf("update worker %s: %w", identity, err)
	}
	if err := tx.Commit(); err != nil {
		return workerregistry.Worker{}, fmt.Errorf("update worker %s: commit: %w", identity, err)
	}
	return next, nil
}

func scanCredential(row rowScanner) (workerregistry.Credential, error) {
	var (
		id, hash, domain, issuer, state string
		issuedNS, expiresNS             int64
		consumedWorker, revokedBy       sql.NullString
		consumedNS, revokedNS           sql.NullInt64
	)
	if err := row.Scan(&id, &hash, &domain, &issuer, &issuedNS, &expiresNS, &state,
		&consumedWorker, &consumedNS, &revokedBy, &revokedNS); err != nil {
		return workerregistry.Credential{}, err
	}
	digest, err := contract.ParseSHA256(hash)
	if err != nil {
		return workerregistry.Credential{}, fmt.Errorf("%w: enrollment credential %s: %v", ErrCorruptRecord, id, err)
	}
	c := workerregistry.Credential{
		ID:           ids.EnrollmentCredentialID(id),
		SecretSHA256: digest,
		TrustDomain:  domain,
		IssuedBy:     issuer,
		IssuedAt:     time.Unix(0, issuedNS).UTC(),
		ExpiresAt:    time.Unix(0, expiresNS).UTC(),
		State:        workerregistry.CredentialState(state),
	}
	if consumedWorker.Valid {
		c.ConsumedWorker = ids.WorkerIdentity(consumedWorker.String)
		c.ConsumedAt = time.Unix(0, consumedNS.Int64).UTC()
	}
	if revokedBy.Valid {
		c.RevokedBy = revokedBy.String
		c.RevokedAt = time.Unix(0, revokedNS.Int64).UTC()
	}
	if err := c.Validate(); err != nil {
		return workerregistry.Credential{}, fmt.Errorf("%w: enrollment credential %s: %v", ErrCorruptRecord, id, err)
	}
	return c, nil
}

func scanWorker(row rowScanner) (workerregistry.Worker, error) {
	var (
		identity, domain, state, credential, changedBy string
		revision, createdNS, updatedNS                 int64
		serial                                         sql.NullString
		notAfterNS                                     sql.NullInt64
	)
	if err := row.Scan(&identity, &domain, &state, &revision, &credential, &changedBy,
		&serial, &notAfterNS, &createdNS, &updatedNS); err != nil {
		return workerregistry.Worker{}, err
	}
	if revision < 1 {
		return workerregistry.Worker{}, fmt.Errorf("%w: worker %s: revision %d", ErrCorruptRecord, identity, revision)
	}
	w := workerregistry.Worker{
		Identity:       ids.WorkerIdentity(identity),
		TrustDomain:    domain,
		State:          workerregistry.State(state),
		Revision:       uint64(revision),
		CredentialID:   ids.EnrollmentCredentialID(credential),
		StateChangedBy: changedBy,
		CreatedAt:      time.Unix(0, createdNS).UTC(),
		UpdatedAt:      time.Unix(0, updatedNS).UTC(),
	}
	if serial.Valid {
		w.Certificate = workerregistry.Certificate{Serial: serial.String, NotAfter: time.Unix(0, notAfterNS.Int64).UTC()}
	}
	if err := w.Validate(); err != nil {
		return workerregistry.Worker{}, fmt.Errorf("%w: worker %s: %v", ErrCorruptRecord, identity, err)
	}
	return w, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "PRIMARY KEY constraint failed")
}

var _ statestore.WorkerRegistryStore = (*Store)(nil)
