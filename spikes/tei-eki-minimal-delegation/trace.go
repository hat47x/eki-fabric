package teideleg

import "time"

// UploadRecord is the answer of the upload route for one input, re-measured by
// the adapter. The digest the adapter computed is kept next to the digest EKI
// reported, so a disagreement is visible in the trace.
type UploadRecord struct {
	InputName       string `json:"input_name"`
	RequestedSHA256 string `json:"requested_sha256"`
	ArtifactID      string `json:"artifact_id"`
	ObservedSHA256  string `json:"observed_sha256"`
	SizeBytes       int64  `json:"size_bytes"`
	// FetchError records a failure to retrieve the uploaded body. The input
	// path is only proven when the body comes back.
	FetchError string `json:"fetch_error,omitempty"`
}

// InputLink binds one TEI input to what EKI admitted.
type InputLink struct {
	Name            string `json:"name"`
	RequestedSHA256 string `json:"requested_sha256"`
	EKISHA256       string `json:"eki_sha256,omitempty"`
	SizeBytes       int64  `json:"size_bytes"`
	DigestAgreed    bool   `json:"digest_agreed"`
}

// ResultLink binds one result artifact to the body TEI actually received.
type ResultLink struct {
	SHA256        string `json:"sha256"`
	Available     bool   `json:"available"`
	FetchedSHA256 string `json:"fetched_sha256,omitempty"`
}

// Links is the correlation the task asks for: input artifact, executable
// digest, toolchain, requested conditions, the actual Attempt, the result
// artifact and the receipt, in one place. Fields EKI does not expose are left
// empty rather than invented.
type Links struct {
	OperationID    string `json:"operation_id"`
	SourceRevision string `json:"source_revision"`
	ExecutorID     string `json:"executor_id"`
	// ExecutorSHA256 is TEI's declared digest. EKI never confirms it.
	ExecutorSHA256 string `json:"executor_sha256"`
	// Toolchain is TEI's declared toolchain. EKI never confirms it.
	Toolchain string `json:"toolchain"`

	RequestedResources ResourceRequest `json:"requested_resources"`
	RequestedDeadline  string          `json:"requested_deadline"`

	Inputs []InputLink `json:"inputs"`

	IdempotencyKey     string `json:"idempotency_key"`
	ClientRequestID    string `json:"client_request_id"`
	TaskID             string `json:"task_id,omitempty"`
	RequestFingerprint string `json:"request_fingerprint,omitempty"`

	// AttemptCount is the only Attempt fact the public projection carries.
	AttemptCount int `json:"attempt_count"`

	ResultArtifacts []ResultLink `json:"result_artifacts,omitempty"`
	ReceiptOutcome  Outcome      `json:"receipt_outcome"`
}

// Trace is the whole delegation record for one operation.
type Trace struct {
	Schema      string      `json:"schema"`
	RecordedAt  time.Time   `json:"recorded_at"`
	Projection  Projection  `json:"projection"`
	Observation Observation `json:"observation"`
	Links       Links       `json:"links"`
	Receipt     Receipt     `json:"receipt"`
}

// NewTrace joins the projection, the observation and the upload answers. It
// takes the receipt as a parameter instead of deciding it, so a caller cannot
// record a receipt that disagrees with the observation by accident.
func NewTrace(p Projection, obs Observation, uploads []UploadRecord, receipt Receipt) Trace {
	links := Links{
		OperationID:        p.Operation.ID,
		SourceRevision:     p.Operation.SourceRevision,
		ExecutorID:         p.Operation.ExecutorID,
		ExecutorSHA256:     p.Operation.ExecutableSHA256,
		Toolchain:          p.Operation.Toolchain,
		RequestedResources: p.Operation.Resources,
		RequestedDeadline:  p.Operation.Deadline.UTC().Format(time.RFC3339),
		IdempotencyKey:     p.IdempotencyKey,
		ClientRequestID:    p.ClientRequestID,
		TaskID:             obs.TaskID,
		RequestFingerprint: obs.RequestFingerprint,
		ReceiptOutcome:     receipt.Outcome,
	}
	for _, s := range obs.Statuses {
		if s.AttemptCount > links.AttemptCount {
			links.AttemptCount = s.AttemptCount
		}
	}
	for _, in := range p.Operation.Inputs {
		link := InputLink{Name: in.Name, RequestedSHA256: in.SHA256Hex, SizeBytes: in.SizeBytes}
		for _, up := range uploads {
			if up.InputName != in.Name {
				continue
			}
			link.EKISHA256 = up.ArtifactID
			link.DigestAgreed = up.ArtifactID == in.SHA256Hex && up.ObservedSHA256 == in.SHA256Hex
		}
		links.Inputs = append(links.Inputs, link)
	}
	if obs.Result != nil {
		for _, a := range obs.Result.Artifacts {
			link := ResultLink{Available: a.Available}
			if a.SHA256 != nil {
				link.SHA256 = a.SHA256.Value
			}
			for _, f := range obs.Fetched {
				if f.ArtifactID == a.ArtifactID {
					link.FetchedSHA256 = f.ObservedSHA256
				}
			}
			links.ResultArtifacts = append(links.ResultArtifacts, link)
		}
	}
	return Trace{
		Schema:      TraceSchema,
		RecordedAt:  time.Now().UTC(),
		Projection:  p,
		Observation: obs,
		Links:       links,
		Receipt:     receipt,
	}
}
