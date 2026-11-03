// Package measurement counts the events ADR-0039 decided EKI would count.
//
// It counts events, never people: the four numbers here are how often EKI refused
// or reclaimed something. Nothing in this package identifies a Client, a Worker,
// or a beneficiary.
package measurement

import (
	"sort"
	"sync"
)

// Kind is one of the events ADR-0039 §2.1 counts.
type Kind string

const (
	// AdmissionRejected is a request that was not accepted. The reason says why:
	// "auth", "duplicate" or "limit".
	AdmissionRejected Kind = "admission.rejected"
	// LeaseReclaimed is a lease the Control Plane took back. The reason is
	// "expired" or "revoked".
	LeaseReclaimed Kind = "lease.reclaimed"
	// ClaimRefused is a result report the Control Plane did not accept.
	ClaimRefused Kind = "claim.refused"
	// LimitRefused is an output the Worker refused to send because it was over
	// the limit.
	LimitRefused Kind = "limit.refused"
	// AuditUnrecorded is an event the Control Plane could not write to the audit
	// sink but carried out anyway (ADR-0040 §5). It is counted so the gap is not
	// silent: the answer itself said nothing about it.
	AuditUnrecorded Kind = "audit.unrecorded"
)

// Valid reports whether the kind is one this package counts.
func (k Kind) Valid() bool {
	switch k {
	case AdmissionRejected, LeaseReclaimed, ClaimRefused, LimitRefused, AuditUnrecorded:
		return true
	}
	return false
}

// Counters keeps the totals. It is safe for concurrent use.
type Counters struct {
	mu     sync.Mutex
	counts map[Kind]map[string]uint64
}

// New returns empty counters.
func New() *Counters { return &Counters{counts: map[Kind]map[string]uint64{}} }

// Add records one event. An unknown kind is ignored rather than invented: a
// counter nobody reads is worse than a missing one, because it looks like
// coverage.
func (c *Counters) Add(kind Kind, reason string) {
	if c == nil || !kind.Valid() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts[kind] == nil {
		c.counts[kind] = map[string]uint64{}
	}
	c.counts[kind][reason]++
}

// Total is how many events of that kind were recorded, whatever the reason.
func (c *Counters) Total(kind Kind) uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var total uint64
	for _, n := range c.counts[kind] {
		total += n
	}
	return total
}

// Reasons is the per-reason breakdown, copied so a caller cannot change the
// counters by writing to the map it got back.
func (c *Counters) Reasons(kind Kind) map[string]uint64 {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]uint64, len(c.counts[kind]))
	for reason, n := range c.counts[kind] {
		out[reason] = n
	}
	return out
}

// Count is one line of a snapshot.
type Count struct {
	Kind   Kind
	Reason string
	Count  uint64
}

// Snapshot returns every count in a stable order, which is what a caller writes
// to a log or a metrics endpoint. Two snapshots taken in sequence can be compared
// line by line.
func (c *Counters) Snapshot() []Count {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Count, 0)
	for kind, reasons := range c.counts {
		for reason, n := range reasons {
			out = append(out, Count{Kind: kind, Reason: reason, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Reason < out[j].Reason
	})
	return out
}
