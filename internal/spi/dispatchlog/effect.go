package dispatchlog

import (
	"fmt"
	"math"
)

// MaxEffectIDLength is the longest accepted EffectID, in bytes.
const MaxEffectIDLength = 128

// MaxGeneration is the largest accepted dispatch generation. It equals the
// largest signed 64-bit integer, so every accepted value fits in a SQLite
// INTEGER column, and all adapters accept the same range.
const MaxGeneration uint64 = math.MaxInt64

// EffectID identifies one external side effect of a Task. It is a non-empty
// string of at most MaxEffectIDLength bytes, made of ASCII letters, digits, and
// the characters '.', '_', ':', and '-'.
type EffectID string

// ParseEffectID checks v and returns it as an EffectID.
func ParseEffectID(v string) (EffectID, error) {
	id := EffectID(v)
	if err := id.Validate(); err != nil {
		return "", err
	}
	return id, nil
}

// String returns the identifier text.
func (id EffectID) String() string {
	return string(id)
}

// Validate reports whether id follows the EffectID rules. The error wraps
// ErrInvalidEffectID and does not include the identifier text, because that
// text may be arbitrary input.
func (id EffectID) Validate() error {
	n := len(id)
	if n == 0 {
		return fmt.Errorf("%w: empty", ErrInvalidEffectID)
	}
	if n > MaxEffectIDLength {
		return fmt.Errorf("%w: %d bytes exceeds the limit of %d", ErrInvalidEffectID, n, MaxEffectIDLength)
	}
	for i := 0; i < n; i++ {
		if !isEffectIDByte(id[i]) {
			return fmt.Errorf("%w: byte %d is outside the allowed characters", ErrInvalidEffectID, i)
		}
	}
	return nil
}

func isEffectIDByte(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	case c == '.', c == '_', c == ':', c == '-':
		return true
	default:
		return false
	}
}

// ValidateGeneration reports whether generation is in the accepted range, from
// 1 through MaxGeneration. The error wraps ErrInvalidGeneration.
func ValidateGeneration(generation uint64) error {
	switch {
	case generation == 0:
		return fmt.Errorf("%w: generation must be at least 1", ErrInvalidGeneration)
	case generation > MaxGeneration:
		return fmt.Errorf("%w: generation exceeds %d", ErrInvalidGeneration, MaxGeneration)
	default:
		return nil
	}
}
