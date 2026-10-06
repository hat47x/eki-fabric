package admission

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

const (
	TaskRequestSchema = "eki.task-request/v1"

	maxInputs      = 64
	maxExtensions  = 32
	maxParameters  = 256
	maxIDBytes     = 128
	maxVersionSpec = 64
)

var (
	executorIDPattern  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)
	artifactIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	inputNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	resultProfilePatt  = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)
	errInvalidRequest  = errors.New("invalid task request")
	errUnknownOrBadKey = errors.New("unknown or malformed field")
)

// Priority is the Client's requested priority. It is a request only; policy
// decides what is effective (RQ-016).
type Priority string

const (
	PriorityBackground Priority = "background"
	PriorityNormal     Priority = "normal"
	PriorityHigh       Priority = "high"
)

type ExecutorRequirement struct {
	ID                string `json:"id"`
	VersionConstraint string `json:"version_constraint,omitempty"`
}

type ArtifactReference struct {
	Name       string `json:"name"`
	ArtifactID string `json:"artifact_id"`
}

type ResourceRequest struct {
	CPUMillis          *int64          `json:"cpu_millis"`
	MemoryBytes        *int64          `json:"memory_bytes"`
	WallTimeSeconds    *int64          `json:"wall_time_seconds"`
	OutputBytesMax     *int64          `json:"output_bytes_max"`
	NetworkBytesMax    *int64          `json:"network_bytes_max"`
	TemporaryDiskBytes int64           `json:"temporary_disk_bytes,omitempty"`
	GPU                json.RawMessage `json:"gpu,omitempty"`
}

type ReliabilityRequest struct {
	Retryable      *bool   `json:"retryable"`
	Preemptible    *bool   `json:"preemptible"`
	Checkpointable bool    `json:"checkpointable"`
	Deterministic  bool    `json:"deterministic"`
	MaxAttempts    *int64  `json:"max_attempts"`
	EffectMode     *string `json:"effect_mode"`
}

// extensionEnvelope is accepted by syntax only. M0 registers no extension, so
// admission rejects any request that carries one.
type extensionEnvelope map[string]json.RawMessage

// TaskRequest is the decoded form of eki.task-request/v1. Required fields are
// pointers so "absent" and "zero" stay distinguishable until validation.
type TaskRequest struct {
	Schema        *string              `json:"schema"`
	Executor      *ExecutorRequirement `json:"executor"`
	Inputs        *[]ArtifactReference `json:"inputs"`
	Parameters    map[string]any       `json:"parameters"`
	Resources     *ResourceRequest     `json:"resources"`
	Reliability   *ReliabilityRequest  `json:"reliability"`
	Priority      Priority             `json:"priority,omitempty"`
	Deadline      string               `json:"deadline,omitempty"`
	ResultProfile string               `json:"result_profile,omitempty"`
	Extensions    []extensionEnvelope  `json:"extensions,omitempty"`
}

// NormalizedRequest is a validated request with defaults applied. It contains
// no Client-controlled authority: only what the Client asked for.
type NormalizedRequest struct {
	Executor      ExecutorRequirement
	Inputs        []ArtifactReference
	Parameters    map[string]any
	Resources     NormalizedResources
	Reliability   NormalizedReliability
	Priority      Priority
	Deadline      time.Time // zero when absent
	ResultProfile string
	// ExtensionCount is carried only so policy can fail closed: M0 registers no
	// extension.
	ExtensionCount int
}

type NormalizedResources struct {
	CPUMillis          int64
	MemoryBytes        int64
	WallTimeSeconds    int64
	OutputBytesMax     int64
	NetworkBytesMax    int64
	TemporaryDiskBytes int64
	GPURequested       bool
}

type NormalizedReliability struct {
	Retryable      bool
	Preemptible    bool
	Checkpointable bool
	Deterministic  bool
	MaxAttempts    uint32
	EffectMode     string
}

// ParseTaskRequest decodes and validates raw against the P0 contract. Every
// failure is a *Error of kind KindBadRequest; nothing here depends on policy.
func ParseTaskRequest(raw []byte) (NormalizedRequest, error) {
	if err := scanJSON(raw); err != nil {
		return NormalizedRequest{}, badRequest("MALFORMED_JSON", err.Error())
	}

	var req TaskRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		return NormalizedRequest{}, badRequest("INVALID_TASK_REQUEST", fmt.Sprintf("%v: %v", errUnknownOrBadKey, err))
	}
	return req.normalize()
}

func (r TaskRequest) normalize() (NormalizedRequest, error) {
	bad := func(format string, args ...any) (NormalizedRequest, error) {
		return NormalizedRequest{}, badRequest("INVALID_TASK_REQUEST", fmt.Sprintf(format, args...))
	}

	if r.Schema == nil || *r.Schema != TaskRequestSchema {
		return bad("schema must be %q", TaskRequestSchema)
	}
	if r.Executor == nil || r.Inputs == nil || r.Parameters == nil || r.Resources == nil || r.Reliability == nil {
		return bad("executor, inputs, parameters, resources and reliability are required")
	}

	exec := *r.Executor
	if len(exec.ID) > maxIDBytes || !executorIDPattern.MatchString(exec.ID) {
		return bad("executor.id is invalid")
	}
	if len(exec.VersionConstraint) > maxVersionSpec {
		return bad("executor.version_constraint exceeds %d bytes", maxVersionSpec)
	}

	inputs := *r.Inputs
	if len(inputs) > maxInputs {
		return bad("inputs has more than %d items", maxInputs)
	}
	names := make(map[string]struct{}, len(inputs))
	for i, in := range inputs {
		if !inputNamePattern.MatchString(in.Name) {
			return bad("inputs[%d].name is invalid", i)
		}
		if !artifactIDPattern.MatchString(in.ArtifactID) {
			return bad("inputs[%d].artifact_id is invalid", i)
		}
		if _, dup := names[in.Name]; dup {
			return bad("inputs[%d].name %q is used more than once", i, in.Name)
		}
		names[in.Name] = struct{}{}
	}

	if len(r.Parameters) > maxParameters {
		return bad("parameters has more than %d properties", maxParameters)
	}
	if len(r.Extensions) > maxExtensions {
		return bad("extensions has more than %d items", maxExtensions)
	}

	res := r.Resources
	if res.CPUMillis == nil || res.MemoryBytes == nil || res.WallTimeSeconds == nil || res.OutputBytesMax == nil || res.NetworkBytesMax == nil {
		return bad("resources.cpu_millis, memory_bytes, wall_time_seconds, output_bytes_max and network_bytes_max are required")
	}
	for _, c := range []struct {
		name     string
		v        int64
		min, max int64
	}{
		{"resources.cpu_millis", *res.CPUMillis, 1, 256_000},
		{"resources.memory_bytes", *res.MemoryBytes, 1 << 20, 1 << 44},
		{"resources.wall_time_seconds", *res.WallTimeSeconds, 1, 604_800},
		{"resources.output_bytes_max", *res.OutputBytesMax, 0, 1 << 40},
		{"resources.network_bytes_max", *res.NetworkBytesMax, 0, 1 << 44},
		{"resources.temporary_disk_bytes", res.TemporaryDiskBytes, 0, 1 << 44},
	} {
		if c.v < c.min || c.v > c.max {
			return bad("%s must be between %d and %d", c.name, c.min, c.max)
		}
	}

	rel := r.Reliability
	if rel.Retryable == nil || rel.Preemptible == nil || rel.MaxAttempts == nil || rel.EffectMode == nil {
		return bad("reliability.retryable, preemptible, max_attempts and effect_mode are required")
	}
	if *rel.MaxAttempts < 1 || *rel.MaxAttempts > 32 {
		return bad("reliability.max_attempts must be between 1 and 32")
	}
	if *rel.EffectMode != "none" {
		return bad("reliability.effect_mode must be \"none\"")
	}

	priority := r.Priority
	switch priority {
	case "":
		priority = PriorityNormal
	case PriorityBackground, PriorityNormal, PriorityHigh:
	default:
		return bad("priority must be background, normal or high")
	}

	var deadline time.Time
	if r.Deadline != "" {
		t, err := time.Parse(time.RFC3339, r.Deadline)
		if err != nil {
			return bad("deadline must be an RFC 3339 date-time")
		}
		deadline = t.UTC()
	}

	if r.ResultProfile != "" && (len(r.ResultProfile) > maxIDBytes || !resultProfilePatt.MatchString(r.ResultProfile)) {
		return bad("result_profile is invalid")
	}

	return NormalizedRequest{
		Executor:   exec,
		Inputs:     append([]ArtifactReference(nil), inputs...),
		Parameters: r.Parameters,
		Resources: NormalizedResources{
			CPUMillis:          *res.CPUMillis,
			MemoryBytes:        *res.MemoryBytes,
			WallTimeSeconds:    *res.WallTimeSeconds,
			OutputBytesMax:     *res.OutputBytesMax,
			NetworkBytesMax:    *res.NetworkBytesMax,
			TemporaryDiskBytes: res.TemporaryDiskBytes,
			GPURequested:       len(res.GPU) > 0 && string(res.GPU) != "null",
		},
		Reliability: NormalizedReliability{
			Retryable:      *rel.Retryable,
			Preemptible:    *rel.Preemptible,
			Checkpointable: rel.Checkpointable,
			Deterministic:  rel.Deterministic,
			MaxAttempts:    uint32(*rel.MaxAttempts),
			EffectMode:     *rel.EffectMode,
		},
		Priority:       priority,
		Deadline:       deadline,
		ResultProfile:  r.ResultProfile,
		ExtensionCount: len(r.Extensions),
	}, nil
}

// canonical returns a stable byte form of the request. Map keys are sorted by
// encoding/json; numbers keep the literal the Client sent, so two requests that
// differ only in number formatting are treated as different. That errs toward
// an idempotency conflict, never toward merging two different requests.
func (n NormalizedRequest) canonical() ([]byte, error) {
	type canon struct {
		Executor      ExecutorRequirement   `json:"executor"`
		Inputs        []ArtifactReference   `json:"inputs"`
		Parameters    map[string]any        `json:"parameters"`
		Resources     NormalizedResources   `json:"resources"`
		Reliability   NormalizedReliability `json:"reliability"`
		Priority      Priority              `json:"priority"`
		Deadline      string                `json:"deadline"`
		ResultProfile string                `json:"result_profile"`
	}
	c := canon{
		Executor: n.Executor, Inputs: n.Inputs, Parameters: n.Parameters,
		Resources: n.Resources, Reliability: n.Reliability, Priority: n.Priority,
		ResultProfile: n.ResultProfile,
	}
	if c.Inputs == nil {
		c.Inputs = []ArtifactReference{}
	}
	if !n.Deadline.IsZero() {
		c.Deadline = n.Deadline.Format(time.RFC3339)
	}
	return json.Marshal(c)
}

// Fingerprint identifies the request for idempotency. It is independent of the
// principal, which scopes the key separately.
func (n NormalizedRequest) Fingerprint() (contract.Digest, error) {
	return n.digest("eki.task-request-fingerprint/v1\n")
}

// TaskIRDigest identifies the admitted Task content.
func (n NormalizedRequest) TaskIRDigest() (contract.Digest, error) {
	return n.digest("eki.task-ir/v1\n")
}

func (n NormalizedRequest) digest(domain string) (contract.Digest, error) {
	body, err := n.canonical()
	if err != nil {
		return contract.Digest{}, fmt.Errorf("canonicalize task request: %w", err)
	}
	sum := sha256.Sum256(append([]byte(domain), body...))
	return contract.ParseSHA256(fmt.Sprintf("%x", sum))
}
