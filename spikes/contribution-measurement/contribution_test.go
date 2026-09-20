package contribution

import (
	"testing"
	"time"
)

type fx struct {
	now          time.Time
	worker       KeyPair
	control      KeyPair
	meter        KeyPair
	authorityKey KeyPair
	authority    *Authority
	ctx          WorkContext
}

func fixture(t *testing.T, min Assurance, requireAccepted bool) fx {
	t.Helper()
	worker, _ := NewKeyPair()
	control, _ := NewKeyPair()
	meter, _ := NewKeyPair()
	auth, _ := NewKeyPair()
	p := Policy{Revision: 5, MinAssurance: min, AllowedMetrics: map[string]bool{"lease-wall-time": true, "gpu-active-time": true}, AllowedUnits: map[string]bool{"millisecond": true}, RequireAcceptedResult: requireAccepted, RecordTTL: 24 * time.Hour}
	a, err := NewAuthority(auth, p, []MeasurementTrust{{ObserverID: "control-plane", Public: control.Public, MaxAssurance: AssuranceControlObserved}, {ObserverID: "attested-meter", Public: meter.Public, MaxAssurance: AssuranceAttested}, {ObserverID: "worker-self", Public: worker.Public, MaxAssurance: AssuranceSelfReported}})
	if err != nil {
		t.Fatal(err)
	}
	return fx{now: time.Unix(1_800_000_000, 0), worker: worker, control: control, meter: meter, authorityKey: auth, authority: a, ctx: WorkContext{ProviderID: "provider-a", WorkerID: "worker-1", TaskID: "task-1", AttemptID: "attempt-1", ResourceClass: "gpu.generic"}}
}

func measurement(f fx, observer string, a Assurance, metric string, value uint64) Measurement {
	return Measurement{Context: f.ctx, Metric: metric, Quantity: Quantity{Unit: "millisecond", Value: value}, Assurance: a, ObserverID: observer, ObservedAt: f.now.Add(-time.Second)}
}
func accepted(f fx) ResultEvidence {
	return ResultEvidence{TaskID: f.ctx.TaskID, AttemptID: f.ctx.AttemptID, Accepted: true, VerificationAuthority: "validator-a"}
}

func TestControlObservedMeasurementProducesSignedRecord(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, true)
	m := measurement(f, "control-plane", AssuranceControlObserved, "lease-wall-time", 120000)
	sm, _ := SignMeasurement(f.control, m)
	r, err := f.authority.Issue(f.now, sm, accepted(f))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecord(f.authorityKey.Public, f.now, r); err != nil {
		t.Fatal(err)
	}
	if r.Payload.MeasurementAssurance != AssuranceControlObserved || r.Payload.Quantity.Value != 120000 {
		t.Fatalf("record=%+v", r.Payload)
	}
}

func TestWorkerSelfReportCannotSatisfyControlObservedPolicy(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, true)
	m := measurement(f, "worker-self", AssuranceSelfReported, "lease-wall-time", 999999999)
	sm, _ := SignMeasurement(f.worker, m)
	if _, err := f.authority.Issue(f.now, sm, accepted(f)); err == nil {
		t.Fatal("self-report must not satisfy stronger policy")
	}
}

func TestWorkerCannotForgeControlObserver(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, true)
	m := measurement(f, "control-plane", AssuranceControlObserved, "lease-wall-time", 999999999)
	sm, _ := SignMeasurement(f.worker, m)
	if _, err := f.authority.Issue(f.now, sm, accepted(f)); err == nil {
		t.Fatal("worker key must not impersonate control observer")
	}
}

func TestControlObserverCannotClaimAttestedAssurance(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, true)
	m := measurement(f, "control-plane", AssuranceAttested, "gpu-active-time", 1000)
	sm, _ := SignMeasurement(f.control, m)
	if _, err := f.authority.Issue(f.now, sm, accepted(f)); err == nil {
		t.Fatal("observer assurance cannot be upgraded by payload")
	}
}

func TestAttestedMeterCanProduceStrongerRecord(t *testing.T) {
	f := fixture(t, AssuranceAttested, true)
	m := measurement(f, "attested-meter", AssuranceAttested, "gpu-active-time", 30000)
	sm, _ := SignMeasurement(f.meter, m)
	r, err := f.authority.Issue(f.now, sm, accepted(f))
	if err != nil {
		t.Fatal(err)
	}
	if r.Payload.MeasurementAssurance != AssuranceAttested {
		t.Fatalf("record=%+v", r.Payload)
	}
}

func TestAcceptedResultAloneDoesNotCreateMeasurement(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, true)
	m := measurement(f, "worker-self", AssuranceSelfReported, "gpu-active-time", 50000)
	sm, _ := SignMeasurement(f.worker, m)
	if _, err := f.authority.Issue(f.now, sm, accepted(f)); err == nil {
		t.Fatal("valid result does not prove claimed resource quantity")
	}
}

func TestPolicyCanRequireAcceptedResultSeparately(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, true)
	m := measurement(f, "control-plane", AssuranceControlObserved, "lease-wall-time", 1000)
	sm, _ := SignMeasurement(f.control, m)
	rejected := ResultEvidence{TaskID: f.ctx.TaskID, AttemptID: f.ctx.AttemptID, Accepted: false}
	if _, err := f.authority.Issue(f.now, sm, rejected); err == nil {
		t.Fatal("accepted-result policy must be independent from measurement assurance")
	}
}

func TestSameMeasurementIsIdempotentButConflictingDoubleCountFails(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, false)
	m := measurement(f, "control-plane", AssuranceControlObserved, "lease-wall-time", 1000)
	sm, _ := SignMeasurement(f.control, m)
	r1, err := f.authority.Issue(f.now, sm, ResultEvidence{TaskID: f.ctx.TaskID, AttemptID: f.ctx.AttemptID})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := f.authority.Issue(f.now, sm, ResultEvidence{TaskID: f.ctx.TaskID, AttemptID: f.ctx.AttemptID})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Payload.RecordID != r2.Payload.RecordID {
		t.Fatal("same evidence must not mint another record")
	}
	m.Quantity.Value = 2000
	sm2, _ := SignMeasurement(f.control, m)
	if _, err := f.authority.Issue(f.now, sm2, ResultEvidence{TaskID: f.ctx.TaskID, AttemptID: f.ctx.AttemptID}); err == nil {
		t.Fatal("conflicting measurement must not double count same attempt metric")
	}
}

func TestRecordKeepsResourceClassAndMeasurementBasisWithoutUniversalScore(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, false)
	m := measurement(f, "control-plane", AssuranceControlObserved, "lease-wall-time", 1000)
	sm, _ := SignMeasurement(f.control, m)
	r, err := f.authority.Issue(f.now, sm, ResultEvidence{TaskID: f.ctx.TaskID, AttemptID: f.ctx.AttemptID})
	if err != nil {
		t.Fatal(err)
	}
	if r.Payload.ResourceClass != "gpu.generic" || r.Payload.Metric != "lease-wall-time" || r.Payload.Quantity.Unit != "millisecond" {
		t.Fatalf("record=%+v", r.Payload)
	}
}

func TestResultEvidenceMustMatchMeasuredAttempt(t *testing.T) {
	f := fixture(t, AssuranceControlObserved, true)
	m := measurement(f, "control-plane", AssuranceControlObserved, "lease-wall-time", 1000)
	sm, _ := SignMeasurement(f.control, m)
	wrong := accepted(f)
	wrong.AttemptID = "attempt-other"
	if _, err := f.authority.Issue(f.now, sm, wrong); err == nil {
		t.Fatal("result evidence must bind to measured attempt")
	}
}
