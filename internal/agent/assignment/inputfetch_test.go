package assignment

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

type recordingFetcher struct {
	fetched []contract.Digest
	err     error
}

func (f *recordingFetcher) Fetch(_ context.Context, digest contract.Digest) error {
	if f.err != nil {
		return f.err
	}
	f.fetched = append(f.fetched, digest)
	return nil
}

// A Task without inputs fetches nothing, even with no fetcher configured: the
// deployment's old behaviour is unchanged.
func TestATaskWithoutInputsNeedsNoFetcher(t *testing.T) {
	service := &Service{cfg: Config{}}
	if err := service.fetchInputs(context.Background(), nil); err != nil {
		t.Fatalf("fetchInputs() = %v, want nothing to do", err)
	}
}

// An Attempt that declares inputs is refused when the deployment has no fetcher:
// starting it unread would produce a result about different data (ADR-0029 §5.7).
func TestDeclaredInputsAreRefusedWithoutAFetcher(t *testing.T) {
	service := &Service{cfg: Config{}}
	err := service.fetchInputs(context.Background(), []contract.Digest{contract.SHA256([]byte("a"))})
	if err == nil {
		t.Fatal("fetchInputs() started an Attempt with unread inputs")
	}
}

// Every declared input is fetched, in order, before the handler starts.
func TestEveryInputIsFetchedInOrder(t *testing.T) {
	first := contract.SHA256([]byte("a"))
	second := contract.SHA256([]byte("b"))
	fetcher := &recordingFetcher{}
	service := &Service{cfg: Config{Inputs: fetcher}}

	if err := service.fetchInputs(context.Background(), []contract.Digest{first, second}); err != nil {
		t.Fatal(err)
	}
	if len(fetcher.fetched) != 2 || fetcher.fetched[0].Hex() != first.Hex() {
		t.Fatalf("fetched = %v, want both digests in order", fetcher.fetched)
	}
}

// A failed fetch stops the sequence: the remaining inputs are not fetched, and the
// Attempt is refused.
func TestAFailedFetchStopsTheSequence(t *testing.T) {
	fetcher := &recordingFetcher{err: errors.New("unavailable")}
	service := &Service{cfg: Config{Inputs: fetcher}}
	if err := service.fetchInputs(context.Background(), []contract.Digest{contract.SHA256([]byte("a"))}); err == nil {
		t.Fatal("fetchInputs() ignored a failed fetch")
	}
}
