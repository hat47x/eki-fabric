package claimclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

// MaxResponseBytes bounds a body-endpoint answer.
const maxBodyResponseBytes = 8 << 10

// ErrBodyNotKept means the Control Plane refused the body of a claim. The
// queue's entry is unaffected: the claim report already happened, and the
// body's refusal does not unreport it. The sender may try again (for
// example after the receiver's store recovers), or the attempt's outcome is
// already recorded and the body no longer matters.
var ErrBodyNotKept = fmt.Errorf("claim body was not kept")

// SendBody sends the body of a completed claim, after the claim itself, to
// the Control Plane's claim-body endpoint (ADR-0034 §2.2). The request is
// one envelope line, a newline, then the raw bytes.
func (c *HTTPClient) SendBody(ctx context.Context, attemptID ids.AttemptID, body []byte, declaredMediaType string) error {
	if attemptID == "" {
		return fmt.Errorf("%w: attempt id is required", ErrBodyNotKept)
	}
	if declaredMediaType == "" {
		declaredMediaType = testprofile.MediaTypeOctetStream
	}
	line, err := json.Marshal(struct {
		AttemptID ids.AttemptID `json:"attempt_id"`
		MediaType string        `json:"media_type,omitempty"`
	}{AttemptID: attemptID, MediaType: declaredMediaType})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBodyNotKept, err)
	}
	payload := append(append([]byte{}, line...), '\n')
	payload = append(payload, body...)

	bodyEndpoint := c.base + testprofile.BodyPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, bodyEndpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBodyNotKept, err)
	}
	req.Header.Set("Content-Type", testprofile.ContentType)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	httpClient := c.http
	timeout := httpClient.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	sendCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := httpClient.Do(req.WithContext(sendCtx))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBodyNotKept, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBodyNotKept, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: control plane answered HTTP %d", ErrBodyNotKept, resp.StatusCode)
	}
	if len(raw) > maxBodyResponseBytes {
		return fmt.Errorf("%w: response is too large", ErrBodyNotKept)
	}
	return nil
}
