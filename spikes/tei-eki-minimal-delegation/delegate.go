package teideleg

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DelegateOptions controls one delegation. Uploads holds the bytes of every
// input, keyed by input name; the adapter refuses to submit a Task whose input
// it did not send.
type DelegateOptions struct {
	Uploads map[string][]byte
	// Deadline is TEI's own wait limit. It is deliberately separate from the
	// deadline in the request: the request's deadline is what TEI asks EKI to
	// record, while this one is how long TEI is willing to wait.
	Deadline time.Time
	// PollInterval is the pause between status reads. Zero means 200ms.
	PollInterval time.Duration
	// MaxPolls bounds the status reads even when the deadline is far away.
	// Zero means 30.
	MaxPolls int
	// Local is the same operation run on TEI's side, when available.
	Local *LocalRun
	// Now is the clock. nil means time.Now. Tests inject it.
	Now func() time.Time
	// Sleep is the wait. nil means time.Sleep. Tests inject it.
	Sleep func(time.Duration)
}

func (o DelegateOptions) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func (o DelegateOptions) sleep(d time.Duration) {
	if o.Sleep != nil {
		o.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Delegation is the result of one Delegate call: the projection that was sent,
// what the input path returned, what was observed, and TEI's receipt.
type Delegation struct {
	Projection  Projection
	Uploads     []UploadRecord
	Observation Observation
	Receipt     Receipt
	Trace       Trace
}

// Delegate runs one side-effect-free operation through EKI's public Client API.
//
// It is the minimal path: intake the inputs, submit one Task, read the public
// status, read the result, re-measure what came back, and decide. It never
// retries by creating a new Task on its own; a retry is the caller's decision,
// because only the caller knows whether the operation is safe to repeat.
func Delegate(ctx context.Context, c *Client, p Projection, opt DelegateOptions) (Delegation, error) {
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
		resp, err := c.CreateIngestIntent(ctx, ingestKey(p.IdempotencyKey, in.Name), p.Ingests[i])
		if err != nil {
			return Delegation{}, fmt.Errorf("ingest intent for %q: %w", in.Name, err)
		}
		accepted, err := c.UploadArtifact(ctx, resp.Upload, body)
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
		fetched, fetchedDigest, ferr := c.GetArtifact(ctx, accepted.Artifact.ArtifactID)
		switch {
		case ferr != nil:
			record.FetchError = ferr.Error()
		case string(fetched) != string(body):
			record.FetchError = "the fetched body differs from the uploaded bytes"
		default:
			record.ObservedSHA256 = fetchedDigest
		}
		uploads = append(uploads, record)
	}
	mark("ingest-and-upload")

	// 2. Submit. A refusal is an answer, not a transport failure.
	accepted, err := c.SubmitTask(ctx, p.IdempotencyKey, p.Task)
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
		obs.Requests = c.RequestCount()
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
		st, err := c.GetTaskStatus(ctx, accepted.TaskID)
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
	res, err := c.GetTaskResult(ctx, accepted.TaskID)
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
			fetched, digest, ferr := c.GetArtifact(ctx, a.ArtifactID)
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
	obs.Requests = c.RequestCount()
	receipt := Evaluate(obs)
	trace := NewTrace(p, obs, uploads, receipt)
	return Delegation{Projection: p, Uploads: uploads, Observation: obs, Receipt: receipt, Trace: trace}, nil
}

func propertyOf(err error) Problem {
	var e *Error
	if errors.As(err, &e) {
		return e.Problem
	}
	return Problem{}
}

func problemOf(err error) Problem { return propertyOf(err) }

// ingestKey derives a bounded, deterministic key per input. The task key alone
// can be close to the 128-character limit, so appending a name is not safe.
func ingestKey(taskKey, inputName string) string {
	return "tei-ing:" + SHA256Hex([]byte(taskKey + "\x00" + inputName))[:40]
}

// isTerminal reports whether a public status is one EKI will not leave.
func isTerminal(status, terminalCode string) bool {
	if terminalCode != "" {
		return true
	}
	switch status {
	case "completed", "cancelled", "failed":
		return true
	}
	return false
}
