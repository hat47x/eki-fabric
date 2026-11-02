package service

import (
	"bytes"
	"context"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
	claimbody "github.com/hat47x/eki-fabric/internal/control/claimbody"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

// bodySink adapts the artifact file store to the receiver's BodySink. The
// receiver hands over the measured maximum from the WorkOrder it read; the
// sink applies it to the bytes it reads.
type bodySink struct {
	store *artifactfile.Store
}

func (b bodySink) Put(body []byte, claimedDigest string, maxBytes int64) (string, int64, error) {
	return b.store.Put(bytes.NewReader(body), claimedDigest, maxBytes)
}

// bodyReceiver adapts the claimbody.Receiver to the Worker listener's
// BodyReceiver: the arguments arrive in the wire order the mTLS handler
// reads them.
type bodyReceiver struct {
	receiver *claimbody.Receiver
}

func (b bodyReceiver) ReceiveBody(ctx context.Context, worker string, attemptID ids.AttemptID, body []byte, declaredMediaType string) error {
	_, err := b.receiver.Receive(ctx, attemptID, worker, body, declaredMediaType)
	return err
}
