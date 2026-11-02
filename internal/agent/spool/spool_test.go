package spool

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	clock := &now
	store, err := New(t.TempDir(), 24*time.Hour, func() time.Time { return *clock })
	if err != nil {
		t.Fatal(err)
	}
	return store, clock
}

func TestABodyIsKeptAndReadBackWithItsDigest(t *testing.T) {
	store, _ := newStore(t)
	body := "hello, worker"
	record, err := store.Put(strings.NewReader(body), 1024)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	if record.Size != int64(len(body)) {
		t.Fatalf("size = %d, want %d", record.Size, len(body))
	}
	if record.Digest != contract.SHA256([]byte(body)) {
		t.Fatal("the digest does not describe the body")
	}
	reader, err := store.Open(record)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("body = %q, want %q", got, body)
	}
}

func TestABodyOverTheLimitIsRefusedAndNothingIsLeft(t *testing.T) {
	store, _ := newStore(t)
	if _, err := store.Put(strings.NewReader("0123456789"), 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want %v", err, ErrTooLarge)
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the store left %d entries", len(entries))
	}
}

func TestAnExpiredBodyIsRefused(t *testing.T) {
	store, clock := newStore(t)
	record, err := store.Put(strings.NewReader("body"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	*clock = record.ExpiresAt
	if _, err := store.Open(record); !errors.Is(err, ErrExpired) {
		t.Fatalf("error = %v, want %v", err, ErrExpired)
	}
}

func TestATamperedBodyIsRefused(t *testing.T) {
	store, _ := newStore(t)
	record, err := store.Put(strings.NewReader("body"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.path(record), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(record); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrDigestMismatch)
	}
}

func TestSweepRemovesTheBodiesWhoseRetentionPassed(t *testing.T) {
	store, clock := newStore(t)
	old, err := store.Put(strings.NewReader("old"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(12 * time.Hour)
	fresh, err := store.Put(strings.NewReader("fresh"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(13 * time.Hour)
	removed, err := store.Sweep()
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := store.Open(old); err == nil {
		t.Fatal("the old body is still readable")
	}
	if _, err := store.Open(fresh); err != nil {
		t.Fatalf("the fresh body was removed: %v", err)
	}
}

func TestUsageCountsWhatTheStoreHolds(t *testing.T) {
	store, clock := newStore(t)
	if _, err := store.Put(strings.NewReader("first"), 1024); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(20 * time.Hour)
	if _, err := store.Put(strings.NewReader("second!"), 1024); err != nil {
		t.Fatal(err)
	}
	usage, err := store.Usage()
	if err != nil {
		t.Fatalf("Usage() error = %v", err)
	}
	if usage.Bodies != 2 || usage.Bytes != int64(len("first")+len("second!")) {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Expired != 0 {
		t.Fatalf("expired = %d, want 0", usage.Expired)
	}
}

func TestUsageCountsABodyPastItsRetentionAsExpired(t *testing.T) {
	store, clock := newStore(t)
	if _, err := store.Put(strings.NewReader("body"), 1024); err != nil {
		t.Fatal(err)
	}
	*clock = clock.Add(25 * time.Hour)
	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if usage.Bodies != 1 || usage.Expired != 1 {
		t.Fatalf("usage = %+v, want one expired body", usage)
	}
	if usage.Bytes != 4 {
		t.Fatalf("bytes = %d, want 4", usage.Bytes)
	}
}

func TestUsageIgnoresFilesTheStoreDidNotWrite(t *testing.T) {
	store, clock := newStore(t)
	if _, err := store.Put(strings.NewReader("body"), 1024); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.dir+"/notes.txt", []byte("not a body"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = clock
	usage, err := store.Usage()
	if err != nil {
		t.Fatal(err)
	}
	if usage.Bodies != 1 {
		t.Fatalf("bodies = %d, want 1", usage.Bodies)
	}
}
