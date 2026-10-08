package grant

import (
	"testing"
	"time"
)

func TestRevokeRecordsReasonAndEffectiveTime(t *testing.T) {
	g := mustIssue(t, testSpec())
	effective := testIssuedAt.Add(10 * testDay)
	var l Ledger

	if _, ok := l.revocationOf(g); ok {
		t.Fatal("an empty ledger reports a revocation")
	}
	checkErr(t, l.Revoke(g, testProvider, ReasonCapacityReclaim, effective), nil)

	rev, ok := l.revocationOf(g)
	if !ok {
		t.Fatal("a revoked grant has no recorded revocation")
	}
	if rev.Reason != ReasonCapacityReclaim {
		t.Errorf("Reason = %q, want %q", rev.Reason, ReasonCapacityReclaim)
	}
	if !rev.EffectiveAt.Equal(effective) {
		t.Errorf("EffectiveAt = %v, want %v", rev.EffectiveAt, effective)
	}
}

func TestRevokeRefusals(t *testing.T) {
	g := mustIssue(t, testSpec())
	after := testIssuedAt.Add(2 * time.Hour)
	var recorded Ledger
	checkErr(t, recorded.Revoke(g, testProvider, ReasonPolicyChange, testIssuedAt.Add(time.Hour)), nil)

	cases := []struct {
		name   string
		ledger *Ledger
		g      Grant
		actor  Identity
		reason ReasonCategory
		at     time.Time
		want   error
	}{
		{"nil ledger", nil, g, testProvider, ReasonCapacityReclaim, after, ErrNilLedger},
		{"nil grant", &Ledger{}, Grant{}, testProvider, ReasonCapacityReclaim, after, ErrNilGrant},
		{"beneficiary revokes", &Ledger{}, g, testBeneficiary, ReasonCapacityReclaim, after, ErrRevokerMismatch},
		{"empty revoker", &Ledger{}, g, "", ReasonCapacityReclaim, after, ErrMissingField},
		{"unknown reason", &Ledger{}, g, testProvider, "because-we-said-so", after, ErrUnknownReason},
		{"empty reason", &Ledger{}, g, testProvider, "", after, ErrUnknownReason},
		{"zero effective time", &Ledger{}, g, testProvider, ReasonCapacityReclaim, time.Time{}, ErrMissingField},
		{"effective before issuance", &Ledger{}, g, testProvider, ReasonCapacityReclaim, testIssuedAt.Add(-time.Second), ErrRevocationBeforeIssue},
		{"second revocation", &recorded, g, testProvider, ReasonCapacityReclaim, after, ErrAlreadyRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkErr(t, tc.ledger.Revoke(tc.g, tc.actor, tc.reason, tc.at), tc.want)
		})
	}
}

// TestRevocationIsOneFactForEveryCopy covers the defect where a per-value
// revocation flag let an unrevoked copy be revoked again at a later time, which
// moved the stop time or reversed it. The first revocation must stand for every
// copy and for every value issued from the same fields.
func TestRevocationIsOneFactForEveryCopy(t *testing.T) {
	original := mustIssue(t, testSpec())
	copyOfOriginal := original
	reissued := mustIssue(t, testSpec())
	first := testIssuedAt.Add(10 * testDay)
	var l Ledger
	checkErr(t, l.Revoke(original, testProvider, ReasonCapacityReclaim, first), nil)

	checkErr(t, l.Revoke(copyOfOriginal, testProvider, ReasonCapacityReclaim, first.Add(19*testDay)), ErrAlreadyRevoked)
	checkErr(t, l.Revoke(copyOfOriginal, testProvider, ReasonCapacityReclaim, first.Add(-time.Hour)), ErrAlreadyRevoked)
	checkErr(t, l.Revoke(reissued, testProvider, ReasonPolicyChange, testIssuedAt.Add(time.Hour)), ErrAlreadyRevoked)

	rev, ok := l.revocationOf(copyOfOriginal)
	if !ok || !rev.EffectiveAt.Equal(first) {
		t.Fatalf("recorded revocation = %v, %t; want the first one at %v", rev.EffectiveAt, ok, first)
	}

	req := Request{ResourceClass: "gpu-class-a", Percent: 1}
	checkErr(t, Admit(original, &l, first.Add(-time.Nanosecond), req), nil)
	checkErr(t, Admit(copyOfOriginal, &l, testIssuedAt.Add(20*testDay), req), ErrGrantNotActive)
	checkErr(t, Admit(reissued, &l, testIssuedAt.Add(20*testDay), req), ErrGrantNotActive)
}

func TestRevocationEffectiveAtIssuanceIsAccepted(t *testing.T) {
	g := mustIssue(t, testSpec())
	var l Ledger
	checkErr(t, l.Revoke(g, testProvider, ReasonSecurityIncident, testIssuedAt), nil)
	req := Request{ResourceClass: "gpu-class-a", Percent: 1}
	checkErr(t, Admit(g, &l, testIssuedAt, req), ErrGrantNotActive)
}

func TestRevocationKeepsEarlierAnswers(t *testing.T) {
	g := mustIssue(t, testSpec())
	effective := testIssuedAt.Add(10 * testDay)
	var l Ledger
	checkErr(t, l.Revoke(g, testProvider, ReasonSecurityIncident, effective), nil)

	req := Request{ResourceClass: "gpu-class-a", Percent: 1}
	cases := []struct {
		name string
		at   time.Time
		want error
	}{
		{"before issuance", testIssuedAt.Add(-time.Nanosecond), ErrGrantNotActive},
		{"at issuance", testIssuedAt, nil},
		{"one nanosecond before revocation", effective.Add(-time.Nanosecond), nil},
		{"at the revocation's effective time", effective, ErrGrantNotActive},
		{"after revocation", effective.Add(time.Hour), ErrGrantNotActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkErr(t, Admit(g, &l, tc.at, req), tc.want)
		})
	}

	var empty Ledger
	checkErr(t, Admit(g, &empty, effective, req), nil)
}

func TestRevocationAfterExpiryChangesNoAnswer(t *testing.T) {
	g := mustIssue(t, testSpec())
	var l Ledger
	checkErr(t, l.Revoke(g, testProvider, ReasonProviderWithdraw, g.ExpiresAt().Add(time.Hour)), nil)

	req := Request{ResourceClass: "gpu-class-a", Percent: 1}
	checkErr(t, Admit(g, &l, g.ExpiresAt().Add(-time.Nanosecond), req), nil)
	checkErr(t, Admit(g, &l, g.ExpiresAt(), req), ErrGrantNotActive)
}
