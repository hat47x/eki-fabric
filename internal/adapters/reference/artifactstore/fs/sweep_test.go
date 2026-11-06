package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The sweep removes what the caller says is expired, and keeps the rest.
func TestSweepRemovesOnlyWhatIsBeforeTheCutoff(t *testing.T) {
	dir := t.TempDir()
	store := Store{Dir: dir}
	old := put(t, store, "old")
	fresh := put(t, store, "fresh")

	// Age the old body past the cutoff without touching the fresh one.
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, old.Hex()), past, past); err != nil {
		t.Fatal(err)
	}
	removed, err := store.SweepOlderThan(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("SweepOlderThan() = %d, want 1", removed)
	}
	if _, _, err := store.Open(context.Background(), old); err == nil {
		t.Fatal("the expired body is still there")
	}
	if _, _, err := store.Open(context.Background(), fresh); err != nil {
		t.Fatalf("the fresh body was removed: %v", err)
	}
}

// Sweeping a directory that does not exist yet is nothing to do, not a failure.
func TestSweepingAnAbsentDirectoryIsNotAnError(t *testing.T) {
	store := Store{Dir: filepath.Join(t.TempDir(), "missing")}
	removed, err := store.SweepOlderThan(context.Background(), time.Now())
	if err != nil || removed != 0 {
		t.Fatalf("SweepOlderThan() = %d, %v; want 0, nil", removed, err)
	}
}

// A store without a directory fails rather than sweeping something unexpected.
func TestSweepingWithoutADirectoryFails(t *testing.T) {
	if _, err := (Store{}).SweepOlderThan(context.Background(), time.Now()); err == nil {
		t.Fatal("SweepOlderThan() ran without a directory")
	}
}
