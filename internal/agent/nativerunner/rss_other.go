//go:build !linux && !windows

package nativerunner

// sampleRSS and sampleCPUMillis are unavailable on this platform. The post-exit
// Rusage checks still run when the platform reports them.
func sampleRSS(int) (uint64, bool) {
	return 0, false
}

func sampleCPUMillis(int) (uint64, bool) {
	return 0, false
}
