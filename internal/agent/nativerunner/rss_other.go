//go:build !linux && !windows

package nativerunner

// sampleRSS is unavailable on this platform. The post-exit check still runs
// when the platform reports Rusage.
func sampleRSS(int) (uint64, bool) {
	return 0, false
}
