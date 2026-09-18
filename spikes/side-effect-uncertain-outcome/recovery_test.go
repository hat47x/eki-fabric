package recovery

import (
	"testing"
	"time"
)

func TestPureComputeDisconnectIsRetryable(t *testing.T) {
	c := Contract{TaskID: "compute", EffectMode: EffectNone, MaxAttempts: 3}
	d, err := RecoverAfterDisconnect(c, Attempt{AttemptNo: 1})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateRetryable {
		t.Fatalf("decision=%+v", d)
	}
}

func TestNonIdempotentDisconnectBeforeDispatchIsRetryable(t *testing.T) {
	c := Contract{TaskID: "notify", EffectMode: EffectNonIdempotent, OperationID: "op-1", RequestDigest: "sha256:req", MaxAttempts: 3}
	d, err := RecoverAfterDisconnect(c, Attempt{AttemptNo: 1, EffectDispatched: false})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateRetryable {
		t.Fatalf("decision=%+v", d)
	}
}

func TestNonIdempotentDisconnectAfterDispatchBecomesUnknown(t *testing.T) {
	c := Contract{TaskID: "notify", EffectMode: EffectNonIdempotent, OperationID: "op-1", RequestDigest: "sha256:req", MaxAttempts: 3}
	d, err := RecoverAfterDisconnect(c, Attempt{AttemptNo: 1, EffectDispatched: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateUnknown {
		t.Fatalf("decision=%+v", d)
	}
}

func TestIdempotentDisconnectAfterDispatchCanRetrySameOperation(t *testing.T) {
	c := Contract{TaskID: "create", EffectMode: EffectIdempotent, OperationID: "op-stable", RequestDigest: "sha256:req", MaxAttempts: 3}
	d, err := RecoverAfterDisconnect(c, Attempt{AttemptNo: 1, EffectDispatched: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateRetryable || d.OperationID != c.OperationID {
		t.Fatalf("decision=%+v", d)
	}
	if err := RetryContract(c, c.OperationID, c.RequestDigest); err != nil {
		t.Fatal(err)
	}
}

func TestRetryCannotChangeOperationIDOrIntent(t *testing.T) {
	c := Contract{TaskID: "create", EffectMode: EffectIdempotent, OperationID: "op-stable", RequestDigest: "sha256:req", MaxAttempts: 3}
	if err := RetryContract(c, "op-new", c.RequestDigest); err == nil {
		t.Fatal("retry must not mint a new operation id")
	}
	if err := RetryContract(c, c.OperationID, "sha256:other"); err == nil {
		t.Fatal("same operation id must not be reused for different intent")
	}
}

func TestIdempotentServiceAppliesEffectOnlyOnce(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key, _ := NewKeyPair()
	svc := NewEffectService(key)

	first, err := svc.ApplyIdempotent(now, "op-1", "sha256:req")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ApplyIdempotent(now.Add(time.Second), "op-1", "sha256:req")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Applications() != 1 {
		t.Fatalf("applications=%d", svc.Applications())
	}
	if first.Receipt.EffectID != second.Receipt.EffectID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestIdempotentServiceRejectsSameKeyDifferentIntent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	key, _ := NewKeyPair()
	svc := NewEffectService(key)
	if _, err := svc.ApplyIdempotent(now, "op-1", "sha256:a"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyIdempotent(now, "op-1", "sha256:b"); err == nil {
		t.Fatal("same idempotency identity with different intent must fail")
	}
}

func TestWorkerCannotForgeAuthoritativeEffectReceipt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	effectAuthority, _ := NewKeyPair()
	attacker, _ := NewKeyPair()
	guard, _ := NewReceiptGuard(effectAuthority.Public)
	c := Contract{TaskID: "create", EffectMode: EffectIdempotent, OperationID: "op-1", RequestDigest: "sha256:req", MaxAttempts: 3}

	fake, _ := signReceipt(attacker, Receipt{OperationID: c.OperationID, RequestDigest: c.RequestDigest, EffectID: "fake", AppliedAt: now})
	if err := guard.Confirm(c, fake); err == nil {
		t.Fatal("worker-controlled receipt must not confirm external side effect")
	}
}

func TestAuthoritativeReceiptCompletesOperation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	effectAuthority, _ := NewKeyPair()
	svc := NewEffectService(effectAuthority)
	guard, _ := NewReceiptGuard(effectAuthority.Public)
	payload := []byte("create resource X")
	digest := DigestRequest(payload)
	c := Contract{TaskID: "create", EffectMode: EffectIdempotent, OperationID: "op-1", RequestDigest: digest, MaxAttempts: 3}

	receipt, err := svc.ApplyIdempotent(now, c.OperationID, c.RequestDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Confirm(c, receipt); err != nil {
		t.Fatal(err)
	}

	d, err := RecoverAfterDisconnect(c, Attempt{AttemptNo: 1, EffectDispatched: true, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateComplete {
		t.Fatalf("decision=%+v", d)
	}
}

func TestCancellationDoesNotEraseUnknownOutcome(t *testing.T) {
	c := Contract{TaskID: "notify", EffectMode: EffectNonIdempotent, OperationID: "op-1", RequestDigest: "sha256:req", MaxAttempts: 3}
	got := Cancel(c, Attempt{AttemptNo: 1, EffectDispatched: true})
	if got != CancellationUnknown {
		t.Fatalf("cancel=%s", got)
	}
}

func TestRetryBudgetStillAppliesToIdempotentEffect(t *testing.T) {
	c := Contract{TaskID: "create", EffectMode: EffectIdempotent, OperationID: "op-1", RequestDigest: "sha256:req", MaxAttempts: 2}
	d, err := RecoverAfterDisconnect(c, Attempt{AttemptNo: 2, EffectDispatched: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateExhausted {
		t.Fatalf("decision=%+v", d)
	}
}
