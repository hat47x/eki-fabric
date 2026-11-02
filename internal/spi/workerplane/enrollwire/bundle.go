package enrollwire

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
)

// ErrInvalidBundleWire is returned for a bundle field that cannot be read back
// into a signed version.
var ErrInvalidBundleWire = errors.New("invalid trust bundle in the enrollment response")

// Bundle is the trust bundle version a Worker should hold after enrolling
// (ADR-0036 §2.1). It travels in the enrollment response so a new Worker starts
// with the version the Control Plane is using.
type Bundle struct {
	Version   uint64         `json:"version"`
	NotAfter  time.Time      `json:"not_after"`
	KeyID     string         `json:"key_id"`
	Signature string         `json:"signature"`
	Issuers   []BundleIssuer `json:"issuers"`
}

// BundleIssuer is one authority in the version.
type BundleIssuer struct {
	Fingerprint string    `json:"fingerprint"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
}

// EncodeBundle converts a signed version into the wire form.
func EncodeBundle(signed trustbundle.Signed) Bundle {
	out := Bundle{
		Version: signed.Bundle.Version, NotAfter: signed.Bundle.NotAfter, KeyID: signed.KeyID,
		Signature: base64.StdEncoding.EncodeToString(signed.Signature),
	}
	for _, issuer := range signed.Bundle.Issuers {
		out.Issuers = append(out.Issuers, BundleIssuer{
			Fingerprint: strings.TrimPrefix(issuer.Fingerprint.String(), "sha256:"), NotBefore: issuer.NotBefore, NotAfter: issuer.NotAfter,
		})
	}
	return out
}

// DecodeBundle reads the wire form back. Every field is required: a Worker that
// accepted a partial version could end up trusting nothing or the wrong root.
func DecodeBundle(wire Bundle) (trustbundle.Signed, error) {
	if wire.Version == 0 || wire.KeyID == "" || wire.Signature == "" || len(wire.Issuers) == 0 || wire.NotAfter.IsZero() {
		return trustbundle.Signed{}, fmt.Errorf("%w: missing field", ErrInvalidBundleWire)
	}
	signature, err := base64.StdEncoding.DecodeString(wire.Signature)
	if err != nil {
		return trustbundle.Signed{}, fmt.Errorf("%w: signature is not base64", ErrInvalidBundleWire)
	}
	bundle := trustbundle.Bundle{Version: wire.Version, NotAfter: wire.NotAfter}
	for _, issuer := range wire.Issuers {
		fingerprint, err := contract.ParseSHA256(issuer.Fingerprint)
		if err != nil {
			return trustbundle.Signed{}, fmt.Errorf("%w: issuer fingerprint", ErrInvalidBundleWire)
		}
		bundle.Issuers = append(bundle.Issuers, trustbundle.Issuer{
			Fingerprint: fingerprint, NotBefore: issuer.NotBefore, NotAfter: issuer.NotAfter,
		})
	}
	if err := bundle.Validate(); err != nil {
		return trustbundle.Signed{}, fmt.Errorf("%w: %v", ErrInvalidBundleWire, err)
	}
	return trustbundle.Signed{Bundle: bundle, KeyID: wire.KeyID, Signature: signature}, nil
}
