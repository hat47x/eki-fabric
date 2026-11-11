package teideleg

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrInvalidOperation means TEI's side of the request is not complete enough
// to compose an EKI request. The adapter refuses locally rather than letting
// EKI answer a request whose meaning is already lost.
var ErrInvalidOperation = errors.New("invalid TEI operation")

var (
	digestHexPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	executorIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)
	inputNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Project composes the EKI requests for one TEI operation. It is pure: the
// same operation always yields the same idempotency key, client request id and
// request body, so a retry converges on the same Task instead of starting a
// second execution.
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
	ingests := make([]IngestIntentRequest, 0, len(op.Inputs))
	for _, in := range op.Inputs {
		inputs = append(inputs, ArtifactReference{Name: in.Name, ArtifactID: in.SHA256Hex})
		ingests = append(ingests, IngestIntentRequest{
			Schema:             "eki.artifact-ingest-intent-request/v1",
			DeclaredSizeBytes:  in.SizeBytes,
			MediaType:          in.MediaType,
			RequestedDataClass: in.DataClass,
			ExpectedDigest:     &Digest{Algorithm: "sha256", Value: in.SHA256Hex},
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
		Schema:          ProjectionSchema,
		Operation:       op,
		IdempotencyKey:  "tei-eki:" + hexDigest[:48],
		ClientRequestID: "tei.eki-" + hexDigest[:32],
		Ingests:         ingests,
		Task: TaskRequest{
			Schema:     taskRequestSchema,
			Executor:   ExecutorRequirement{ID: op.ExecutorID, VersionConstraint: op.ExecutorVersionConstraint},
			Inputs:     inputs,
			Parameters: parameters,
			Resources:  op.Resources,
			Reliability: ReliabilityRequest{
				// The operation is read-only and deterministic. Retrying is safe
				// only because effect_mode is none; a future effectful operation
				// must not reuse this composition unchanged.
				Retryable:      true,
				Preemptible:    true,
				Checkpointable: false,
				Deterministic:  true,
				MaxAttempts:    2,
				EffectMode:     "none",
			},
			Deadline:        op.Deadline.UTC().Format(time.RFC3339),
			Priority:        op.Priority,
			ClientRequestID: "tei.eki-" + hexDigest[:32],
		},
		Bindings: bindings(),
	}
	return projection, nil
}

// operationDigest is the canonical identity of one delegation. It includes
// the requested conditions and the deadline, so a different request is a
// different key: EKI rejects reusing a key for a different body with 409.
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
	case len(op.Command) == 0:
		return fmt.Errorf("%w: command is empty", ErrInvalidOperation)
	case !digestHexPattern.MatchString(op.ExecutableSHA256):
		return fmt.Errorf("%w: executable_sha256 is not a SHA-256 hex digest", ErrInvalidOperation)
	case op.Toolchain == "":
		return fmt.Errorf("%w: toolchain is empty", ErrInvalidOperation)
	case !executorIDPattern.MatchString(op.ExecutorID):
		return fmt.Errorf("%w: executor id %q does not match EKI's pattern", ErrInvalidOperation, op.ExecutorID)
	case len(op.Inputs) == 0:
		return fmt.Errorf("%w: at least one input artifact is required", ErrInvalidOperation)
	case op.Deadline.IsZero():
		return fmt.Errorf("%w: deadline is required", ErrInvalidOperation)
	case op.Resources.CPUMillis <= 0 || op.Resources.MemoryBytes <= 0 ||
		op.Resources.WallTimeSeconds <= 0 || op.Resources.OutputBytesMax < 0 ||
		op.Resources.NetworkBytesMax < 0:
		return fmt.Errorf("%w: resources are incomplete or negative", ErrInvalidOperation)
	case op.ExpectedOutputSHA256 != "" && !digestHexPattern.MatchString(op.ExpectedOutputSHA256):
		return fmt.Errorf("%w: expected_output_sha256 is not a SHA-256 hex digest", ErrInvalidOperation)
	}
	for i, in := range op.Inputs {
		switch {
		case !inputNamePattern.MatchString(in.Name):
			return fmt.Errorf("%w: inputs[%d].name %q does not match EKI's pattern", ErrInvalidOperation, i, in.Name)
		case !digestHexPattern.MatchString(in.SHA256Hex):
			return fmt.Errorf("%w: inputs[%d].sha256 is not a SHA-256 hex digest", ErrInvalidOperation, i)
		case in.SizeBytes < 0:
			return fmt.Errorf("%w: inputs[%d].size_bytes is negative", ErrInvalidOperation, i)
		case in.MediaType == "":
			return fmt.Errorf("%w: inputs[%d].media_type is empty", ErrInvalidOperation, i)
		case in.DataClass == "":
			return fmt.Errorf("%w: inputs[%d].data_class is empty", ErrInvalidOperation, i)
		}
	}
	return nil
}

// bindings is the mapping table this spike verified against EKI at the revision
// recorded in RESULTS_2026-11-11.md. Every row whose status is not "enforced"
// is a reason TEI must not read the receipt as a stronger claim than it is.
func bindings() []Binding {
	return []Binding{
		{
			TEIField: "入力の内容同一性（入力Artifactのdigest）",
			EKIField: "task_request.inputs[].artifact_id（SHA-256 hex）",
			Status:   BindingEnforced,
			Note:     "Admission refuses an input unless the deployment keeps a body under that digest (#763). The upload route re-measures the digest while writing (#245), so the name and the bytes agree.",
		},
		{
			TEIField: "実行体のdigest",
			EKIField: "task_request.parameters.tei.executable_sha256（TaskRequest.executor に digest の欄はない）",
			Status:   BindingRecordedOnly,
			Note:     "EKI's ExecutorRequirement carries only id and version_constraint. Parameters are not passed to Workers (openapi.json, TaskRequest.parameters), so the digest records what TEI asked for, not what ran.",
		},
		{
			TEIField: "executorの版",
			EKIField: "task_request.executor.version_constraint",
			Status:   BindingRecordedOnly,
			Note:     "The contract records the constraint into the request fingerprint and does not evaluate it; a fleet that cannot satisfy it fails later, at execution.",
		},
		{
			TEIField: "executorの選択",
			EKIField: "task_request.executor.id",
			Status:   BindingEnforced,
			Note:     "Admission refuses an executor that the deployment did not approve (approved_executors).",
		},
		{
			TEIField: "toolchain",
			EKIField: "task_request.parameters.tei.toolchain",
			Status:   BindingRecordedOnly,
			Note:     "No field places a toolchain. EKI's P0 execution is a fixed command from the Worker's trusted configuration, not a command built from Task fields.",
		},
		{
			TEIField: "実行条件（CPU・メモリ・disk・出力・network・wall time）",
			EKIField: "task_request.resources",
			Status:   BindingEnforced,
			Note:     "Admission checks the per-task ceiling; the Worker watches CPU, memory and temporary disk and stops the process tree on a breach. network is not enforced and GPU is unsupported at this revision.",
		},
		{
			TEIField: "期限",
			EKIField: "task_request.deadline",
			Status:   BindingRecordedOnly,
			Note:     "The deadline is admitted, but a Task is not moved to a terminal state when it passes (#111). A client must impose its own wait limit; TEI treats the absence of a result as undetermined.",
		},
		{
			TEIField: "副作用の不在",
			EKIField: "task_request.reliability.effect_mode",
			Status:   BindingEnforced,
			Note:     "P0 admits only effect_mode \"none\". This is what makes a duplicate or retried execution safe for this operation.",
		},
		{
			TEIField: "再実行の予算",
			EKIField: "task_request.reliability.max_attempts",
			Status:   BindingRecordedOnly,
			Note:     "Policy sets the effective retry budget; the requested value is not a guarantee.",
		},
		{
			TEIField: "実際のAttempt",
			EKIField: "task_public_status.attempt_count（Attempt・Lease・Fenceの識別子は公開されない）",
			Status:   BindingRecordedOnly,
			Note:     "The public projection excludes Worker, Lease and Attempt topology by design, so TEI can count Attempts but cannot correlate a result with one. Lease and Fence remain EKI's responsibility and are not delegated.",
		},
		{
			TEIField: "結果Artifactの取得",
			EKIField: "task_result.artifacts[]・GET /v1/artifacts/{artifact_id}",
			Status:   BindingEnforced,
			Note:     "A result entry says whether the body is fetchable now. An accepted result exists only after EKI's Result Acceptance, which at this revision still lacks the independent verifiers the default policy requires (#242).",
		},
		{
			TEIField: "結果のdigest",
			EKIField: "task_result.output_digest",
			Status:   BindingRecordedOnly,
			Note:     "The field is optional and is the digest recorded in the durable AcceptedResult, never recomputed by the Client. TEI must compare it with its own expected digest; a missing value is unverifiable, not a match.",
		},
		{
			TEIField: "キャッシュの局所性・配置の希望",
			EKIField: "（要求に欄がない）",
			Status:   BindingUnavailable,
			Note:     "Placement is EKI's decision. The only deployment-level hint is public_inputs; TEI cannot ask for a warm build cache, which is what makes a distributed build slower than a local one.",
		},
		{
			TEIField: "結果のReceipt（独立した検証）",
			EKIField: "（TEI側で構成する。EKIのAcceptedResultとは別物）",
			Status:   BindingUnavailable,
			Note:     "EKI does not issue a TEI-readable receipt. The receipt in this spike is assembled by TEI from what it observed, and says what was checked and what was not.",
		},
	}
}

// NewDeadline returns the absolute deadline for one delegation. The caller
// fixes it once so that a retry of the same operation keeps the same
// idempotency key.
func NewDeadline(now time.Time, wait time.Duration) time.Time {
	return now.UTC().Add(wait).Truncate(time.Second)
}
