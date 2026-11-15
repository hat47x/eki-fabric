package teitypedadapter

// Operation represents one TEI operation to be executed.
type Operation struct {
	ID                        string
	SourceRevision            string
	ExecutableSHA256          string
	Toolchain                 string
	Command                   string
	ExecutorID                string
	ExecutorVersionConstraint string
	Inputs                    []ArtifactInput
	Resources                 ResourceRequest
	Reliability               ReliabilityRequest
	Deadline                  string
	Priority                  string
	ResultProfile             string
	ClientRequestID           string
}

// ArtifactInput describes one input artifact.
type ArtifactInput struct {
	Name      string
	SHA256Hex string
	MediaType string
	SizeBytes int64
	DataClass string
}

// ResourceRequest describes the resource requirements.
type ResourceRequest struct {
	CPUMillis       int64
	MemoryBytes     int64
	WallTimeSeconds int64
}

// ReliabilityRequest describes reliability requirements.
type ReliabilityRequest struct {
	Retryable      bool
	Preemptible    bool
	Checkpointable bool
	Deterministic  bool
	MaxAttempts    int
	EffectMode     string
}

// Projection is the result of projecting a TEI Operation onto EKI.
type Projection struct {
	Schema          string
	IdempotencyKey  string
	ClientRequestID string
	Task            TaskRequest
	Ingests         []IngestIntent
	Bindings        []Binding
}

// TaskRequest represents eki.task-request/v1.
type TaskRequest struct {
	Schema          string
	Executor        ExecutorRequirement
	Inputs          []ArtifactReference
	Parameters      map[string]any
	Resources       ResourceRequest
	Reliability     ReliabilityRequest
	Deadline        string
	Priority        string
	ResultProfile   string
	ClientRequestID string
}

// ExecutorRequirement represents the executor requirement.
type ExecutorRequirement struct {
	ID                string
	VersionConstraint string
}

// ArtifactReference represents an input artifact reference.
type ArtifactReference struct {
	Name       string
	ArtifactID string
}

// IngestIntent represents an artifact ingest intent.
type IngestIntent struct {
	Schema             string
	DeclaredSizeBytes  int64
	MediaType          string
	RequestedDataClass string
	ExpectedDigest     string
}

// Binding describes where a TEI field maps to in EKI.
type Binding struct {
	TEIField     string
	EKIField     string
	Enforced     bool
	RecordedOnly bool
	Missing      bool
	Note         string
}

const (
	SchemaTaskRequest  = "eki.task-request/v1"
	SchemaIngestIntent = "eki.artifact-ingest-intent-request/v1"
	SchemaProjection   = "tei.eki-projection/v0"
)
