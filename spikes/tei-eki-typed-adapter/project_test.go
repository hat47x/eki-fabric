package teitypedadapter_test

import (
	"testing"

	"github.com/hat47x/eki-fabric/spikes/tei-eki-typed-adapter"
)

func TestProjectPureComputation(t *testing.T) {
	op := teitypedadapter.Operation{
		ID:               "pure-compute-v1",
		SourceRevision:   "test-rev",
		ExecutableSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Toolchain:        "go1.25.0",
		Command:          "test-bin",
		ExecutorID:       "tei-pure-compute",
		Inputs: []teitypedadapter.ArtifactInput{
			{Name: "input.txt", SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", MediaType: "text/plain", SizeBytes: 11},
		},
		Resources: teitypedadapter.ResourceRequest{
			CPUMillis:       1000,
			MemoryBytes:     1024 * 1024,
			WallTimeSeconds: 60,
		},
		Reliability: teitypedadapter.ReliabilityRequest{
			Retryable:      true,
			Preemptible:    true,
			Checkpointable: false,
			Deterministic:  true,
			MaxAttempts:    2,
			EffectMode:     "none",
		},
		Deadline:      "2026-11-11T22:00:00Z",
		Priority:      "normal",
		ResultProfile: "default",
	}

	proj, err := teitypedadapter.Project(op)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	if proj.Schema != teitypedadapter.SchemaProjection {
		t.Errorf("Schema = %q, want %q", proj.Schema, teitypedadapter.SchemaProjection)
	}
	if proj.Task.Schema != teitypedadapter.SchemaTaskRequest {
		t.Errorf("Task.Schema = %q, want %q", proj.Task.Schema, teitypedadapter.SchemaTaskRequest)
	}
	if proj.Task.Executor.ID != "tei-pure-compute" {
		t.Errorf("Executor.ID = %q, want %q", proj.Task.Executor.ID, "tei-pure-compute")
	}
	if len(proj.Task.Inputs) != 1 {
		t.Fatalf("len(Inputs) = %d, want 1", len(proj.Task.Inputs))
	}
	if proj.Task.Inputs[0].ArtifactID != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("Input ArtifactID = %q", proj.Task.Inputs[0].ArtifactID)
	}
	if proj.Task.Reliability.EffectMode != "none" {
		t.Errorf("EffectMode = %q, want %q", proj.Task.Reliability.EffectMode, "none")
	}

	// Check bindings reflect gaps
	foundMissing := false
	foundRecorded := false
	for _, b := range proj.Bindings {
		if b.Missing {
			foundMissing = true
		}
		if b.RecordedOnly {
			foundRecorded = true
		}
	}
	if !foundMissing {
		t.Error("expected at least one missing binding")
	}
	if !foundRecorded {
		t.Error("expected at least one recorded-only binding")
	}
}
