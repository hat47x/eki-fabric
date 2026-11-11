package teideleg

import (
	"context"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, f *fakeEKI) *Client {
	t.Helper()
	srv := f.server()
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: "test-token"}
}

func mustProject(t *testing.T, op Operation) Projection {
	t.Helper()
	p, err := Project(op)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func delegateOnce(t *testing.T, c *Client, p Projection, body []byte, local *LocalRun, deadline time.Time) Delegation {
	t.Helper()
	d, err := Delegate(context.Background(), c, p, DelegateOptions{
		Uploads:      map[string][]byte{"workspace": body},
		Deadline:     deadline,
		PollInterval: time.Millisecond,
		MaxPolls:     3,
		Local:        local,
		Sleep:        func(time.Duration) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func completedResult(f *fakeEKI, output []byte) *TaskResult {
	digest := f.putArtifact(output)
	return &TaskResult{
		Schema: "eki.task-result/v1",
		Artifacts: []ResultArtifact{{
			SHA256: &Digest{Algorithm: "sha256", Value: digest}, SizeBytes: int64(len(output)),
			Available: true, ArtifactID: digest,
		}},
		OutputDigest: &Digest{Algorithm: "sha256", Value: digest},
	}
}

func TestMockDelegationHappyPath(t *testing.T) {
	body := []byte("the workspace bytes")
	op := sampleOperation(SHA256Hex(body), int64(len(body)))
	p := mustProject(t, op)

	f := newFakeEKI(t)
	output := []byte(`{"valid":true}`)
	result := completedResult(f, output)
	f.autoComplete(1, result, nil)

	d := delegateOnce(t, testClient(t, f), p, body, &LocalRun{
		Schema: LocalRunSchema, ExitCode: 0, OutputSHA256: result.OutputDigest.Value,
	}, time.Now().Add(time.Minute))

	if d.Receipt.Outcome != OutcomeAccepted {
		t.Fatalf("outcome = %s: %s\n%v", d.Receipt.Outcome, d.Receipt.Reason, d.Receipt.Basis)
	}
	if d.Observation.SubmitStatus != 202 {
		t.Fatalf("submit status = %d", d.Observation.SubmitStatus)
	}
	if d.Trace.Links.TaskID == "" || d.Trace.Links.RequestFingerprint == "" {
		t.Fatalf("links = %+v", d.Trace.Links)
	}
	if d.Trace.Links.AttemptCount != 1 {
		t.Errorf("attempt count = %d", d.Trace.Links.AttemptCount)
	}
	if len(d.Trace.Links.Inputs) != 1 || !d.Trace.Links.Inputs[0].DigestAgreed {
		t.Errorf("input links = %+v", d.Trace.Links.Inputs)
	}
	if len(d.Trace.Links.ResultArtifacts) != 1 ||
		d.Trace.Links.ResultArtifacts[0].FetchedSHA256 != result.OutputDigest.Value {
		t.Errorf("result links = %+v", d.Trace.Links.ResultArtifacts)
	}
	if len(d.Receipt.NotChecked) == 0 {
		t.Error("a receipt must say what it did not check")
	}
}

func TestMockDuplicateSubmissionConvergesOnOneTask(t *testing.T) {
	body := []byte("bundle")
	op := sampleOperation(SHA256Hex(body), int64(len(body)))
	p := mustProject(t, op)

	f := newFakeEKI(t)
	output := []byte("output")
	result := completedResult(f, output)
	f.autoComplete(1, result, nil)
	c := testClient(t, f)

	first := delegateOnce(t, c, p, body, &LocalRun{ExitCode: 0, OutputSHA256: result.OutputDigest.Value}, time.Now().Add(time.Minute))
	second := delegateOnce(t, c, p, body, &LocalRun{ExitCode: 0, OutputSHA256: result.OutputDigest.Value}, time.Now().Add(time.Minute))

	if first.Trace.Links.TaskID != second.Trace.Links.TaskID {
		t.Fatalf("a retry created a second Task: %s vs %s", first.Trace.Links.TaskID, second.Trace.Links.TaskID)
	}
	if !second.Observation.Replayed {
		t.Error("the second submission should be replayed")
	}
	if len(f.tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(f.tasks))
	}
	if second.Trace.Links.AttemptCount != 1 {
		t.Errorf("attempt count = %d; a duplicate submission must not create another Attempt", second.Trace.Links.AttemptCount)
	}
	if second.Receipt.Outcome != OutcomeAccepted {
		t.Fatalf("outcome = %s", second.Receipt.Outcome)
	}
}

func TestMockInputThatWasNeverUploadedIsRefused(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	client := testClient(t, f)

	_, err := client.SubmitTask(context.Background(), p.IdempotencyKey, p.Task)
	if err == nil {
		t.Fatal("submitting without the input should be refused")
	}
	if !IsCode(err, "ARTIFACT_NOT_AVAILABLE") {
		t.Fatalf("error = %v", err)
	}
	receipt := Evaluate(Observation{
		SubmitStatus:  StatusOf(err),
		SubmitProblem: problemOf(err),
	})
	if receipt.Outcome != OutcomeRejected {
		t.Fatalf("outcome = %s: %s", receipt.Outcome, receipt.Reason)
	}
}

func TestMockPartialArtifactIsNotAccepted(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	output := []byte("output")
	result := completedResult(f, output)
	result.Artifacts[0].Available = false
	result.Artifacts[0].ArtifactID = ""
	f.autoComplete(1, result, nil)

	d := delegateOnce(t, testClient(t, f), p, body, &LocalRun{
		ExitCode: 0, OutputSHA256: result.OutputDigest.Value,
	}, time.Now().Add(time.Minute))
	if d.Receipt.Outcome != OutcomeUndetermined {
		t.Fatalf("outcome = %s: %s", d.Receipt.Outcome, d.Receipt.Reason)
	}
	if !strings.Contains(d.Receipt.Reason, "Artifact") {
		t.Errorf("reason = %q", d.Receipt.Reason)
	}
}

func TestMockResultNeverArrives(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	f.autoHold("verifying", 1)
	c := testClient(t, f)

	inFlight := delegateOnce(t, c, p, body, &LocalRun{ExitCode: 0, OutputSHA256: strings.Repeat("d", 64)}, time.Now().Add(time.Minute))
	if inFlight.Receipt.Outcome != OutcomeUndetermined {
		t.Fatalf("in-flight outcome = %s", inFlight.Receipt.Outcome)
	}

	expired := delegateOnce(t, c, p, body, &LocalRun{ExitCode: 0, OutputSHA256: strings.Repeat("d", 64)}, time.Now().Add(-time.Second))
	if expired.Receipt.Outcome != OutcomeUndetermined {
		t.Fatalf("expired outcome = %s: %s", expired.Receipt.Outcome, expired.Receipt.Reason)
	}
	if !strings.Contains(expired.Receipt.Reason, "期限") {
		t.Errorf("reason = %q; a wait limit that passed must be visible", expired.Receipt.Reason)
	}
}

func TestMockTerminalWithoutResultIsRejected(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	f.autoComplete(1, nil, &Problem{
		Type: "about:blank", Title: "The task finished without an accepted result",
		Status: 409, Code: "RESULT_NOT_AVAILABLE",
	})

	d := delegateOnce(t, testClient(t, f), p, body, &LocalRun{ExitCode: 0, OutputSHA256: strings.Repeat("d", 64)}, time.Now().Add(time.Minute))
	if d.Receipt.Outcome != OutcomeRejected {
		t.Fatalf("outcome = %s: %s", d.Receipt.Outcome, d.Receipt.Reason)
	}
}

func TestMockDigestMismatchIsRejected(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	result := completedResult(f, []byte("different output"))
	f.autoComplete(1, result, nil)

	d := delegateOnce(t, testClient(t, f), p, body, &LocalRun{
		ExitCode: 0, OutputSHA256: strings.Repeat("e", 64),
	}, time.Now().Add(time.Minute))
	if d.Receipt.Outcome != OutcomeRejected {
		t.Fatalf("outcome = %s: %s", d.Receipt.Outcome, d.Receipt.Reason)
	}
	if !strings.Contains(d.Receipt.Reason, "一致しない") {
		t.Errorf("reason = %q", d.Receipt.Reason)
	}
}

func TestMockResultWithoutDigestIsUndetermined(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	f.autoComplete(1, &TaskResult{Schema: "eki.task-result/v1"}, nil)

	d := delegateOnce(t, testClient(t, f), p, body, &LocalRun{
		ExitCode: 0, OutputSHA256: strings.Repeat("f", 64),
	}, time.Now().Add(time.Minute))
	if d.Receipt.Outcome != OutcomeUndetermined {
		t.Fatalf("outcome = %s: %s", d.Receipt.Outcome, d.Receipt.Reason)
	}
}

func TestMockRetryIsVisibleButDoesNotBlockAcceptance(t *testing.T) {
	body := []byte("bundle")
	p := mustProject(t, sampleOperation(SHA256Hex(body), int64(len(body))))
	f := newFakeEKI(t)
	result := completedResult(f, []byte("output"))
	f.autoComplete(3, result, nil)

	d := delegateOnce(t, testClient(t, f), p, body, &LocalRun{
		ExitCode: 0, OutputSHA256: result.OutputDigest.Value,
	}, time.Now().Add(time.Minute))
	if d.Receipt.Outcome != OutcomeAccepted {
		t.Fatalf("outcome = %s: %s", d.Receipt.Outcome, d.Receipt.Reason)
	}
	if !d.Receipt.Attempt.Retried || d.Receipt.Attempt.ObservedCount != 3 {
		t.Errorf("attempt summary = %+v", d.Receipt.Attempt)
	}
}
