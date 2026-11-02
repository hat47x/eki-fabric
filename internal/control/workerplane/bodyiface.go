package workerplane

import (
	"context"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// BodyReceiver receives the body a Worker sends after its claim report
// (ADR-0034 §2.2). The receiver measures the bytes against the claim it
// already recorded and keeps them only when they agree; the identity comes
// from the transport, never from the request.
type BodyReceiver interface {
	ReceiveBody(ctx context.Context, worker string, attemptID ids.AttemptID, body []byte, declaredMediaType string) error
}
