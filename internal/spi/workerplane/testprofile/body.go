package testprofile

import (
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// BodyPath is the URL path of the claim-body endpoint of the test profile.
// It follows the claim report (Path) as ADR-0034 §2.2 orders: the claim is a
// report of a digest, the body is what makes the report checkable.
const BodyPath = "/test-profile/v1/claims/body"

// MediaTypeOctetStream is the media type a Worker declares when it has none
// to declare. The receiver records the declared type and never acts on it.
const MediaTypeOctetStream = "application/octet-stream"

// MaxKnownBodyBytes caps a claim body in flight. The test profile carries
// outputs up to the WorkOrder's maximum (1 MiB unless declared); the cap is
// the transport's guard, not the binding. The binding is the digest.
const MaxKnownBodyBytes = 8 << 20

// BodyEnvelope is the header a Worker sends before the body's bytes. The
// receiver measures the bytes it reads against the completed claim it has
// already recorded — never against this envelope.
type BodyEnvelope struct {
	AttemptID ids.AttemptID `json:"attempt_id"`
	// MediaType is what the Worker declares. A missing field means
	// MediaTypeOctetStream. The receiver records it and acts on nothing.
	MediaType string `json:"media_type,omitempty"`
}

