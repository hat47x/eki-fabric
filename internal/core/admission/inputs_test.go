package admission

import (
	"context"
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

type fixedInputs map[string]contract.Digest

func (f fixedInputs) ResolveArtifact(_ context.Context, id string) (contract.Digest, error) {
	if d, ok := f[id]; ok {
		return d, nil
	}
	return contract.Digest{}, ErrUnresolvableArtifact
}

// requestWithInputs is the P0 example with one declared input.
func requestWithInputs(id string) []byte {
	return []byte(`{
  "schema": "eki.task-request/v1",
  "executor": {"id": "dev.example.transform", "version_constraint": "^1"},
  "inputs": [{"name": "a", "artifact_id": "` + id + `"}],
  "parameters": {"mode": "normalize"},
  "resources": {"cpu_millis": 1000, "memory_bytes": 268435456, "wall_time_seconds": 300, "output_bytes_max": 16777216, "network_bytes_max": 134217728},
  "reliability": {"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true, "max_attempts": 3, "effect_mode": "none"},
  "priority": "normal"
}`)
}

// A deployment that can resolve inputs admits them, and the digests come back with
// the Task so the record can carry them (ADR-0029 §5.6).
func TestInputsAreAdmittedWhenTheDeploymentCanResolveThem(t *testing.T) {
	digest := contract.SHA256([]byte("artifact-a"))
	svc, _ := newService(t, func(cfg *Config) { cfg.Inputs = fixedInputs{"art_1": digest} })

	accepted, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-input-001", requestWithInputs("art_1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted.InputDigests) != 1 || accepted.InputDigests[0].Hex() != digest.Hex() {
		t.Fatalf("InputDigests = %v, want the resolved digest", accepted.InputDigests)
	}
}

// Without a resolver, inputs stay refused exactly as before: an Artifact is never
// trusted by name.
func TestInputsStayRefusedWithoutAResolver(t *testing.T) {
	svc, _ := newService(t)
	_, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-input-002", requestWithInputs("art_1"))
	wantError(t, err, KindUnprocessable, "ARTIFACT_NOT_AVAILABLE")
}

// An input the deployment cannot resolve fails the admission: a Task with a hole
// where its input should be would be placed against nothing.
func TestAnUnresolvableInputIsRefused(t *testing.T) {
	svc, _ := newService(t, func(cfg *Config) { cfg.Inputs = fixedInputs{} })
	_, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-input-003", requestWithInputs("art_unknown"))

	var problem *Error
	if !errors.As(err, &problem) || problem.Code != "INPUT_ARTIFACT_UNRESOLVABLE" {
		t.Fatalf("Submit() error = %v, want INPUT_ARTIFACT_UNRESOLVABLE", err)
	}
}

// A request without inputs is unaffected by a resolver being configured.
func TestATaskWithoutInputsIsAdmittedWithAResolver(t *testing.T) {
	svc, _ := newService(t, func(cfg *Config) { cfg.Inputs = fixedInputs{} })
	accepted, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-input-004", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted.InputDigests) != 0 {
		t.Fatalf("InputDigests = %v, want none", accepted.InputDigests)
	}
}
