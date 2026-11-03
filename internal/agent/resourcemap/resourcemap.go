// Package resourcemap turns a Task's absolute resource request into the
// percentages the Local Governor judges (ADR-0037 §11.1).
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

// Host is what this machine has to offer. Both values must be known before any
// percentage means anything.
type Host struct {
	CPUMillis   int64
	MemoryBytes int64
}

// Known reports whether the host can be used as a denominator.
func (h Host) Known() bool { return h.CPUMillis > 0 && h.MemoryBytes > 0 }

// ErrUnknownHost means the deployment did not say what this machine has, so no
// percentage can be computed.
var ErrUnknownHost = errors.New("resourcemap: host totals are not configured")

// Demand returns the strictest percentage the request needs, between 1 and 100.
//
// The strictest one, not the sum: CPU and memory are separate limits, and adding
// them would count one short resource twice and refuse work that would fit.
func Demand(cpuMillis, memoryBytes int64, host Host) (map[governor.Resource]int, error) {
	if !host.Known() {
		return nil, ErrUnknownHost
	}
	if cpuMillis < 0 || memoryBytes < 0 {
		return nil, fmt.Errorf("resourcemap: negative request (%d cpu, %d memory)", cpuMillis, memoryBytes)
	}
	need := map[governor.Resource]int{}
	if cpuMillis > 0 {
		need[governor.ResourceCPU] = percent(cpuMillis, host.CPUMillis)
	}
	if memoryBytes > 0 {
		need[governor.ResourceMemory] = percent(memoryBytes, host.MemoryBytes)
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
