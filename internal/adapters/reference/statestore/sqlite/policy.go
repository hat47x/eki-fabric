package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/policystate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

func (s *Store) LoadCurrentPolicy(ctx context.Context) (policystate.State, error) {
	if err := ctx.Err(); err != nil {
		return policystate.State{}, err
	}
	state, err := scanPolicy(s.db.QueryRowContext(ctx, `
SELECT h.epoch, h.revision, h.status, h.activated_at_ns, h.revoked_at_ns, h.revoke_reason
FROM policy_current c
JOIN policy_history h ON h.epoch = c.epoch
WHERE c.singleton = 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return policystate.State{}, statestore.ErrPolicyUnavailable
	}
	return state, err
}

func (s *Store) CompareAndSwapPolicy(ctx context.Context, expectedEpoch uint64, next policystate.State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin policy update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentEpoch uint64
	err = tx.QueryRowContext(ctx, `SELECT epoch FROM policy_current WHERE singleton = 1`).Scan(&currentEpoch)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		currentEpoch = 0
	case err != nil:
		return fmt.Errorf("read current policy epoch: %w", err)
	}
	if currentEpoch != expectedEpoch {
		return fmt.Errorf("%w: current epoch=%d expected=%d", statestore.ErrPolicyChanged, currentEpoch, expectedEpoch)
	}

	if expectedEpoch == 0 {
		if next.Epoch != 1 || next.Status != policystate.StatusActive {
			return fmt.Errorf("%w: initial policy must be active at epoch 1", statestore.ErrConflict)
		}
	} else {
		current, err := scanPolicy(tx.QueryRowContext(ctx, `
SELECT epoch, revision, status, activated_at_ns, revoked_at_ns, revoke_reason
FROM policy_history WHERE epoch = ?`, expectedEpoch))
		if err != nil {
			return fmt.Errorf("read policy epoch %d: %w", expectedEpoch, err)
		}
		if err := policystate.ValidateTransition(current, next); err != nil {
			return err
		}
	}

	if next.Status == policystate.StatusActive {
		var revokedCount int
		if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*) FROM policy_history WHERE revision = ? AND status = 'revoked'`,
			next.Revision,
		).Scan(&revokedCount); err != nil {
			return fmt.Errorf("check revoked policy revision %q: %w", next.Revision, err)
		}
		if revokedCount != 0 {
			return fmt.Errorf("%w: %s", statestore.ErrPolicyReactivation, next.Revision)
		}
	}

	var revokedNS any
	if next.Status == policystate.StatusRevoked {
		revokedNS = next.RevokedAt.UTC().UnixNano()
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO policy_history (
	epoch, revision, status, activated_at_ns, revoked_at_ns, revoke_reason
) VALUES (?, ?, ?, ?, ?, ?)`,
		int64(next.Epoch),
		next.Revision,
		string(next.Status),
		next.ActivatedAt.UTC().UnixNano(),
		revokedNS,
		next.RevokeReason,
	); err != nil {
		return fmt.Errorf("insert policy epoch %d: %w", next.Epoch, err)
	}

	if expectedEpoch == 0 {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO policy_current (singleton, epoch) VALUES (1, ?)`,
			int64(next.Epoch),
		); err != nil {
			return fmt.Errorf("initialize current policy epoch %d: %w", next.Epoch, err)
		}
	} else {
		res, err := tx.ExecContext(ctx, `
UPDATE policy_current SET epoch = ?
WHERE singleton = 1 AND epoch = ?`,
			int64(next.Epoch),
			int64(expectedEpoch),
		)
		if err != nil {
			return fmt.Errorf("advance current policy epoch: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("advance current policy epoch: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("%w: policy epoch changed while updating", statestore.ErrPolicyChanged)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit policy epoch %d: %w", next.Epoch, err)
	}
	return nil
}

func scanPolicy(row rowScanner) (policystate.State, error) {
	var (
		epoch       int64
		revision    string
		status      string
		activatedNS int64
		revokedNS   sql.NullInt64
		reason      string
	)
	if err := row.Scan(&epoch, &revision, &status, &activatedNS, &revokedNS, &reason); err != nil {
		return policystate.State{}, err
	}
	if epoch < 1 {
		return policystate.State{}, fmt.Errorf("%w: policy epoch %d", ErrCorruptRecord, epoch)
	}
	state := policystate.State{
		Epoch:        uint64(epoch),
		Revision:     revision,
		Status:       policystate.Status(status),
		ActivatedAt:  time.Unix(0, activatedNS).UTC(),
		RevokeReason: reason,
	}
	if revokedNS.Valid {
		state.RevokedAt = time.Unix(0, revokedNS.Int64).UTC()
	}
	if err := state.Validate(); err != nil {
		return policystate.State{}, fmt.Errorf("%w: policy epoch %d: %v", ErrCorruptRecord, epoch, err)
	}
	return state, nil
}

var _ statestore.PolicyStore = (*Store)(nil)
