package teitypedadapter_test

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/spikes/tei-eki-typed-adapter"
)

func TestProjectRejectsEmptyOperation(t *testing.T) {
	_, err := teitypedadapter.Project(teitypedadapter.Operation{})
	if err == nil {
		t.Fatal("expected error for empty operation, got nil")
	}
}

func TestProjectRejectsInvalidExecutorID(t *testing.T) {
	_, err := teitypedadapter.Project(teitypedadapter.Operation{
		ID:               "op",
		SourceRevision:   "rev",
		ExecutableSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Toolchain:        "go1.25.0",
		Command:          "cmd",
		ExecutorID:       "INVALID ID",
		Inputs: []teitypedadapter.ArtifactInput{
			{Name: "input.txt", SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", MediaType: "text/plain", SizeBytes: 11},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid executor id, got nil")
	}
}

func TestProjectRecordsMissingBindings(t *testing.T) {
	proj, err := teitypedadapter.Project(teitypedadapter.Operation{
		ID:               "pure-compute-v1",
		SourceRevision:   "rev",
		ExecutableSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Toolchain:        "go1.25.0",
		Command:          "cmd",
		ExecutorID:       "tei-pure-compute",
		Inputs: []teitypedadapter.ArtifactInput{
			{Name: "input.txt", SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", MediaType: "text/plain", SizeBytes: 11},
		},
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	foundMissing := false
	for _, b := range proj.Bindings {
		if b.Missing {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Fatal("expected at least one missing binding")
	}
}

// TestProjectRejectsLeaseExpired documents a gap: deadline validation is
// not yet enforced in the typed adapter spike.
func TestProjectRejectsLeaseExpired(t *testing.T) {
	t.Skip("gap: deadline/lease validation not yet implemented in Project")
	_, err := teitypedadapter.Project(teitypedadapter.Operation{
		ID:               "lease-expired",
		SourceRevision:   "rev",
		ExecutableSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Toolchain:        "go1.25.0",
		Command:          "cmd",
		ExecutorID:       "tei-pure-compute",
		Inputs: []teitypedadapter.ArtifactInput{
			{Name: "input.txt", SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", MediaType: "text/plain", SizeBytes: 11},
		},
		Resources: teitypedadapter.ResourceRequest{
			WallTimeSeconds: 1,
		},
		Reliability: teitypedadapter.ReliabilityRequest{
			MaxAttempts: 1,
		},
		Deadline: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})
	if err == nil {
		t.Fatal("expected error for expired lease/deadline, got nil")
	}
}

// TestProjectRejectsStaleResource documents a gap: resource staleness is
// not yet enforced in the typed adapter spike.
func TestProjectRejectsStaleResource(t *testing.T) {
	t.Skip("gap: resource staleness validation not yet implemented in Project")
	_, err := teitypedadapter.Project(teitypedadapter.Operation{
		ID:               "stale-resource",
		SourceRevision:   "rev",
		ExecutableSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Toolchain:        "go1.25.0",
		Command:          "cmd",
		ExecutorID:       "tei-pure-compute",
		Inputs: []teitypedadapter.ArtifactInput{
			{Name: "input.txt", SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", MediaType: "text/plain", SizeBytes: 11},
		},
		Resources: teitypedadapter.ResourceRequest{
			WallTimeSeconds: 1,
		},
		Reliability: teitypedadapter.ReliabilityRequest{
			MaxAttempts: 1,
		},
		Deadline: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})
	if err == nil {
		t.Fatal("expected error for stale resource/expired deadline, got nil")
	}
}

// TestProjectRejectsInsufficientAuthority documents a gap: authority
// validation is not yet implemented in the typed adapter spike.
func TestProjectRejectsInsufficientAuthority(t *testing.T) {
	t.Skip("gap: authority validation not yet implemented in Project")
	_, err := teitypedadapter.Project(teitypedadapter.Operation{
		ID:               "insufficient-authority",
		SourceRevision:   "rev",
		ExecutableSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Toolchain:        "go1.25.0",
		Command:          "cmd",
		ExecutorID:       "tei-pure-compute",
		Inputs: []teitypedadapter.ArtifactInput{
			{Name: "input.txt", SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", MediaType: "text/plain", SizeBytes: 11},
		},
		Resources: teitypedadapter.ResourceRequest{
			WallTimeSeconds: 1,
		},
		Reliability: teitypedadapter.ReliabilityRequest{
			MaxAttempts: 1,
		},
		Deadline: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})
	if err == nil {
		t.Fatal("expected error for insufficient authority/expired deadline, got nil")
	}
}

// TestProjectRejectsOldAttempt documents a gap: attempt versioning is
// not yet implemented in the typed adapter spike.
func TestProjectRejectsOldAttempt(t *testing.T) {
	t.Skip("gap: attempt versioning not yet implemented in Project")
	_, err := teitypedadapter.Project(teitypedadapter.Operation{
		ID:               "old-attempt",
		SourceRevision:   "rev",
		ExecutableSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Toolchain:        "go1.25.0",
		Command:          "cmd",
		ExecutorID:       "tei-pure-compute",
		Inputs: []teitypedadapter.ArtifactInput{
			{Name: "input.txt", SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", MediaType: "text/plain", SizeBytes: 11},
		},
		Resources: teitypedadapter.ResourceRequest{
			WallTimeSeconds: 1,
		},
		Reliability: teitypedadapter.ReliabilityRequest{
			MaxAttempts: 1,
		},
		Deadline: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
	})
	if err == nil {
		t.Fatal("expected error for old attempt/expired deadline, got nil")
	}
}
