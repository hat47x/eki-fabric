package teitypedadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

var (
	digestHexPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	executorIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)
	inputNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Project converts a TEI Operation into an EKI Projection.
func Project(op Operation) (Projection, error) {
	if err := validateOperation(op); err != nil {
		return Projection{}, err
	}

	digest, err := operationDigest(op)
	if err != nil {
		return Projection{}, err
	}
	hexDigest := hex.EncodeToString(digest[:])

	inputs := make([]ArtifactReference, 0, len(op.Inputs))
	ingests := make([]IngestIntent, 0, len(op.Inputs))
	for _, in := range op.Inputs {
		inputs = append(inputs, ArtifactReference{
			Name:       in.Name,
			ArtifactID: in.SHA256Hex,
		})
		ingests = append(ingests, IngestIntent{
			Schema:             SchemaIngestIntent,
			DeclaredSizeBytes:  in.SizeBytes,
			MediaType:          in.MediaType,
			RequestedDataClass: in.DataClass,
			ExpectedDigest:     in.SHA256Hex,
		})
	}

	parameters := map[string]any{
		"tei": map[string]any{
			"operation":         op.ID,
			"source_revision":   op.SourceRevision,
			"executable_sha256": op.ExecutableSHA256,
			"toolchain":         op.Toolchain,
			"command":           op.Command,
		},
	}

	projection := Projection{
		Schema:         SchemaProjection,
		IdempotencyKey: "tei-eki:" + hexDigest[:48],
		ClientRequestID: "tei.eki-" + hexDigest[:32],
		Ingests:        ingests,
		Task: TaskRequest{
			Schema:     SchemaTaskRequest,
			Executor:   ExecutorRequirement{ID: op.ExecutorID, VersionConstraint: op.ExecutorVersionConstraint},
			Inputs:     inputs,
			Parameters: parameters,
			Resources: ResourceRequest{
				CPUMillis:       op.Resources.CPUMillis,
				MemoryBytes:     op.Resources.MemoryBytes,
				WallTimeSeconds: op.Resources.WallTimeSeconds,
			},
			Reliability: ReliabilityRequest{
				Retryable:      op.Reliability.Retryable,
				Preemptible:    op.Reliability.Preemptible,
				Checkpointable: op.Reliability.Checkpointable,
				Deterministic:  op.Reliability.Deterministic,
				MaxAttempts:    op.Reliability.MaxAttempts,
				EffectMode:     op.Reliability.EffectMode,
			},
			Deadline:        op.Deadline,
			Priority:        op.Priority,
			ResultProfile:   op.ResultProfile,
			ClientRequestID: "tei.eki-" + hexDigest[:32],
		},
		Bindings: bindings(),
	}

	return projection, nil
}

func operationDigest(op Operation) ([32]byte, error) {
	raw, err := json.Marshal(op)
	if err != nil {
		return [32]byte{}, fmt.Errorf("canonical operation: %w", err)
	}
	return sha256.Sum256(raw), nil
}

func validateOperation(op Operation) error {
	switch {
	case op.ID == "":
		return fmt.Errorf("%w: operation id is empty", ErrInvalidOperation)
	case op.SourceRevision == "":
		return fmt.Errorf("%w: source revision is empty", ErrInvalidOperation)
	case op.Command == "":
		return fmt.Errorf("%w: command is empty", ErrInvalidOperation)
	case !digestHexPattern.MatchString(op.ExecutableSHA256):
		return fmt.Errorf("%w: executable_sha256 is not a SHA-256 hex digest", ErrInvalidOperation)
	case op.Toolchain == "":
		return fmt.Errorf("%w: toolchain is empty", ErrInvalidOperation)
	case !executorIDPattern.MatchString(op.ExecutorID):
		return fmt.Errorf("%w: executor id %q does not match EKI's pattern", ErrInvalidOperation, op.ExecutorID)
	case len(op.Inputs) == 0:
		return fmt.Errorf("%w: at least one input artifact is required", ErrInvalidOperation)
	}
	for _, in := range op.Inputs {
		if !digestHexPattern.MatchString(in.SHA256Hex) {
			return fmt.Errorf("%w: input %q sha256 is not a hex digest", ErrInvalidOperation, in.Name)
		}
		if !inputNamePattern.MatchString(in.Name) {
			return fmt.Errorf("%w: input name %q does not match EKI's pattern", ErrInvalidOperation, in.Name)
		}
	}
	return nil
}

func bindings() []Binding {
	return []Binding{
		{TEIField: "operation.id", EKIField: "task_request.client_request_id", Enforced: true, RecordedOnly: false, Missing: false, Note: "ClientRequestID is used as idempotency handle"},
		{TEIField: "operation.inputs[].sha256", EKIField: "task_request.inputs[].artifact_id", Enforced: true, RecordedOnly: false, Missing: false, Note: "EKI ingests by digest"},
		{TEIField: "operation.executable_sha256", EKIField: "task_request.executor.digest", Enforced: false, RecordedOnly: true, Missing: true, Note: "OpenAPI has no executor digest field"},
		{TEIField: "operation.toolchain", EKIField: "task_request.parameters.tei.toolchain", Enforced: false, RecordedOnly: true, Missing: false, Note: "Parameters are not delivered to Worker"},
		{TEIField: "operation.command", EKIField: "task_request.parameters.tei.command", Enforced: false, RecordedOnly: true, Missing: false, Note: "Parameters are not delivered to Worker"},
		{TEIField: "operation.effect_mode", EKIField: "task_request.reliability.effect_mode", Enforced: true, RecordedOnly: false, Missing: false, Note: "Only 'none' is accepted in P0"},
		{TEIField: "operation.deadline", EKIField: "task_request.deadline", Enforced: true, RecordedOnly: false, Missing: false, Note: "EKI does not enforce terminal transition"},
		{TEIField: "task_public_status.attempt_id", EKIField: "(none)", Enforced: false, RecordedOnly: false, Missing: true, Note: "Not exposed in P0"},
		{TEIField: "task_result.output_digest", EKIField: "task_result.output_digest", Enforced: false, RecordedOnly: true, Missing: false, Note: "Optional in OpenAPI"},
	}
}

// ErrInvalidOperation means the TEI operation is incomplete.
var ErrInvalidOperation = errors.New("invalid TEI operation")
