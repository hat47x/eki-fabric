// Package teideleg is a TEI-side adapter for one side-effect-free TEI
// operation. It composes an execution request from TEI's own vocabulary,
// maps it onto EKI's external Client API (spec/external/openapi.json), and
// records what was requested, what EKI admitted, what was observed, and what
// the result means for TEI.
//
// The package depends only on the Go standard library and on EKI's
// language-neutral external contract. It does not import EKI's Go packages,
// so the dependency direction stays TEI -> EKI and EKI keeps its own
// scheduling, Attempt, Lease, Fencing and recovery responsibilities.
package teideleg

import (
	"encoding/json"
	"time"
)

// Schema identifiers of this spike's records. They are not EKI contracts and
// carry no authority (see the spike README). Each is versioned so a later
// change is visible rather than silent.
const (
	// ProjectionSchema names the record that pairs one TEI operation with the
	// EKI requests derived from it.
	ProjectionSchema = "tei.eki-delegation-projection/v0"
	// TraceSchema names the record that follows one delegation from request to
	// receipt.
	TraceSchema = "tei.eki-delegation-trace/v0"
	// ReceiptSchema names the TEI-side verdict of one delegation.
	ReceiptSchema = "tei.eki-delegation-receipt/v0"
	// LocalRunSchema names the record of the same operation run locally, used
	// as the comparison baseline.
	LocalRunSchema = "tei.eki-delegation-local-run/v0"
)

// EKI's task-request schema. The adapter fixes it instead of reading it from a
// request, so a mismatch with the contract is a test failure, not a silent
// downgrade.
const taskRequestSchema = "eki.task-request/v1"

// Digest is EKI's SHA-256 digest object.
//
// UnmarshalJSON accepts a bare hex string as well. The published contract
// declares an object, but the reference implementation at the revision under
// test sends a string in ArtifactDescriptor.digest. The adapter records the
// contract mismatch in RESULTS_2026-11-11.md and accepts both forms rather
// than making the live run unable to read the answer.
type Digest struct {
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

// UnmarshalJSON accepts {"algorithm":"sha256","value":"..."} and "..." (hex).
func (d *Digest) UnmarshalJSON(raw []byte) error {
	if len(raw) > 0 && raw[0] == '"' {
		var hexValue string
		if err := json.Unmarshal(raw, &hexValue); err != nil {
			return err
		}
		*d = Digest{Algorithm: "sha256", Value: hexValue}
		return nil
	}
	type plain Digest
	var p plain
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	*d = Digest(p)
	return nil
}

// ArtifactReference is one entry of TaskRequest.inputs. EKI's deployment
// resolves an artifact_id that is the SHA-256 hex of the body.
type ArtifactReference struct {
	Name       string `json:"name"`
	ArtifactID string `json:"artifact_id"`
}

// ExecutorRequirement is TaskRequest.executor. The contract has no field for
// the executable digest or the toolchain; those exist only as Client-declared
// parameters.
type ExecutorRequirement struct {
	ID                string `json:"id"`
	VersionConstraint string `json:"version_constraint,omitempty"`
}

// ResourceRequest is TaskRequest.resources.
type ResourceRequest struct {
	CPUMillis          int64 `json:"cpu_millis"`
	MemoryBytes        int64 `json:"memory_bytes"`
	TemporaryDiskBytes int64 `json:"temporary_disk_bytes,omitempty"`
	WallTimeSeconds    int64 `json:"wall_time_seconds"`
	OutputBytesMax     int64 `json:"output_bytes_max"`
	NetworkBytesMax    int64 `json:"network_bytes_max"`
}

// ReliabilityRequest is TaskRequest.reliability.
type ReliabilityRequest struct {
	Retryable      bool   `json:"retryable"`
	Preemptible    bool   `json:"preemptible"`
	Checkpointable bool   `json:"checkpointable"`
	Deterministic  bool   `json:"deterministic"`
	MaxAttempts    int    `json:"max_attempts"`
	EffectMode     string `json:"effect_mode"`
}

// TaskRequest is eki.task-request/v1 as far as this adapter fills it.
type TaskRequest struct {
	Schema          string              `json:"schema"`
	Executor        ExecutorRequirement `json:"executor"`
	Inputs          []ArtifactReference `json:"inputs"`
	Parameters      map[string]any      `json:"parameters"`
	Resources       ResourceRequest     `json:"resources"`
	Reliability     ReliabilityRequest  `json:"reliability"`
	Deadline        string              `json:"deadline,omitempty"`
	Priority        string              `json:"priority,omitempty"`
	ResultProfile   string              `json:"result_profile,omitempty"`
	ClientRequestID string              `json:"client_request_id,omitempty"`
}

// TaskAccepted is eki.task-accepted/v1.
type TaskAccepted struct {
	Schema             string `json:"schema"`
	TaskID             string `json:"task_id"`
	Status             string `json:"status"`
	RequestFingerprint string `json:"request_fingerprint"`
	ClientRequestID    string `json:"client_request_id,omitempty"`
	Replayed           bool   `json:"replayed"`
	SubmittedAt        string `json:"submitted_at"`
}

// PublicAssuranceSummary is the assurance part of a public status or result.
type PublicAssuranceSummary struct {
	Profile string `json:"profile,omitempty"`
	Status  string `json:"status,omitempty"`
}

// TaskPublicStatus is eki.task-public-status/v1. It intentionally excludes
// Worker, Lease and Attempt identity; attempt_count is the only Attempt signal.
type TaskPublicStatus struct {
	Schema             string                  `json:"schema"`
	TaskID             string                  `json:"task_id"`
	Status             string                  `json:"status"`
	RequestFingerprint string                  `json:"request_fingerprint,omitempty"`
	Replayed           bool                    `json:"replayed,omitempty"`
	SubmittedAt        string                  `json:"submitted_at"`
	UpdatedAt          string                  `json:"updated_at"`
	Assurance          *PublicAssuranceSummary `json:"assurance,omitempty"`
	TerminalCode       string                  `json:"terminal_code,omitempty"`
	PublicMessage      string                  `json:"public_message,omitempty"`
	AttemptCount       int                     `json:"attempt_count,omitempty"`
}

// ResultArtifact is one entry of TaskResult.artifacts.
type ResultArtifact struct {
	SHA256     *Digest `json:"sha256"`
	SizeBytes  int64   `json:"size_bytes"`
	Available  bool    `json:"available"`
	ArtifactID string  `json:"artifact_id,omitempty"`
	MediaType  string  `json:"media_type,omitempty"`
	DataClass  string  `json:"data_class,omitempty"`
}

// TaskResult is eki.task-result/v1. output_digest is optional, so a client
// that needs to pin the result must treat its absence as unverifiable.
type TaskResult struct {
	Schema       string                  `json:"schema"`
	TaskID       string                  `json:"task_id"`
	Artifacts    []ResultArtifact        `json:"artifacts"`
	Assurance    *PublicAssuranceSummary `json:"assurance,omitempty"`
	OutputDigest *Digest                 `json:"output_digest,omitempty"`
}

// Problem is a Problem Details response body.
type Problem struct {
	Type      string `json:"type,omitempty"`
	Title     string `json:"title,omitempty"`
	Status    int    `json:"status,omitempty"`
	Code      string `json:"code,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
}

// IngestIntentRequest is eki.artifact-ingest-intent-request/v1.
type IngestIntentRequest struct {
	Schema             string  `json:"schema"`
	DeclaredSizeBytes  int64   `json:"declared_size_bytes"`
	MediaType          string  `json:"media_type"`
	RequestedDataClass string  `json:"requested_data_class"`
	ExpectedDigest     *Digest `json:"expected_digest,omitempty"`
}

// UploadSession is the bounded upload returned by the ingest intent route.
type UploadSession struct {
	UploadID  string `json:"upload_id"`
	Method    string `json:"method"`
	Href      string `json:"href"`
	ExpiresAt string `json:"expires_at"`
	MaxBytes  int64  `json:"max_bytes"`
}

// IngestIntentResponse is eki.artifact-ingest-intent-response/v1.
type IngestIntentResponse struct {
	Schema   string        `json:"schema"`
	IngestID string        `json:"ingest_id"`
	Upload   UploadSession `json:"upload"`
}

// ArtifactFacetState is the facet set of an artifact descriptor.
type ArtifactFacetState struct {
	ContentIdentity string `json:"content_identity"`
	SafeProcessing  string `json:"safe_processing"`
	Provenance      string `json:"provenance"`
	Availability    string `json:"availability"`
}

// ArtifactDescriptor is eki.artifact-descriptor/v1.
type ArtifactDescriptor struct {
	Schema     string             `json:"schema"`
	ArtifactID string             `json:"artifact_id"`
	Digest     *Digest            `json:"digest,omitempty"`
	SizeBytes  int64              `json:"size_bytes"`
	MediaType  string             `json:"media_type"`
	DataClass  string             `json:"data_class"`
	Facets     ArtifactFacetState `json:"facets"`
}

// UploadAccepted is eki.artifact-upload-accepted/v1.
type UploadAccepted struct {
	Schema   string             `json:"schema"`
	IngestID string             `json:"ingest_id"`
	Artifact ArtifactDescriptor `json:"artifact"`
}

// BindingStatus says how strongly EKI enforces one mapped field.
type BindingStatus string

const (
	// BindingEnforced means EKI checks the value at a boundary the Client
	// cannot bypass.
	BindingEnforced BindingStatus = "enforced"
	// BindingRecordedOnly means the value travels in the request but EKI does
	// not enforce or deliver it to the execution boundary. It is evidence of
	// what TEI asked for, never a guarantee of what ran.
	BindingRecordedOnly BindingStatus = "recorded-only"
	// BindingUnavailable means the request has nowhere to carry the value.
	BindingUnavailable BindingStatus = "unavailable"
)

// Binding is one row of the mapping table between TEI's operation and EKI's
// request. It exists so a reader can see which parts of the delegation are
// guarantees and which are declarations.
type Binding struct {
	// TEIField is what TEI needs to express.
	TEIField string `json:"tei_field"`
	// EKIField is where it goes (or why it cannot go anywhere).
	EKIField string `json:"eki_field"`
	// Status is how EKI treats the value.
	Status BindingStatus `json:"status"`
	// Note explains the status at this EKI revision.
	Note string `json:"note"`
}

// Input is one immutable input artifact of the operation, identified by its
// bytes.
type Input struct {
	Name      string
	SHA256Hex string
	SizeBytes int64
	MediaType string
	DataClass string
}

// Operation is TEI's side of one side-effect-free operation. The local-only
// fields (Command, WorkspaceRoot) are never sent to EKI; they define what the
// local baseline runs and what the EKI executor id denotes.
type Operation struct {
	// ID is TEI's own operation name, for example "tei.validate".
	ID string `json:"id"`
	// SourceRevision is the TEI source revision the operation is defined at.
	SourceRevision string `json:"source_revision"`
	// Command is the local command line, for example
	// ["validate","-workspace","reference/workspace","-json"].
	Command []string `json:"command"`
	// ExecutableSHA256 is the digest of the TEI binary that performed the local
	// run and that the operator is expected to have registered as the executor.
	ExecutableSHA256 string `json:"executable_sha256"`
	// Toolchain is the compiler or runtime the operation is expected to run on.
	Toolchain string `json:"toolchain"`
	// ExecutorID is EKI's registered executor identifier.
	ExecutorID string `json:"executor_id"`
	// ExecutorVersionConstraint is recorded in the request; EKI does not
	// evaluate it at this revision.
	ExecutorVersionConstraint string `json:"executor_version_constraint,omitempty"`
	// Inputs are the immutable inputs of the operation.
	Inputs []Input `json:"inputs"`
	// Resources is the requested resource envelope.
	Resources ResourceRequest `json:"resources"`
	// Deadline is the requested deadline. EKI admits it but does not converge
	// the Task at this revision.
	Deadline time.Time `json:"deadline"`
	// ExpectedOutputSHA256 is TEI's independent expectation of the output when
	// one exists. A receipt needs it to compare against.
	ExpectedOutputSHA256 string `json:"expected_output_sha256,omitempty"`
	// Priority is the requested priority; policy may override it.
	Priority string `json:"priority,omitempty"`
}

// Projection is one TEI operation and the EKI requests derived from it.
type Projection struct {
	Schema          string    `json:"schema"`
	Operation       Operation `json:"operation"`
	IdempotencyKey  string    `json:"idempotency_key"`
	ClientRequestID string    `json:"client_request_id"`
	// Ingests holds one bounded ingest intent per input, in declaration order.
	// Each intent is created before the Task, because admission refuses an
	// input whose body the deployment does not keep (#763).
	Ingests  []IngestIntentRequest `json:"ingest_intents"`
	Task     TaskRequest           `json:"task_request"`
	Bindings []Binding             `json:"bindings"`
}
