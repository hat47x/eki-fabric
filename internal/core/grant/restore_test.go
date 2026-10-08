package grant

import (
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// policyVariant issues a grant whose digest differs from testSpec's, because
// only the policy revision differs.
func policyVariant(t *testing.T, label string) Grant {
	t.Helper()
	spec := testSpec()
	spec.PolicyRevision = testPolicy(label)
	return mustIssue(t, spec)
}

// sameRecords reports whether two exports hold the same digests, reasons, and
// effective instants, in the same order.
func sameRecords(a, b []RecordedRevocation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Grant != b[i].Grant ||
			a[i].Revocation.Reason != b[i].Revocation.Reason ||
			!a[i].Revocation.EffectiveAt.Equal(b[i].Revocation.EffectiveAt) {
			return false
		}
	}
	return true
}

func TestRestoreRevocationAppliesLikeRevoke(t *testing.T) {
	g := mustIssue(t, testSpec())
	effective := testIssuedAt.Add(10 * testDay).In(time.FixedZone("offset-plus-nine", 9*60*60))

	var restored Ledger
	checkErr(t, restored.RestoreRevocation(g.Digest(), g.IssuedAt(), Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: effective}), nil)

	var revoked Ledger
	checkErr(t, revoked.Revoke(g, testProvider, ReasonCapacityReclaim, effective), nil)

	want, err := revoked.Export()
	checkErr(t, err, nil)
	got, err := restored.Export()
	checkErr(t, err, nil)
	if !sameRecords(got, want) {
		t.Fatalf("restored export = %+v, want %+v", got, want)
	}
	if len(got) != 1 {
		t.Fatalf("restored export has %d records, want 1", len(got))
	}
	if loc := got[0].Revocation.EffectiveAt.Location(); loc != time.UTC {
		t.Errorf("restored effective time location = %v, want UTC", loc)
	}

	req := Request{ResourceClass: "gpu-class-a", Percent: 1}
	checkErr(t, Admit(g, &restored, effective.Add(-time.Nanosecond), req), nil)
	checkErr(t, Admit(g, &restored, effective, req), ErrGrantNotActive)

	// The record is keyed by digest, so a copy issued from the same fields is refused too.
	reissued := mustIssue(t, testSpec())
	checkErr(t, Admit(reissued, &restored, effective, req), ErrGrantNotActive)
}

func TestRestoreRevocationRefusesInvalidRecords(t *testing.T) {
	g := mustIssue(t, testSpec())
	digest := g.Digest()
	issued := g.IssuedAt()
	effective := issued.Add(time.Hour)

	cases := []struct {
		name      string
		nilLedger bool
		d         contract.Digest
		issuedAt  time.Time
		rev       Revocation
		want      error
	}{
		{name: "nil ledger", nilLedger: true, d: digest, issuedAt: issued,
			rev: Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: effective}, want: ErrNilLedger},
		{name: "zero digest", d: contract.Digest{}, issuedAt: issued,
			rev: Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: effective}, want: ErrMissingField},
		{name: "zero issuance time", d: digest, issuedAt: time.Time{},
			rev: Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: effective}, want: ErrMissingField},
		{name: "unknown reason", d: digest, issuedAt: issued,
			rev: Revocation{Reason: "because-we-said-so", EffectiveAt: effective}, want: ErrUnknownReason},
		{name: "empty reason", d: digest, issuedAt: issued,
			rev: Revocation{Reason: "", EffectiveAt: effective}, want: ErrUnknownReason},
		{name: "zero effective time", d: digest, issuedAt: issued,
			rev: Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: time.Time{}}, want: ErrMissingField},
		{name: "effective before issuance", d: digest, issuedAt: issued,
			rev: Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: issued.Add(-time.Second)}, want: ErrRevocationBeforeIssue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var l *Ledger
			if !tc.nilLedger {
				l = &Ledger{}
			}
			checkErr(t, l.RestoreRevocation(tc.d, tc.issuedAt, tc.rev), tc.want)
			if l == nil {
				return
			}
			got, err := l.Export()
			checkErr(t, err, nil)
			if len(got) != 0 {
				t.Fatalf("refused restore stored %d records, want none", len(got))
			}
		})
	}
}

func TestRestoreRevocationRefusesSecondRecordForDigest(t *testing.T) {
	g := mustIssue(t, testSpec())
	reissued := mustIssue(t, testSpec())
	first := testIssuedAt.Add(10 * testDay)
	var l Ledger
	checkErr(t, l.RestoreRevocation(g.Digest(), g.IssuedAt(), Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: first}), nil)

	// The same digest from a copy, with a later time and another reason, is refused.
	checkErr(t, l.RestoreRevocation(reissued.Digest(), reissued.IssuedAt(),
		Revocation{Reason: ReasonPolicyChange, EffectiveAt: first.Add(time.Hour)}), ErrAlreadyRevoked)
	// An earlier time is refused too, so the first record stands.
	checkErr(t, l.RestoreRevocation(g.Digest(), g.IssuedAt(),
		Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: first.Add(-time.Hour)}), ErrAlreadyRevoked)

	got, err := l.Export()
	checkErr(t, err, nil)
	if len(got) != 1 || got[0].Revocation.Reason != ReasonCapacityReclaim || !got[0].Revocation.EffectiveAt.Equal(first) {
		t.Fatalf("export after refused restores = %+v, want the first record only", got)
	}
}

func TestRestoreRevocationRefusalLeavesLedgerUnchanged(t *testing.T) {
	kept := policyVariant(t, "kept")
	other := policyVariant(t, "other")
	first := kept.IssuedAt().Add(10 * testDay)

	var l Ledger
	checkErr(t, l.RestoreRevocation(kept.Digest(), kept.IssuedAt(), Revocation{Reason: ReasonSecurityIncident, EffectiveAt: first}), nil)
	before, err := l.Export()
	checkErr(t, err, nil)

	checkErr(t, l.RestoreRevocation(other.Digest(), other.IssuedAt(), Revocation{Reason: "bogus", EffectiveAt: first}), ErrUnknownReason)
	checkErr(t, l.RestoreRevocation(other.Digest(), other.IssuedAt(),
		Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: other.IssuedAt().Add(-time.Second)}), ErrRevocationBeforeIssue)

	after, err := l.Export()
	checkErr(t, err, nil)
	if !sameRecords(before, after) {
		t.Fatalf("export changed after refused restores: before %+v, after %+v", before, after)
	}

	// The grant whose restore was refused is still not revoked.
	req := Request{ResourceClass: "gpu-class-a", Percent: 1}
	checkErr(t, Admit(other, &l, first.Add(time.Hour), req), nil)
}

func TestExportIsSortedAndIndependentOfInsertionOrder(t *testing.T) {
	grants := []Grant{
		policyVariant(t, "rev-a"),
		policyVariant(t, "rev-b"),
		policyVariant(t, "rev-c"),
		policyVariant(t, "rev-d"),
	}
	at := testIssuedAt.Add(testDay)

	var forward, backward Ledger
	for _, g := range grants {
		checkErr(t, forward.RestoreRevocation(g.Digest(), g.IssuedAt(), Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: at}), nil)
	}
	for i := len(grants) - 1; i >= 0; i-- {
		g := grants[i]
		checkErr(t, backward.RestoreRevocation(g.Digest(), g.IssuedAt(), Revocation{Reason: ReasonCapacityReclaim, EffectiveAt: at}), nil)
	}

	first, err := forward.Export()
	checkErr(t, err, nil)
	second, err := backward.Export()
	checkErr(t, err, nil)
	if len(first) != len(grants) {
		t.Fatalf("export has %d records, want %d", len(first), len(grants))
	}
	if !sameRecords(first, second) {
		t.Fatalf("exports differ by insertion order: %+v and %+v", first, second)
	}
	for i := 1; i < len(first); i++ {
		if first[i-1].Grant.String() >= first[i].Grant.String() {
			t.Fatalf("export is not sorted by digest at index %d", i)
		}
	}
	again, err := forward.Export()
	checkErr(t, err, nil)
	if !sameRecords(first, again) {
		t.Fatal("two exports of the same ledger differ")
	}
}

func TestExportOfNilAndEmptyLedger(t *testing.T) {
	var nilLedger *Ledger
	got, err := nilLedger.Export()
	checkErr(t, err, ErrNilLedger)
	if got != nil {
		t.Fatalf("nil ledger exported %+v, want nothing", got)
	}

	var empty Ledger
	got, err = empty.Export()
	checkErr(t, err, nil)
	if len(got) != 0 {
		t.Fatalf("empty ledger exported %d records, want 0", len(got))
	}
}
