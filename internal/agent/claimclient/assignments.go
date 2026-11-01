package claimclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

var _ workerplane.AssignmentSource = (*HTTPClient)(nil)

// running is the running-Lease report one poll carries. The Agent writes it
// between polls and the assignments request reads it; the mutex keeps a
// Handler finishing an Attempt on another goroutine from racing the poll.
type running struct {
	mu    sync.Mutex
	lease string
}

func (r *running) set(leaseID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lease = leaseID
}

func (r *running) get() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lease
}

// ListAssignments asks the Control Plane for the signed Lease and WorkOrder of
// the Attempts bound to this Worker, and reports the Lease this Agent is
// executing as running (heartbeat, ADR-0030 §2.2). The principal is not sent:
// the Control Plane takes it from the client certificate, so the request
// cannot ask for another Worker's material. The route exists only on the mTLS
// listener.
//
// The returned bytes are hostile until the caller verifies them.
func (c *HTTPClient) ListAssignments(ctx context.Context, _ workerplane.Principal) ([]workerplane.Assignment, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+testprofile.AssignmentsPath,
		bytes.NewReader(testprofile.EncodeAssignmentsRequest(c.runningIDs())))
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

// runningIDs returns the current running-Lease report.
func (c *HTTPClient) runningIDs() []string {
	if c.beats == nil {
		return nil
	}
	if id := c.beats.get(); id != "" {
		return []string{id}
	}
	return nil
}

// ReportRunning records the Lease this Agent is executing, so the next poll
// reports it as running. It is the Agent side of the heartbeat (ADR-0030
// §2.2): the assignment loop writes it when a Handler takes an Attempt, and
// leaves the report empty when the Handler finishes.
func (c *HTTPClient) ReportRunning(leaseID string) {
	if c.beats == nil {
		c.beats = &running{}
	}
	c.beats.set(leaseID)
}
