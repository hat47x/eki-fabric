package grant

import (
	"slices"
	"testing"
	"time"
)

func TestRevokeRecordsReasonAndEffectiveTime(t *testing.T) {
	g := mustIssue(t, testSpec())
	effective := testIssuedAt.Add(10 * testDay)

	r, err := g.Revoke(testProvider, ReasonCapacityReclaim, effective)
	checkErr(t, err, nil)

	rev, ok := r.Revocation()
	if !ok {
		t.Fatal("a revoked grant reports no revocation")
	}
	if rev.Reason != ReasonCapacityReclaim {
		t.Errorf("Reason = %q, want %q", rev.Reason, ReasonCapacityReclaim)
	}
	if !rev.EffectiveAt.Equal(effective) {
		t.Errorf("EffectiveAt = %v, want %v", rev.EffectiveAt, effective)
	}
	if _, ok := g.Revocation(); ok {
		t.Error("Revoke changed the receiver instead of returning a copy")
	}
}

func TestRevokeRefusals(t *testing.T) {
	g := mustIssue(t, testSpec())
	after := testIssuedAt.Add(2 * time.Hour)
	revoked, err := g.Revoke(testProvider, ReasonPolicyChange, testIssuedAt.Add(time.Hour))
	checkErr(t, err, nil)

	cases := []struct {
		name   string
		g      Grant
		actor  Identity
		reason ReasonCategory
		at     time.Time
		want   error
	}{
		{"nil grant", Grant{}, testProvider, ReasonCapacityReclaim, after, ErrNilGrant},
		{"beneficiary revokes", g, testBeneficiary, ReasonCapacityReclaim, after, ErrRevokerMismatch},
		{"empty revoker", g, "", ReasonCapacityReclaim, after, ErrMissingField},
		{"unknown reason", g, testProvider, "because-we-said-so", after, ErrUnknownReason},
		{"empty reason", g, testProvider, "", after, ErrUnknownReason},
		{"zero effective time", g, testProvider, ReasonCapacityReclaim, time.Time{}, ErrMissingField},
		{"effective before issuance", g, testProvider, ReasonCapacityReclaim, testIssuedAt.Add(-time.Second), ErrRevocationBeforeIssue},
		{"second revocation", revoked, testProvider, ReasonCapacityReclaim, after, ErrAlreadyRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.g.Revoke(tc.actor, tc.reason, tc.at)
			checkErr(t, err, tc.want)
		})
	}
}

func TestRevocationEffectiveAtIssuanceIsAccepted(t *testing.T) {
	g := mustIssue(t, testSpec())
	r, err := g.Revoke(testProvider, ReasonSecurityIncident, testIssuedAt)
	checkErr(t, err, nil)
	if r.ActiveAt(testIssuedAt) {
		t.Error("a grant revoked at issuance is active at issuance")
	}
}

func TestRevocationNeverRewritesHistory(t *testing.T) {
	g := mustIssue(t, testSpec())
	effective := testIssuedAt.Add(10 * testDay)

	r, err := g.Revoke(testProvider, ReasonSecurityIncident, effective)
	checkErr(t, err, nil)

	if r.Digest() != g.Digest() {
		t.Error("revocation changed the grant digest")
	}
	if r.Provider() != g.Provider() || r.Beneficiary() != g.Beneficiary() {
		t.Error("revocation changed the named parties")
	}
	if !slices.Equal(r.Ceilings(), g.Ceilings()) {
		t.Error("revocation changed the ceilings")
	}
	if !r.IssuedAt().Equal(g.IssuedAt()) || !r.ExpiresAt().Equal(g.ExpiresAt()) {
		t.Error("revocation changed the validity window")
	}
	if r.MaxConcurrentLeases() != g.MaxConcurrentLeases() || r.Purpose() != g.Purpose() {
		t.Error("revocation changed the lease cap or purpose")
	}
	if r.PolicyRevision() != g.PolicyRevision() {
		t.Error("revocation changed the policy revision")
	}

	cases := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before issuance", testIssuedAt.Add(-time.Nanosecond), false},
		{"at issuance", testIssuedAt, true},
		{"one nanosecond before revocation", effective.Add(-time.Nanosecond), true},
		{"at the revocation's effective time", effective, false},
		{"after revocation", effective.Add(time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.ActiveAt(tc.at); got != tc.want {
				t.Errorf("ActiveAt() = %t, want %t", got, tc.want)
			}
		})
	}

	if !g.ActiveAt(effective) {
		t.Error("the unrevoked original stopped being active")
	}
}

func TestRevocationAfterExpiryChangesNoAnswer(t *testing.T) {
	g := mustIssue(t, testSpec())
	r, err := g.Revoke(testProvider, ReasonProviderWithdraw, g.ExpiresAt().Add(time.Hour))
	checkErr(t, err, nil)

	if !r.ActiveAt(g.ExpiresAt().Add(-time.Nanosecond)) {
		t.Error("a revocation after expiry removed an answer that was already true")
	}
	if r.ActiveAt(g.ExpiresAt()) {
		t.Error("the grant is active at its exclusive expiry instant")
	}
}
