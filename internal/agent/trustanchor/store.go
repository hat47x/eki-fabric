package trustanchor

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

// StoreFromFile rebuilds the in-memory version store from a document. It is what
// a caller needs before accepting another version: the store refuses a version
// that does not follow the newest one it holds, and the document is the record of
// what that was (ADR-0036, #478).
func StoreFromFile(doc File) (*trustbundle.Store, error) {
	if len(doc.TrustBundleVersions) == 0 {
		return nil, fmt.Errorf("%w: the document holds no version", ErrInvalid)
	}
	first, err := enrollwire.DecodeBundle(doc.TrustBundleVersions[0])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	store, err := trustbundle.NewStore(first)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for _, wire := range doc.TrustBundleVersions[1:] {
		signed, err := enrollwire.DecodeBundle(wire)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		current, err := store.Current()
		if err != nil {
			return nil, err
		}
		if err := signed.Bundle.Follows(current.Bundle); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		store.Append(signed)
	}
	return store, nil
}

// ErrNoVersion is returned when a document has no history to rebuild a store from.
var ErrNoVersion = errors.New("trustanchor: the document holds no trust bundle version")
