package grant

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"
)

var testReclaimAt = testIssuedAt.Add(10 * testDay)

// revokedGrants returns n grants, each with its own beneficiary, and a ledger
// that records every one of them as revoked at the same instant.
func revokedGrants(t *testing.T, n int, at time.Time) (*Ledger, []Grant) {
	t.Helper()
	ledger := new(Ledger)
	out := make([]Grant, 0, n)
	for i := 0; i < n; i++ {
		spec := testSpec()
		spec.Beneficiary = Identity(fmt.Sprintf("beneficiary-%04d", i))
		g := mustIssue(t, spec)
		if err := ledger.Revoke(g, testProvider, ReasonCapacityReclaim, at); err != nil {
			t.Fatalf("Revoke() error = %v", err)
		}
		out = append(out, g)
	}
	return ledger, out
}

func testReclaimConfig() ReclaimConfig {
	return ReclaimConfig{Window: 10 * time.Minute, Slots: 600, MaxLeasesPerStop: 10}
}

func TestPlanReclaimGivesEachGrantItsOwnSlotWhenSlotsSuffice(t *testing.T) {
	cfg := testReclaimConfig()
	ledger, grants := revokedGrants(t, 200, testReclaimAt)
	plan, err := PlanReclaim(ledger, grants, testReclaimAt, 0, cfg)
	checkErr(t, err, nil)
	if len(plan) != len(grants) {
		t.Fatalf("plan has %d assignments, want %d", len(plan), len(grants))
	}

	slotSeen := make(map[int]bool, len(plan))
	grantSeen := make(map[string]bool, len(plan))
	for _, a := range plan {
		if slotSeen[a.Slot] {
			t.Fatalf("slot %d is used by more than one grant", a.Slot)
		}
		slotSeen[a.Slot] = true
		grantSeen[a.Grant.String()] = true

		if a.Slot < 0 || a.Slot >= cfg.Slots {
			t.Fatalf("slot %d is outside 0..%d", a.Slot, cfg.Slots-1)
		}
		if want := time.Duration(a.Slot) * time.Second; a.Offset != want {
			t.Errorf("offset = %v, want %v for slot %d", a.Offset, want, a.Slot)
		}
		if a.Offset < 0 || a.Offset >= cfg.Window {
			t.Errorf("offset %v is outside the window %v", a.Offset, cfg.Window)
		}
		if !a.StopAt.Equal(testReclaimAt.Add(a.Offset)) {
			t.Errorf("StopAt = %v, want effective time plus offset", a.StopAt)
		}
	}
	for _, g := range grants {
		if !grantSeen[g.Digest().String()] {
			t.Error("a revoked grant has no assignment")
		}
	}
}

func TestPlanReclaimIsDeterministicAndOrderIndependent(t *testing.T) {
	cfg := testReclaimConfig()
	ledger, grants := revokedGrants(t, 120, testReclaimAt)

	first, err := PlanReclaim(ledger, grants, testReclaimAt, 0, cfg)
	checkErr(t, err, nil)
	again, err := PlanReclaim(ledger, grants, testReclaimAt, 0, cfg)
	checkErr(t, err, nil)
	if !reflect.DeepEqual(first, again) {
		t.Fatal("the same input produced different plans")
	}

	reversed := slices.Clone(grants)
	slices.Reverse(reversed)
	shuffled, err := PlanReclaim(ledger, reversed, testReclaimAt, 0, cfg)
	checkErr(t, err, nil)
	if !reflect.DeepEqual(first, shuffled) {
		t.Fatal("the order of the input changed the plan")
	}
}

func TestPlanReclaimBoundsStopsPerInstant(t *testing.T) {
	cfg := ReclaimConfig{Window: 10 * time.Minute, Slots: 10, MaxLeasesPerStop: 10}
	ledger, grants := revokedGrants(t, 50, testReclaimAt)
	plan, err := PlanReclaim(ledger, grants, testReclaimAt, 0, cfg)
	checkErr(t, err, nil)
	if len(plan) != 50 {
		t.Fatalf("plan has %d assignments, want 50", len(plan))
	}

	perSlot := make([]int, cfg.Slots)
	for _, a := range plan {
		perSlot[a.Slot]++
	}
	limit := ceilDiv(50, cfg.Slots)
	for slot, n := range perSlot {
		if n > limit {
			t.Errorf("slot %d holds %d stops, want at most %d", slot, n, limit)
		}
	}
}

func TestPlanReclaimSpreadsStopsAcrossTheWindow(t *testing.T) {
	cfg := testReclaimConfig()
	ledger, grants := revokedGrants(t, 200, testReclaimAt)
	plan, err := PlanReclaim(ledger, grants, testReclaimAt, 0, cfg)
	checkErr(t, err, nil)

	var latest time.Duration
	for _, a := range plan {
		latest = max(latest, a.Offset)
	}
	if latest < cfg.Window/2 {
		t.Errorf("latest offset = %v, want stops spread into the second half of %v", latest, cfg.Window)
	}
}

// TestPlanReclaimTrustsOnlyTheLedger checks that the stop plan follows the
// ledger, not the grant value. A grant whose copy looks revoked is refused
// when the ledger has no revocation for it.
func TestPlanReclaimTrustsOnlyTheLedger(t *testing.T) {
	cfg := testReclaimConfig()
	ledger, revoked := revokedGrants(t, 1, testReclaimAt)
	unrecorded := mustIssue(t, testSpec())
	var empty Ledger

	_, err := PlanReclaim(&empty, revoked, testReclaimAt, 0, cfg)
	checkErr(t, err, ErrGrantNotRevoked)
	_, err = PlanReclaim(ledger, []Grant{unrecorded}, testReclaimAt, 0, cfg)
	checkErr(t, err, ErrGrantNotRevoked)
}

func TestPlanReclaimRefusals(t *testing.T) {
	cfg := testReclaimConfig()
	ledger, revoked := revokedGrants(t, 3, testReclaimAt)
	notRevoked := mustIssue(t, testSpec())

	laterSpec := testSpec()
	laterSpec.Beneficiary = "beneficiary-later"
	otherTime := mustIssue(t, laterSpec)
	checkErr(t, ledger.Revoke(otherTime, testProvider, ReasonCapacityReclaim, testReclaimAt.Add(time.Second)), nil)

	cases := []struct {
		name    string
		ledger  *Ledger
		grants  []Grant
		at      time.Time
		running int
		cfg     ReclaimConfig
		want    error
	}{
		{"nil ledger", nil, revoked, testReclaimAt, 0, cfg, ErrNilLedger},
		{"grant not revoked", ledger, []Grant{notRevoked}, testReclaimAt, 0, cfg, ErrGrantNotRevoked},
		{"grant revoked at a different time", ledger, []Grant{revoked[0], otherTime}, testReclaimAt, 0, cfg, ErrRevocationTimeMismatch},
		{"nil grant", ledger, []Grant{{}}, testReclaimAt, 0, cfg, ErrNilGrant},
		{"same grant listed twice", ledger, []Grant{revoked[0], revoked[0]}, testReclaimAt, 0, cfg, ErrDuplicateGrant},
		{"zero effective time", ledger, revoked, time.Time{}, 0, cfg, ErrMissingField},
		{"zero window", ledger, revoked, testReclaimAt, 0, ReclaimConfig{Window: 0, Slots: 10, MaxLeasesPerStop: 10}, ErrInvalidReclaimConfig},
		{"window over one day", ledger, revoked, testReclaimAt, 0, ReclaimConfig{Window: 25 * time.Hour, Slots: 10, MaxLeasesPerStop: 10}, ErrInvalidReclaimConfig},
		{"zero slots", ledger, revoked, testReclaimAt, 0, ReclaimConfig{Window: time.Minute, Slots: 0, MaxLeasesPerStop: 10}, ErrInvalidReclaimConfig},
		{"slots over the bound", ledger, revoked, testReclaimAt, 0, ReclaimConfig{Window: 24 * time.Hour, Slots: MaxReclaimSlots + 1, MaxLeasesPerStop: 10}, ErrInvalidReclaimConfig},
		{"slots narrower than one second", ledger, revoked, testReclaimAt, 0, ReclaimConfig{Window: 10 * time.Minute, Slots: 3600, MaxLeasesPerStop: 10}, ErrInvalidReclaimConfig},
		{"zero leases per stop", ledger, revoked, testReclaimAt, 0, ReclaimConfig{Window: time.Minute, Slots: 10, MaxLeasesPerStop: 0}, ErrInvalidReclaimConfig},
		{"negative running leases", ledger, revoked, testReclaimAt, -1, cfg, ErrInvalidRunningLeases},
		{"running leases over the bound", ledger, revoked, testReclaimAt, MaxRunningLeases + 1, cfg, ErrInvalidRunningLeases},
		{"window too small for the running leases", ledger, revoked, testReclaimAt, 100,
			ReclaimConfig{Window: 10 * time.Minute, Slots: 9, MaxLeasesPerStop: 10}, ErrInsufficientSlots},
		{"too many grants", ledger, make([]Grant, MaxReclaimGrants+1), testReclaimAt, 0, cfg, ErrTooManyGrants},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := PlanReclaim(tc.ledger, tc.grants, tc.at, tc.running, tc.cfg)
			checkErr(t, err, tc.want)
		})
	}
}

func TestPlanReclaimAcceptsRunningLeasesAtTheSlotFloor(t *testing.T) {
	cfg := ReclaimConfig{Window: 10 * time.Minute, Slots: 10, MaxLeasesPerStop: 10}
	ledger, grants := revokedGrants(t, 2, testReclaimAt)
	_, err := PlanReclaim(ledger, grants, testReclaimAt, 100, cfg)
	checkErr(t, err, nil)
}

func TestPlanReclaimEmptyListPlansNothing(t *testing.T) {
	plan, err := PlanReclaim(new(Ledger), nil, testReclaimAt, 0, testReclaimConfig())
	checkErr(t, err, nil)
	if plan != nil {
		t.Errorf("plan = %v, want nil for an empty grant list", plan)
	}
}
