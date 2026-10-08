package contribution

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var testWindowStart = time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)

func testRef(t *testing.T) AcceptedResultRef {
	t.Helper()
	return testRefFor(t, "task:alpha", "result:alpha")
}

func testRefFor(t *testing.T, taskLabel, resultLabel string) AcceptedResultRef {
	t.Helper()
	ref, err := NewAcceptedResultRef(
		contract.SHA256([]byte(taskLabel)),
		contract.SHA256([]byte(resultLabel)),
		ResultStatusAccepted,
	)
	if err != nil {
		t.Fatalf("NewAcceptedResultRef() error = %v", err)
	}
	return ref
}

func controlObservation(id string, value int64) Observation {
	return Observation{
		Observer: Observer{Kind: ObserverControl, ID: id},
		Quantity: Quantity{Unit: UnitMillisecond, Value: value},
	}
}

func testInput(t *testing.T) RecordInput {
	t.Helper()
	return RecordInput{
		ProviderID:    "provider-alpha",
		ResourceClass: "gpu-class-a",
		Metric:        MetricGPUActiveTime,
		Observations:  []Observation{controlObservation("control-plane-1", 120000)},
		Work:          testRef(t),
		WindowStart:   testWindowStart,
		WindowEnd:     testWindowStart.Add(time.Hour),
		Provenance:    "control-plane lease meter sample",
	}
}

func mustRecord(t *testing.T, in RecordInput) Record {
	t.Helper()
	rec, err := NewRecord(in)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	return rec
}

func mustDigest(t *testing.T, rec Record) contract.Digest {
	t.Helper()
	d, err := rec.Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	return d
}

func TestNewRecordCarriesEveryRequiredField(t *testing.T) {
	in := testInput(t)
	rec := mustRecord(t, in)

	if got := rec.SchemaVersion(); got != SchemaVersion {
		t.Errorf("SchemaVersion() = %d, want %d", got, SchemaVersion)
	}
	if got := rec.ProviderID(); got != "provider-alpha" {
		t.Errorf("ProviderID() = %q", got)
	}
	if got := rec.ResourceClass(); got != "gpu-class-a" {
		t.Errorf("ResourceClass() = %q", got)
	}
	if got := rec.Metric(); got != MetricGPUActiveTime {
		t.Errorf("Metric() = %q", got)
	}
	if got := rec.Quantity(); got != (Quantity{Unit: UnitMillisecond, Value: 120000}) {
		t.Errorf("Quantity() = %+v", got)
	}
	observers := rec.Observers()
	if len(observers) != 1 || observers[0] != (Observer{Kind: ObserverControl, ID: "control-plane-1"}) {
		t.Errorf("Observers() = %+v", observers)
	}
	if got := rec.Assurance(); got != AssuranceControlObserved {
		t.Errorf("Assurance() = %q", got)
	}
	if got := rec.TaskDigest(); got != in.Work.taskDigest {
		t.Errorf("TaskDigest() does not match the work reference")
	}
	if got := rec.ResultDigest(); got != in.Work.resultDigest {
		t.Errorf("ResultDigest() does not match the work reference")
	}
	if got := rec.Provenance(); got != in.Provenance {
		t.Errorf("Provenance() = %q", got)
	}
	if !rec.WindowStart().Equal(testWindowStart) || !rec.WindowEnd().Equal(testWindowStart.Add(time.Hour)) {
		t.Errorf("window = [%v, %v]", rec.WindowStart(), rec.WindowEnd())
	}
}

func TestObserversReturnsACopy(t *testing.T) {
	rec := mustRecord(t, testInput(t))
	observers := rec.Observers()
	observers[0].ID = "tampered"
	if got := rec.Observers()[0].ID; got != "control-plane-1" {
		t.Fatalf("Observers() exposed internal state: ID = %q", got)
	}
}

func TestAcceptedResultRefOnlyAcceptsAcceptedStatus(t *testing.T) {
	task := contract.SHA256([]byte("task"))
	result := contract.SHA256([]byte("result"))
	tests := []struct {
		name    string
		status  ResultStatus
		task    contract.Digest
		result  contract.Digest
		wantErr error
	}{
		{"accepted", ResultStatusAccepted, task, result, nil},
		{"rejected result", ResultStatusRejected, task, result, ErrResultNotAccepted},
		{"unverified result", ResultStatusUnverified, task, result, ErrResultNotAccepted},
		{"empty status", "", task, result, ErrUnknownResultStatus},
		{"invented status", "accepted-ish", task, result, ErrUnknownResultStatus},
		{"accepted without task digest", ResultStatusAccepted, contract.Digest{}, result, ErrMissingField},
		{"accepted without result digest", ResultStatusAccepted, task, contract.Digest{}, ErrMissingField},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAcceptedResultRef(tt.task, tt.result, tt.status)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseAcceptedResultRefRejectsMalformedDigestText(t *testing.T) {
	goodTask := contract.SHA256([]byte("task:alpha")).String()
	goodResult := contract.SHA256([]byte("result:alpha")).String()
	tests := []struct {
		name    string
		task    string
		result  string
		status  string
		wantErr error
	}{
		{"well-formed and accepted", goodTask, goodResult, "accepted", nil},
		{"empty task digest", "", goodResult, "accepted", ErrMissingField},
		{"digest without algorithm prefix", strings.TrimPrefix(goodTask, "sha256:"), goodResult, "accepted", ErrInvalidField},
		{"other algorithm", "sha1:" + strings.Repeat("a", 40), goodResult, "accepted", ErrInvalidField},
		{"short hex", "sha256:abcd", goodResult, "accepted", ErrInvalidField},
		{"non-hex characters", "sha256:" + strings.Repeat("z", 64), goodResult, "accepted", ErrInvalidField},
		{"malformed result digest", goodTask, "sha256:xyz", "accepted", ErrInvalidField},
		{"well-formed but rejected result", goodTask, goodResult, "rejected", ErrResultNotAccepted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := ParseAcceptedResultRef(tt.task, tt.result, tt.status)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseAcceptedResultRef() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			in := testInput(t)
			in.Work = ref
			rec := mustRecord(t, in)
			if rec.TaskDigest().String() != tt.task {
				t.Errorf("TaskDigest() = %s, want %s", rec.TaskDigest(), tt.task)
			}
		})
	}
}

func TestRecordCannotBeBuiltWithoutAcceptedWork(t *testing.T) {
	in := testInput(t)
	in.Work = AcceptedResultRef{}
	if _, err := NewRecord(in); !errors.Is(err, ErrMissingField) {
		t.Fatalf("NewRecord() with zero work reference: error = %v, want ErrMissingField", err)
	}
}

func TestBoundedFieldsAcceptTheLimitAndRejectOverflow(t *testing.T) {
	maxObservers := make([]Observation, 0, MaxObservations)
	for i := range MaxObservations {
		maxObservers = append(maxObservers, controlObservation("control-"+string(rune('a'+i)), 5000))
	}
	tooManyObservers := make([]Observation, 0, MaxObservations+1)
	for i := range MaxObservations + 1 {
		tooManyObservers = append(tooManyObservers, controlObservation("control-"+string(rune('a'+i)), 5000))
	}

	tests := []struct {
		name    string
		mutate  func(*RecordInput)
		wantErr error
	}{
		{
			name:   "provider identity at limit",
			mutate: func(in *RecordInput) { in.ProviderID = strings.Repeat("p", MaxIdentifierLength) },
		},
		{
			name:    "provider identity over limit",
			mutate:  func(in *RecordInput) { in.ProviderID = strings.Repeat("p", MaxIdentifierLength+1) },
			wantErr: ErrFieldTooLong,
		},
		{
			name:    "resource class over limit",
			mutate:  func(in *RecordInput) { in.ResourceClass = strings.Repeat("c", MaxIdentifierLength+1) },
			wantErr: ErrFieldTooLong,
		},
		{
			name:   "provenance at limit",
			mutate: func(in *RecordInput) { in.Provenance = strings.Repeat("v", MaxProvenanceLength) },
		},
		{
			name:    "provenance over limit",
			mutate:  func(in *RecordInput) { in.Provenance = strings.Repeat("v", MaxProvenanceLength+1) },
			wantErr: ErrFieldTooLong,
		},
		{
			name:   "quantity at maximum",
			mutate: func(in *RecordInput) { in.Observations[0].Quantity.Value = MaxQuantity },
		},
		{
			name:    "quantity over maximum",
			mutate:  func(in *RecordInput) { in.Observations[0].Quantity.Value = MaxQuantity + 1 },
			wantErr: ErrQuantityTooLarge,
		},
		{
			name:   "window at maximum duration",
			mutate: func(in *RecordInput) { in.WindowEnd = in.WindowStart.Add(MaxWindowDuration) },
		},
		{
			name:    "window over maximum duration",
			mutate:  func(in *RecordInput) { in.WindowEnd = in.WindowStart.Add(MaxWindowDuration + time.Nanosecond) },
			wantErr: ErrWindowTooLong,
		},
		{
			name:   "observations at count limit",
			mutate: func(in *RecordInput) { in.Observations = maxObservers },
		},
		{
			name:    "observations over count limit",
			mutate:  func(in *RecordInput) { in.Observations = tooManyObservers },
			wantErr: ErrFieldTooLong,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := testInput(t)
			tt.mutate(&in)
			rec, err := NewRecord(in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewRecord() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil && rec.ProviderID() != "" {
				t.Fatalf("rejected input produced a non-zero record")
			}
		})
	}
}

func TestNewRecordFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*RecordInput)
		wantErr error
	}{
		{"empty provider identity", func(in *RecordInput) { in.ProviderID = "" }, ErrMissingField},
		{"blank provider identity", func(in *RecordInput) { in.ProviderID = "   " }, ErrMissingField},
		{"provider identity with padding", func(in *RecordInput) { in.ProviderID = " provider-alpha" }, ErrInvalidField},
		{"provider identity with control character", func(in *RecordInput) { in.ProviderID = "provider\x00alpha" }, ErrInvalidField},
		{"provider identity not UTF-8", func(in *RecordInput) { in.ProviderID = "provider\xff" }, ErrInvalidField},
		{"empty resource class", func(in *RecordInput) { in.ResourceClass = "" }, ErrMissingField},
		{"empty provenance", func(in *RecordInput) { in.Provenance = "" }, ErrMissingField},
		{"multi-line provenance", func(in *RecordInput) { in.Provenance = "line one\nline two" }, ErrInvalidField},
		{"empty metric", func(in *RecordInput) { in.Metric = "" }, ErrMissingField},
		{"unknown metric", func(in *RecordInput) { in.Metric = "gpu-time" }, ErrUnknownMetric},
		{"zero work reference", func(in *RecordInput) { in.Work = AcceptedResultRef{} }, ErrMissingField},
		{"zero window start", func(in *RecordInput) { in.WindowStart = time.Time{} }, ErrMissingField},
		{"zero window end", func(in *RecordInput) { in.WindowEnd = time.Time{} }, ErrMissingField},
		{"window end equal to start", func(in *RecordInput) { in.WindowEnd = in.WindowStart }, ErrInvalidWindow},
		{"window reversed", func(in *RecordInput) { in.WindowEnd = in.WindowStart.Add(-time.Second) }, ErrInvalidWindow},
		{"no observations", func(in *RecordInput) { in.Observations = nil }, ErrMissingField},
		{"observer with empty kind", func(in *RecordInput) { in.Observations[0].Observer.Kind = "" }, ErrMissingField},
		{"observer with unknown kind", func(in *RecordInput) { in.Observations[0].Observer.Kind = "human-reviewer" }, ErrUnknownObserver},
		{"observer with empty identity", func(in *RecordInput) { in.Observations[0].Observer.ID = "" }, ErrMissingField},
		{"empty unit", func(in *RecordInput) { in.Observations[0].Quantity.Unit = "" }, ErrMissingField},
		{"unknown unit", func(in *RecordInput) { in.Observations[0].Quantity.Unit = "s" }, ErrUnknownUnit},
		{"unit not declared by metric", func(in *RecordInput) { in.Observations[0].Quantity.Unit = UnitByte }, ErrUnitMismatch},
		{"negative quantity", func(in *RecordInput) { in.Observations[0].Quantity.Value = -1 }, ErrNegativeQuantity},
		{"zero quantity", func(in *RecordInput) { in.Observations[0].Quantity.Value = 0 }, ErrZeroQuantity},
		{
			name: "same observer reporting conflicting kinds",
			mutate: func(in *RecordInput) {
				in.Observations = append(in.Observations, Observation{
					Observer: Observer{Kind: ObserverWorker, ID: "control-plane-1"},
					Quantity: Quantity{Unit: UnitMillisecond, Value: 120000},
				})
			},
			wantErr: ErrObserverConflict,
		},
		{
			name: "same observer reporting different values",
			mutate: func(in *RecordInput) {
				in.Observations = append(in.Observations, controlObservation("control-plane-1", 120001))
			},
			wantErr: ErrObservationMismatch,
		},
		{
			name: "two observers disagree",
			mutate: func(in *RecordInput) {
				in.Observations = append(in.Observations, controlObservation("control-plane-2", 120001))
			},
			wantErr: ErrObservationMismatch,
		},
		{
			name: "worker mixed with control observer",
			mutate: func(in *RecordInput) {
				in.Observations = append(in.Observations, Observation{
					Observer: Observer{Kind: ObserverWorker, ID: "worker-1"},
					Quantity: Quantity{Unit: UnitMillisecond, Value: 120000},
				})
			},
			wantErr: ErrWorkerNotIndependent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := testInput(t)
			tt.mutate(&in)
			rec, err := NewRecord(in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewRecord() error = %v, want %v", err, tt.wantErr)
			}
			if rec.ProviderID() != "" || rec.Quantity() != (Quantity{}) {
				t.Fatalf("rejected input produced a non-zero record")
			}
		})
	}
}

func TestZeroRecordFailsClosed(t *testing.T) {
	var zero Record
	if _, err := zero.Digest(); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("zero Record Digest() error = %v, want ErrUnsupportedSchema", err)
	}
	if _, err := NewAuditView(zero, false); err == nil {
		t.Fatalf("zero Record produced an audit view")
	}
}

func TestEmptyInputIsRejectedAtTheFirstRequiredField(t *testing.T) {
	if _, err := NewRecord(RecordInput{}); !errors.Is(err, ErrMissingField) {
		t.Fatalf("NewRecord(RecordInput{}) error = %v, want ErrMissingField", err)
	}
}
