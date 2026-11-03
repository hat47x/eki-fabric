package enroll

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

// AcceptBundleToFile verifies the version an enrollment response carries and
// writes it into the Agent's trust anchor document: read, rebuild the store,
// accept, append, replace (ADR-0036 §2.1, #478).
//
// The whole sequence fails closed. A version that does not verify, or that does
// not follow the stored history, leaves the file exactly as it was.
func AcceptBundleToFile(path string, out enrollwire.Response, key ed25519.PublicKey, now time.Time) error {
	return AcceptBundleToFileWithKeys(path, out, []ed25519.PublicKey{key}, now)
}

// AcceptBundleToFileWithKeys is the same step with the keys a handover leaves in
// play. The Control Plane may sign the next version with a key the Agent already
// trusts or with the one it is moving to (#478, ADR-0038 §7); trying each in turn is
// what keeps a rotation from stopping the Agent from following the history.
//
// A key that does not verify changes nothing: the store only grows on success, so
// trying the next key is safe.
func AcceptBundleToFileWithKeys(path string, out enrollwire.Response, keys []ed25519.PublicKey, now time.Time) error {
	if len(keys) == 0 {
		return ErrNoBundle
	}
	if out.TrustBundle == nil {
		return ErrNoBundle
	}
	signed, err := enrollwire.DecodeBundle(*out.TrustBundle)
	if err != nil {
		return err
	}
	doc, err := trustanchor.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the trust anchor: %w", err)
	}
	store, err := trustanchor.StoreFromFile(doc)
	if err != nil {
		return fmt.Errorf("rebuild the trust bundle store: %w", err)
	}
	var last error
	accepted := false
	for _, key := range keys {
		err := store.AcceptSigned(signed, key, now)
		if err == nil {
			accepted = true
			break
		}
		last = err
	}
	if !accepted {
		return last
	}
	next, err := doc.WithVersion(signed)
	if err != nil {
		return err
	}
	return trustanchor.WriteFile(path, next)
}
