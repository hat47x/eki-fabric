package admission

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/taskstate"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

var testNow = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func example(t testing.TB, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "external", "examples", name))
	if err != nil {
		t.Fatalf("read example %s: %v", name, err)
	}
	return raw
}

// requestWithoutInputs is the P0 example with its artifact input removed,
// because M0 has no Artifact Store and rejects any input.
func requestWithoutInputs() string {
	return `{
  "schema": "eki.task-request/v1",
  "executor": {"id": "dev.example.transform", "version_constraint": "^1"},
  "inputs": [],
  "parameters": {"mode": "normalize"},
  "resources": {"cpu_millis": 1000, "memory_bytes": 268435456, "wall_time_seconds": 300, "output_bytes_max": 16777216, "network_bytes_max": 134217728},
  "reliability": {"retryable": true, "preemptible": true, "checkpointable": false, "deterministic": true, "max_attempts": 3, "effect_mode": "none"},
  "priority": "normal"
}`
}

func testPolicy() Policy {
	return Policy{
		ApprovedExecutors:   []string{"dev.example.transform"},
		PerTask:             ResourceCeiling{CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeSeconds: 3600, OutputBytesMax: 1 << 30, NetworkBytesMax: 1 << 30, TemporaryDiskBytes: 1 << 30},
		MaxAttempts:         5,
		MaxOutstandingTasks: 3,
		MaxDeadlineHorizon:  24 * time.Hour,
	}
}

func newService(t testing.TB, mutate ...func(*Config)) (*Service, *memory.Store) {
	t.Helper()
	store := memory.New()
	cfg := Config{Store: store, Policy: testPolicy(), Now: func() time.Time { return testNow }}
	for _, m := range mutate {
		m(&cfg)
	}
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return svc, store
}

func wantError(t testing.TB, err error, kind Kind, code string) *Error {
	t.Helper()
	var ae *Error
	if !errors.As(err, &ae) {
		t.Fatalf("error = %v (%T), want *admission.Error", err, err)
	}
	if ae.Kind != kind || ae.Code != code {
		t.Fatalf("error kind=%d code=%s, want kind=%d code=%s (detail: %s)", ae.Kind, ae.Code, kind, code, ae.Detail)
	}
	return ae
}

// --- strict JSON ---

func TestScanJSONRejectsWhatEncodingJSONWouldAccept(t *testing.T) {
	deep := strings.Repeat(`{"a":`, maxJSONDepth+2) + `1` + strings.Repeat(`}`, maxJSONDepth+2)
	wide := `{"a":[` + strings.TrimSuffix(strings.Repeat("1,", maxJSONArrayItems+1), ",") + `]}`
	var fields strings.Builder
	fields.WriteString("{")
	for i := 0; i <= maxJSONObjectFields; i++ {
		if i > 0 {
			fields.WriteString(",")
		}
		fmt.Fprintf(&fields, `"k%d":1`, i)
	}
	fields.WriteString("}")

	cases := map[string]string{
		"duplicate key":             `{"a":1,"a":2}`,
		"duplicate nested key":      `{"a":{"b":1,"b":2}}`,
		"invalid utf-8 in string":   "{\"a\":\"\xff\"}",
		"trailing object":           `{"a":1}{"b":2}`,
		"trailing garbage":          `{"a":1} x`,
		"top-level array":           `[1]`,
		"top-level string":          `"x"`,
		"empty":                     ``,
		"truncated":                 `{"a":`,
		"deep nesting":              deep,
		"wide array":                wide,
		"wide object":               fields.String(),
		"oversized string":          `{"a":"` + strings.Repeat("x", maxJSONStringBytes+1) + `"}`,
		"oversized key":             `{"` + strings.Repeat("k", maxJSONKeyBytes+1) + `":1}`,
		"bom":                       "\xef\xbb\xbf{}",
		"duplicate key via escapes": `{"a":1,"a":2}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if err := scanJSON([]byte(raw)); err == nil {
				t.Fatalf("scanJSON accepted %q", truncate(raw))
			}
		})
	}
}

func TestScanJSONAcceptsAWellFormedObject(t *testing.T) {
	if err := scanJSON([]byte(requestWithoutInputs())); err != nil {
		t.Fatalf("scanJSON() error = %v", err)
	}
}

// --- request parsing ---

func TestParseTaskRequestAcceptsTheP0Example(t *testing.T) {
	got, err := ParseTaskRequest(example(t, "valid-task-request.json"))
	if err != nil {
		t.Fatalf("ParseTaskRequest() error = %v", err)
	}
	if got.Executor.ID != "dev.example.transform" || len(got.Inputs) != 1 || got.Reliability.MaxAttempts != 3 {
		t.Fatalf("unexpected request: %#v", got)
	}
}

func TestParseTaskRequestRejectsTheP0InvalidExamples(t *testing.T) {
	for _, name := range []string{
		"invalid-arbitrary-url-input.json",
		"invalid-client-selected-worker.json",
		"invalid-effect-mode.json",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTaskRequest(example(t, name))
			if err == nil {
				t.Fatal("ParseTaskRequest accepted an invalid example")
			}
			var ae *Error
			if !errors.As(err, &ae) || ae.Kind != KindBadRequest {
				t.Fatalf("error = %v, want a bad request", err)
			}
		})
	}
}

func TestParseTaskRequestAppliesDefaults(t *testing.T) {
	got, err := ParseTaskRequest([]byte(strings.Replace(requestWithoutInputs(), `, "priority": "normal"`, "", 1)))
	if err != nil {
		t.Fatalf("ParseTaskRequest() error = %v", err)
	}
	if got.Priority != PriorityNormal {
		t.Fatalf("Priority = %q, want normal", got.Priority)
	}
}

func TestParseTaskRequestRejectsSchemaViolations(t *testing.T) {
	base := requestWithoutInputs()
	replace := func(old, new string) string {
		t.Helper()
		if !strings.Contains(base, old) {
			t.Fatalf("test bug: %q not in base", old)
		}
		return strings.Replace(base, old, new, 1)
	}
	cases := map[string]string{
		"wrong schema":            replace(`"eki.task-request/v1"`, `"eki.task-request/v2"`),
		"missing executor":        replace(`"executor": {"id": "dev.example.transform", "version_constraint": "^1"},`, ``),
		"missing parameters":      replace(`"parameters": {"mode": "normalize"},`, ``),
		"null parameters":         replace(`{"mode": "normalize"}`, `null`),
		"unknown top-level field": replace(`"priority": "normal"`, `"priority": "normal", "worker_id": "w1"`),
		"unknown nested field":    replace(`"effect_mode": "none"`, `"effect_mode": "none", "force": true`),
		"executor id uppercase":   replace(`"dev.example.transform"`, `"Dev.Example"`),
		"executor id empty":       replace(`"dev.example.transform"`, `""`),
		"cpu zero":                replace(`"cpu_millis": 1000`, `"cpu_millis": 0`),
		"cpu too large":           replace(`"cpu_millis": 1000`, `"cpu_millis": 256001`),
		"memory too small":        replace(`"memory_bytes": 268435456`, `"memory_bytes": 1`),
		"wall time too long":      replace(`"wall_time_seconds": 300`, `"wall_time_seconds": 604801`),
		"negative output":         replace(`"output_bytes_max": 16777216`, `"output_bytes_max": -1`),
		"fractional cpu":          replace(`"cpu_millis": 1000`, `"cpu_millis": 1000.5`),
		"string cpu":              replace(`"cpu_millis": 1000`, `"cpu_millis": "1000"`),
		"missing resource field":  replace(`"network_bytes_max": 134217728`, `"x_unused": 1`),
		"attempts zero":           replace(`"max_attempts": 3`, `"max_attempts": 0`),
		"attempts too many":       replace(`"max_attempts": 3`, `"max_attempts": 33`),
		"retryable missing":       replace(`"retryable": true,`, ``),
		"retryable string":        replace(`"retryable": true`, `"retryable": "yes"`),
		"effect mode external":    replace(`"effect_mode": "none"`, `"effect_mode": "non-idempotent-external"`),
		"effect mode empty":       replace(`"effect_mode": "none"`, `"effect_mode": ""`),
		"bad priority":            replace(`"priority": "normal"`, `"priority": "urgent"`),
		"bad deadline":            replace(`"priority": "normal"`, `"priority": "normal", "deadline": "tomorrow"`),
		"bad result profile":      replace(`"priority": "normal"`, `"priority": "normal", "result_profile": "Bad Profile"`),
		"input with url":          replace(`"inputs": []`, `"inputs": [{"name":"a","artifact_id":"art_1","url":"http://x"}]`),
		"input bad name":          replace(`"inputs": []`, `"inputs": [{"name":"","artifact_id":"art_1"}]`),
		"input bad artifact":      replace(`"inputs": []`, `"inputs": [{"name":"a","artifact_id":"../etc/passwd"}]`),
		"input duplicate name":    replace(`"inputs": []`, `"inputs": [{"name":"a","artifact_id":"art_1"},{"name":"a","artifact_id":"art_2"}]`),
		"inputs not array":        replace(`"inputs": []`, `"inputs": {}`),
		"not an object":           `[]`,
		"empty object":            `{}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTaskRequest([]byte(raw))
			if err == nil {
				t.Fatalf("ParseTaskRequest accepted %s", name)
			}
			var ae *Error
			if !errors.As(err, &ae) || ae.Kind != KindBadRequest {
				t.Fatalf("error = %v, want a bad request", err)
			}
		})
	}
}

func TestFingerprintIsStableAndSensitive(t *testing.T) {
	parse := func(raw string) NormalizedRequest {
		t.Helper()
		r, err := ParseTaskRequest([]byte(raw))
		if err != nil {
			t.Fatalf("ParseTaskRequest() error = %v", err)
		}
		return r
	}
	base := requestWithoutInputs()

	fp := func(r NormalizedRequest) string {
		t.Helper()
		d, err := r.Fingerprint()
		if err != nil {
			t.Fatalf("Fingerprint() error = %v", err)
		}
		return d.Hex()
	}

	// Whitespace and key order are not semantic.
	reordered := `{"priority":"normal","reliability":{"effect_mode":"none","max_attempts":3,"deterministic":true,"checkpointable":false,"preemptible":true,"retryable":true},"resources":{"network_bytes_max":134217728,"output_bytes_max":16777216,"wall_time_seconds":300,"memory_bytes":268435456,"cpu_millis":1000},"parameters":{"mode":"normalize"},"inputs":[],"executor":{"version_constraint":"^1","id":"dev.example.transform"},"schema":"eki.task-request/v1"}`
	if fp(parse(base)) != fp(parse(reordered)) {
		t.Fatal("key order or whitespace changed the fingerprint")
	}

	// An omitted default and the same default spelled out are the same request.
	explicit := strings.Replace(base, `, "priority": "normal"`, "", 1)
	if fp(parse(base)) != fp(parse(explicit)) {
		t.Fatal("omitting the default priority changed the fingerprint")
	}

	// Anything that changes what would run changes the fingerprint.
	for name, changed := range map[string]string{
		"parameter":   strings.Replace(base, `"normalize"`, `"other"`, 1),
		"cpu":         strings.Replace(base, `"cpu_millis": 1000`, `"cpu_millis": 1001`, 1),
		"attempts":    strings.Replace(base, `"max_attempts": 3`, `"max_attempts": 2`, 1),
		"executor":    strings.Replace(base, `"version_constraint": "^1"`, `"version_constraint": "^2"`, 1),
		"priority":    strings.Replace(base, `"priority": "normal"`, `"priority": "high"`, 1),
		"preemptible": strings.Replace(base, `"preemptible": true`, `"preemptible": false`, 1),
	} {
		if fp(parse(base)) == fp(parse(changed)) {
			t.Errorf("changing the %s did not change the fingerprint", name)
		}
	}

	// The two digests are domain-separated.
	a, _ := parse(base).Fingerprint()
	b, _ := parse(base).TaskIRDigest()
	if a == b {
		t.Fatal("fingerprint and Task IR digest are the same value")
	}
}

// --- policy ---

func TestPolicyRejectsBeforeAnythingIsStored(t *testing.T) {
	base := requestWithoutInputs()
	cases := []struct {
		name string
		raw  string
		code string
	}{
		{"unapproved executor", strings.Replace(base, "dev.example.transform", "evil.tool", 1), "EXECUTOR_NOT_APPROVED"},
		{"artifact input", strings.Replace(base, `"inputs": []`, `"inputs": [{"name":"a","artifact_id":"art_1"}]`, 1), "ARTIFACT_NOT_AVAILABLE"},
		{"gpu", strings.Replace(base, `"network_bytes_max": 134217728`, `"network_bytes_max": 134217728, "gpu": {"count": 1}`, 1), "GPU_NOT_SUPPORTED"},
		{"cpu over ceiling", strings.Replace(base, `"cpu_millis": 1000`, `"cpu_millis": 4001`, 1), "RESOURCE_BUDGET_EXCEEDED"},
		{"memory over ceiling", strings.Replace(base, `"memory_bytes": 268435456`, `"memory_bytes": 2147483648`, 1), "RESOURCE_BUDGET_EXCEEDED"},
		{"time over ceiling", strings.Replace(base, `"wall_time_seconds": 300`, `"wall_time_seconds": 3601`, 1), "RESOURCE_BUDGET_EXCEEDED"},
		{"attempts over policy", strings.Replace(base, `"max_attempts": 3`, `"max_attempts": 6`, 1), "RETRY_BUDGET_EXCEEDED"},
		{"deadline in the past", strings.Replace(base, `"priority": "normal"`, `"priority": "normal", "deadline": "2026-10-06T08:00:00Z"`, 1), "DEADLINE_IN_PAST"},
		{"deadline now", strings.Replace(base, `"priority": "normal"`, `"priority": "normal", "deadline": "2026-10-06T09:00:00Z"`, 1), "DEADLINE_IN_PAST"},
		{"deadline too far", strings.Replace(base, `"priority": "normal"`, `"priority": "normal", "deadline": "2026-10-08T09:00:00Z"`, 1), "DEADLINE_TOO_FAR"},
		{"extension", strings.Replace(base, `"priority": "normal"`, `"priority": "normal", "extensions": [{"kind":"x"}]`, 1), "EXTENSION_NOT_SUPPORTED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := newService(t)
			_, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-00000001", []byte(tc.raw))
			ae := wantError(t, err, KindUnprocessable, tc.code)
			if ae.PolicyReasonCategory == "" {
				t.Error("policy rejection has no reason category")
			}
			// Nothing was admitted, so nothing counts against the quota.
			if _, err := store.LoadAdmission(context.Background(), ids.TaskID("tsk_00000000000000000000000000000000")); !errors.Is(err, statestore.ErrNotFound) {
				t.Errorf("unexpected store state: %v", err)
			}
		})
	}
}

func TestPolicyAcceptsRequestsAtTheCeiling(t *testing.T) {
	svc, _ := newService(t)
	raw := strings.NewReplacer(
		`"cpu_millis": 1000`, `"cpu_millis": 4000`,
		`"memory_bytes": 268435456`, `"memory_bytes": 1073741824`,
		`"wall_time_seconds": 300`, `"wall_time_seconds": 3600`,
		`"max_attempts": 3`, `"max_attempts": 5`,
	).Replace(requestWithoutInputs())
	if _, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-00000001", []byte(raw)); err != nil {
		t.Fatalf("Submit() at the ceiling error = %v", err)
	}
}

func TestNewRejectsAnIncompletePolicy(t *testing.T) {
	good := testPolicy()
	for name, mutate := range map[string]func(*Policy){
		"no executors":       func(p *Policy) { p.ApprovedExecutors = nil },
		"bad executor id":    func(p *Policy) { p.ApprovedExecutors = []string{"Bad ID"} },
		"zero cpu ceiling":   func(p *Policy) { p.PerTask.CPUMillis = 0 },
		"zero attempts":      func(p *Policy) { p.MaxAttempts = 0 },
		"too many attempts":  func(p *Policy) { p.MaxAttempts = 33 },
		"no outstanding cap": func(p *Policy) { p.MaxOutstandingTasks = 0 },
		"negative horizon":   func(p *Policy) { p.MaxDeadlineHorizon = -1 },
	} {
		p := good
		mutate(&p)
		if _, err := New(Config{Store: memory.New(), Policy: p}); !errors.Is(err, ErrInvalidPolicy) {
			t.Errorf("%s: New() error = %v, want ErrInvalidPolicy", name, err)
		}
	}
	if _, err := New(Config{Policy: good}); err == nil {
		t.Error("New() without a store succeeded")
	}
}

// --- submit ---

func TestSubmitAdmitsAndBindsThePrincipal(t *testing.T) {
	svc, store := newService(t)
	got, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-00000001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got.Replayed || !got.SubmittedAt.Equal(testNow) {
		t.Fatalf("unexpected result: %#v", got)
	}

	admission, err := store.LoadAdmission(context.Background(), got.TaskID)
	if err != nil {
		t.Fatalf("LoadAdmission() error = %v", err)
	}
	if admission.Principal != "alice" || admission.MaxAttempts != 3 || admission.Task.Phase != taskstate.TaskPhaseAdmitted {
		t.Fatalf("unexpected admission: %#v", admission)
	}
}

func TestSubmitIsIdempotent(t *testing.T) {
	svc, _ := newService(t)
	alice := Principal{ID: "alice"}
	ctx := context.Background()

	first, err := svc.Submit(ctx, alice, "key-00000001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("first Submit() error = %v", err)
	}
	// The same request again, reformatted, converges on the same Task.
	again, err := svc.Submit(ctx, alice, "key-00000001", []byte(strings.ReplaceAll(requestWithoutInputs(), "\n", " ")))
	if err != nil {
		t.Fatalf("replayed Submit() error = %v", err)
	}
	if again.TaskID != first.TaskID || !again.Replayed {
		t.Fatalf("replay created %s (replayed=%v), want %s", again.TaskID, again.Replayed, first.TaskID)
	}

	// A different request under the same key is a conflict, and creates nothing.
	other := strings.Replace(requestWithoutInputs(), `"normalize"`, `"different"`, 1)
	_, err = svc.Submit(ctx, alice, "key-00000001", []byte(other))
	wantError(t, err, KindConflict, "IDEMPOTENCY_KEY_CONFLICT")

	// Another principal using the same key string is unrelated.
	bob, err := svc.Submit(ctx, Principal{ID: "bob"}, "key-00000001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("bob Submit() error = %v", err)
	}
	if bob.TaskID == first.TaskID || bob.Replayed {
		t.Fatalf("bob was served alice's task: %#v", bob)
	}
}

func TestSubmitRejectsBadKeysAndPrincipals(t *testing.T) {
	svc, _ := newService(t)
	body := []byte(requestWithoutInputs())
	ctx := context.Background()

	for _, key := range []string{"", "short", strings.Repeat("k", 129), "has space!", "ctl\x00char-1"} {
		_, err := svc.Submit(ctx, Principal{ID: "alice"}, key, body)
		var ae *Error
		// A bad key is the Client's mistake, never a storage failure.
		if !errors.As(err, &ae) || ae.Kind != KindBadRequest || ae.Code != "IDEMPOTENCY_KEY_INVALID" {
			t.Errorf("key %q: error = %v, want a bad-request IDEMPOTENCY_KEY_INVALID", key, err)
		}
	}
	if _, err := svc.Submit(ctx, Principal{}, "key-00000001", body); err == nil {
		t.Error("an empty principal was accepted")
	}
	if _, err := svc.Submit(ctx, Principal{ID: strings.Repeat("p", 257)}, "key-00000001", body); err == nil {
		t.Error("an oversized principal was accepted")
	}
}

// An Idempotency-Key is remembered for as long as the admission record exists,
// and admission records are not deleted in this version (#283). A retry with
// the same key and the same request therefore converges on the same Task even
// after that Task has ended, and the key cannot be reused for anything else.
func TestIdempotencyKeyOutlivesTheTask(t *testing.T) {
	svc, _ := newService(t)
	alice := Principal{ID: "alice"}
	ctx := context.Background()

	first, err := svc.Submit(ctx, alice, "key-lifetime-001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if _, err := svc.Cancel(ctx, alice, first.TaskID.String()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	status, err := svc.Status(ctx, alice, first.TaskID.String())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Status != "cancelled" {
		t.Fatalf("status = %s, want the Task to be finished", status.Status)
	}

	// The retry still lands on the finished Task instead of admitting new work.
	replay, err := svc.Submit(ctx, alice, "key-lifetime-001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("replay after the Task ended error = %v", err)
	}
	if replay.TaskID != first.TaskID || !replay.Replayed {
		t.Fatalf("replay after the Task ended = %#v, want %s", replay, first.TaskID)
	}

	// The key still belongs to that request: another body is a conflict.
	other := strings.Replace(requestWithoutInputs(), `"normalize"`, `"different"`, 1)
	_, err = svc.Submit(ctx, alice, "key-lifetime-001", []byte(other))
	wantError(t, err, KindConflict, "IDEMPOTENCY_KEY_CONFLICT")
}

func TestSubmitEnforcesTheOutstandingTaskQuota(t *testing.T) {
	svc, _ := newService(t)
	alice := Principal{ID: "alice"}
	ctx := context.Background()

	var firstID ids.TaskID
	for i := 0; i < 3; i++ {
		// Identical bodies under different keys are different admissions.
		got, err := svc.Submit(ctx, alice, fmt.Sprintf("key-quota-%03d", i), []byte(requestWithoutInputs()))
		if err != nil {
			t.Fatalf("Submit(%d) error = %v", i, err)
		}
		if i == 0 {
			firstID = got.TaskID
		}
	}
	_, err := svc.Submit(ctx, alice, "key-quota-999", []byte(requestWithoutInputs()))
	ae := wantError(t, err, KindTooManyRequests, "TASK_QUOTA_EXCEEDED")
	if ae.Retryable {
		t.Error("a quota rejection must not be retryable when nothing frees the quota on its own (#282)")
	}
	if ae.Detail == "" {
		t.Error("a quota rejection must say how to free capacity")
	}

	// A replay of an admitted task still succeeds at the limit.
	if got, err := svc.Submit(ctx, alice, "key-quota-000", []byte(requestWithoutInputs())); err != nil || got.TaskID != firstID {
		t.Fatalf("replay at the limit = %#v, %v", got, err)
	}

	// Another principal is unaffected.
	if _, err := svc.Submit(ctx, Principal{ID: "bob"}, "key-quota-000", []byte(requestWithoutInputs())); err != nil {
		t.Fatalf("bob was limited by alice's quota: %v", err)
	}

	// Finishing work frees capacity.
	if _, err := svc.Cancel(ctx, alice, firstID.String()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if _, err := svc.Submit(ctx, alice, "key-quota-999", []byte(requestWithoutInputs())); err != nil {
		t.Fatalf("Submit() after a task finished error = %v", err)
	}
}

// A deployment whose admitted Tasks finish without the Client may tell it to
// retry: waiting does clear the quota there (#282).
func TestQuotaRejectionIsRetryableWhenTasksFinishOnTheirOwn(t *testing.T) {
	svc, _ := newService(t, func(c *Config) { c.QuotaDrainsAutomatically = true })
	alice := Principal{ID: "alice"}
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := svc.Submit(ctx, alice, fmt.Sprintf("key-quota-%03d", i), []byte(requestWithoutInputs())); err != nil {
			t.Fatalf("Submit(%d) error = %v", i, err)
		}
	}
	_, err := svc.Submit(ctx, alice, "key-quota-999", []byte(requestWithoutInputs()))
	ae := wantError(t, err, KindTooManyRequests, "TASK_QUOTA_EXCEEDED")
	if !ae.Retryable {
		t.Error("a quota rejection must be retryable when admitted Tasks finish on their own")
	}
}

func TestConcurrentIdenticalSubmissionsCreateOneTask(t *testing.T) {
	svc, _ := newService(t)
	const n = 16
	results := make(chan Accepted, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-concurrent", []byte(requestWithoutInputs()))
			if err != nil {
				t.Errorf("Submit() error = %v", err)
				return
			}
			results <- got
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	seen := map[ids.TaskID]int{}
	for r := range results {
		seen[r.TaskID]++
	}
	if len(seen) != 1 {
		t.Fatalf("concurrent submissions created %d tasks: %v", len(seen), seen)
	}
}

func TestStoreFailureIsReportedGenericallyAndLogged(t *testing.T) {
	var logged []string
	svc, _ := newService(t, func(c *Config) {
		c.Store = failingStore{memory.New()}
		c.OnInternalError = func(op string, err error) { logged = append(logged, op+": "+err.Error()) }
	})
	_, err := svc.Submit(context.Background(), Principal{ID: "alice"}, "key-00000001", []byte(requestWithoutInputs()))
	ae := wantError(t, err, KindInternal, "INTERNAL_ERROR")
	if strings.Contains(ae.Detail, "disk") || strings.Contains(ae.Error(), "disk") {
		t.Fatalf("the internal failure leaked to the Client: %v", ae)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "disk is on fire") {
		t.Fatalf("internal failure was not logged: %v", logged)
	}
}

type failingStore struct{ *memory.Store }

func (failingStore) AdmitTask(context.Context, statestore.Admission, statestore.AdmitLimit) (statestore.AdmitResult, error) {
	return statestore.AdmitResult{}, errors.New("disk is on fire")
}

// --- status and ownership ---

func TestStatusIsVisibleOnlyToTheSubmitter(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	got, err := svc.Submit(ctx, Principal{ID: "alice"}, "key-00000001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	status, err := svc.Status(ctx, Principal{ID: "alice"}, got.TaskID.String())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Status != "queued" || status.TaskID != got.TaskID || !status.SubmittedAt.Equal(testNow) {
		t.Fatalf("unexpected status: %#v", status)
	}

	// Someone else's task, a task that does not exist, and a malformed id all
	// give the same answer, so ids cannot be probed.
	for name, id := range map[string]string{
		"other principal": got.TaskID.String(),
		"unknown":         "tsk_00000000000000000000000000000001",
		"malformed":       "not-a-task",
		"empty":           "",
	} {
		principal := Principal{ID: "mallory"}
		_, err := svc.Status(ctx, principal, id)
		ae := wantError(t, err, KindNotFound, "TASK_NOT_FOUND")
		if ae.TaskID != "" || strings.Contains(ae.Detail, id) && id != "" {
			t.Errorf("%s: the not-found answer discloses the id: %#v", name, ae)
		}
	}
}

func TestProjectNeverExposesInternalState(t *testing.T) {
	svc, _ := newService(t)
	now := testNow
	base, err := taskstate.NewTask("tsk_00000000000000000000000000000001", mustDigest(t), now)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*taskstate.TaskRecord)
		status string
		code   string
	}{
		{"admitted", func(*taskstate.TaskRecord) {}, "queued", ""},
		{"ready", func(r *taskstate.TaskRecord) { r.Phase = taskstate.TaskPhaseReady }, "queued", ""},
		{"active", func(r *taskstate.TaskRecord) { r.Phase = taskstate.TaskPhaseActive }, "running", ""},
		{"verifying", func(r *taskstate.TaskRecord) { r.Phase = taskstate.TaskPhaseVerifying }, "verifying", ""},
		{"reconciling", func(r *taskstate.TaskRecord) { r.Phase = taskstate.TaskPhaseReconciling }, "reconciling", ""},
		{"held", func(r *taskstate.TaskRecord) { r.Phase = taskstate.TaskPhaseHeld }, "attention-required", ""},
		{"completed", func(r *taskstate.TaskRecord) {
			r.Phase, r.TerminalOutcome = taskstate.TaskPhaseTerminal, taskstate.TerminalOutcomeCompleted
		}, "completed", "COMPLETED"},
		{"cancelled", func(r *taskstate.TaskRecord) {
			r.Phase, r.TerminalOutcome = taskstate.TaskPhaseTerminal, taskstate.TerminalOutcomeCancelled
		}, "cancelled", "CANCELLED"},
		{"failed", func(r *taskstate.TaskRecord) {
			r.Phase, r.TerminalOutcome = taskstate.TaskPhaseTerminal, taskstate.TerminalOutcomeFailed
		}, "failed", "FAILED"},
		{"exhausted", func(r *taskstate.TaskRecord) {
			r.Phase, r.TerminalOutcome = taskstate.TaskPhaseTerminal, taskstate.TerminalOutcomeExhausted
		}, "failed", "EXHAUSTED"},
		{"abandoned", func(r *taskstate.TaskRecord) {
			r.Phase, r.TerminalOutcome = taskstate.TaskPhaseTerminal, taskstate.TerminalOutcomeAbandoned
		}, "failed", "ABANDONED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := base
			tc.mutate(&record)
			got := svc.project(record)
			if got.Status != tc.status || got.TerminalCode != tc.code {
				t.Fatalf("project() = %#v, want status %s code %s", got, tc.status, tc.code)
			}
		})
	}

	record := base
	record.Phase = taskstate.TaskPhaseActive
	record.CancellationRequested = true
	if got := svc.project(record); got.PublicMessage == "" {
		t.Error("a pending cancellation is not visible to the Client")
	}
}

// Every internal TerminalOutcome has a public code. A new internal value must
// be added to the mapping on purpose: the public contract does not follow the
// internal enum's spelling (#281).
func TestEveryTerminalOutcomeHasAPublicCode(t *testing.T) {
	cases := map[taskstate.TerminalOutcome]string{
		taskstate.TerminalOutcomeCompleted: TerminalCompleted,
		taskstate.TerminalOutcomeCancelled: TerminalCancelled,
		taskstate.TerminalOutcomeFailed:    TerminalFailed,
		taskstate.TerminalOutcomeExhausted: TerminalExhausted,
		taskstate.TerminalOutcomeAbandoned: TerminalAbandoned,
	}
	for outcome, want := range cases {
		got, ok := publicTerminalCode(outcome)
		if !ok || got != want {
			t.Errorf("publicTerminalCode(%q) = %q, %v, want %q", outcome, got, ok, want)
		}
	}
	// The empty outcome is not terminal and has no public code.
	if _, ok := publicTerminalCode(taskstate.TerminalOutcomeNone); ok {
		t.Error("the empty outcome was given a public code")
	}
}

// An outcome this version cannot name must not leak its spelling, and it must
// not claim completion or cancellation: the Client sees FAILED, and the
// internal error is reported.
func TestUnknownTerminalOutcomeFallsBackToFailed(t *testing.T) {
	var operations []string
	svc, _ := newService(t, func(c *Config) {
		c.OnInternalError = func(operation string, err error) {
			operations = append(operations, operation)
		}
	})
	record, err := taskstate.NewTask("tsk_00000000000000000000000000000001", mustDigest(t), testNow)
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	record.Phase = taskstate.TaskPhaseTerminal
	record.TerminalOutcome = taskstate.TerminalOutcome("SOMETHING_NEW")

	got := svc.project(record)
	if got.Status != "failed" || got.TerminalCode != TerminalFailed {
		t.Fatalf("project() = %#v, want failed/%s", got, TerminalFailed)
	}
	if len(operations) != 1 {
		t.Fatalf("internal errors = %v, want one report of the unmapped outcome", operations)
	}
}

// --- cancel ---

func TestCancelEndsATaskThatNeverStarted(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	alice := Principal{ID: "alice"}
	got, err := svc.Submit(ctx, alice, "key-00000001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	if _, err := svc.Cancel(ctx, alice, got.TaskID.String()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	status, err := svc.Status(ctx, alice, got.TaskID.String())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Status != "cancelled" || status.TerminalCode != "CANCELLED" {
		t.Fatalf("status after cancel = %#v", status)
	}
	record, _ := store.LoadTask(ctx, got.TaskID)
	if !record.CancellationRequested || record.Revision != 2 {
		t.Fatalf("record = %#v", record)
	}

	// Repeating the cancellation is not an error and changes nothing.
	if _, err := svc.Cancel(ctx, alice, got.TaskID.String()); err != nil {
		t.Fatalf("repeated Cancel() error = %v", err)
	}
	again, _ := store.LoadTask(ctx, got.TaskID)
	if again.Revision != record.Revision {
		t.Fatalf("a repeated cancel changed the task: revision %d -> %d", record.Revision, again.Revision)
	}
}

func TestCancelOnlyRecordsIntentForRunningWork(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	alice := Principal{ID: "alice"}
	got, err := svc.Submit(ctx, alice, "key-00000001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	// Move the task to active, as a scheduler would.
	record, _ := store.LoadTask(ctx, got.TaskID)
	active := record
	active.Phase = taskstate.TaskPhaseActive
	active.Revision++
	if err := store.CompareAndSwapTask(ctx, got.TaskID, record.Revision, active); err != nil {
		t.Fatalf("CompareAndSwapTask() error = %v", err)
	}

	if _, err := svc.Cancel(ctx, alice, got.TaskID.String()); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	after, _ := store.LoadTask(ctx, got.TaskID)
	if after.Phase != taskstate.TaskPhaseActive || !after.CancellationRequested {
		t.Fatalf("a running task was ended by a cancel request instead of recording intent: %#v", after)
	}
	status, _ := svc.Status(ctx, alice, got.TaskID.String())
	if status.Status != "running" || status.PublicMessage == "" {
		t.Fatalf("status = %#v", status)
	}
}

func TestCancelIsRefusedForFinishedAndForeignTasks(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()
	alice := Principal{ID: "alice"}
	got, err := svc.Submit(ctx, alice, "key-00000001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	_, err = svc.Cancel(ctx, Principal{ID: "mallory"}, got.TaskID.String())
	wantError(t, err, KindNotFound, "TASK_NOT_FOUND")
	record, _ := store.LoadTask(ctx, got.TaskID)
	if record.CancellationRequested {
		t.Fatal("a foreign principal cancelled someone else's task")
	}

	// Completion needs an AcceptedResult, so a test that only needs a finished
	// Task ends it as failed.
	done := record
	done.Phase, done.TerminalOutcome = taskstate.TaskPhaseTerminal, taskstate.TerminalOutcomeFailed
	done.Revision++
	if err := store.CompareAndSwapTask(ctx, got.TaskID, record.Revision, done); err != nil {
		t.Fatalf("CompareAndSwapTask() error = %v", err)
	}
	_, err = svc.Cancel(ctx, alice, got.TaskID.String())
	wantError(t, err, KindConflict, "TASK_ALREADY_FINISHED")
}

func TestCancelAndSubmitRespectCancelledContexts(t *testing.T) {
	svc, _ := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Submit(ctx, Principal{ID: "alice"}, "key-00000001", []byte(requestWithoutInputs())); !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit() error = %v, want context.Canceled", err)
	}
	if _, err := svc.Status(ctx, Principal{ID: "alice"}, "tsk_00000000000000000000000000000001"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Status() error = %v, want context.Canceled", err)
	}
}

func mustDigest(t testing.TB) contract.Digest {
	t.Helper()
	return contract.SHA256([]byte("task ir"))
}

// The wall clock can step backwards between the submission and the
// cancellation (an NTP correction, a VM clock sync, a restart). The record
// must keep a non-decreasing update time, or the cancellation is rejected as
// an invalid record and surfaces as an internal error (issue #219).
func TestCancelSurvivesTheClockSteppingBackwards(t *testing.T) {
	now := testNow
	svc, store := newService(t, func(c *Config) { c.Now = func() time.Time { return now } })
	ctx := context.Background()
	alice := Principal{ID: "alice"}
	got, err := svc.Submit(ctx, alice, "key-00000001", []byte(requestWithoutInputs()))
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}

	now = testNow.Add(-8 * time.Second)
	if _, err := svc.Cancel(ctx, alice, got.TaskID.String()); err != nil {
		t.Fatalf("Cancel() with the clock behind the creation time error = %v", err)
	}
	record, err := store.LoadTask(ctx, got.TaskID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if record.UpdatedAt.Before(record.CreatedAt) {
		t.Fatalf("updated %v precedes created %v", record.UpdatedAt, record.CreatedAt)
	}
}
