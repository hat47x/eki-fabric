package ids

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const randomBytes = 16

var ErrInvalidID = errors.New("invalid EKI identifier")

type TaskID string
type AttemptID string
type LeaseID string

func NewTaskID() (TaskID, error) {
	v, err := newID("tsk_")
	return TaskID(v), err
}

func NewAttemptID() (AttemptID, error) {
	v, err := newID("atm_")
	return AttemptID(v), err
}

func NewLeaseID() (LeaseID, error) {
	v, err := newID("les_")
	return LeaseID(v), err
}

func ParseTaskID(v string) (TaskID, error) {
	if err := validateID("tsk_", v); err != nil {
		return "", err
	}
	return TaskID(v), nil
}

func ParseAttemptID(v string) (AttemptID, error) {
	if err := validateID("atm_", v); err != nil {
		return "", err
	}
	return AttemptID(v), nil
}

func ParseLeaseID(v string) (LeaseID, error) {
	if err := validateID("les_", v); err != nil {
		return "", err
	}
	return LeaseID(v), nil
}

func (v TaskID) String() string    { return string(v) }
func (v AttemptID) String() string { return string(v) }
func (v LeaseID) String() string   { return string(v) }

func newID(prefix string) (string, error) {
	var raw [randomBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate %s identifier: %w", prefix, err)
	}
	return prefix + hex.EncodeToString(raw[:]), nil
}

func validateID(prefix, v string) error {
	if !strings.HasPrefix(v, prefix) {
		return fmt.Errorf("%w: expected prefix %q", ErrInvalidID, prefix)
	}
	suffix := strings.TrimPrefix(v, prefix)
	if len(suffix) != randomBytes*2 {
		return fmt.Errorf("%w: expected %d hexadecimal characters after prefix", ErrInvalidID, randomBytes*2)
	}
	if _, err := hex.DecodeString(suffix); err != nil {
		return fmt.Errorf("%w: malformed hexadecimal suffix", ErrInvalidID)
	}
	return nil
}
