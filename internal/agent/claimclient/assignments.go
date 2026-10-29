package claimclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

var _ workerplane.AssignmentSource = (*HTTPClient)(nil)

// ListAssignments asks the Control Plane for the signed Lease and WorkOrder of
// the Attempts bound to this Worker. The principal is not sent: the Control
// Plane takes it from the client certificate, so the request cannot ask for
// another Worker's material. The route exists only on the mTLS listener.
//
// The returned bytes are hostile until the caller verifies them.
func (c *HTTPClient) ListAssignments(ctx context.Context, _ workerplane.Principal) ([]workerplane.Assignment, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+testprofile.AssignmentsPath, bytes.NewReader(testprofile.EncodeAssignmentsRequest()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", testprofile.ContentType)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return nil, fmt.Errorf("control plane answered HTTP %d to the assignments request", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, testprofile.MaxAssignmentsBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > testprofile.MaxAssignmentsBytes {
		return nil, fmt.Errorf("assignments response is too large")
	}
	return testprofile.DecodeAssignments(raw)
}
