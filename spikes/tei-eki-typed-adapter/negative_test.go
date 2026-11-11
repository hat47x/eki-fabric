package teitypedadapter_test

import (
	"testing"

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
