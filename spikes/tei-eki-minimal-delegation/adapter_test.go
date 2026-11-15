package teideleg

import (
	"context"
	"testing"
	"time"
)

func TestNewAdapterRejectsNilAPI(t *testing.T) {
	if _, err := NewAdapter(nil, DelegateOptions{}); err == nil {
		t.Fatal("NewAdapter accepted a nil API")
	}
}

func TestAdapterExecuteRejectsIncompleteOperation(t *testing.T) {
	adapter, err := NewAdapter(testClient(t, newFakeEKI(t)), DelegateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Execute(context.Background(), Operation{}, nil); err == nil {
		t.Fatal("Execute accepted an incomplete operation")
	}
}

func TestAdapterExecuteRunsTheProjectionAndReturnsTheTrace(t *testing.T) {
	body := []byte("the workspace bytes")
	op := sampleOperation(SHA256Hex(body), int64(len(body)))

	f := newFakeEKI(t)
	output := []byte(`{"valid":true}`)
	result := completedResult(f, output)
	f.autoComplete(1, result, nil)

	adapter, err := NewAdapter(testClient(t, f), DelegateOptions{
		Deadline:     time.Now().Add(time.Minute),
		PollInterval: time.Millisecond,
		MaxPolls:     3,
		Sleep:        func(time.Duration) {},
		Local:        &LocalRun{Schema: LocalRunSchema, ExitCode: 0, OutputSHA256: result.OutputDigest.Value},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := adapter.Execute(context.Background(), op, map[string][]byte{"workspace": body})
	if err != nil {
		t.Fatal(err)
	}
	if d.Receipt.Outcome != OutcomeAccepted {
		t.Fatalf("outcome = %s: %s", d.Receipt.Outcome, d.Receipt.Reason)
	}
	if d.Trace.Schema != TraceSchema || d.Trace.Links.OperationID != "tei.validate" {
		t.Fatalf("trace = %+v", d.Trace)
	}
	if d.Trace.Links.Inputs[0].DigestAgreed != true {
		t.Errorf("input link = %+v", d.Trace.Links.Inputs)
	}
	if d.Receipt.Admission.TaskID == "" {
		t.Error("the receipt must name the admitted Task")
	}
}

func TestUploadDigestMismatchStopsBeforeSubmit(t *testing.T) {
	body := []byte("the workspace bytes")
	op := sampleOperation(SHA256Hex(body), int64(len(body)))

	f := newFakeEKI(t)
	f.uploadArtifactIDOverride = "9999999999999999999999999999999999999999999999999999999999999999"
	adapter, err := NewAdapter(testClient(t, f), DelegateOptions{
		Deadline: time.Now().Add(time.Minute), MaxPolls: 1, Sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := adapter.Execute(context.Background(), op, map[string][]byte{"workspace": body})
	if err != nil {
		t.Fatal(err)
	}
	if d.Receipt.Outcome != OutcomeRejected {
		t.Fatalf("outcome = %s: %s", d.Receipt.Outcome, d.Receipt.Reason)
	}
	if d.Observation.InputError == "" {
		t.Error("the observation should record why the delegation stopped")
	}
	if len(f.tasks) != 0 {
		t.Fatalf("a Task was submitted after the input digest disagreed: %d", len(f.tasks))
	}
	if len(d.Trace.Links.Inputs) != 1 || d.Trace.Links.Inputs[0].DigestAgreed {
		t.Errorf("input link = %+v", d.Trace.Links.Inputs)
	}
	if d.Trace.Links.TaskID != "" {
		t.Errorf("trace names a Task that was never submitted: %s", d.Trace.Links.TaskID)
	}
}

func TestAdapterExecuteProjectionReusesTheExactRequest(t *testing.T) {
	body := []byte("bundle")
	op := sampleOperation(SHA256Hex(body), int64(len(body)))
	projection, err := Project(op)
	if err != nil {
		t.Fatal(err)
	}

	f := newFakeEKI(t)
	result := completedResult(f, []byte("output"))
	f.autoComplete(1, result, nil)
	adapter, err := NewAdapter(testClient(t, f), DelegateOptions{
		Deadline: time.Now().Add(time.Minute), MaxPolls: 1, Sleep: func(time.Duration) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := adapter.ExecuteProjection(context.Background(), projection, map[string][]byte{"workspace": body})
	if err != nil {
		t.Fatal(err)
	}
	if d.Projection.IdempotencyKey != projection.IdempotencyKey {
		t.Fatalf("the adapter composed a different request: %s vs %s",
			d.Projection.IdempotencyKey, projection.IdempotencyKey)
	}
}
