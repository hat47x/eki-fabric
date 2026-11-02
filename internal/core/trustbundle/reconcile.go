package trustbundle

import (
	"errors"
	"fmt"
)

// ErrConfigAhead is returned when the configured version is newer than the one
// the State Store holds (ADR-0036 §6). The Control Plane refuses to start: it
// cannot tell whether the configuration is a rollback, a mistake, or a sign that
// the store was replaced, and either answer would change who is trusted.
var ErrConfigAhead = errors.New("trustbundle: configured version is ahead of the stored one")

// ErrConfigDiverged is returned when the configured version equals the stored
// one but the contents differ. A version number identifies one decision, so two
// different bundles cannot share it.
var ErrConfigDiverged = errors.New("trustbundle: configured version differs from the stored one")

// Reconcile decides, at startup, which version the Control Plane should use.
//
// The rules follow ADR-0036 §6: the first run takes the configured version and
// stores it; afterwards the store is authoritative, so a stale configuration is
// ignored, while a configuration that is ahead of the store, or that disagrees
// with it at the same version, stops the start.
func Reconcile(config Bundle, stored []Signed) (Bundle, error) {
	if err := config.Validate(); err != nil {
		return Bundle{}, err
	}
	if len(stored) == 0 {
		return config, nil
	}
	current := stored[len(stored)-1].Bundle
	if err := current.Validate(); err != nil {
		return Bundle{}, err
	}
	switch {
	case config.Version < current.Version:
		return current, nil
	case config.Version > current.Version:
		return Bundle{}, fmt.Errorf("%w: configured %d, stored %d", ErrConfigAhead, config.Version, current.Version)
	case config.Digest() != current.Digest():
		return Bundle{}, fmt.Errorf("%w: version %d", ErrConfigDiverged, config.Version)
	default:
		return current, nil
	}
}
