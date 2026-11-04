package claimbody

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubOwners struct {
	owner string
	err   error
}

func (s stubOwners) LoadAttemptOwner(context.Context, string) (string, error) {
	return s.owner, s.err
}

type recordedMeta struct {
	digest    string
	owner     string
	taskID    string
	size      int64
	mediaType string
	expiresAt time.Time
}

type stubMeta struct{ recorded *recordedMeta }

func (s stubMeta) PutMeta(digest, owner, taskID string, size int64, mediaType string, expiresAt time.Time) error {
	*s.recorded = recordedMeta{digest, owner, taskID, size, mediaType, expiresAt}
	return nil
}

func TestWithMetaRequiresBothHalves(t *testing.T) {
	receiver := &Receiver{}
	if _, err := receiver.WithMeta(nil, stubMeta{}, time.Hour); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidConfig)
	}
	if _, err := receiver.WithMeta(stubOwners{}, nil, time.Hour); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidConfig)
	}
	if _, err := receiver.WithMeta(stubOwners{}, stubMeta{}, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestTheMetadataRecordsWhoMayReadTheBody(t *testing.T) {
	var recorded recordedMeta
	receiver := &Receiver{}
	if _, err := receiver.WithMeta(stubOwners{owner: "alice"}, stubMeta{recorded: &recorded}, time.Hour); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err := receiver.recordMeta(context.Background(), "attempt-1", "abc", 12, "text/plain"); err != nil {
		t.Fatalf("recordMeta() error = %v", err)
	}
	if recorded.owner != "alice" || recorded.size != 12 || recorded.mediaType != "text/plain" {
		t.Fatalf("recorded = %+v", recorded)
	}
	if recorded.expiresAt.Before(before.Add(time.Hour)) {
		t.Fatalf("expires_at = %v, want about an hour from now", recorded.expiresAt)
	}
}

func TestAnAttemptWithoutAnOwnerIsRefused(t *testing.T) {
	var recorded recordedMeta
	receiver := &Receiver{}
	if _, err := receiver.WithMeta(stubOwners{}, stubMeta{recorded: &recorded}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := receiver.recordMeta(context.Background(), "attempt-1", "abc", 12, ""); err == nil {
		t.Fatal("recordMeta() accepted an attempt with no owner")
	}
}

func TestAReceiverWithoutMetaRecordsNothing(t *testing.T) {
	receiver := &Receiver{}
	if err := receiver.recordMeta(context.Background(), "attempt-1", "abc", 12, ""); err != nil {
		t.Fatalf("recordMeta() error = %v", err)
	}
}
