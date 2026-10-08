package contribution

import (
	"errors"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestRevokeMarksTheOriginalRecordByDigest(t *testing.T) {
	rec := mustRecord(t, testInput(t))
	recDigest := mustDigest(t, rec)

	c, err := Revoke(rec)
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if c.Kind() != CorrectionRevoked {
		t.Errorf("Kind() = %q, want %q", c.Kind(), CorrectionRevoked)
	}
	if c.Target() != recDigest {
		t.Errorf("Target() does not match the revoked record's digest")
	}
	if !c.Successor().IsZero() {
		t.Errorf("a revocation must not name a successor")
	}
	if c.TaskDigest() != rec.TaskDigest() || c.ResultDigest() != rec.ResultDigest() {
		t.Errorf("correction does not carry the record's work binding")
	}
	if mustDigest(t, rec) != recDigest {
		t.Errorf("revoking changed the original record")
	}
}

func TestSupersedeLinksTheReplacementToTheSameWork(t *testing.T) {
	original := mustRecord(t, testInput(t))
	corrected := testInput(t)
	corrected.Observations[0].Quantity.Value = 118000
	replacement := mustRecord(t, corrected)

	c, err := Supersede(original, replacement)
	if err != nil {
		t.Fatalf("Supersede() error = %v", err)
	}
	if c.Kind() != CorrectionSuperseded {
		t.Errorf("Kind() = %q, want %q", c.Kind(), CorrectionSuperseded)
	}
	if c.Target() != mustDigest(t, original) {
		t.Errorf("Target() does not match the original record")
	}
	if c.Successor() != mustDigest(t, replacement) {
		t.Errorf("Successor() does not match the replacement record")
	}
}

func TestSupersedeRejectsSelfAndOtherWork(t *testing.T) {
	original := mustRecord(t, testInput(t))

	otherProvider := testInput(t)
	otherProvider.ProviderID = "provider-beta"
	otherClass := testInput(t)
	otherClass.ResourceClass = "gpu-class-b"
	otherTask := testInput(t)
	otherTask.Work = testRefFor(t, "task:beta", "result:alpha")
	otherResult := testInput(t)
	otherResult.Work = testRefFor(t, "task:alpha", "result:beta")

	tests := []struct {
		name      string
		successor Record
	}{
		{"the record itself", original},
		{"different provider", mustRecord(t, otherProvider)},
		{"different resource class", mustRecord(t, otherClass)},
		{"different task", mustRecord(t, otherTask)},
		{"different result", mustRecord(t, otherResult)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Supersede(original, tt.successor); !errors.Is(err, ErrInvalidCorrection) {
				t.Fatalf("Supersede() error = %v, want ErrInvalidCorrection", err)
			}
		})
	}
}

func TestCorrectionsRejectZeroRecords(t *testing.T) {
	var zero Record
	valid := mustRecord(t, testInput(t))

	if _, err := Revoke(zero); err == nil {
		t.Errorf("Revoke(zero record) succeeded")
	}
	if _, err := Supersede(valid, zero); err == nil {
		t.Errorf("Supersede(valid, zero record) succeeded")
	}
	if _, err := Supersede(zero, valid); err == nil {
		t.Errorf("Supersede(zero record, valid) succeeded")
	}
}

func TestCorrectionDigestDistinguishesKindAndTarget(t *testing.T) {
	rec := mustRecord(t, testInput(t))
	revoked, err := Revoke(rec)
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	again, err := Revoke(rec)
	if err != nil {
		t.Fatalf("Revoke() second call error = %v", err)
	}
	revokedDigest, err := revoked.Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	againDigest, err := again.Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	if revokedDigest != againDigest {
		t.Errorf("identical revocations produced different digests")
	}

	corrected := testInput(t)
	corrected.Observations[0].Quantity.Value = 118000
	superseded, err := Supersede(rec, mustRecord(t, corrected))
	if err != nil {
		t.Fatalf("Supersede() error = %v", err)
	}
	supersededDigest, err := superseded.Digest()
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	if supersededDigest == revokedDigest {
		t.Errorf("revocation and supersession share a digest")
	}
}

func TestMalformedCorrectionFailsClosed(t *testing.T) {
	target := contract.SHA256([]byte("target"))
	successor := contract.SHA256([]byte("successor"))
	tests := []struct {
		name string
		c    Correction
	}{
		{"zero value", Correction{}},
		{"unknown kind", Correction{kind: "withdrawn", target: target, taskDigest: target, resultDigest: target}},
		{"revocation naming a successor", Correction{kind: CorrectionRevoked, target: target, successor: successor, taskDigest: target, resultDigest: target}},
		{"supersession without successor", Correction{kind: CorrectionSuperseded, target: target, taskDigest: target, resultDigest: target}},
		{"supersession naming itself", Correction{kind: CorrectionSuperseded, target: target, successor: target, taskDigest: target, resultDigest: target}},
		{"missing work binding", Correction{kind: CorrectionRevoked, target: target}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.c.Digest(); !errors.Is(err, ErrInvalidCorrection) {
				t.Fatalf("Digest() error = %v, want ErrInvalidCorrection", err)
			}
		})
	}
}
