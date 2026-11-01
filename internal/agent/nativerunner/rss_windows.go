package nativerunner

import (
	"syscall"
	"unsafe"
)

// descendantPIDs walks the process tree from the root PID using the Windows
// tool help snapshot (#351). The runner's root process (cmd, a shell, a build
// tool) spends most of its resources in children, so summing the tree is what
// makes the watch's numbers match what the machine actually spends.
//
// A grandchild spawned after a snapshot is missed until the next snapshot;
// the 20ms loop resnapshots every tick, and a short-lived grandchild's usage
// is bounded by the same limits being enforced on its parents.
func descendantPIDs(root int) []int {
	snapshot, _, _ := procCreateToolhelp32Snapshot.Call(uintptr(toolhelpSnapshotProcess), 0)
	if snapshot == 0 || snapshot == uintptr(syscall.InvalidHandle) {
		return nil
	}
	defer procCloseHandle.Call(snapshot)

	type processEntry struct {
		Size            uint32
		Usage           uint32
		ProcessID       uint32
		DefaultHeapID   uintptr
		ModuleID        uint32
		Threads         uint32
		ParentProcessID uint32
		PriClassBase    int32
		Flags           uint32
		ExeFile         [260]uint16
	}
	entry := processEntry{Size: uint32(unsafe.Sizeof(processEntry{}))}
	parent := map[uint32]uint32{}
	ok, _, _ := procProcess32First.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	for ok != 0 {
		parent[entry.ProcessID] = entry.ParentProcessID
		entry = processEntry{Size: uint32(unsafe.Sizeof(processEntry{}))}
		ok, _, _ = procProcess32Next.Call(snapshot, uintptr(unsafe.Pointer(&entry)))
	}
	children := map[uint32][]uint32{}
	for pid, pp := range parent {
		children[pp] = append(children[pp], pid)
	}
	out := make([]int, 0, 4)
	frontier := append([]uint32(nil), children[uint32(root)]...)
	seen := map[uint32]bool{uint32(root): true}
	for len(frontier) > 0 {
		pid := frontier[0]
		frontier = frontier[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, int(pid))
		frontier = append(frontier, children[pid]...)
	}
	return out
}

// sampleRSS sums the working set of the root process and every descendant in
// the snapshot (#351). A child that exits between enumeration and query is
// skipped (its usage is gone); a child that spawns after the snapshot waits
// for the next tick.
func sampleRSS(pid int) (uint64, bool) {
	pids := append([]int{pid}, descendantPIDs(pid)...)
	var total uint64
	found := false
	for _, p := range pids {
		handle, ok := openProcess(p)
		if !ok {
			continue
		}
		counters := processMemoryCounters{CB: uint32(unsafe.Sizeof(processMemoryCounters{}))}
		okProc, _, _ := procGetProcessMemoryInfo.Call(handle, uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
		procCloseHandle.Call(handle)
		if okProc == 0 {
			continue
		}
		total += uint64(counters.WorkingSetSize)
		found = true
	}
	return total, found
}

// sampleCPUMillis sums the cumulative user and kernel CPU time of the root
// process and every descendant (#351).
func sampleCPUMillis(pid int) (uint64, bool) {
	pids := append([]int{pid}, descendantPIDs(pid)...)
	var total uint64
	found := false
	for _, p := range pids {
		handle, ok := openProcess(p)
		if !ok {
			continue
		}
		var creation, exit, kernel, user filetime
		okProc, _, _ := procGetProcessTimes.Call(
			handle,
			uintptr(unsafe.Pointer(&creation)),
			uintptr(unsafe.Pointer(&exit)),
			uintptr(unsafe.Pointer(&kernel)),
			uintptr(unsafe.Pointer(&user)),
		)
		procCloseHandle.Call(handle)
		if okProc == 0 {
			continue
		}
		total += (kernel.ticks100ns() + user.ticks100ns()) / 10000
		found = true
	}
	return total, found
}
