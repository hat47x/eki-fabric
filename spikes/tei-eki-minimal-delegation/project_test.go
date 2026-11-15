package teideleg

import (
	"strings"
	"testing"
	"time"
)

func sampleOperation(inputDigest string, size int64) Operation {
	return Operation{
		ID:             "tei.validate",
		SourceRevision: "b8cbd432a",
		Command:        []string{"validate", "-workspace", "reference/workspace", "-json"},
		// A fixed digest: the test is about mapping, not about a real binary.
		ExecutableSHA256: strings.Repeat("a", 64),
		Toolchain:        "go1.26.10",
		ExecutorID:       "tei.validate",
		Inputs: []Input{{
			Name: "workspace", SHA256Hex: inputDigest, SizeBytes: size,
			MediaType: "application/gzip", DataClass: "internal",
		}},
		Resources: ResourceRequest{
			CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeSeconds: 600,
			OutputBytesMax: 1 << 20, NetworkBytesMax: 0,
		},
		Deadline:             time.Date(2026, 11, 11, 5, 0, 0, 0, time.UTC),
		ExpectedOutputSHA256: strings.Repeat("b", 64),
		Priority:             "normal",
	}
}

func TestProjectBindsWhatEKIEnforces(t *testing.T) {
	body := []byte("deterministic workspace bundle")
	op := sampleOperation(SHA256Hex(body), int64(len(body)))
	p, err := Project(op)
	if err != nil {
		t.Fatal(err)
	}
	if p.Task.Schema != "eki.task-request/v1" {
		t.Errorf("task schema = %q", p.Task.Schema)
	}
	if p.Task.Executor.ID != "tei.validate" {
		t.Errorf("executor = %q", p.Task.Executor.ID)
	}
	if len(p.Task.Inputs) != 1 || p.Task.Inputs[0].ArtifactID != SHA256Hex(body) {
		t.Fatalf("inputs = %+v", p.Task.Inputs)
	}
	if p.Task.Reliability.EffectMode != "none" {
		t.Errorf("effect_mode = %q, want none", p.Task.Reliability.EffectMode)
	}
	if p.Task.Deadline != "2026-11-11T05:00:00Z" {
		t.Errorf("deadline = %q", p.Task.Deadline)
	}
	if len(p.Ingests) != 1 || p.Ingests[0].ExpectedDigest == nil ||
		p.Ingests[0].ExpectedDigest.Value != SHA256Hex(body) {
		t.Fatalf("ingests = %+v", p.Ingests)
	}
	if p.Ingests[0].DeclaredSizeBytes != int64(len(body)) {
		t.Errorf("declared size = %d", p.Ingests[0].DeclaredSizeBytes)
	}

	// The executable digest and the toolchain travel only as parameters. That
	// is the finding the receipt must not overstate.
	tei, ok := p.Task.Parameters["tei"].(map[string]any)
	if !ok {
		t.Fatalf("parameters = %+v", p.Task.Parameters)
	}
	if tei["executable_sha256"] != op.ExecutableSHA256 {
		t.Errorf("parameters executable digest = %v", tei["executable_sha256"])
	}
	if tei["toolchain"] != op.Toolchain {
		t.Errorf("parameters toolchain = %v", tei["toolchain"])
	}

	statuses := map[string]BindingStatus{}
	for _, b := range p.Bindings {
		statuses[b.TEIField] = b.Status
	}
	for field, want := range map[string]BindingStatus{
		"入力の内容同一性（入力Artifactのdigest）": BindingEnforced,
		"実行体のdigest": BindingRecordedOnly,
		"toolchain":  BindingRecordedOnly,
		"期限":         BindingRecordedOnly,
		"実際のAttempt": BindingRecordedOnly,
		"キャッシュの局所性・配置の希望":    BindingUnavailable,
		"結果のReceipt（独立した検証）": BindingUnavailable,
	} {
		if statuses[field] != want {
			t.Errorf("binding %q = %q, want %q", field, statuses[field], want)
		}
	}
}

func TestProjectIsDeterministic(t *testing.T) {
	body := []byte("same bytes")
	op := sampleOperation(SHA256Hex(body), int64(len(body)))
	a, err := Project(op)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Project(op)
	if err != nil {
		t.Fatal(err)
	}
	if a.IdempotencyKey != b.IdempotencyKey || a.ClientRequestID != b.ClientRequestID {
		t.Fatalf("a duplicate request must converge: %q/%q vs %q/%q",
			a.IdempotencyKey, a.ClientRequestID, b.IdempotencyKey, b.ClientRequestID)
	}
	if len(a.IdempotencyKey) < 8 || len(a.IdempotencyKey) > 128 {
		t.Errorf("idempotency key length = %d", len(a.IdempotencyKey))
	}

	// A different requested condition is a different delegation. EKI answers a
	// reused key with 409, which is the behaviour TEI needs.
	other := op
	other.Deadline = op.Deadline.Add(time.Hour)
	c, err := Project(other)
	if err != nil {
		t.Fatal(err)
	}
	if a.IdempotencyKey == c.IdempotencyKey {
		t.Fatal("a different deadline must not reuse the idempotency key")
	}
}

func TestProjectRefusesIncompleteOperation(t *testing.T) {
	good := sampleOperation(strings.Repeat("c", 64), 10)
	cases := map[string]func(*Operation){
		"no id":            func(o *Operation) { o.ID = "" },
		"no revision":      func(o *Operation) { o.SourceRevision = "" },
		"no command":       func(o *Operation) { o.Command = nil },
		"no executable":    func(o *Operation) { o.ExecutableSHA256 = "" },
		"no toolchain":     func(o *Operation) { o.Toolchain = "" },
		"bad executor":     func(o *Operation) { o.ExecutorID = "Tei/Validate" },
		"no inputs":        func(o *Operation) { o.Inputs = nil },
		"no deadline":      func(o *Operation) { o.Deadline = time.Time{} },
		"bad input digest": func(o *Operation) { o.Inputs[0].SHA256Hex = "xyz" },
		"no media type":    func(o *Operation) { o.Inputs[0].MediaType = "" },
		"bad expectation":  func(o *Operation) { o.ExpectedOutputSHA256 = "short" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			op := good
			op.Inputs = append([]Input(nil), good.Inputs...)
			mutate(&op)
			if _, err := Project(op); err == nil {
				t.Fatal("Project accepted an incomplete operation")
			}
		})
	}
}
