//go:build windows

package nativerunner

import "os"

// processUsage reports no memory figure on Windows in this reference Runner.
// CPU time is read from os.ProcessState and is checked on Windows.
func processUsage(*os.ProcessState) (maxRSSBytes uint64) {
	return 0
}
