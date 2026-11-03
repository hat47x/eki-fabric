package service

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

// ErrTrustBundleKey means the configured control key cannot be used to verify a
// signed version.
var ErrTrustBundleKey = fmt.Errorf("invalid trust bundle key")

// sponsorKey parses the control public key an Administrator signs versions with
// (ADR-0038 §6). The Control Plane holds only this half, so a compromise can refuse
// a version but cannot create one.
//
// The key is accepted as plain hex or with the "sha256:" style prefix used
// elsewhere for fingerprints stripped, because the two forms appear side by side in
// the configuration and asking an operator to remember which is which invites the
// wrong one.
func sponsorKey(value string) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: empty", ErrTrustBundleKey)
	}
	if _, rest, ok := strings.Cut(trimmed, ":"); ok {
		trimmed = rest
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: not hexadecimal", ErrTrustBundleKey)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrTrustBundleKey, len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// admitterFor builds the Administrator side when everything it needs is present.
// A missing piece returns nil, which hides the route: an admission path without a
// key or without a store would accept nothing, and a hidden route says that
// plainly.
func admitterFor(store *trustbundle.Store, book *trustbundle.ApprovalBook, key string) (*trustAdmitter, error) {
	if store == nil || book == nil || strings.TrimSpace(key) == "" {
		return nil, nil
	}
	public, err := sponsorKey(key)
	if err != nil {
		return nil, err
	}
	return &trustAdmitter{store: store, key: public, book: book}, nil
}
