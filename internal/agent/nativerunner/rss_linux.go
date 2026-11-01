//go:build linux

package nativerunner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// processGroupPIDs returns the PIDs whose process group ID is pgid. The runner
// starts the command as a process-group leader (pgid == pid), and bubblewrap's
// child stays in that group, so summing the group also covers sandboxed
// children.
func processGroupPIDs(pgid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	pids := make([]int, 0, 4)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue
		}
		text := string(stat)
		end := strings.LastIndex(text, ")")
		if end < 0 || end+2 >= len(text) {
			continue
		}
		fields := strings.Fields(text[end+2:])
		if len(fields) < 3 {
			continue
		}
		group, err := strconv.Atoi(fields[2])
		if err != nil || group != pgid {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

// sampleRSS sums VmRSS of the process group.
func sampleRSS(pgid int) (uint64, bool) {
	var total uint64
	found := false
	for _, pid := range processGroupPIDs(pgid) {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "VmRSS:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				break
			}
			kib, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				break
			}
			total += kib * 1024
			found = true
			break
		}
	}
	return total, found
}

// sampleCPUMillis sums utime and stime of the process group.
func sampleCPUMillis(pgid int) (uint64, bool) {
	var ticks uint64
	found := false
	for _, pid := range processGroupPIDs(pgid) {
		data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			continue
		}
		text := string(data)
		end := strings.LastIndex(text, ")")
		if end < 0 || end+2 >= len(text) {
			continue
		}
		fields := strings.Fields(text[end+2:])
		if len(fields) < 13 {
			continue
		}
		user, err := strconv.ParseUint(fields[11], 10, 64)
		if err != nil {
			continue
		}
		system, err := strconv.ParseUint(fields[12], 10, 64)
		if err != nil {
			continue
		}
		ticks += user + system
		found = true
	}
	// USER_HZ is 100 on the Linux targets this reference runner supports.
	return ticks * 10, found
}
