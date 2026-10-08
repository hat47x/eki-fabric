package grant

import (
	"errors"
	"testing"
	"time"
)

var testInWindow = testIssuedAt.Add(testDay)

func TestAdmitDecisions(t *testing.T) {
	cases := []struct {
		name string
		spec func(*Spec)
		at   time.Time
		req  Request
		want error
	}{
		{name: "accepts a lease within the ceiling", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 40}},
		{name: "accepts when active leases still fit under the ceiling", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 10, ActiveLeases: 3, ActivePercent: 30}},
		{name: "accepts at the issuance instant", at: testIssuedAt,
			req: Request{ResourceClass: "gpu-class-a", Percent: 1}},
		{name: "accepts the last instant of the window", at: testIssuedAt.Add(30*testDay - time.Nanosecond),
			req: Request{ResourceClass: "gpu-class-a", Percent: 1}},
		{name: "refuses one nanosecond before issuance", at: testIssuedAt.Add(-time.Nanosecond),
			req: Request{ResourceClass: "gpu-class-a", Percent: 1}, want: ErrGrantNotActive},
		{name: "refuses at the exclusive expiry instant", at: testIssuedAt.Add(30 * testDay),
			req: Request{ResourceClass: "gpu-class-a", Percent: 1}, want: ErrGrantNotActive},
		{name: "refuses long after expiry", at: testIssuedAt.Add(31 * testDay),
			req: Request{ResourceClass: "gpu-class-a", Percent: 1}, want: ErrGrantNotActive},
		{name: "refuses a class the grant does not cover", at: testInWindow,
			req: Request{ResourceClass: "memory-class-c", Percent: 1}, want: ErrResourceNotGranted},
		{name: "refuses a request above the ceiling", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 41}, want: ErrCeilingExceeded},
		{name: "refuses when the aggregate would exceed the ceiling", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 10, ActiveLeases: 2, ActivePercent: 35}, want: ErrCeilingExceeded},
		{name: "refuses at the lease cap", at: testInWindow,
			req: Request{ResourceClass: "cpu-class-b", Percent: 1, ActiveLeases: 4, ActivePercent: 4}, want: ErrLeaseLimitReached},
		{name: "no lease cap set: percent ceiling still applies", at: testInWindow,
			spec: func(s *Spec) { s.MaxConcurrentLeases = 0 },
			req:  Request{ResourceClass: "cpu-class-b", Percent: 1, ActiveLeases: 24, ActivePercent: 24}},
		{name: "no lease cap set: aggregate still refused at the ceiling", at: testInWindow,
			spec: func(s *Spec) { s.MaxConcurrentLeases = 0 },
			req:  Request{ResourceClass: "cpu-class-b", Percent: 2, ActiveLeases: 24, ActivePercent: 24}, want: ErrCeilingExceeded},
		{name: "refuses percent zero", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 0}, want: ErrPercentOutOfRange},
		{name: "refuses percent 101", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 101}, want: ErrPercentOutOfRange},
		{name: "refuses negative active leases", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 1, ActiveLeases: -1}, want: ErrInvalidRequest},
		{name: "refuses more leases than active percent", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 1, ActiveLeases: 2, ActivePercent: 1}, want: ErrInvalidRequest},
		{name: "refuses active percent with no leases", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 1, ActivePercent: 5}, want: ErrInvalidRequest},
		{name: "refuses active percent above 100", at: testInWindow,
			req: Request{ResourceClass: "gpu-class-a", Percent: 1, ActiveLeases: 5, ActivePercent: 101}, want: ErrPercentOutOfRange},
		{name: "refuses a malformed class name", at: testInWindow,
			req: Request{ResourceClass: "GPU", Percent: 1}, want: ErrInvalidField},
		{name: "refuses an empty class name", at: testInWindow,
			req: Request{ResourceClass: "", Percent: 1}, want: ErrMissingField},
		{name: "refuses a zero decision time", at: time.Time{},
			req: Request{ResourceClass: "gpu-class-a", Percent: 1}, want: ErrMissingField},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := testSpec()
			if tc.spec != nil {
				tc.spec(&spec)
			}
			g := mustIssue(t, spec)
			checkErr(t, Admit(g, tc.at, tc.req), tc.want)
		})
	}
}

func TestAdmitRefusesNilGrant(t *testing.T) {
	req := Request{ResourceClass: "gpu-class-a", Percent: 1}
	checkErr(t, Admit(Grant{}, testInWindow, req), ErrNilGrant)
}

func TestAdmitRefusesRevokedAtOrBeforeDecisionTime(t *testing.T) {
	g := mustIssue(t, testSpec())
	effective := testIssuedAt.Add(2 * time.Hour)
	r, err := g.Revoke(testProvider, ReasonCapacityReclaim, effective)
	checkErr(t, err, nil)
	req := Request{ResourceClass: "gpu-class-a", Percent: 1}

	cases := []struct {
		name string
		at   time.Time
		want error
	}{
		{"before the effective time", effective.Add(-time.Nanosecond), nil},
		{"at the effective time", effective, ErrGrantNotActive},
		{"after the effective time", effective.Add(time.Hour), ErrGrantNotActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkErr(t, Admit(r, tc.at, req), tc.want)
		})
	}
}

// TestAdmitNeverAcceptsRevokedAtOrBeforeDecisionTime enumerates a small state
// space. It covers the revocation hour (or none), the decision hour, and four
// requests. Admit must never return nil when the grant was revoked at or
// before the decision time. Outside that case, Admit must agree with a simple
// model of the active window and the ceiling.
func TestAdmitNeverAcceptsRevokedAtOrBeforeDecisionTime(t *testing.T) {
	const expiryHour = 720
	const noRevocation = -1
	revocationHours := []int{noRevocation, 0, 1, 100, 719, 720, 721}
	decisionHours := []int{-1, 0, 1, 100, 719, 720, 721}
	requests := []Request{
		{ResourceClass: "gpu-class-a", Percent: 40},
		{ResourceClass: "gpu-class-a", Percent: 41},
		{ResourceClass: "cpu-class-b", Percent: 25},
		{ResourceClass: "memory-class-c", Percent: 1},
	}
	modelCeiling := map[ResourceClass]int{"gpu-class-a": 40, "cpu-class-b": 25}
	hour := func(h int) time.Time { return testIssuedAt.Add(time.Duration(h) * time.Hour) }

	cases := 0
	for _, rh := range revocationHours {
		base := mustIssue(t, testSpec())
		g := base
		if rh != noRevocation {
			var err error
			g, err = base.Revoke(testProvider, ReasonCapacityReclaim, hour(rh))
			checkErr(t, err, nil)
		}
		for _, dh := range decisionHours {
			for _, req := range requests {
				cases++
				err := Admit(g, hour(dh), req)

				revokedByDecision := rh != noRevocation && rh <= dh
				if revokedByDecision {
					if err == nil {
						t.Fatalf("revoked at hour %d, decided at hour %d, class %q: admitted", rh, dh, req.ResourceClass)
					}
					if !errors.Is(err, ErrGrantNotActive) {
						t.Fatalf("revoked at hour %d, decided at hour %d: error = %v, want ErrGrantNotActive", rh, dh, err)
					}
					continue
				}

				active := dh >= 0 && dh < expiryHour && (rh == noRevocation || dh < rh)
				ceiling, granted := modelCeiling[req.ResourceClass]
				wantAdmit := active && granted && req.Percent <= ceiling
				if wantAdmit != (err == nil) {
					t.Fatalf("revocation hour %d, decision hour %d, class %q, percent %d: admitted = %t, error = %v",
						rh, dh, req.ResourceClass, req.Percent, err == nil, err)
				}
			}
		}
	}
	if cases != len(revocationHours)*len(decisionHours)*len(requests) {
		t.Fatalf("enumerated %d cases, want %d", cases, len(revocationHours)*len(decisionHours)*len(requests))
	}
}
