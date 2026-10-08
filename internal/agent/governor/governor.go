// Package governor decides, on the endpoint, how much of the machine the Worker
// may offer right now.
//
// The decision is made locally and never waits for the Control Plane. The
// central side can only narrow what a person has allowed here: the effective
// ceiling for each resource is the smallest of the central grant, the local
// policy, and the limits derived from the endpoint's state. An unknown or stale
// reading counts as the most restrictive state. This follows the human-first
// rule (RQ-005), immediate local opt-out (RQ-006), local bounds that central
// cannot raise (RQ-007), and energy and device safety (RQ-053).
//
// Decide always returns a Decision that is safe to act on. When its inputs are
// invalid it returns the safe decision together with an error, so a caller that
// ignores the error still stops and admits nothing.
//
// Decide does not enforce anything. Enforcement belongs to the platform and is
// reported to Decide as Capabilities. A resource the platform cannot enforce
// gets a ceiling of zero, so the Worker never claims a limit it cannot hold
// (RQ-047).
package governor

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// Resource is a kind of machine capacity the governor decides about.
type Resource string

const (
	ResourceCPU     Resource = "cpu"
	ResourceMemory  Resource = "memory"
	ResourceGPU     Resource = "gpu"
	ResourceNetwork Resource = "network"
	ResourceDiskIO  Resource = "disk_io"
)

// Resources lists every resource the governor decides about, in a fixed order.
var Resources = []Resource{ResourceCPU, ResourceMemory, ResourceGPU, ResourceNetwork, ResourceDiskIO}

func (r Resource) valid() bool {
	for _, k := range Resources {
		if k == r {
			return true
		}
	}
	return false
}

// PowerSource is where the endpoint draws its power from.
type PowerSource string

const (
	PowerUnknown PowerSource = "unknown"
	PowerAC      PowerSource = "ac"
	PowerBattery PowerSource = "battery"
)

func (p PowerSource) valid() bool {
	return p == PowerUnknown || p == PowerAC || p == PowerBattery
}

// ThermalState is the platform's thermal assessment.
type ThermalState string

const (
	ThermalUnknown  ThermalState = "unknown"
	ThermalNormal   ThermalState = "normal"
	ThermalElevated ThermalState = "elevated"
	ThermalCritical ThermalState = "critical"
)

func (t ThermalState) valid() bool {
	switch t {
	case ThermalUnknown, ThermalNormal, ThermalElevated, ThermalCritical:
		return true
	}
	return false
}

// NetworkClass describes the current connection's cost. Bulk transfer on a
// metered connection is refused, so the network ceiling is zero unless the
// connection is known to be unmetered.
type NetworkClass string

const (
	NetworkUnknown   NetworkClass = "unknown"
	NetworkUnmetered NetworkClass = "unmetered"
	NetworkMetered   NetworkClass = "metered"
)

func (n NetworkClass) valid() bool {
	return n == NetworkUnknown || n == NetworkUnmetered || n == NetworkMetered
}

// Observation is one reading of the endpoint's state, taken at At.
//
// LastUserInput is the time of the most recent keyboard, mouse, or touch input.
// The zero value means the platform cannot report it, which is treated as
// unknown rather than as absent.
type Observation struct {
	At             time.Time
	LastUserInput  time.Time
	Power          PowerSource
	BatteryPercent int // 0..100, or -1 when unknown
	Thermal        ThermalState
	Network        NetworkClass
}

func (o Observation) validate() error {
	if o.At.IsZero() {
		return errors.New("reading time is required")
	}
	if !o.Power.valid() {
		return fmt.Errorf("unknown power source %q", o.Power)
	}
	if !o.Thermal.valid() {
		return fmt.Errorf("unknown thermal state %q", o.Thermal)
	}
	if !o.Network.valid() {
		return fmt.Errorf("unknown network class %q", o.Network)
	}
	if o.BatteryPercent != -1 && (o.BatteryPercent < 0 || o.BatteryPercent > 100) {
		return fmt.Errorf("battery percent %d is outside 0..100 and not -1", o.BatteryPercent)
	}
	return nil
}

// unknownState is what a stale reading is treated as: nothing is known about
// the endpoint, so every restriction that depends on it applies.
func unknownState(at time.Time) Observation {
	return Observation{
		At:             at,
		Power:          PowerUnknown,
		BatteryPercent: -1,
		Thermal:        ThermalUnknown,
		Network:        NetworkUnknown,
	}
}

// Interval is a span of absolute time during which local policy allows work.
// Callers expand recurring schedules into concrete intervals.
type Interval struct {
	From time.Time
	To   time.Time
}

// LocalPolicy is what the person on this endpoint has allowed. It comes from
// local configuration, never from the Control Plane.
type LocalPolicy struct {
	// OptedOut stops all work and refuses all new work, whatever else is true.
	OptedOut bool
	// ActiveWithin is how recent a user input must be to count as the person
	// being present. It must be positive.
	ActiveWithin time.Duration
	// MaxObservationAge is how old a reading may be before it is discarded and
	// replaced with unknown. It must be positive.
	MaxObservationAge time.Duration
	// BatteryStopPercent stops work on battery below this level. It is 1..100.
	BatteryStopPercent int
	// Schedule lists the intervals during which work is allowed. An empty
	// schedule allows nothing: a deployment must say when it may run.
	Schedule []Interval
	// Caps is the local ceiling, in percent, for each resource. A resource with
	// no entry gets a ceiling of zero.
	Caps map[Resource]int
}

func (p LocalPolicy) validate() error {
	if p.ActiveWithin <= 0 {
		return errors.New("active window must be positive")
	}
	if p.MaxObservationAge <= 0 {
		return errors.New("maximum observation age must be positive")
	}
	if p.BatteryStopPercent < 1 || p.BatteryStopPercent > 100 {
		return errors.New("battery stop level must be 1..100")
	}
	if err := validateCeilings(p.Caps); err != nil {
		return err
	}
	for _, iv := range p.Schedule {
		if iv.From.IsZero() || iv.To.IsZero() || !iv.From.Before(iv.To) {
			return errors.New("each schedule interval needs a start before its end")
		}
	}
	return nil
}

// Grant is the central side's offer to use part of this endpoint. It can only
// narrow what local policy allows.
type Grant struct {
	IssuedAt   time.Time
	ValidUntil time.Time
	// Ceilings is the central ceiling, in percent, for each resource offered.
	Ceilings map[Resource]int
}

func validateCeilings(c map[Resource]int) error {
	for r, v := range c {
		if !r.valid() {
			return fmt.Errorf("unknown resource %q", r)
		}
		if v < 0 || v > 100 {
			return fmt.Errorf("ceiling for %s is %d, outside 0..100", r, v)
		}
	}
	return nil
}

// Capabilities reports which resources the platform can actually enforce on
// this endpoint. It is detected at start, not assumed.
type Capabilities map[Resource]bool

// Reason explains why a restriction applies. Reasons are stable codes.
type Reason string

const (
	ReasonPolicyInvalid       Reason = "POLICY_INVALID"
	ReasonObservationInvalid  Reason = "OBSERVATION_INVALID"
	ReasonObservationStale    Reason = "OBSERVATION_STALE"
	ReasonOptedOut            Reason = "OPTED_OUT"
	ReasonOutsideSchedule     Reason = "OUTSIDE_SCHEDULE"
	ReasonGrantMissing        Reason = "GRANT_MISSING"
	ReasonGrantNotCurrent     Reason = "GRANT_NOT_CURRENT"
	ReasonGrantInvalid        Reason = "GRANT_INVALID"
	ReasonUserPresent         Reason = "USER_PRESENT"
	ReasonUserUnknown         Reason = "USER_UNKNOWN"
	ReasonThermalCritical     Reason = "THERMAL_CRITICAL"
	ReasonThermalElevated     Reason = "THERMAL_ELEVATED"
	ReasonThermalUnknown      Reason = "THERMAL_UNKNOWN"
	ReasonBatteryLow          Reason = "BATTERY_LOW"
	ReasonOnBattery           Reason = "ON_BATTERY"
	ReasonBatteryLevelUnknown Reason = "BATTERY_LEVEL_UNKNOWN"
	ReasonPowerUnknown        Reason = "POWER_UNKNOWN"
	ReasonNetworkNotUnmetered Reason = "NETWORK_NOT_UNMETERED"
	ReasonLocalCap            Reason = "LOCAL_CAP"
	ReasonGrantCap            Reason = "GRANT_CAP"
	ReasonNotEnforceable      Reason = "NOT_ENFORCEABLE"
	ReasonNoCapacity          Reason = "NO_CAPACITY"
)

// Decision is what the endpoint may do at one moment.
type Decision struct {
	// AcceptNewLeases is false when no new work may start.
	AcceptNewLeases bool
	// StopRunning is true when running work must stop now. The agent stops it
	// gracefully and then forcibly, as the platform allows.
	StopRunning bool
	// Ceilings holds the effective ceiling, in percent, for every resource in
	// Resources. Zero means no use is allowed for that resource. It is
	// all zero when StopRunning is true.
	Ceilings map[Resource]int
	// NotEnforceable lists resources the grant and policy would allow but the
	// platform cannot enforce, so their ceilings are zero.
	NotEnforceable []Resource
	// Reasons lists why restrictions apply, sorted and without duplicates.
	Reasons []Reason
}

var (
	ErrInvalidPolicy      = errors.New("invalid local policy")
	ErrInvalidObservation = errors.New("invalid observation")
	ErrInvalidGrant       = errors.New("invalid grant")
	ErrNotAccepting       = errors.New("endpoint is not accepting new work")
	ErrInvalidRequirement = errors.New("invalid resource requirement")
	ErrCapacityExceeded   = errors.New("requested capacity exceeds the current ceiling")
)

// Ceilings used when the state is uncertain but work may continue carefully.
const (
	cautiousCeiling = 25
	batteryCeiling  = 50
)

// Decide returns what the endpoint may do at now. It never waits and never
// performs I/O.
func Decide(now time.Time, obs Observation, pol LocalPolicy, grant *Grant, caps Capabilities) (Decision, error) {
	if err := pol.validate(); err != nil {
		return safeDecision(ReasonPolicyInvalid), fmt.Errorf("%w: %v", ErrInvalidPolicy, err)
	}
	if pol.OptedOut {
		return safeDecision(ReasonOptedOut), nil
	}

	// Without a current grant there is no authority to offer anything.
	if grant == nil {
		return safeDecision(ReasonGrantMissing), nil
	}
	if now.Before(grant.IssuedAt) || !now.Before(grant.ValidUntil) {
		return safeDecision(ReasonGrantNotCurrent), nil
	}
	if err := validateCeilings(grant.Ceilings); err != nil {
		return safeDecision(ReasonGrantInvalid), fmt.Errorf("%w: %v", ErrInvalidGrant, err)
	}

	reasons := reasonSet{}
	state := obs
	switch {
	case obs.validate() != nil:
		return safeDecision(ReasonObservationInvalid), fmt.Errorf("%w: %v", ErrInvalidObservation, obs.validate())
	case now.Before(obs.At):
		return safeDecision(ReasonObservationInvalid), fmt.Errorf("%w: reading is from the future", ErrInvalidObservation)
	case now.Sub(obs.At) > pol.MaxObservationAge:
		reasons.add(ReasonObservationStale)
		state = unknownState(obs.At)
	}

	stop := false
	accept := true
	stateCap := make(map[Resource]int, len(Resources))
	for _, r := range Resources {
		stateCap[r] = 100
	}
	limit := func(r Resource, v int) {
		if v < stateCap[r] {
			stateCap[r] = v
		}
	}
	limitAll := func(v int) {
		for _, r := range Resources {
			limit(r, v)
		}
	}

	if !inSchedule(now, pol.Schedule) {
		reasons.add(ReasonOutsideSchedule)
		stop = true
	}

	// The person comes first. Recent input means work must stop now; a
	// missing or future input time means the person's presence is unknown.
	switch presenceAt(now, state.LastUserInput, pol.ActiveWithin) {
	case presenceActive:
		reasons.add(ReasonUserPresent)
		stop = true
	case presenceUnknown:
		reasons.add(ReasonUserUnknown)
		accept = false
		limitAll(cautiousCeiling)
	}

	switch state.Thermal {
	case ThermalCritical:
		reasons.add(ReasonThermalCritical)
		stop = true
	case ThermalElevated:
		reasons.add(ReasonThermalElevated)
		limitAll(cautiousCeiling)
	case ThermalUnknown:
		reasons.add(ReasonThermalUnknown)
		accept = false
		limitAll(cautiousCeiling)
	}

	switch state.Power {
	case PowerBattery:
		switch {
		case state.BatteryPercent < 0:
			reasons.add(ReasonBatteryLevelUnknown)
			accept = false
			limitAll(cautiousCeiling)
		case state.BatteryPercent < pol.BatteryStopPercent:
			reasons.add(ReasonBatteryLow)
			stop = true
		default:
			reasons.add(ReasonOnBattery)
			limitAll(batteryCeiling)
		}
	case PowerUnknown:
		reasons.add(ReasonPowerUnknown)
		accept = false
		limitAll(cautiousCeiling)
	}

	// Bulk transfer over a metered or unknown connection is refused.
	if state.Network != NetworkUnmetered {
		reasons.add(ReasonNetworkNotUnmetered)
		limit(ResourceNetwork, 0)
	}

	ceilings := make(map[Resource]int, len(Resources))
	var notEnforceable []Resource
	for _, r := range Resources {
		g := grant.Ceilings[r]
		l := pol.Caps[r]
		v := min(g, l, stateCap[r])
		if g > 0 && l < g {
			reasons.add(ReasonLocalCap)
		}
		if l > 0 && g > 0 && g < l {
			reasons.add(ReasonGrantCap)
		}
		if v > 0 && !caps[r] {
			notEnforceable = append(notEnforceable, r)
			reasons.add(ReasonNotEnforceable)
			v = 0
		}
		ceilings[r] = v
	}

	if stop {
		for _, r := range Resources {
			ceilings[r] = 0
		}
		accept = false
	}
	if !anyPositive(ceilings) {
		reasons.add(ReasonNoCapacity)
		accept = false
	}

	sort.Slice(notEnforceable, func(i, j int) bool { return notEnforceable[i] < notEnforceable[j] })
	return Decision{
		AcceptNewLeases: accept,
		StopRunning:     stop,
		Ceilings:        ceilings,
		NotEnforceable:  notEnforceable,
		Reasons:         reasons.sorted(),
	}, nil
}

// Admit reports whether a new lease needing the given percentages of each
// resource may start now. Every requested resource must be within the current
// ceiling. An empty requirement is refused, because a lease must say what it
// needs.
func (d Decision) Admit(need map[Resource]int) error {
	if !d.AcceptNewLeases {
		return ErrNotAccepting
	}
	if len(need) == 0 {
		return fmt.Errorf("%w: no resource is named", ErrInvalidRequirement)
	}
	for r, p := range need {
		if !r.valid() {
			return fmt.Errorf("%w: unknown resource %q", ErrInvalidRequirement, r)
		}
		if p < 1 || p > 100 {
			return fmt.Errorf("%w: %s needs %d, outside 1..100", ErrInvalidRequirement, r, p)
		}
		if d.Ceilings[r] < p {
			return fmt.Errorf("%w: %s needs %d, ceiling is %d", ErrCapacityExceeded, r, p, d.Ceilings[r])
		}
	}
	return nil
}

// safeDecision is what the endpoint does when it cannot judge: stop, and admit
// nothing.
func safeDecision(reasons ...Reason) Decision {
	ceilings := make(map[Resource]int, len(Resources))
	for _, r := range Resources {
		ceilings[r] = 0
	}
	set := reasonSet{}
	for _, r := range reasons {
		set.add(r)
	}
	return Decision{
		AcceptNewLeases: false,
		StopRunning:     true,
		Ceilings:        ceilings,
		Reasons:         set.sorted(),
	}
}

type presence int

const (
	presenceUnknown presence = iota
	presenceIdle
	presenceActive
)

func presenceAt(now, lastInput time.Time, activeWithin time.Duration) presence {
	if lastInput.IsZero() || lastInput.After(now) {
		return presenceUnknown
	}
	if now.Sub(lastInput) < activeWithin {
		return presenceActive
	}
	return presenceIdle
}

func inSchedule(now time.Time, schedule []Interval) bool {
	for _, iv := range schedule {
		if !now.Before(iv.From) && now.Before(iv.To) {
			return true
		}
	}
	return false
}

func anyPositive(c map[Resource]int) bool {
	for _, v := range c {
		if v > 0 {
			return true
		}
	}
	return false
}

type reasonSet map[Reason]struct{}

func (s reasonSet) add(r Reason) { s[r] = struct{}{} }

func (s reasonSet) sorted() []Reason {
	out := make([]Reason, 0, len(s))
	for r := range s {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
