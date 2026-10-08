package governor

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)

// fresh returns a reading taken a second ago, with the endpoint in a calm state.
func fresh() Observation {
	return Observation{
		At:             now.Add(-time.Second),
		LastUserInput:  now.Add(-time.Hour),
		Power:          PowerAC,
		BatteryPercent: -1,
		Thermal:        ThermalNormal,
		Network:        NetworkUnmetered,
	}
}

func policy() LocalPolicy {
	return LocalPolicy{
		ActiveWithin:       2 * time.Minute,
		MaxObservationAge:  30 * time.Second,
		BatteryStopPercent: 30,
		Schedule:           []Interval{{From: now.Add(-time.Hour), To: now.Add(time.Hour)}},
		Caps: map[Resource]int{
			ResourceCPU: 80, ResourceMemory: 80, ResourceGPU: 80, ResourceNetwork: 80, ResourceDiskIO: 80,
		},
	}
}

func grantAll(v int) *Grant {
	c := make(map[Resource]int, len(Resources))
	for _, r := range Resources {
		c[r] = v
	}
	return &Grant{IssuedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), Ceilings: c}
}

func allEnforceable() Capabilities {
	c := Capabilities{}
	for _, r := range Resources {
		c[r] = true
	}
	return c
}

func hasReason(d Decision, r Reason) bool {
	for _, x := range d.Reasons {
		if x == r {
			return true
		}
	}
	return false
}

func mustDecide(t *testing.T, obs Observation, pol LocalPolicy, grant *Grant, caps Capabilities) Decision {
	t.Helper()
	d, err := Decide(now, obs, pol, grant, caps)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	return d
}

func TestCalmEndpointAcceptsWorkUpToTheLowestCeiling(t *testing.T) {
	pol := policy()
	pol.Caps[ResourceMemory] = 40 // the person's own limit is the binding one for memory
	d := mustDecide(t, fresh(), pol, grantAll(100), allEnforceable())

	if !d.AcceptNewLeases || d.StopRunning {
		t.Fatalf("calm endpoint: accept=%t stop=%t, want true/false; reasons=%v", d.AcceptNewLeases, d.StopRunning, d.Reasons)
	}
	if d.Ceilings[ResourceCPU] != 80 || d.Ceilings[ResourceMemory] != 40 {
		t.Fatalf("ceilings = %v, want cpu 80 memory 40", d.Ceilings)
	}
	if !hasReason(d, ReasonLocalCap) {
		t.Errorf("a local cap was binding but no reason was given: %v", d.Reasons)
	}
}

// The central grant can narrow the local policy but never widen it (RQ-007).
func TestCentralGrantCannotRaiseTheLocalCeiling(t *testing.T) {
	d := mustDecide(t, fresh(), policy(), grantAll(100), allEnforceable())
	for r, v := range d.Ceilings {
		if v > 80 {
			t.Fatalf("%s ceiling %d exceeds the local cap 80", r, v)
		}
	}

	narrow := mustDecide(t, fresh(), policy(), grantAll(20), allEnforceable())
	if narrow.Ceilings[ResourceCPU] != 20 || !hasReason(narrow, ReasonGrantCap) {
		t.Fatalf("grant of 20 should bind: ceilings=%v reasons=%v", narrow.Ceilings, narrow.Reasons)
	}
}

func TestOptOutWinsOverEverything(t *testing.T) {
	pol := policy()
	pol.OptedOut = true
	d, err := Decide(now, fresh(), pol, grantAll(100), allEnforceable())
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if d.AcceptNewLeases || !d.StopRunning || !hasReason(d, ReasonOptedOut) {
		t.Fatalf("opt-out: %+v", d)
	}
	for r, v := range d.Ceilings {
		if v != 0 {
			t.Fatalf("opted out but %s ceiling is %d", r, v)
		}
	}
}

// RQ-006: stopping must not depend on the Control Plane. An opted-out endpoint
// refuses work even with no grant at all.
func TestOptOutDoesNotNeedAGrant(t *testing.T) {
	pol := policy()
	pol.OptedOut = true
	d := mustDecide(t, fresh(), pol, nil, allEnforceable())
	if !hasReason(d, ReasonOptedOut) || d.AcceptNewLeases {
		t.Fatalf("opt-out without a grant: %+v", d)
	}
}

func TestPresentPersonStopsWorkNow(t *testing.T) {
	obs := fresh()
	obs.LastUserInput = now.Add(-30 * time.Second) // within the 2 minute active window
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if !d.StopRunning || d.AcceptNewLeases || !hasReason(d, ReasonUserPresent) {
		t.Fatalf("active user: %+v", d)
	}
	for r, v := range d.Ceilings {
		if v != 0 {
			t.Fatalf("active user but %s ceiling is %d", r, v)
		}
	}
}

func TestUnknownPresenceAdmitsNothingButKeepsRunningCautiously(t *testing.T) {
	obs := fresh()
	obs.LastUserInput = time.Time{} // the platform cannot tell
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if d.StopRunning {
		t.Fatalf("unknown presence should not stop running work: %+v", d)
	}
	if d.AcceptNewLeases || !hasReason(d, ReasonUserUnknown) {
		t.Fatalf("unknown presence should admit nothing: %+v", d)
	}
	if d.Ceilings[ResourceCPU] != cautiousCeiling {
		t.Fatalf("cpu ceiling = %d, want the cautious %d", d.Ceilings[ResourceCPU], cautiousCeiling)
	}
}

func TestCriticalHeatStopsWork(t *testing.T) {
	obs := fresh()
	obs.Thermal = ThermalCritical
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if !d.StopRunning || !hasReason(d, ReasonThermalCritical) {
		t.Fatalf("critical thermal: %+v", d)
	}
}

func TestElevatedHeatLowersTheCeilingButKeepsAccepting(t *testing.T) {
	obs := fresh()
	obs.Thermal = ThermalElevated
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if d.StopRunning || !d.AcceptNewLeases {
		t.Fatalf("elevated thermal should keep working at a lower ceiling: %+v", d)
	}
	if d.Ceilings[ResourceCPU] != cautiousCeiling || !hasReason(d, ReasonThermalElevated) {
		t.Fatalf("ceilings=%v reasons=%v", d.Ceilings, d.Reasons)
	}
}

func TestBatteryBelowThresholdStopsWorkOnBatteryOnly(t *testing.T) {
	low := fresh()
	low.Power, low.BatteryPercent = PowerBattery, 20
	d := mustDecide(t, low, policy(), grantAll(100), allEnforceable())
	if !d.StopRunning || !hasReason(d, ReasonBatteryLow) {
		t.Fatalf("low battery: %+v", d)
	}

	// The same level on AC power is not a reason to stop.
	low.Power = PowerAC
	d = mustDecide(t, low, policy(), grantAll(100), allEnforceable())
	if d.StopRunning || !d.AcceptNewLeases {
		t.Fatalf("low battery on AC should not stop work: %+v", d)
	}
}

func TestBatteryAboveThresholdLimitsCeiling(t *testing.T) {
	obs := fresh()
	obs.Power, obs.BatteryPercent = PowerBattery, 80
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if d.Ceilings[ResourceCPU] != batteryCeiling || !hasReason(d, ReasonOnBattery) {
		t.Fatalf("on battery: ceilings=%v reasons=%v", d.Ceilings, d.Reasons)
	}
}

func TestUnknownPowerSourceIsTreatedAsCautious(t *testing.T) {
	obs := fresh()
	obs.Power = PowerUnknown
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if d.AcceptNewLeases || !hasReason(d, ReasonPowerUnknown) || d.Ceilings[ResourceCPU] != cautiousCeiling {
		t.Fatalf("unknown power: %+v", d)
	}
}

// RQ-053: bulk transfer on a metered or unknown connection is refused.
func TestMeteredNetworkDisallowsNetworkButNotCompute(t *testing.T) {
	obs := fresh()
	obs.Network = NetworkMetered
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if d.Ceilings[ResourceNetwork] != 0 || d.Ceilings[ResourceCPU] != 80 {
		t.Fatalf("metered: ceilings=%v", d.Ceilings)
	}
	if !d.AcceptNewLeases {
		t.Fatal("metered network should not stop compute")
	}
	if err := d.Admit(map[Resource]int{ResourceNetwork: 10}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("network lease on a metered connection: %v", err)
	}
}

func TestOutsideScheduleStopsWork(t *testing.T) {
	pol := policy()
	pol.Schedule = []Interval{{From: now.Add(time.Hour), To: now.Add(2 * time.Hour)}}
	d := mustDecide(t, fresh(), pol, grantAll(100), allEnforceable())
	if !d.StopRunning || !hasReason(d, ReasonOutsideSchedule) {
		t.Fatalf("outside schedule: %+v", d)
	}
}

func TestEmptyScheduleAllowsNothing(t *testing.T) {
	pol := policy()
	pol.Schedule = nil
	d := mustDecide(t, fresh(), pol, grantAll(100), allEnforceable())
	if d.AcceptNewLeases || !d.StopRunning {
		t.Fatalf("a deployment with no schedule must not run work: %+v", d)
	}
}

func TestMissingOrExpiredGrantOffersNothing(t *testing.T) {
	cases := map[string]*Grant{
		"missing": nil,
		"expired": {IssuedAt: now.Add(-2 * time.Hour), ValidUntil: now.Add(-time.Minute), Ceilings: grantAll(100).Ceilings},
		"future":  {IssuedAt: now.Add(time.Minute), ValidUntil: now.Add(time.Hour), Ceilings: grantAll(100).Ceilings},
	}
	for name, g := range cases {
		d := mustDecide(t, fresh(), policy(), g, allEnforceable())
		if d.AcceptNewLeases || !d.StopRunning {
			t.Errorf("%s grant: %+v", name, d)
		}
	}
}

// RQ-047: a limit the platform cannot enforce is never claimed.
func TestUnenforceableResourceGetsNoCeiling(t *testing.T) {
	caps := allEnforceable()
	caps[ResourceDiskIO] = false
	d := mustDecide(t, fresh(), policy(), grantAll(100), caps)
	if d.Ceilings[ResourceDiskIO] != 0 {
		t.Fatalf("disk I/O is not enforceable but has ceiling %d", d.Ceilings[ResourceDiskIO])
	}
	if !reflect.DeepEqual(d.NotEnforceable, []Resource{ResourceDiskIO}) || !hasReason(d, ReasonNotEnforceable) {
		t.Fatalf("NotEnforceable = %v reasons = %v", d.NotEnforceable, d.Reasons)
	}
	if d.Ceilings[ResourceCPU] != 80 {
		t.Fatal("an unenforceable resource must not affect the others")
	}
}

// A resource the local policy does not name gets no ceiling: the person must
// say what may be used.
func TestResourceWithoutLocalCapIsNotOffered(t *testing.T) {
	pol := policy()
	delete(pol.Caps, ResourceGPU)
	d := mustDecide(t, fresh(), pol, grantAll(100), allEnforceable())
	if d.Ceilings[ResourceGPU] != 0 {
		t.Fatalf("gpu without a local cap has ceiling %d", d.Ceilings[ResourceGPU])
	}
}

func TestStaleReadingIsTreatedAsUnknown(t *testing.T) {
	obs := fresh()
	obs.At = now.Add(-10 * time.Minute)
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if !hasReason(d, ReasonObservationStale) || d.AcceptNewLeases {
		t.Fatalf("stale reading: %+v", d)
	}
	if d.Ceilings[ResourceCPU] != cautiousCeiling {
		t.Fatalf("a stale reading must not keep full ceilings: %v", d.Ceilings)
	}
}

// Inputs that cannot be trusted produce the safe decision and an error. A
// caller that ignores the error still stops and admits nothing.
func TestInvalidInputsFailClosed(t *testing.T) {
	cases := map[string]func(*Observation, *LocalPolicy, **Grant){
		"observation without a time": func(o *Observation, _ *LocalPolicy, _ **Grant) { o.At = time.Time{} },
		"unknown thermal state":      func(o *Observation, _ *LocalPolicy, _ **Grant) { o.Thermal = "warm" },
		"battery over 100":           func(o *Observation, _ *LocalPolicy, _ **Grant) { o.BatteryPercent = 150 },
		"reading from the future":    func(o *Observation, _ *LocalPolicy, _ **Grant) { o.At = now.Add(time.Minute) },
		"zero active window":         func(_ *Observation, p *LocalPolicy, _ **Grant) { p.ActiveWithin = 0 },
		"zero battery stop":          func(_ *Observation, p *LocalPolicy, _ **Grant) { p.BatteryStopPercent = 0 },
		"unknown local resource":     func(_ *Observation, p *LocalPolicy, _ **Grant) { p.Caps["teleport"] = 10 },
		"local cap over 100":         func(_ *Observation, p *LocalPolicy, _ **Grant) { p.Caps[ResourceCPU] = 101 },
		"inverted schedule": func(_ *Observation, p *LocalPolicy, _ **Grant) {
			p.Schedule = []Interval{{From: now.Add(time.Hour), To: now}}
		},
		"grant with unknown resource": func(_ *Observation, _ *LocalPolicy, g **Grant) {
			(*g).Ceilings["teleport"] = 10
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			obs, pol, grant := fresh(), policy(), grantAll(100)
			mutate(&obs, &pol, &grant)
			d, err := Decide(now, obs, pol, grant, allEnforceable())
			if err == nil {
				t.Fatal("invalid input was accepted")
			}
			if d.AcceptNewLeases || !d.StopRunning {
				t.Fatalf("invalid input must yield the safe decision: %+v", d)
			}
			for r, v := range d.Ceilings {
				if v != 0 {
					t.Fatalf("%s ceiling %d on an invalid input", r, v)
				}
			}
		})
	}
}

func TestAdmitChecksEachRequestedResource(t *testing.T) {
	d := mustDecide(t, fresh(), policy(), grantAll(50), allEnforceable())

	if err := d.Admit(map[Resource]int{ResourceCPU: 50, ResourceMemory: 10}); err != nil {
		t.Fatalf("within the ceiling: %v", err)
	}
	if err := d.Admit(map[Resource]int{ResourceCPU: 51}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("over the ceiling: %v", err)
	}
	if err := d.Admit(nil); !errors.Is(err, ErrInvalidRequirement) {
		t.Fatalf("empty requirement: %v", err)
	}
	if err := d.Admit(map[Resource]int{ResourceCPU: 0}); !errors.Is(err, ErrInvalidRequirement) {
		t.Fatalf("zero percent: %v", err)
	}
	if err := d.Admit(map[Resource]int{"teleport": 1}); !errors.Is(err, ErrInvalidRequirement) {
		t.Fatalf("unknown resource: %v", err)
	}

	stopped := mustDecide(t, fresh(), policy(), nil, allEnforceable())
	if err := stopped.Admit(map[Resource]int{ResourceCPU: 1}); !errors.Is(err, ErrNotAccepting) {
		t.Fatalf("admitted while not accepting: %v", err)
	}
}

// The invariants hold for every combination of the inputs, not only for the
// cases above.
func TestInvariantsHoldAcrossAllStates(t *testing.T) {
	inputs := []time.Time{time.Time{}, now.Add(-time.Second), now.Add(-10 * time.Minute)}
	lastInputs := []time.Time{time.Time{}, now.Add(-30 * time.Second), now.Add(-time.Hour), now.Add(time.Minute)}
	powers := []PowerSource{PowerUnknown, PowerAC, PowerBattery}
	batteries := []int{-1, 10, 80}
	thermals := []ThermalState{ThermalUnknown, ThermalNormal, ThermalElevated, ThermalCritical}
	networks := []NetworkClass{NetworkUnknown, NetworkUnmetered, NetworkMetered}
	grants := map[string]*Grant{
		"none": nil, "current": grantAll(100), "partial": {IssuedAt: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), Ceilings: map[Resource]int{ResourceCPU: 60}},
		"expired": {IssuedAt: now.Add(-2 * time.Hour), ValidUntil: now.Add(-time.Minute), Ceilings: grantAll(100).Ceilings},
	}
	caps := map[string]Capabilities{"all": allEnforceable(), "none": {}}
	schedules := map[string][]Interval{
		"in":    {{From: now.Add(-time.Hour), To: now.Add(time.Hour)}},
		"out":   {{From: now.Add(time.Hour), To: now.Add(2 * time.Hour)}},
		"empty": nil,
	}

	checked := 0
	for _, at := range inputs {
		for _, last := range lastInputs {
			for _, power := range powers {
				for _, batt := range batteries {
					for _, th := range thermals {
						for _, net := range networks {
							for gname, g := range grants {
								for cname, c := range caps {
									for sname, sched := range schedules {
										for _, optedOut := range []bool{false, true} {
											obs := Observation{At: at, LastUserInput: last, Power: power, BatteryPercent: batt, Thermal: th, Network: net}
											pol := policy()
											pol.OptedOut = optedOut
											pol.Schedule = sched
											d, err := Decide(now, obs, pol, g, c)
											checked++
											assertInvariants(t, d, err, obs, pol, g, c, gname+"/"+cname+"/"+sname)
											// The same reading, when stale, must never be less
											// restrictive than when fresh.
											if at.Equal(now.Add(-10 * time.Minute)) {
												fresh := obs
												fresh.At = now.Add(-time.Second)
												fd, ferr := Decide(now, fresh, pol, g, c)
												if err == nil && ferr == nil {
													assertNotLessRestrictive(t, d, fd, gname+"/"+cname+"/"+sname)
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d states checked; the enumeration is not exercising the rules", checked)
	}
}

func assertInvariants(t *testing.T, d Decision, err error, obs Observation, pol LocalPolicy, g *Grant, c Capabilities, label string) {
	t.Helper()
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf("%s: "+format+"\n  decision: %+v\n  obs: %+v", append([]any{label}, append(args, d, obs)...)...)
	}

	if len(d.Ceilings) != len(Resources) {
		fail("ceilings must cover every resource, got %d", len(d.Ceilings))
	}
	for r, v := range d.Ceilings {
		if v < 0 || v > 100 {
			fail("%s ceiling %d is outside 0..100", r, v)
		}
		if v > 0 && !c[r] {
			fail("%s has a ceiling but the platform cannot enforce it", r)
		}
		if v > pol.Caps[r] {
			fail("%s ceiling %d exceeds the local cap %d", r, v, pol.Caps[r])
		}
		if g != nil && now.Before(g.ValidUntil) && now.After(g.IssuedAt) && v > g.Ceilings[r] {
			fail("%s ceiling %d exceeds the current grant %d", r, v, g.Ceilings[r])
		}
	}
	if d.StopRunning {
		for r, v := range d.Ceilings {
			if v != 0 {
				fail("stopping, but %s ceiling is %d", r, v)
			}
		}
		if d.AcceptNewLeases {
			fail("stopping, but still accepting new leases")
		}
	}
	if pol.OptedOut && (d.AcceptNewLeases || !d.StopRunning) {
		fail("opted out must stop and refuse")
	}
	if !d.AcceptNewLeases {
		if e := d.Admit(map[Resource]int{ResourceCPU: 1}); !errors.Is(e, ErrNotAccepting) {
			fail("not accepting, but Admit returned %v", e)
		}
	}
	if err != nil && (d.AcceptNewLeases || !d.StopRunning) {
		fail("an error must come with the safe decision")
	}
	if obs.At.IsZero() && err == nil {
		fail("a reading without a time must be reported as an error")
	}
	if !reflect.DeepEqual(d, mustRepeat(obs, pol, g, c)) {
		fail("the decision is not deterministic")
	}
}

// assertNotLessRestrictive checks that a stale decision never permits more than
// the same reading when fresh.
func assertNotLessRestrictive(t *testing.T, stale, fresh Decision, label string) {
	t.Helper()
	if stale.AcceptNewLeases && !fresh.AcceptNewLeases {
		t.Fatalf("%s: a stale reading accepts new work that the fresh reading refuses\n  stale: %+v\n  fresh: %+v", label, stale, fresh)
	}
	if stale.StopRunning && !fresh.StopRunning {
		t.Fatalf("%s: a stale reading stops nothing the fresh reading stops\n  stale: %+v\n  fresh: %+v", label, stale, fresh)
	}
	for _, r := range Resources {
		if stale.Ceilings[r] > fresh.Ceilings[r] {
			t.Fatalf("%s: stale %s ceiling %d exceeds the fresh ceiling %d", label, r, stale.Ceilings[r], fresh.Ceilings[r])
		}
	}
}

// A stale reading still carries the stop conditions it recorded.
func TestStaleCriticalHeatStillStopsWork(t *testing.T) {
	obs := fresh()
	obs.Thermal = ThermalCritical
	obs.At = now.Add(-10 * time.Minute)
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if !d.StopRunning || d.AcceptNewLeases || !hasReason(d, ReasonObservationStale) || !hasReason(d, ReasonThermalCritical) {
		t.Fatalf("stale critical heat: %+v", d)
	}
}

func TestStaleLowBatteryStillStopsWork(t *testing.T) {
	obs := fresh()
	obs.Power, obs.BatteryPercent = PowerBattery, 10
	obs.At = now.Add(-10 * time.Minute)
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if !d.StopRunning || !hasReason(d, ReasonBatteryLow) {
		t.Fatalf("stale low battery: %+v", d)
	}
}

// Input time is an absolute fact. A stale reading does not hide it.
func TestStaleReadingKeepsRecentUserInputAsPresence(t *testing.T) {
	obs := fresh()
	obs.LastUserInput = now.Add(-20 * time.Second)
	obs.At = now.Add(-10 * time.Minute)
	d := mustDecide(t, obs, policy(), grantAll(100), allEnforceable())
	if !d.StopRunning || !hasReason(d, ReasonUserPresent) {
		t.Fatalf("stale reading hid recent user input: %+v", d)
	}
}

func TestGrantWithoutAnIssueTimeOrWindowIsRejected(t *testing.T) {
	noIssue := grantAll(100)
	noIssue.IssuedAt = time.Time{}
	noWindow := grantAll(100)
	noWindow.ValidUntil = noWindow.IssuedAt
	for name, g := range map[string]*Grant{"no issue time": noIssue, "empty window": noWindow} {
		d, err := Decide(now, fresh(), policy(), g, allEnforceable())
		if !errors.Is(err, ErrInvalidGrant) || d.AcceptNewLeases || !d.StopRunning {
			t.Errorf("%s: err=%v decision=%+v", name, err, d)
		}
	}
}

// A malformed reading is an error even when opt-out or a missing grant would
// otherwise explain the stop.
func TestInvalidObservationIsReportedEvenWithoutGrantOrOptOut(t *testing.T) {
	obs := fresh()
	obs.At = time.Time{}
	optedOut := policy()
	optedOut.OptedOut = true
	for name, c := range map[string]struct {
		pol   LocalPolicy
		grant *Grant
	}{
		"opted out":     {optedOut, grantAll(100)},
		"missing grant": {policy(), nil},
		"valid grant":   {policy(), grantAll(100)},
	} {
		d, err := Decide(now, obs, c.pol, c.grant, allEnforceable())
		if !errors.Is(err, ErrInvalidObservation) || d.AcceptNewLeases || !d.StopRunning {
			t.Errorf("%s: err=%v decision=%+v", name, err, d)
		}
	}
}

// A zero central ceiling is explained by the grant, not left silent.
func TestZeroCentralCeilingIsExplained(t *testing.T) {
	g := grantAll(80)
	g.Ceilings[ResourceCPU] = 0
	d := mustDecide(t, fresh(), policy(), g, allEnforceable())
	if d.Ceilings[ResourceCPU] != 0 || !hasReason(d, ReasonGrantCap) {
		t.Fatalf("zero grant for cpu: ceilings=%v reasons=%v", d.Ceilings, d.Reasons)
	}
}

// A reason names only a limit that binds on the ceiling it explains.
func TestReasonsNameOnlyTheLimitThatBinds(t *testing.T) {
	battery := fresh()
	battery.Power, battery.BatteryPercent = PowerBattery, 80
	d := mustDecide(t, battery, policy(), grantAll(100), allEnforceable())
	if hasReason(d, ReasonLocalCap) || !hasReason(d, ReasonOnBattery) {
		t.Fatalf("battery binds, not the local cap: reasons=%v", d.Reasons)
	}

	thermal := fresh()
	thermal.Thermal = ThermalElevated
	d = mustDecide(t, thermal, policy(), grantAll(50), allEnforceable())
	if hasReason(d, ReasonGrantCap) || !hasReason(d, ReasonThermalElevated) {
		t.Fatalf("thermal binds, not the grant: reasons=%v", d.Reasons)
	}
}

func mustRepeat(obs Observation, pol LocalPolicy, g *Grant, c Capabilities) Decision {
	d, _ := Decide(now, obs, pol, g, c)
	return d
}
