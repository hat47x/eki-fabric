package teideleg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Error is a non-2xx answer from EKI. Problem is decoded when the deployment
// answered Problem Details; a bare status still yields a usable Error.
type Error struct {
	Status  int
	Problem Problem
	Body    string
}

func (e *Error) Error() string {
	if e.Problem.Code != "" {
		return fmt.Sprintf("EKI answered %d %s: %s", e.Status, e.Problem.Code, e.Problem.Title)
	}
	return fmt.Sprintf("EKI answered %d: %s", e.Status, strings.TrimSpace(e.Body))
}

// IsCode reports whether the error is a Problem Details answer with the given
// code, for example RESULT_NOT_READY.
func IsCode(err error, code string) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Status > 0 && e.Problem.Code == code
}

// StatusOf returns the HTTP status of an EKI error, or 0.
func StatusOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// Client is a minimal EKI External Client Plane client. It sends only what the
// external contract declares and never reads EKI's internal state.
type Client struct { // BaseURL is the Client API root, for example "http://127.0.0.1:18080".
	BaseURL string
	// Token is the Bearer token of the principal.
	Token string
	// HTTP is the transport. nil means a client with a 60 second timeout.
	HTTP *http.Client
	// MaxBodyBytes bounds a response body read. Zero means 64 MiB.
	MaxBodyBytes int64

	mu       sync.Mutex
	requests int
}

// RequestCount is the number of HTTP requests this client has sent. It makes
// the cost of the delegation envelope visible next to the work it carries.
func (c *Client) RequestCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

// Client is one implementation of the adapter's API. The assertion keeps the
// two from drifting silently when either side changes.
var _ API = (*Client)(nil)

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) maxBody() int64 {
	if c.MaxBodyBytes > 0 {
		return c.MaxBodyBytes
	}
	return 64 << 20
}

// CreateIngestIntent reserves a bounded upload session.
func (c *Client) CreateIngestIntent(ctx context.Context, key string, req IngestIntentRequest) (IngestIntentResponse, error) {
	var out IngestIntentResponse
	raw, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, http.MethodPost, "/v1/artifacts/ingest-intents", key, "application/json", raw, http.StatusCreated, &out)
	return out, err
}

// UploadArtifact sends the bytes of one input. It follows the href the
// deployment issued; the client never chooses the destination.
func (c *Client) UploadArtifact(ctx context.Context, session UploadSession, body []byte) (UploadAccepted, error) {
	var out UploadAccepted
	href, err := c.resolve(session.Href)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, http.MethodPut, href, "", "application/octet-stream", body, http.StatusAccepted, &out)
	return out, err
}

// GetArtifact fetches an artifact body and returns it with the digest it
// measured. A body whose digest does not equal the identifier is refused by
// the caller, not trusted because the header said so.
func (c *Client) GetArtifact(ctx context.Context, artifactID string) ([]byte, string, error) {
	body, err := c.doRaw(ctx, http.MethodGet, "/v1/artifacts/"+url.PathEscape(artifactID), "", nil, http.StatusOK)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}

// SubmitTask submits one task request under an idempotency key.
func (c *Client) SubmitTask(ctx context.Context, key string, req TaskRequest) (TaskAccepted, error) {
	var out TaskAccepted
	raw, err := json.Marshal(req)
	if err != nil {
		return out, err
	}
	err = c.do(ctx, http.MethodPost, "/v1/tasks", key, "application/json", raw, http.StatusAccepted, &out)
	return out, err
}

// GetTaskStatus reads the public status. Lease and Attempt identifiers are not
// part of the answer.
func (c *Client) GetTaskStatus(ctx context.Context, taskID string) (TaskPublicStatus, error) {
	var out TaskPublicStatus
	err := c.do(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(taskID), "", "", nil, http.StatusOK, &out)
	return out, err
}

// GetTaskResult reads the accepted result. RESULT_NOT_READY and
// RESULT_NOT_AVAILABLE are returned as *Error, because they are answers about
// the Task, not transport failures.
func (c *Client) GetTaskResult(ctx context.Context, taskID string) (TaskResult, error) {
	var out TaskResult
	err := c.do(ctx, http.MethodGet, "/v1/tasks/"+url.PathEscape(taskID)+"/result", "", "", nil, http.StatusOK, &out)
	return out, err
}

// CancelTask requests cancellation.
func (c *Client) CancelTask(ctx context.Context, key, taskID string) error {
	return c.do(ctx, http.MethodPost, "/v1/tasks/"+url.PathEscape(taskID)+"/cancel", key, "", nil, http.StatusAccepted, nil)
}

func (c *Client) resolve(href string) (string, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", fmt.Errorf("base URL: %w", err)
	}
	target, err := url.Parse(href)
	if err != nil {
		return "", fmt.Errorf("upload href: %w", err)
	}
	return base.ResolveReference(target).String(), nil
}

func (c *Client) do(ctx context.Context, method, pathOrURL, key, contentType string, body []byte, wantStatus int, out any) error {
	raw, err := c.doRaw(ctx, method, pathOrURL, key, bodyWithType(contentType, body), wantStatus)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s %s: %w", method, pathOrURL, err)
	}
	return nil
}

// bodyWithType returns a reader for body with the content type remembered by
// the caller. It exists so signatures stay explicit about what is JSON and
// what is opaque bytes.
func bodyWithType(contentType string, body []byte) *requestBody {
	return &requestBody{contentType: contentType, reader: bytes.NewReader(body)}
}

type requestBody struct {
	contentType string
	reader      io.Reader
}

func (c *Client) doRaw(ctx context.Context, method, pathOrURL, key string, payload *requestBody, wantStatus int) ([]byte, error) {
	target := pathOrURL
	if strings.HasPrefix(pathOrURL, "/") {
		target = strings.TrimRight(c.BaseURL, "/") + pathOrURL
	}
	var body io.Reader
	if payload != nil {
		body = payload.reader
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if payload != nil && payload.contentType != "" {
		req.Header.Set("Content-Type", payload.contentType)
	}
	res, err := c.httpClient().Do(req)
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, c.maxBody()))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != wantStatus {
		e := &Error{Status: res.StatusCode, Body: string(raw)}
		_ = json.Unmarshal(raw, &e.Problem)
		return nil, e
	}
	return raw, nil
}
