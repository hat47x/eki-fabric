package assignment

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// ErrInputDigestMismatch means the bytes served for an input are not the bytes the
// Task declared. It is refused rather than passed on: the handler would otherwise
// run on data the Control Plane never named.
var ErrInputDigestMismatch = errors.New("assignment: input does not match its digest")

// DefaultMaxInputBytes bounds one fetched input. A larger artifact is a deployment
// question, and reading it into memory to hash it would spend the endpoint's memory
// on the answer.
const DefaultMaxInputBytes = 64 << 20

// HTTPFetcher fetches inputs from the Worker Plane's artifact route (ADR-0029 §5.10).
//
// It checks the digest itself: the route names content by digest, so the fetcher can
// tell whether what arrived is what was asked for. A mismatch, a refusal and an
// oversized body are all failures of the fetch, and the Attempt is not started.
type HTTPFetcher struct {
	// BaseURL is the Worker Plane origin, without a trailing slash.
	BaseURL string
	// Client carries the Worker's certificate. Nil uses http.DefaultClient, which
	// will not authenticate: a real deployment always sets it.
	Client *http.Client
	// Save is where the verified bytes go. Nil discards them, which is only useful
	// in tests: the handler needs the input on disk to read it.
	Save func(digest contract.Digest, body []byte) error
	// MaxBytes bounds the body. Zero means DefaultMaxInputBytes.
	MaxBytes int64
}

// Fetch implements Fetcher.
func (f HTTPFetcher) Fetch(ctx context.Context, digest contract.Digest) error {
	if f.BaseURL == "" {
		return errors.New("assignment: the artifact route has no base URL")
	}
	limit := f.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxInputBytes
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	url := strings.TrimRight(f.BaseURL, "/") + "/test-profile/v1/artifacts/" + digest.Hex()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("assignment: build artifact request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("assignment: fetch input: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("assignment: fetch input: %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return fmt.Errorf("assignment: read input: %w", err)
	}
	if int64(len(body)) > limit {
		return fmt.Errorf("assignment: input %s exceeds %d bytes", digest.Hex(), limit)
	}
	if got := contract.FromSHA256(sha256.Sum256(body)); got.Hex() != digest.Hex() {
		return fmt.Errorf("%w: %s", ErrInputDigestMismatch, digest.Hex())
	}
	if f.Save == nil {
		return nil
	}
	if err := f.Save(digest, body); err != nil {
		return fmt.Errorf("assignment: store input %s: %w", digest.Hex(), err)
	}
	return nil
}

var _ Fetcher = HTTPFetcher{}
