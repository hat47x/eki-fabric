package contribution

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAssuranceLevelsAreAClosedSet(t *testing.T) {
	want := []Assurance{"self-reported", "control-observed", "cross-validated", "attested-metering"}
	if got := AssuranceLevels(); !slices.Equal(got, want) {
		t.Fatalf("AssuranceLevels() = %v, want %v", got, want)
	}
	for _, level := range want {
		if !level.Valid() {
			t.Errorf("%q should be valid", level)
		}
	}
	for _, bad := range []Assurance{"", "gold", "Self-Reported", "self_reported", "attested", "verified"} {
		if bad.Valid() {
			t.Errorf("%q should not be a valid assurance level", bad)
		}
	}
}

func TestAssuranceIsNotAnInputToTheConstructor(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(RecordInput{}), reflect.TypeOf(Observation{}), reflect.TypeOf(Observer{})} {
		for i := 0; i < typ.NumField(); i++ {
			if strings.Contains(strings.ToLower(typ.Field(i).Name), "assurance") {
				t.Errorf("%s has a caller-supplied assurance field %q", typ.Name(), typ.Field(i).Name)
			}
		}
	}
}

// TestAssuranceDerivationReachesEveryLevelOnlyThroughItsObservers enumerates the
// closed level set and checks which observer configurations produce each level.
// A single worker observation is the only configuration that yields self-reported.
// The other single observers each yield their own level, and only two or more
// non-worker principals yield cross-validated.
func TestAssuranceDerivationReachesEveryLevelOnlyThroughItsObservers(t *testing.T) {
	worker := Observer{Kind: ObserverWorker, ID: "worker-1"}
	control := Observer{Kind: ObserverControl, ID: "control-1"}
	control2 := Observer{Kind: ObserverControl, ID: "control-2"}
	meter := Observer{Kind: ObserverAttestedMeter, ID: "meter-1"}

	configurations := []struct {
		name      string
		observers []Observer
		want      Assurance
	}{
		{"single worker", []Observer{worker}, AssuranceSelfReported},
		{"single control observer", []Observer{control}, AssuranceControlObserved},
		{"single attested meter", []Observer{meter}, AssuranceAttestedMetering},
		{"two independent control observers", []Observer{control, control2}, AssuranceCrossValidated},
	}

	producedBy := map[Assurance][]string{}
	for _, c := range configurations {
		in := testInput(t)
		in.Observations = nil
		for _, o := range c.observers {
			in.Observations = append(in.Observations, Observation{Observer: o, Quantity: Quantity{Unit: UnitMillisecond, Value: 120000}})
		}
		rec := mustRecord(t, in)
		if rec.Assurance() != c.want {
			t.Errorf("%s: Assurance() = %q, want %q", c.name, rec.Assurance(), c.want)
		}
		producedBy[rec.Assurance()] = append(producedBy[rec.Assurance()], c.name)
	}

	for _, level := range AssuranceLevels() {
		if len(producedBy[level]) == 0 {
			t.Errorf("no configuration produces %q", level)
		}
	}
	if got := producedBy[AssuranceSelfReported]; !slices.Equal(got, []string{"single worker"}) {
		t.Errorf("self-reported is produced by %v, want only the single worker observation", got)
	}
}

func TestSelfReportedQuantityCannotBeCombinedWithAnyOtherObserver(t *testing.T) {
	worker := Observation{Observer: Observer{Kind: ObserverWorker, ID: "worker-1"}, Quantity: Quantity{Unit: UnitMillisecond, Value: 120000}}
	control := controlObservation("control-1", 120000)
	controlOther := controlObservation("control-1", 99999)
	meter := Observation{Observer: Observer{Kind: ObserverAttestedMeter, ID: "meter-1"}, Quantity: Quantity{Unit: UnitMillisecond, Value: 120000}}
	control2 := controlObservation("control-2", 120000)

	tests := []struct {
		name          string
		observations  []Observation
		wantErr       error
		want          Assurance
		wantObservers int
	}{
		{"worker alone", []Observation{worker}, nil, AssuranceSelfReported, 1},
		{"worker repeated under its own identity", []Observation{worker, worker}, nil, AssuranceSelfReported, 1},
		{"worker with control observer", []Observation{worker, control}, ErrWorkerNotIndependent, "", 0},
		{"worker with attested meter", []Observation{worker, meter}, ErrWorkerNotIndependent, "", 0},
		{"worker with two control observers", []Observation{worker, control, control2}, ErrWorkerNotIndependent, "", 0},
		{"worker disagreeing with control observer", []Observation{worker, controlOther}, ErrWorkerNotIndependent, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := testInput(t)
			in.Observations = tt.observations
			rec, err := NewRecord(in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewRecord() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if rec.Assurance() != tt.want {
				t.Errorf("Assurance() = %q, want %q", rec.Assurance(), tt.want)
			}
			if got := len(rec.Observers()); got != tt.wantObservers {
				t.Errorf("len(Observers()) = %d, want %d", got, tt.wantObservers)
			}
		})
	}
}

func TestRepeatedObservationsByOnePrincipalCountAsOneObserver(t *testing.T) {
	in := testInput(t)
	in.Observations = []Observation{
		controlObservation("control-1", 120000),
		controlObservation("control-1", 120000),
	}
	rec := mustRecord(t, in)
	if rec.Assurance() != AssuranceControlObserved {
		t.Errorf("Assurance() = %q, want %q; one principal must not cross-validate itself", rec.Assurance(), AssuranceControlObserved)
	}
	if got := len(rec.Observers()); got != 1 {
		t.Errorf("len(Observers()) = %d, want 1", got)
	}
}

func TestCrossValidationNeedsTwoIndependentAgreeingPrincipals(t *testing.T) {
	control1 := controlObservation("control-1", 120000)
	control2 := controlObservation("control-2", 120000)
	meter := Observation{Observer: Observer{Kind: ObserverAttestedMeter, ID: "meter-1"}, Quantity: Quantity{Unit: UnitMillisecond, Value: 120000}}

	tests := []struct {
		name          string
		observations  []Observation
		want          Assurance
		wantObservers int
	}{
		{"two control observers agree", []Observation{control1, control2}, AssuranceCrossValidated, 2},
		{"control observer and attested meter agree", []Observation{control1, meter}, AssuranceCrossValidated, 2},
		{"one principal repeated beside a second principal", []Observation{control1, control1, control2}, AssuranceCrossValidated, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := testInput(t)
			in.Observations = tt.observations
			rec := mustRecord(t, in)
			if rec.Assurance() != tt.want {
				t.Errorf("Assurance() = %q, want %q", rec.Assurance(), tt.want)
			}
			if got := len(rec.Observers()); got != tt.wantObservers {
				t.Errorf("len(Observers()) = %d, want %d", got, tt.wantObservers)
			}
		})
	}
}

func TestAssuranceFollowsObserverKindForSingleObservers(t *testing.T) {
	tests := []struct {
		kind ObserverKind
		want Assurance
	}{
		{ObserverWorker, AssuranceSelfReported},
		{ObserverControl, AssuranceControlObserved},
		{ObserverAttestedMeter, AssuranceAttestedMetering},
	}
	for _, tt := range tests {
		got, err := assuranceFor([]Observer{{Kind: tt.kind, ID: "principal"}})
		if err != nil || got != tt.want {
			t.Errorf("assuranceFor(%s) = %q, %v; want %q", tt.kind, got, err, tt.want)
		}
	}
	if _, err := assuranceFor([]Observer{{Kind: "human-reviewer", ID: "principal"}}); !errors.Is(err, ErrUnknownObserver) {
		t.Errorf("assuranceFor(unknown kind) error = %v, want ErrUnknownObserver", err)
	}
	if _, err := assuranceFor(nil); !errors.Is(err, ErrMissingField) {
		t.Errorf("assuranceFor(nil) error = %v, want ErrMissingField", err)
	}
}
