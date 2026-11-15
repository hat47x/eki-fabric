package teideleg

import (
	"context"
	"testing"
	"time"
)

// A Task that another caller submitted can still be read through the same
// rules. These tests use the fake EKI: they pin what TEI decides given EKI's
// answers, not what EKI does.
func TestEvaluateTaskNotReadyBecomesUndetermined(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	f.autoHold("verifying", 1)
	c := testClient(t, f)
	accepted, err := c.SubmitTask(context.Background(), p.IdempotencyKey, TaskRequest{
		Schema:      "eki.task-request/v1",
		Executor:    ExecutorRequirement{ID: "tei.validate"},
		Inputs:      []ArtifactReference{},
		Parameters:  map[string]any{},
		Resources:   p.Task.Resources,
		Reliability: p.Task.Reliability,
	})
	if err != nil {
		t.Fatal(err)
	}
	obs, receipt, err := EvaluateTask(context.Background(), c, accepted.TaskID, nil, 0, time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != OutcomeUndetermined {
		t.Fatalf("outcome = %s: %s", receipt.Outcome, receipt.Reason)
	}
	if receipt.Attempt.ObservedCount != 1 {
		t.Errorf("attempt summary = %+v", receipt.Attempt)
	}
	if obs.SubmitStatus != 202 {
		t.Errorf("submit status = %d; a Task that answers a status read exists here", obs.SubmitStatus)
	}
}

func TestEvaluateTaskWithResultStillNeedsTheLocalExpectation(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	result := completedResult(f, []byte("output"))
	f.autoComplete(1, result, nil)

	submitted, err := testClient(t, f).SubmitTask(context.Background(), p.IdempotencyKey, TaskRequest{
		Schema:      "eki.task-request/v1",
		Executor:    ExecutorRequirement{ID: "tei.validate"},
		Inputs:      []ArtifactReference{},
		Parameters:  map[string]any{},
		Resources:   p.Task.Resources,
		Reliability: p.Task.Reliability,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, f)
	// Without a local expectation the receipt cannot claim equivalence.
	_, receipt, err := EvaluateTask(context.Background(), c, submitted.TaskID, nil, 0, time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != OutcomeUndetermined {
		t.Fatalf("outcome = %s: %s", receipt.Outcome, receipt.Reason)
	}

	// With the expectation it can, and the fetched body is re-measured.
	obs, accepted, err := EvaluateTask(context.Background(), c, submitted.TaskID,
		&LocalRun{ExitCode: 0, OutputSHA256: result.OutputDigest.Value}, 0, time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Outcome != OutcomeAccepted {
		t.Fatalf("outcome = %s: %s", accepted.Outcome, accepted.Reason)
	}
	if len(obs.Fetched) != 1 || obs.Fetched[0].ObservedSHA256 != result.OutputDigest.Value {
		t.Errorf("fetched = %+v", obs.Fetched)
	}
}

func TestEvaluateTaskPartialArtifactIsNotAccepted(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	result := completedResult(f, []byte("output"))
	result.Artifacts[0].Available = false
	result.Artifacts[0].ArtifactID = ""
	f.autoComplete(1, result, nil)
	c := testClient(t, f)
	submitted, err := c.SubmitTask(context.Background(), p.IdempotencyKey, TaskRequest{
		Schema:      "eki.task-request/v1",
		Executor:    ExecutorRequirement{ID: "tei.validate"},
		Inputs:      []ArtifactReference{},
		Parameters:  map[string]any{},
		Resources:   p.Task.Resources,
		Reliability: p.Task.Reliability,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, receipt, err := EvaluateTask(context.Background(), c, submitted.TaskID,
		&LocalRun{ExitCode: 0, OutputSHA256: result.OutputDigest.Value}, 0, time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome == OutcomeAccepted {
		t.Fatalf("a result whose body cannot be fetched was accepted: %+v", receipt)
	}
}
