package teitypedadapter_test

import (
	"encoding/json"
	"testing"

	"github.com/hat47x/eki-fabric/spikes/tei-eki-typed-adapter"
)

func TestProjectionSchemaStability(t *testing.T) {
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

	encoded, err := json.Marshal(proj)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded teitypedadapter.Projection
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.Schema != proj.Schema {
		t.Error("Projection schema is not stable across JSON round-trip")
	}
	if decoded.IdempotencyKey != proj.IdempotencyKey {
		t.Error("IdempotencyKey is not stable across JSON round-trip")
	}
}

func TestInvalidOperationRejectsLocally(t *testing.T) {
	tests := []struct {
		name string
		op   teitypedadapter.Operation
	}{
		{
			name: "empty id",
			op:   teitypedadapter.Operation{},
		},
		{
			name: "invalid executor id",
			op: teitypedadapter.Operation{
				ID:               "op",
				SourceRevision:   "rev",
				ExecutableSHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				Toolchain:        "go1.25.0",
				Command:          "cmd",
				ExecutorID:       "INVALID ID",
				Inputs: []teitypedadapter.ArtifactInput{
					{Name: "input.txt", SHA256Hex: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", MediaType: "text/plain", SizeBytes: 11},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := teitypedadapter.Project(tt.op)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tt.name)
			}
		})
	}
}
