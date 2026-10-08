package contribution

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestDigestIsDeterministicForIdenticalInput(t *testing.T) {
	first := mustDigest(t, mustRecord(t, testInput(t)))
	second := mustDigest(t, mustRecord(t, testInput(t)))
	if first != second {
		t.Fatalf("identical input produced different digests: %s and %s", first, second)
	}
	if !strings.HasPrefix(first.String(), "sha256:") {
		t.Fatalf("digest = %q, want sha256 prefix", first.String())
	}
}

func TestDigestChangesWhenAnyFieldChanges(t *testing.T) {
	base := mustDigest(t, mustRecord(t, testInput(t)))

	mutations := []struct {
		name   string
		mutate func(*RecordInput)
	}{
		{"provider identity", func(in *RecordInput) { in.ProviderID = "provider-beta" }},
		{"resource class", func(in *RecordInput) { in.ResourceClass = "cpu-class-b" }},
		{"metric name", func(in *RecordInput) { in.Metric = MetricLeaseWallTime }},
		{"quantity value", func(in *RecordInput) { in.Observations[0].Quantity.Value++ }},
		{"observer identity", func(in *RecordInput) { in.Observations[0].Observer.ID = "control-plane-2" }},
		{"observer kind", func(in *RecordInput) { in.Observations[0].Observer.Kind = ObserverAttestedMeter }},
		{"task digest", func(in *RecordInput) {
			in.Work = testRefFor(t, "task:beta", "result:alpha")
		}},
		{"result digest", func(in *RecordInput) {
			in.Work = testRefFor(t, "task:alpha", "result:beta")
		}},
		{"provenance", func(in *RecordInput) { in.Provenance = "different meter description" }},
		{"window start", func(in *RecordInput) { in.WindowStart = in.WindowStart.Add(time.Nanosecond) }},
		{"window end", func(in *RecordInput) { in.WindowEnd = in.WindowEnd.Add(time.Second) }},
	}

	seen := map[contract.Digest]string{base: "base"}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			in := testInput(t)
			m.mutate(&in)
			got := mustDigest(t, mustRecord(t, in))
			if got == base {
				t.Fatalf("changing %s did not change the digest", m.name)
			}
			if other, dup := seen[got]; dup {
				t.Fatalf("changing %s produced the same digest as %s", m.name, other)
			}
			seen[got] = m.name
		})
	}
}

// TestLengthPrefixPreventsFieldBoundaryCollisions checks that moving a character
// from one string field to the next changes the digest.
func TestLengthPrefixPreventsFieldBoundaryCollisions(t *testing.T) {
	left := testInput(t)
	left.ProviderID = "ab"
	left.ResourceClass = "c"

	right := testInput(t)
	right.ProviderID = "a"
	right.ResourceClass = "bc"

	if mustDigest(t, mustRecord(t, left)) == mustDigest(t, mustRecord(t, right)) {
		t.Fatalf("shifting a boundary between fields did not change the digest")
	}
}

func TestObserverOrderDoesNotChangeTheRecord(t *testing.T) {
	control1 := controlObservation("control-1", 120000)
	control2 := controlObservation("control-2", 120000)

	forward := testInput(t)
	forward.Observations = []Observation{control1, control2}
	reverse := testInput(t)
	reverse.Observations = []Observation{control2, control1}

	forwardRec := mustRecord(t, forward)
	reverseRec := mustRecord(t, reverse)
	if mustDigest(t, forwardRec) != mustDigest(t, reverseRec) {
		t.Fatalf("observation order changed the digest")
	}
	if got := forwardRec.Observers(); got[0].ID != "control-1" || got[1].ID != "control-2" {
		t.Fatalf("observers are not in canonical order: %+v", got)
	}
	if !slices.Equal(forwardRec.Observers(), reverseRec.Observers()) {
		t.Fatalf("observer lists differ by input order")
	}
}

func TestTamperedRecordFailsClosed(t *testing.T) {
	base := mustRecord(t, testInput(t))
	workerAndControl := []Observer{
		{Kind: ObserverControl, ID: "control-1"},
		{Kind: ObserverWorker, ID: "worker-1"},
	}

	tests := []struct {
		name    string
		mutate  func(*Record)
		wantErr error
	}{
		{"zero schema version", func(r *Record) { r.schemaVersion = 0 }, ErrUnsupportedSchema},
		{"future schema version", func(r *Record) { r.schemaVersion = SchemaVersion + 1 }, ErrUnsupportedSchema},
		{"unknown assurance level", func(r *Record) { r.assurance = "gold" }, ErrUnknownAssurance},
		{"assurance above what the observer supports", func(r *Record) { r.assurance = AssuranceAttestedMetering }, ErrInvalidField},
		{"worker folded into a multi-observer record", func(r *Record) {
			r.observers = workerAndControl
			r.assurance = AssuranceCrossValidated
		}, ErrWorkerNotIndependent},
		{"duplicate observer identity", func(r *Record) {
			r.observers = []Observer{{Kind: ObserverControl, ID: "control-1"}, {Kind: ObserverControl, ID: "control-1"}}
			r.assurance = AssuranceCrossValidated
		}, ErrInvalidField},
		{"observers out of canonical order", func(r *Record) {
			r.observers = []Observer{{Kind: ObserverControl, ID: "control-2"}, {Kind: ObserverControl, ID: "control-1"}}
			r.assurance = AssuranceCrossValidated
		}, ErrInvalidField},
		{"unknown metric", func(r *Record) { r.metric = "gpu-time" }, ErrUnknownMetric},
		{"quantity in an undeclared unit", func(r *Record) { r.quantity.Unit = UnitByte }, ErrUnitMismatch},
		{"zero quantity", func(r *Record) { r.quantity.Value = 0 }, ErrZeroQuantity},
		{"zero task digest", func(r *Record) { r.taskDigest = contract.Digest{} }, ErrMissingField},
		{"reversed window", func(r *Record) { r.windowEnd = r.windowStart }, ErrInvalidWindow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := base
			tt.mutate(&rec)
			if _, err := rec.Digest(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Digest() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
