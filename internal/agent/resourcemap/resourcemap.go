// Package resourcemap turns a Task's absolute resource request into the
// percentages the Local Governor judges (ADR-0037 §11.1, §11.4).
//
// It is a separate package because the mapping is a decision, not plumbing: a
// number invented at the call site ("just use 10") would make the Governor look
// like it judged something when it did not.
package resourcemap

import (
	"errors"
	"fmt"

	"github.com/hat47x/eki-fabric/internal/agent/governor"
)

// Host is what this machine has to offer. A zero total means that resource cannot
// be judged: no percentage means anything without a denominator.
type Host struct {
	CPUMillis   int64
	MemoryBytes int64
	DiskBytes   int64
}

// Known reports whether the host can be used as a denominator for CPU and memory.
func (h Host) Known() bool { return h.CPUMillis > 0 && h.MemoryBytes > 0 }

// KnownDisk reports whether disk can be judged. It is asked separately because a
// deployment may know its CPU and memory and not its disk (ADR-0037 §11.4).
func (h Host) KnownDisk() bool { return h.DiskBytes > 0 }

// ErrUnknownHost means the deployment did not say what this machine has, so no
// percentage can be computed.
var ErrUnknownHost = errors.New("resourcemap: host totals are not configured")

// Request is what a WorkOrder asks for, in absolute units.
type Request struct {
	CPUMillis   int64
	MemoryBytes int64
	// DiskBytes is temporary disk. It is judged against the host's total capacity,
	// not its free space: a moving denominator would make the same request pass or
	// fail depending on what else is running (ADR-0037 §11.4).
	DiskBytes int64
}

// Demand returns the percentages the request needs, each between 1 and 100.
//
// Resources are kept apart rather than summed because they are separate limits;
// the caller asks Strictest for the one that decides admission.
func Demand(req Request, host Host) (map[governor.Resource]int, error) {
	if !host.Known() {
		return nil, ErrUnknownHost
	}
	if req.CPUMillis < 0 || req.MemoryBytes < 0 || req.DiskBytes < 0 {
		return nil, fmt.Errorf("resourcemap: negative request (%d cpu, %d memory, %d disk)", req.CPUMillis, req.MemoryBytes, req.DiskBytes)
	}
	need := map[governor.Resource]int{}
	if req.CPUMillis > 0 {
		need[governor.ResourceCPU] = percent(req.CPUMillis, host.CPUMillis)
	}
	if req.MemoryBytes > 0 {
		need[governor.ResourceMemory] = percent(req.MemoryBytes, host.MemoryBytes)
	}
	// Disk is optional: a host that does not report its capacity gets no judgement
	// for disk, rather than a made-up denominator.
	if req.DiskBytes > 0 && host.KnownDisk() {
		need[governor.ResourceDiskIO] = percent(req.DiskBytes, host.DiskBytes)
	}
	return need, nil
}

// Strictest returns the single percentage that decides admission, or 0 when the
// request asks for nothing.
func Strictest(need map[governor.Resource]int) int {
	strictest := 0
	for _, value := range need {
		if value > strictest {
			strictest = value
		}
	}
	return strictest
}

// percent rounds up and clamps to 1..100. Rounding up is deliberate: a request
// that needs a sliver of the machine still needs the machine, and rounding it to 0
// would make "there is a request" look like "there is none".
func percent(part, whole int64) int {
	if part <= 0 {
		return 0
	}
	value := (part*100 + whole - 1) / whole
	if value < 1 {
		return 1
	}
	if value > 100 {
		return 100
	}
	return int(value)
}
