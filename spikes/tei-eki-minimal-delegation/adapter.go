package teideleg

import (
	"context"
	"fmt"
	"time"
)

// API is the part of EKI's External Client Plane this adapter uses. It is
// deliberately narrow so that a caller can supply either the real Client or a
// test double, and so that adding a method to EKI's contract does not
// automatically widen what TEI depends on.
//
// The methods are the ones declared in spec/external/openapi.json. No method
// reads EKI's internal state (no database, no Worker record, no Lease).
type API interface {
	CreateIngestIntent(ctx context.Context, key string, req IngestIntentRequest) (IngestIntentResponse, error)
	UploadArtifact(ctx context.Context, session UploadSession, body []byte) (UploadAccepted, error)
	GetArtifact(ctx context.Context, artifactID string) (body []byte, measuredSHA256 string, err error)
	SubmitTask(ctx context.Context, key string, req TaskRequest) (TaskAccepted, error)
	GetTaskStatus(ctx context.Context, taskID string) (TaskPublicStatus, error)
	GetTaskResult(ctx context.Context, taskID string) (TaskResult, error)
	CancelTask(ctx context.Context, key, taskID string) error
	// RequestCount is the number of requests the client has sent. A test
	// double may return 0; the trace then simply omits a number it cannot know.
	RequestCount() int
}

// Adapter is the TEI-side entry point for one delegation. A TEI caller builds
// an Operation, supplies the input bytes, and receives a Delegation whose
// Trace records what was requested, observed and decided.
//
// The Adapter never retries by creating a new Task and never turns a missing
// result into a success. Its only side effects are through the API it was
// given.
type Adapter struct {
	api     API
	options DelegateOptions
}

// NewAdapter returns an Adapter. Options carries the wait limit, polling and
// the local baseline; Uploads is supplied per Execute call.
func NewAdapter(api API, options DelegateOptions) (*Adapter, error) {
	if api == nil {
		return nil, fmt.Errorf("teideleg: an EKI API implementation is required")
	}
	return &Adapter{api: api, options: options}, nil
}

// Execute projects the operation, performs the delegation, and returns the
// trace. A refusal by EKI is recorded in the returned Delegation and receipt,
// not returned as an error; an error means the delegation could not be carried
// out at all (for example, an input's bytes are missing).
func (a *Adapter) Execute(ctx context.Context, op Operation, uploads map[string][]byte) (Delegation, error) {
	projection, err := Project(op)
	if err != nil {
		return Delegation{}, err
	}
	return a.ExecuteProjection(ctx, projection, uploads)
}

// ExecuteProjection is Execute for a projection composed earlier, so a caller
// can inspect or persist the exact request body before sending it.
func (a *Adapter) ExecuteProjection(ctx context.Context, projection Projection, uploads map[string][]byte) (Delegation, error) {
	options := a.options
	options.Uploads = uploads
	return Delegate(ctx, a.api, projection, options)
}

// Delegate runs one side-effect-free operation through EKI's public Client API.
//
// It is the minimal path: intake the inputs, submit one Task, read the public
// status, read the result, re-measure what came back, and decide. It never
// retries by creating a new Task on its own; a retry is the caller's decision,
// because only the caller knows whether the operation is safe to repeat.
func Delegate(ctx context.Context, api API, p Projection, opt DelegateOptions) (Delegation, error) {
	if api == nil {
		return Delegation{}, fmt.Errorf("delegate: an EKI API implementation is required")
	}
	if len(p.Operation.Inputs) == 0 {
		return Delegation{}, fmt.Errorf("delegate: the projection has no inputs")
	}
	now := opt.now()
	obs := Observation{Now: now, StartedAt: now, Deadline: opt.Deadline, Local: opt.Local}
	uploads := make([]UploadRecord, 0, len(p.Operation.Inputs))
	phaseStart := opt.now()
	mark := func(name string) {
		obs.Timings = append(obs.Timings, PhaseTiming{Name: name, DurationMillis: opt.now().Sub(phaseStart).Milliseconds()})
		phaseStart = opt.now()
	}

	// 1. The input path. Each input is declared, uploaded and read back, so the
	// trace can show that the body EKI would hand to a Worker is the body TEI
	// sent.
	for i, in := range p.Operation.Inputs {
		body, ok := opt.Uploads[in.Name]
		if !ok {
			return Delegation{}, fmt.Errorf("delegate: no bytes for input %q", in.Name)
		}
		resp, err := api.CreateIngestIntent(ctx, ingestKey(p.IdempotencyKey, in.Name), p.Ingests[i])
		if err != nil {
			return Delegation{}, fmt.Errorf("ingest intent for %q: %w", in.Name, err)
		}
		accepted, err := api.UploadArtifact(ctx, resp.Upload, body)
		if err != nil {
			return Delegation{}, fmt.Errorf("upload %q: %w", in.Name, err)
		}
		record := UploadRecord{
			InputName:       in.Name,
			RequestedSHA256: in.SHA256Hex,
			ArtifactID:      accepted.Artifact.ArtifactID,
			ObservedSHA256:  SHA256Hex(body),
			SizeBytes:       int64(len(body)),
		}
		fetched, fetchedDigest, ferr := api.GetArtifact(ctx, accepted.Artifact.ArtifactID)
		switch {
		case ferr != nil:
			record.FetchError = ferr.Error()
		case string(fetched) != string(body):
			record.FetchError = "the fetched body differs from the uploaded bytes"
		default:
			record.ObservedSHA256 = fetchedDigest
		}
		uploads = append(uploads, record)
		// EKI names the body by the digest it measured. If that is not the
		// body TEI meant to send, no Task is submitted: a Task naming the
		// intended digest would be refused, and one naming the wrong digest
		// would ask a Worker to run on input TEI did not choose.
		if record.ArtifactID != in.SHA256Hex {
			obs.InputError = fmt.Sprintf("input %q: EKI named %q, TEI asked for %q", in.Name, record.ArtifactID, in.SHA256Hex)
			break
		}
		if record.FetchError != "" {
			obs.InputError = fmt.Sprintf("input %q: the uploaded body could not be read back: %s", in.Name, record.FetchError)
			break
		}
	}
	mark("ingest-and-upload")
	if obs.InputError != "" {
		obs.FinishedAt = opt.now()
		obs.Now = obs.FinishedAt
		obs.Requests = api.RequestCount()
		receipt := Evaluate(obs)
		trace := NewTrace(p, obs, uploads, receipt)
		return Delegation{Projection: p, Uploads: uploads, Observation: obs, Receipt: receipt, Trace: trace}, nil
	}

	// 2. Submit. A refusal is an answer, not a transport failure.
	accepted, err := api.SubmitTask(ctx, p.IdempotencyKey, p.Task)
	if err != nil {
		status := StatusOf(err)
		if status == 0 {
			return Delegation{}, fmt.Errorf("submit: %w", err)
		}
		obs.SubmitStatus = status
		obs.SubmitProblem = problemOf(err)
		mark("submit")
		obs.FinishedAt = opt.now()
		obs.Now = obs.FinishedAt
		obs.Requests = api.RequestCount()
		receipt := Evaluate(obs)
		trace := NewTrace(p, obs, uploads, receipt)
		return Delegation{Projection: p, Uploads: uploads, Observation: obs, Receipt: receipt, Trace: trace}, nil
	}
	obs.SubmitStatus = 202
	obs.TaskID = accepted.TaskID
	obs.RequestFingerprint = accepted.RequestFingerprint
	obs.ClientRequestID = accepted.ClientRequestID
	obs.Replayed = accepted.Replayed
	mark("submit")
	polls := opt.MaxPolls
	if polls <= 0 {
		polls = 30
	}
	interval := opt.PollInterval
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	var waited int64

	// 3. Observe. The public status is the only progress signal; it carries a
	// count of Attempts, not their identity.
	for i := 0; i < polls; i++ {
		st, err := api.GetTaskStatus(ctx, accepted.TaskID)
		if err != nil {
			if StatusOf(err) == 0 {
				return Delegation{}, fmt.Errorf("status: %w", err)
			}
			obs.Statuses = append(obs.Statuses, StatusSnapshot{
				At: opt.now(), Status: "error:" + problemOf(err).Code,
			})
			break
		}
		obs.Statuses = append(obs.Statuses, StatusSnapshot{
			At: opt.now(), Status: st.Status, TerminalCode: st.TerminalCode, AttemptCount: st.AttemptCount,
		})
		obs.StatusReads++
		if isTerminal(st.Status, st.TerminalCode) || !opt.now().Before(opt.Deadline) {
			break
		}
		waited += interval.Milliseconds()
		opt.sleep(interval)
	}
	mark("status")
	obs.WaitedMillis = waited

	// 4. The result answer. RESULT_NOT_READY and RESULT_NOT_AVAILABLE are
	// meaningful answers about the Task.
	now = opt.now()
	obs.Now = now
	res, err := api.GetTaskResult(ctx, accepted.TaskID)
	if err != nil {
		status := StatusOf(err)
		if status == 0 {
			return Delegation{}, fmt.Errorf("result: %w", err)
		}
		obs.ResultStatus = status
		obs.ResultProblem = problemOf(err)
	} else {
		obs.ResultStatus = 200
		obs.Result = &res
		// 5. Re-measure the bodies that are still available.
		for _, a := range res.Artifacts {
			if !a.Available || a.ArtifactID == "" || a.SHA256 == nil {
				continue
			}
			fetched, digest, ferr := api.GetArtifact(ctx, a.ArtifactID)
			f := FetchedBody{ArtifactID: a.ArtifactID, ExpectedSHA256: a.SHA256.Value, SizeBytes: int64(len(fetched))}
			if ferr != nil {
				f.Error = ferr.Error()
			} else {
				f.ObservedSHA256 = digest
			}
			obs.Fetched = append(obs.Fetched, f)
		}
	}

	obs.FinishedAt = opt.now()
	mark("result")
	obs.Now = obs.FinishedAt
	obs.Requests = api.RequestCount()
	receipt := Evaluate(obs)
	trace := NewTrace(p, obs, uploads, receipt)
	return Delegation{Projection: p, Uploads: uploads, Observation: obs, Receipt: receipt, Trace: trace}, nil
}
