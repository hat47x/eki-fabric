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
	if err := store.AcceptSigned(signed, key, now); err != nil {
		return err
	}
	next, err := doc.WithVersion(signed)
	if err != nil {
		return err
	}
	return trustanchor.WriteFile(path, next)
}
