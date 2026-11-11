package teideleg

import (
	"fmt"
	"time"
)

// Outcome is TEI's three-valued view of one delegation. It is deliberately not
// a boolean: "no result yet" and "a result that cannot be trusted" are
// different situations and TEI must not collapse them.
type Outcome string

const (
	// OutcomeAccepted means EKI answered a result and TEI checked it against
	// its own expectation. It never means EKI proved correctness.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRejected means the result can never be used, for example a digest
	// mismatch or a terminal failure.
	OutcomeRejected Outcome = "rejected"
	// OutcomeUndetermined means the evidence is not enough either way. The
	// caller must decide whether to wait, retry as a new Task, or run locally.
	OutcomeUndetermined Outcome = "undetermined"
)

// LocalRun is the same operation run locally. It is the comparison baseline:
// without it, a receipt can say EKI answered, but not whether the answer is
// equivalent to the work TEI would have done itself.
type LocalRun struct {
	Schema string `json:"schema"`
	// Command is the local command line as executed.
	Command []string `json:"command"`
	// ExitCode is the process exit status. A non-zero exit makes the output
	// digest unverifiable, not a mismatch.
	ExitCode int `json:"exit_code"`
	// DurationMillis is the wall time including process start.
	DurationMillis int64 `json:"duration_millis"`
	// OutputSHA256 is the digest of stdout. It is the expectation a delegated
	// result is compared with when the operation is deterministic.
	OutputSHA256 string `json:"output_sha256,omitempty"`
	OutputBytes  int64  `json:"output_bytes"`
	// CacheState records whether a build cache or module cache was warm. It is
	// the condition that changes the time most, so the comparison is invalid
	// without it.
	CacheState string `json:"cache_state"`
	// SourceRevision and ExecutableSHA256 pin what was run.
	SourceRevision   string `json:"source_revision"`
	ExecutableSHA256 string `json:"executable_sha256,omitempty"`
}

// PhaseTiming is one client-side phase of the delegation.
type PhaseTiming struct {
	Name           string `json:"name"`
	DurationMillis int64  `json:"duration_millis"`
}

// StatusSnapshot is one observed public status.
type StatusSnapshot struct {
	At           time.Time `json:"at"`
	Status       string    `json:"status"`
	TerminalCode string    `json:"terminal_code,omitempty"`
	AttemptCount int       `json:"attempt_count,omitempty"`
}

// FetchedBody is the result of fetching one result artifact and re-measuring
// its digest.
type FetchedBody struct {
	ArtifactID     string `json:"artifact_id"`
	ExpectedSHA256 string `json:"expected_sha256"`
	ObservedSHA256 string `json:"observed_sha256,omitempty"`
	SizeBytes      int64  `json:"size_bytes"`
	Error          string `json:"error,omitempty"`
}

// Observation is what TEI saw through EKI's public Client API. It contains no
// Lease, Fence or Attempt identifier, because the public projection does not
// expose them; those responsibilities stay inside EKI.
type Observation struct {
	// Now is the time the receipt is decided at.
	Now time.Time `json:"now"`
	// Deadline is the wait limit TEI imposed on itself. It is not EKI's
	// deadline field, which EKI records but does not act on.
	Deadline time.Time `json:"deadline"`
	// StartedAt and FinishedAt bound the client-side round trip. They are
	// wall-clock values on the caller's host, not EKI's processing time.
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	// StatusReads and Requests are the client's own message counts, so the
	// cost of the envelope is visible next to the work it carried.
	StatusReads int `json:"status_reads,omitempty"`
	Requests    int `json:"requests,omitempty"`
	// Timings are the client-side phases of the delegation. WaitedMillis is
	// the deliberate polling wait inside the status phase; subtracting it from
	// the total separates the envelope's cost from the time TEI chose to wait.
	Timings      []PhaseTiming `json:"timings,omitempty"`
	WaitedMillis int64         `json:"waited_millis,omitempty"`

	SubmitStatus       int     `json:"submit_status,omitempty"`
	SubmitProblem      Problem `json:"submit_problem,omitempty"`
	TaskID             string  `json:"task_id,omitempty"`
	RequestFingerprint string  `json:"request_fingerprint,omitempty"`
	ClientRequestID    string  `json:"client_request_id,omitempty"`
	Replayed           bool    `json:"replayed,omitempty"`

	// InputError is set when the delegation stopped before submitting because
	// the input EKI would hand to a Worker is not the input TEI asked for.
	// A Task is never submitted in that state.
	InputError string `json:"input_error,omitempty"`

	Statuses []StatusSnapshot `json:"statuses,omitempty"`

	ResultStatus  int         `json:"result_status,omitempty"`
	ResultProblem Problem     `json:"result_problem,omitempty"`
	Result        *TaskResult `json:"result,omitempty"`

	Fetched []FetchedBody `json:"fetched,omitempty"`

	Local *LocalRun `json:"local,omitempty"`
}

// AdmissionSummary is the admitted Task as TEI can see it.
type AdmissionSummary struct {
	Status             int    `json:"status"`
	TaskID             string `json:"task_id,omitempty"`
	RequestFingerprint string `json:"request_fingerprint,omitempty"`
	ClientRequestID    string `json:"client_request_id,omitempty"`
	Replayed           bool   `json:"replayed"`
	Code               string `json:"code,omitempty"`
}

// AttemptSummary is what the public status says about Attempts. It is a count,
// not an identity: TEI cannot name, fence or reclaim an Attempt.
type AttemptSummary struct {
	ObservedCount int  `json:"observed_count"`
	Retried       bool `json:"retried"`
}

// ResultSummary is the result evidence the receipt was decided from.
type ResultSummary struct {
	Status         int    `json:"status"`
	Code           string `json:"code,omitempty"`
	OutputDigest   string `json:"output_digest,omitempty"`
	ArtifactCount  int    `json:"artifact_count"`
	AvailableCount int    `json:"available_count"`
	FetchedCount   int    `json:"fetched_count"`
	DigestMismatch int    `json:"fetched_digest_mismatch_count"`
}

// Receipt is TEI's verdict for one delegation, with the basis separated from
// what was not checked. A reader must be able to see why it says accepted,
// rejected or undetermined without reading the trace.
type Receipt struct {
	Schema  string  `json:"schema"`
	Outcome Outcome `json:"outcome"`
	Reason  string  `json:"reason"`
	// Basis lists the checks that passed or failed, in order.
	Basis []string `json:"basis"`
	// NotChecked is the part of the claim the evidence cannot support. It is
	// never empty: EKI's guarantee and TEI's own check are different things.
	NotChecked []string         `json:"not_checked"`
	Admission  AdmissionSummary `json:"admission"`
	Attempt    AttemptSummary   `json:"attempt"`
	Result     ResultSummary    `json:"result"`
	Local      *LocalRun        `json:"local,omitempty"`
}

// Evaluate decides what one delegation means for TEI. It only reads what the
// public Client API returns; it never assumes a stronger EKI guarantee than
// the contract states.
func Evaluate(obs Observation) Receipt {
	r := Receipt{
		Schema: ReceiptSchema,
		Local:  obs.Local,
		Admission: AdmissionSummary{
			Status: obs.SubmitStatus, TaskID: obs.TaskID,
			RequestFingerprint: obs.RequestFingerprint,
			ClientRequestID:    obs.ClientRequestID,
			Replayed:           obs.Replayed,
		},
		NotChecked: []string{
			"EKIが結果の原データを再計算したか（AcceptedResult の digest は記録値であり、Client は再計算を強制できない）",
			"Attempt・Lease・Fence の識別子との対応（公開の status と result には現れない。これらは EKI の責務のまま）",
		},
	}

	// 0. The input path. A mismatch here stops the delegation before a Task is
	// submitted, so there is nothing for EKI to refuse.
	if obs.InputError != "" {
		r.Outcome, r.Reason = OutcomeRejected, "入力Artifactの同一性を確かめられないため、Taskを送らなかった"
		r.Basis = append(r.Basis, obs.InputError)
		return r
	}

	// 1. Admission.
	if obs.SubmitStatus == 0 {
		r.Outcome, r.Reason = OutcomeUndetermined, "実行要求を送っていない"
		r.Basis = append(r.Basis, "no submission was made")
		return r
	}
	if obs.SubmitStatus != 202 {
		r.Admission.Code = obs.SubmitProblem.Code
		r.Outcome = OutcomeRejected
		r.Reason = fmt.Sprintf("EKI が実行要求を受理しなかった（%d %s）", obs.SubmitStatus, obs.SubmitProblem.Code)
		r.Basis = append(r.Basis, "admission refused the request; no Attempt was created")
		return r
	}
	r.Basis = append(r.Basis, "EKI admitted the request (202)")

	// 2. Attempts.
	for _, s := range obs.Statuses {
		if s.AttemptCount > r.Attempt.ObservedCount {
			r.Attempt.ObservedCount = s.AttemptCount
		}
	}
	r.Attempt.Retried = r.Attempt.ObservedCount > 1
	if r.Attempt.Retried {
		r.Basis = append(r.Basis, fmt.Sprintf("the Task created %d Attempts; effect_mode none makes the extra execution harmless", r.Attempt.ObservedCount))
	}

	// 3. The result answer.
	if obs.ResultStatus == 0 {
		r.Outcome, r.Reason = decideWithoutResult(obs)
		r.Basis = append(r.Basis, "no result answer was received")
		return r
	}
	r.Result.Status = obs.ResultStatus
	if obs.ResultStatus != 200 {
		r.Result.Code = obs.ResultProblem.Code
		return decideFromProblem(obs, r)
	}
	return decideFromResult(obs, r)
}

func decideWithoutResult(obs Observation) (Outcome, string) {
	if DeadlinePassed(obs) {
		return OutcomeUndetermined, "期限までに結果の応答がなく、EKI は Task を終端へ動かしていない（#111）"
	}
	return OutcomeUndetermined, "実行中で、結果はまだ得られていない"
}

// DeadlinePassed reports whether TEI's own wait limit has passed. It is a
// TEI-side decision, not EKI's deadline.
func DeadlinePassed(obs Observation) bool {
	if obs.Deadline.IsZero() {
		return false
	}
	now := obs.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !now.Before(obs.Deadline)
}

func decideFromProblem(obs Observation, r Receipt) Receipt {
	code := obs.ResultProblem.Code
	switch code {
	case "RESULT_NOT_READY":
		r.Outcome = OutcomeUndetermined
		if DeadlinePassed(obs) {
			r.Reason = "期限を過ぎても RESULT_NOT_READY のままで、EKI は終端状態にしていない（#111）"
		} else {
			r.Reason = "EKI はまだ受理済みの結果を持っていない（RESULT_NOT_READY）"
		}
		r.Basis = append(r.Basis, "the result answer is retryable; no accepted result exists yet")
	case "RESULT_NOT_AVAILABLE":
		r.Outcome = OutcomeRejected
		r.Reason = "Task は終端したが、使える結果がない（RESULT_NOT_AVAILABLE）"
		r.Basis = append(r.Basis, "a terminal Task without an accepted result is a refusal, not a pending state")
	default:
		r.Outcome = OutcomeUndetermined
		r.Reason = fmt.Sprintf("結果の取得が想定外の答えになった（%d %s）", obs.ResultStatus, code)
		r.Basis = append(r.Basis, "unknown result answer; the adapter does not guess its meaning")
	}
	for _, s := range obs.Statuses {
		if s.TerminalCode != "" {
			r.Basis = append(r.Basis, "terminal_code="+s.TerminalCode)
		}
	}
	return r
}

func decideFromResult(obs Observation, r Receipt) Receipt {
	res := obs.Result
	if res == nil {
		r.Outcome, r.Reason = OutcomeUndetermined, "200 応答の本文を解釈できなかった"
		r.Basis = append(r.Basis, "result body missing")
		return r
	}
	outputDigest := ""
	if res.OutputDigest != nil {
		outputDigest = res.OutputDigest.Value
	}
	r.Result.OutputDigest = outputDigest
	r.Result.ArtifactCount = len(res.Artifacts)
	for _, a := range res.Artifacts {
		if a.Available {
			r.Result.AvailableCount++
		}
	}

	// A result that names no digest and no artifact cannot be checked. EKI is
	// allowed to omit output_digest, so absence is not a failure of EKI; it is
	// a limit of what the receipt can claim.
	if outputDigest == "" && len(res.Artifacts) == 0 {
		r.Outcome, r.Reason = OutcomeUndetermined, "結果に digest も Artifact もなく、同等性を確かめられない"
		r.Basis = append(r.Basis, "nothing in the answer can be compared with TEI's expectation")
		return r
	}

	// Partial artifact receipt: a result that says bodies exist but serves none
	// is not an accepted result for TEI.
	if len(res.Artifacts) > 0 && r.Result.AvailableCount == 0 {
		r.Outcome, r.Reason = OutcomeUndetermined, "結果の Artifact が一つも取得できない（部分的な受領）"
		r.Basis = append(r.Basis, "artifacts are listed but none is available; the body cannot be verified")
		return r
	}

	if obs.Local == nil || obs.Local.OutputSHA256 == "" {
		r.Outcome, r.Reason = OutcomeUndetermined, "TEI 側の期待値（ローカル実行の digest）がなく、同等性を判定できない"
		r.Basis = append(r.Basis, "no local expectation; the receipt cannot claim equivalence")
		return r
	}
	if obs.Local.ExitCode != 0 {
		r.Outcome, r.Reason = OutcomeUndetermined, "ローカル実行が正常終了していないため、期待値に使えない"
		r.Basis = append(r.Basis, fmt.Sprintf("local run exited %d", obs.Local.ExitCode))
		return r
	}

	// Compare the output digest with TEI's own expectation.
	expected := obs.Local.OutputSHA256
	if outputDigest != "" {
		if outputDigest != expected {
			r.Outcome = OutcomeRejected
			r.Reason = "結果の digest が TEI の期待値と一致しない（実行環境・キャッシュ・toolchain の差であり得る）"
			r.Basis = append(r.Basis, fmt.Sprintf("output_digest=%s expected=%s", outputDigest, expected))
			return r
		}
		r.Basis = append(r.Basis, "output_digest equals TEI's local expectation")
	}

	// Re-measure every fetched body. A digest header is a claim; the bytes are
	// the evidence.
	for _, f := range obs.Fetched {
		if f.Error != "" {
			r.Outcome, r.Reason = OutcomeUndetermined, "結果の Artifact 本文を取得できなかった"
			r.Basis = append(r.Basis, fmt.Sprintf("%s: %s", f.ArtifactID, f.Error))
			return r
		}
		r.Result.FetchedCount++
		if f.ObservedSHA256 != f.ExpectedSHA256 {
			r.Result.DigestMismatch++
			r.Outcome = OutcomeRejected
			r.Reason = "取得した Artifact の digest が名前に一致しない"
			r.Basis = append(r.Basis, fmt.Sprintf("%s: observed=%s expected=%s", f.ArtifactID, f.ObservedSHA256, f.ExpectedSHA256))
			return r
		}
		r.Basis = append(r.Basis, fmt.Sprintf("artifact %s fetched and re-hashed to its name", f.ArtifactID))
	}

	r.Outcome = OutcomeAccepted
	r.Reason = "EKI が結果を返し、TEI の期待値と一致した"
	r.Basis = append(r.Basis, "the delegated result is equivalent to TEI's local expectation")
	r.NotChecked = append(r.NotChecked,
		"結果が独立した検証者に確認されたか（EKI の Result Acceptance は独立検証の入力を持たない。#242）",
		"キャッシュ・toolchain・CPU が要求と同一だったか（要求に固定の欄がない）",
	)
	return r
}
