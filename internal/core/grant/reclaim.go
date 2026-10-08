package grant

import (
	"cmp"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// Bounds on reclaim planning input. A value outside a bound is refused.
const (
	// MaxReclaimGrants bounds the grants planned in one call.
	MaxReclaimGrants = 16384
	// MaxReclaimSlots bounds the number of distinct stop instants in a window.
	MaxReclaimSlots = 3600
	// MaxReclaimWindow bounds the grace window in which stops are spread.
	MaxReclaimWindow = 24 * time.Hour
	// MaxRunningLeases bounds the running lease count passed to PlanReclaim.
	MaxRunningLeases = 1 << 20

	// minSlotWidth keeps two stop instants at least one second apart, so a
	// slot is a real instant and not a sub-second rounding artifact.
	minSlotWidth = time.Second

	reclaimDomain = "eki.grant.reclaim-slot.v1"
)

// ReclaimConfig sets the grace window that PlanReclaim spreads stops across.
type ReclaimConfig struct {
	// Window is the length of the grace period, measured from the effective time.
	Window time.Duration
	// Slots is the number of distinct stop instants inside Window. Slot i stops
	// at offset i * (Window / Slots).
	Slots int
	// MaxLeasesPerStop is the expected number of running leases one instant can
	// absorb. PlanReclaim uses it only to size the window, as described there.
	MaxLeasesPerStop int
}

// ReclaimAssignment is the planned stop for one revoked grant. It is data only.
type ReclaimAssignment struct {
	// Grant is the identity of the revoked grant.
	Grant contract.Digest
	// Slot is the stop instant index, from 0 to Slots-1.
	Slot int
	// Offset is the time after the effective time at which the grant's leases stop.
	Offset time.Duration
	// StopAt is the effective time plus Offset.
	StopAt time.Time
}

// PlanReclaim returns the stop assignments for grants that were all revoked at
// effectiveAt, so that their leases do not all stop at the same instant. It
// returns data only. It schedules nothing, performs no I/O, and reads no clock.
// The caller decides when to act on each assignment.
//
// Placement is deterministic. Each grant starts at the slot chosen by a SHA-256
// hash of its digest, and a grant that finds its slot full moves on to the next
// slot. Each slot holds at most ceil(n/Slots) grants, where n is the number of
// grants. No instant therefore receives more than that many stops. When n is at
// most Slots, every grant gets its own slot. Input order does not matter.
//
// runningLeases sets only a floor on the slot count. The window must have at
// least ceil(runningLeases/MaxLeasesPerStop) slots, or the plan is refused. The
// package does not know how many leases each grant holds, so this is a sizing
// check and not a guarantee about leases at any one instant.
//
// Every grant must have a revocation recorded in ledger at effectiveAt. A nil
// ledger is refused, because it cannot show that the grants were revoked.
//
// An empty grant list returns nil with no error.
func PlanReclaim(ledger *Ledger, revoked []Grant, effectiveAt time.Time, runningLeases int, cfg ReclaimConfig) ([]ReclaimAssignment, error) {
	if err := checkReclaimConfig(cfg); err != nil {
		return nil, err
	}
	if ledger == nil {
		return nil, ErrNilLedger
	}
	if effectiveAt.IsZero() {
		return nil, fmt.Errorf("%w: reclaim effective time", ErrMissingField)
	}
	if runningLeases < 0 || runningLeases > MaxRunningLeases {
		return nil, ErrInvalidRunningLeases
	}
	if ceilDiv(runningLeases, cfg.MaxLeasesPerStop) > cfg.Slots {
		return nil, ErrInsufficientSlots
	}
	if len(revoked) > MaxReclaimGrants {
		return nil, ErrTooManyGrants
	}
	at := normalizeTime(effectiveAt)

	type item struct {
		digest contract.Digest
		key    string
	}
	items := make([]item, 0, len(revoked))
	for _, g := range revoked {
		if !g.issued {
			return nil, ErrNilGrant
		}
		rev, ok := ledger.revocationOf(g)
		if !ok {
			return nil, ErrGrantNotRevoked
		}
		if !rev.EffectiveAt.Equal(at) {
			return nil, ErrRevocationTimeMismatch
		}
		d := g.Digest()
		items = append(items, item{digest: d, key: d.String()})
	}
	slices.SortFunc(items, func(a, b item) int {
		return cmp.Compare(a.key, b.key)
	})
	for i := 1; i < len(items); i++ {
		if items[i].key == items[i-1].key {
			return nil, ErrDuplicateGrant
		}
	}
	n := len(items)
	if n == 0 {
		return nil, nil
	}

	slots := cfg.Slots
	step := cfg.Window / time.Duration(slots)
	perSlot := ceilDiv(n, slots)
	counts := make([]int, slots)
	out := make([]ReclaimAssignment, 0, n)
	for _, it := range items {
		start := int(slotHash(it.key) % uint64(slots))
		placed := false
		for k := 0; k < slots; k++ {
			s := (start + k) % slots
			if counts[s] >= perSlot {
				continue
			}
			counts[s]++
			offset := time.Duration(s) * step
			out = append(out, ReclaimAssignment{
				Grant:  it.digest,
				Slot:   s,
				Offset: offset,
				StopAt: at.Add(offset),
			})
			placed = true
			break
		}
		if !placed {
			// Total capacity is slots*perSlot, which is at least n, so this is
			// unreachable. It is still refused rather than silently dropping a grant.
			return nil, ErrInsufficientSlots
		}
	}
	slices.SortFunc(out, func(a, b ReclaimAssignment) int {
		if c := cmp.Compare(a.Slot, b.Slot); c != 0 {
			return c
		}
		return cmp.Compare(a.Grant.String(), b.Grant.String())
	})
	return out, nil
}

func checkReclaimConfig(cfg ReclaimConfig) error {
	if cfg.Window <= 0 || cfg.Window > MaxReclaimWindow {
		return ErrInvalidReclaimConfig
	}
	if cfg.Slots < 1 || cfg.Slots > MaxReclaimSlots {
		return ErrInvalidReclaimConfig
	}
	if cfg.Window/time.Duration(cfg.Slots) < minSlotWidth {
		return ErrInvalidReclaimConfig
	}
	if cfg.MaxLeasesPerStop < 1 || cfg.MaxLeasesPerStop > MaxRunningLeases {
		return ErrInvalidReclaimConfig
	}
	return nil
}

// slotHash maps a grant key to a stable 64-bit value. It uses SHA-256 over a
// domain tag and the key, so the result does not depend on any random source.
func slotHash(key string) uint64 {
	sum := sha256.Sum256([]byte(reclaimDomain + key))
	return binary.BigEndian.Uint64(sum[:8])
}

// ceilDiv returns ceil(a/b) for a >= 0 and b >= 1.
func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}
