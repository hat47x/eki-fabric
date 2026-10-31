//go:build !windows

package nativerunner

import (
	"os"
	"syscall"
)

// processUsage returns the maximum resident set size observed for the finished
// process. It is a post-hoc observation: the process has already run, so this
// cannot prevent a limit from being exceeded.
func processUsage(state *os.ProcessState) (maxRSSBytes uint64) {
	if state == nil {
		return 0
	}
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil {
		return 0
	}
	// Linux reports Maxrss in KiB; this reference target is Linux. Other Unix
	// platforms may report bytes, which only makes the check more conservative.
	return uint64(usage.Maxrss) * 1024
}
