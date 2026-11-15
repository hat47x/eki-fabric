package service

import (
	"errors"
	"strings"
	"testing"
)

// baseAssignmentConfig is the smallest configuration the input checks see. The
// anchor file is empty on purpose: the tests below fail inside the input
// validation, which runs before the anchor is read (#763).
func baseAssignmentConfig(t *testing.T) *AssignmentConfig {
	t.Helper()
	return &AssignmentConfig{
		ReplayDB: t.TempDir() + "/replay.db", EventsFile: t.TempDir() + "/events.jsonl",
		TrustAnchorFile: t.TempDir() + "/anchor.json",
		Machine:         MachineConfig{CPUMillis: 1000, MemoryBytes: 1 << 30}, CeilingPercent: 50,
		ApprovedExecutors: []ExecutorApprovalConfig{{
			DecisionID: "placement-001", PayloadDigest: hex64("a"), ExecutorDigest: hex64("b"),
		}},
	}
}

func wantInputsProblem(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to mention %q", err, want)
	}
}

// A base URL without a directory is a configuration error: the fetch would have
// nowhere to put the bytes (#763).
func TestABaseURLWithoutADirectoryIsRefused(t *testing.T) {
	c := baseAssignmentConfig(t)
	c.ArtifactBaseURL = "https://worker.test"
	_, _, err := prepareAssignments(c)
	wantInputsProblem(t, err, "inputs_dir is required")
}

// A directory without a base URL is refused too: files nothing writes to would
// keep an operator waiting (#763).
func TestADirectoryWithoutABaseURLIsRefused(t *testing.T) {
	c := baseAssignmentConfig(t)
	c.InputsDir = t.TempDir()
	_, _, err := prepareAssignments(c)
	wantInputsProblem(t, err, "artifact_base_url is required")
}

// A pair pointing at a directory that is not there is refused at start, not on
// the first assignment (#763).
func TestAMissingInputsDirectoryIsRefusedAtStart(t *testing.T) {
	c := baseAssignmentConfig(t)
	c.ArtifactBaseURL = "https://worker.test"
	c.InputsDir = c.ReplayDB + "/does-not-exist"
	_, _, err := prepareAssignments(c)
	wantInputsProblem(t, err, "inputs_dir")
}

// A usable pair builds a fetcher; the settings alone decide, so a deployment that
// sets both does not silently run unfetched assignments.
func TestAUsablePairBuildsAFetcher(t *testing.T) {
	dir := t.TempDir()
	if inputFetcher("https://worker.test", dir, nil) == nil {
		t.Fatal("inputFetcher() returned nil for a usable pair")
	}
}

// Neither setting leaves the Agent without a fetcher, and an assignment that
// declares inputs is then refused rather than started unread.
func TestNoSettingsLeavesNoFetcher(t *testing.T) {
	if inputFetcher("", "", nil) != nil {
		t.Fatal("inputFetcher() built a fetcher without settings")
	}
}

// hex64 is a 64-character hex string made from one repeated character: the
// validators check the shape, not the content.
func hex64(c string) string {
	out := ""
	for i := 0; i < 64; i++ {
		out += c
	}
	return out
}
