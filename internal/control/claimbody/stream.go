package claimbody

import (
	"errors"
	"fmt"
	"io"
)

// ErrSizeMismatch means the bytes available do not match the size recorded for
// the artifact. Serving them would hand the requester a body that is not the one
// the record describes (#494).
var ErrSizeMismatch = errors.New("the stored body does not match the recorded size")

// BodySource opens the stored body for a digest.
type BodySource interface {
	Open(digestHex string) (io.ReadCloser, int64, error)
}

// Stream writes the stored body to w after the access decision allowed it. It
// writes exactly the recorded size and refuses to write anything at all when the
// stored bytes are shorter or longer: a truncated body served as if complete is
// worse than an error, because the requester cannot tell it is short.
//
// The check is done while copying rather than by trusting Content-Length: a
// header that promises more than the store holds is exactly the case this
// guards.
func Stream(outcome Outcome, source BodySource, digestHex string, recordedSize int64, w io.Writer) (int64, error) {
	if outcome != OutcomeDeliver {
		return 0, fmt.Errorf("claimbody: outcome %v cannot be streamed", outcome)
	}
	if source == nil {
		return 0, fmt.Errorf("claimbody: no body source")
	}
	reader, size, err := source.Open(digestHex)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	if size != recordedSize {
		return 0, fmt.Errorf("%w: stored %d bytes, record says %d", ErrSizeMismatch, size, recordedSize)
	}
	written, err := io.Copy(w, io.LimitReader(reader, recordedSize))
	if err != nil {
		return written, err
	}
	if written != recordedSize {
		return written, fmt.Errorf("%w: wrote %d of %d bytes", ErrSizeMismatch, written, recordedSize)
	}
	return written, nil
}
