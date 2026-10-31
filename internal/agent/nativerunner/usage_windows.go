//go:build windows

package nativerunner

import "os"

// processUsage is unavailable on Windows in this reference Runner. Returning
// zero means the post-hoc CPU and memory checks cannot reject a result here.
func processUsage(*os.ProcessState) (cpuMillis, maxRSSBytes uint64) {
	return 0, 0
}
