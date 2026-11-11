package teideleg

import (
	"context"
	"fmt"
	"time"
)

// EvaluateTask reads an already-submitted Task through EKI's public Client API
// and decides what it means for TEI. It is the second half of a delegation:
// a caller that submitted the Task itself (for example a test that drives
// EKI's own processes) can still get TEI's receipt without reimplementing the
// rules.
//
// It never assumes a result exists: RESULT_NOT_READY and RESULT_NOT_AVAILABLE
// are answers about the Task and become an undetermined or rejected receipt.
func EvaluateTask(ctx context.Context, api API, taskID string, local *LocalRun, wait time.Duration, poll time.Duration, now func() time.Time) (Observation, Receipt, error) {
	if api == nil {
		return Observation{}, Receipt{}, fmt.Errorf("evaluate task: an EKI API implementation is required")
	}
	if taskID == "" {
		return Observation{}, Receipt{}, fmt.Errorf("evaluate task: a Task id is required")
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}

	started := now()
	obs := Observation{StartedAt: started, Now: started, Local: local}
	if wait > 0 {
		obs.Deadline = started.Add(wait)
	}

	// Status polling. The public status carries attempt_count, not Attempt
	// identity, so that is all the receipt can say about Attempts.
	for {
		st, err := api.GetTaskStatus(ctx, taskID)
		if err != nil {
			if StatusOf(err) == 0 {
				return Observation{}, Receipt{}, fmt.Errorf("status: %w", err)
			}
			obs.Statuses = append(obs.Statuses, StatusSnapshot{
				At: now(), Status: "error:" + problemOf(err).Code,
			})
			break
		}
		obs.Statuses = append(obs.Statuses, StatusSnapshot{
			At: now(), Status: st.Status, TerminalCode: st.TerminalCode, AttemptCount: st.AttemptCount,
		})
		obs.StatusReads++
		obs.TaskID = taskID
		obs.RequestFingerprint = st.RequestFingerprint
		obs.Replayed = st.Replayed
		if isTerminal(st.Status, st.TerminalCode) {
			break
		}
		if obs.Deadline.IsZero() || !now().Before(obs.Deadline) {
			break
		}
		time.Sleep(poll)
		obs.WaitedMillis += poll.Milliseconds()
	}

	// A Task that someone else submitted still has to look admitted to the
	// receipt. The public status is the only proof: a Task that answers a
	// status read exists in this deployment and was accepted when it was
	// created. A status that never answered leaves the admission unknown, and
	// the receipt stays undetermined rather than assuming one.
	if obs.StatusReads > 0 {
		obs.SubmitStatus = 202
	}

	// The result answer.
	res, err := api.GetTaskResult(ctx, taskID)
	if err != nil {
		status := StatusOf(err)
		if status == 0 {
			return Observation{}, Receipt{}, fmt.Errorf("result: %w", err)
		}
		obs.ResultStatus = status
		obs.ResultProblem = problemOf(err)
	} else {
		obs.ResultStatus = 200
		obs.Result = &res
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

	obs.FinishedAt = now()
	obs.Now = obs.FinishedAt
	obs.Requests = api.RequestCount()
	receipt := Evaluate(obs)
	return obs, receipt, nil
}
