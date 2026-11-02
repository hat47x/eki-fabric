package enroll

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

// ErrNoBundle is returned when a response carries no trust bundle and the caller
// asked to accept one. It is not an error for the enrollment itself: an older
// Control Plane does not send a version.
var ErrNoBundle = fmt.Errorf("the enrollment response carries no trust bundle")

// AcceptBundle verifies the version an enrollment response carries and keeps it
// in the store the Agent uses (ADR-0036 §2.1). The key is the Control Plane's
// signing key, which the Agent was given out of band: without a key to verify
// against, a version would be taken on the word of whoever answered.
//
// A nil store is a programming error rather than a deployment choice: the Agent
// that installs a version must have somewhere to keep it.
func AcceptBundle(out enrollwire.Response, key ed25519.PublicKey, store *trustbundle.Store, now time.Time) error {
	if out.TrustBundle == nil {
		return ErrNoBundle
	}
	if store == nil {
		return fmt.Errorf("no trust bundle store to keep the version in")
	}
	signed, err := enrollwire.DecodeBundle(*out.TrustBundle)
	if err != nil {
		return err
	}
	return store.AcceptSigned(signed, key, now)
}
