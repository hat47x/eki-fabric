// Package policystate defines the durable policy snapshot used to fence
// security-sensitive Lease issuance.
package policystate

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

type Status string

const (
	StatusActive  Status = "active"
	StatusRevoked Status = "revoked"
)

var ErrInvalidPolicyState = errors.New("invalid policy state")

type State struct {
	Epoch        uint64
	Revision     string
	Status       Status
	ActivatedAt  time.Time
	RevokedAt    time.Time
	RevokeReason string
}

func NewActive(epoch uint64, revision string, activatedAt time.Time) (State, error) {
	state := State{
		Epoch:       epoch,
		Revision:    revision,
		Status:      StatusActive,
		ActivatedAt: activatedAt.UTC(),
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

func Revoke(current State, nextEpoch uint64, revokedAt time.Time, reason string) (State, error) {
	if err := current.Validate(); err != nil {
		return State{}, err
	}
	if current.Status != StatusActive {
		return State{}, fmt.Errorf("%w: only active policy can be revoked", ErrInvalidPolicyState)
	}
	next := State{
		Epoch:        nextEpoch,
		Revision:     current.Revision,
		Status:       StatusRevoked,
		ActivatedAt:  current.ActivatedAt,
		RevokedAt:    revokedAt.UTC(),
		RevokeReason: reason,
	}
	if err := ValidateTransition(current, next); err != nil {
		return State{}, err
	}
	return next, nil
}

func (s State) Validate() error {
	if s.Epoch == 0 {
		return fmt.Errorf("%w: epoch must be positive", ErrInvalidPolicyState)
	}
	if s.Revision == "" || len(s.Revision) > 128 || !utf8.ValidString(s.Revision) {
		return fmt.Errorf("%w: revision must be 1..128 valid UTF-8 bytes", ErrInvalidPolicyState)
	}
	if s.ActivatedAt.IsZero() {
		return fmt.Errorf("%w: activated_at is required", ErrInvalidPolicyState)
	}
	switch s.Status {
	case StatusActive:
		if !s.RevokedAt.IsZero() || s.RevokeReason != "" {
			return fmt.Errorf("%w: active state cannot carry revocation fields", ErrInvalidPolicyState)
		}
	case StatusRevoked:
		if s.RevokedAt.IsZero() || s.RevokedAt.Before(s.ActivatedAt) {
			return fmt.Errorf("%w: revoked_at must not precede activated_at", ErrInvalidPolicyState)
		}
		if s.RevokeReason == "" || len(s.RevokeReason) > 512 || !utf8.ValidString(s.RevokeReason) {
			return fmt.Errorf("%w: revoke reason must be 1..512 valid UTF-8 bytes", ErrInvalidPolicyState)
		}
	default:
		return fmt.Errorf("%w: unknown status %q", ErrInvalidPolicyState, s.Status)
	}
	return nil
}

func ValidateTransition(current, next State) error {
	if err := current.Validate(); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if next.Epoch != current.Epoch+1 {
		return fmt.Errorf("%w: next epoch %d must follow %d", ErrInvalidPolicyState, next.Epoch, current.Epoch)
	}
	switch next.Status {
	case StatusRevoked:
		if current.Status != StatusActive ||
			next.Revision != current.Revision ||
			!next.ActivatedAt.Equal(current.ActivatedAt) {
			return fmt.Errorf("%w: revocation must preserve the current active revision", ErrInvalidPolicyState)
		}
	case StatusActive:
		if next.Revision == current.Revision {
			return fmt.Errorf("%w: active transition must introduce a different revision", ErrInvalidPolicyState)
		}
		notBefore := current.ActivatedAt
		if current.Status == StatusRevoked {
			notBefore = current.RevokedAt
		}
		if next.ActivatedAt.Before(notBefore) {
			return fmt.Errorf("%w: activation time moved backwards", ErrInvalidPolicyState)
		}
	}
	return nil
}

func (s State) ActiveRevision(revision string) bool {
	return s.Status == StatusActive && s.Revision == revision
}
