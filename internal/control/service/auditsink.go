package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/control/operatorapi"
)

// auditSink sends one audit record to the deployment's store (ADR-0040 §7).
//
// It is deliberately one HTTP POST: EKI does not store the records, so the shape
// that fits is the one that reports success or failure and nothing else. No retry:
// a record written late would look like a record written at that late time, which
// is worse than a gap that the counter shows.
type auditSink struct {
	url    string
	client *http.Client
}

// auditSinkTimeout bounds one write. A store that does not answer in time counts as
// a failure: waiting longer only delays the answer the requester is waiting for.
const auditSinkTimeout = 5 * time.Second

func newAuditSink(url string) *auditSink {
	return &auditSink{url: url, client: &http.Client{Timeout: auditSinkTimeout}}
}

// Send writes one record. Anything but 2xx is a failure, including a redirect: a
// redirect means the record went somewhere the operator did not name.
func (s *auditSink) Send(ctx context.Context, record any) error {
	body, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("audit sink: encode record: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("audit sink: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("audit sink: post: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("audit sink: %s answered %d", s.url, response.StatusCode)
	}
	return nil
}

// artifactAuditErr builds the retrieval audit that can refuse (ADR-0040 §7). Nil
// means no sink is configured, and the handler keeps using the logger.
func artifactAuditErr(cfg Config, log *slog.Logger, ctx context.Context) func(clientapi.ArtifactAccess) error {
	if cfg.AuditSinkURL == "" {
		return nil
	}
	sink := newAuditSink(cfg.AuditSinkURL)
	log.Info("artifact audit goes to the configured sink", "url", cfg.AuditSinkURL)
	return func(record clientapi.ArtifactAccess) error {
		return sink.Send(ctx, record)
	}
}

// operatorAuditErr is the same for the Operator API. A refusal here stops the
// operation (ADR-0040 §5).
func operatorAuditErr(cfg Config, log *slog.Logger, ctx context.Context) func(operatorapi.Access) error {
	if cfg.AuditSinkURL == "" {
		return nil
	}
	sink := newAuditSink(cfg.AuditSinkURL)
	return func(record operatorapi.Access) error {
		return sink.Send(ctx, record)
	}
}
