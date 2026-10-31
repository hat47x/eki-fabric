//go:build linux

package nativerunner

import (
	"os"
	"strconv"
	"strings"
)

// sampleRSS reads VmRSS from procfs.
func sampleRSS(pid int) (uint64, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kib, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return kib * 1024, true
	}
	return 0, false
}

// sampleCPUMillis reads utime and stime from procfs.
func sampleCPUMillis(pid int) (uint64, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	text := string(data)
	end := strings.LastIndex(text, ")")
	if end < 0 || end+2 >= len(text) {
		return 0, false
	}
	fields := strings.Fields(text[end+2:])
	// fields[0] is the process state (field 3 of stat). utime is field 14 and
	// stime is field 15, so they are fields[11] and fields[12] here.
	if len(fields) < 13 {
		return 0, false
	}
	user, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, false
	}
	system, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, false
	}
	// USER_HZ is 100 on the Linux targets this reference runner supports.
	return (user + system) * 10, true
}
