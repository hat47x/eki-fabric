//go:build windows

package nativerunner

import (
	"syscall"
	"unsafe"
)

var (
	modKernel32              = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess          = modKernel32.NewProc("OpenProcess")
	procCloseHandle          = modKernel32.NewProc("CloseHandle")
	procGetProcessTimes      = modKernel32.NewProc("GetProcessTimes")
	modPsapi                 = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo = modPsapi.NewProc("GetProcessMemoryInfo")
	processQueryLimitedInfo  = uint32(0x1000)
)

type filetime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func (f filetime) ticks100ns() uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

type processMemoryCounters struct {
	CB                      uint32
	PageFaultCount          uint32
	PeakWorkingSetSize      uintptr
	WorkingSetSize          uintptr
	QuotaPeakPagedPoolUsage uintptr
	QuotaPagedPoolUsage     uintptr
	QuotaPeakNonPagedPool   uintptr
	QuotaNonPagedPoolUsage  uintptr
	PagefileUsage           uintptr
	PeakPagefileUsage       uintptr
}

func openProcess(pid int) (uintptr, bool) {
	handle, _, _ := procOpenProcess.Call(uintptr(processQueryLimitedInfo), 0, uintptr(uint32(pid)))
	return handle, handle != 0
}

// sampleRSS asks Windows for the current working set of the process.
func sampleRSS(pid int) (uint64, bool) {
	handle, ok := openProcess(pid)
	if !ok {
		return 0, false
	}
	defer procCloseHandle.Call(handle)
	counters := processMemoryCounters{CB: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	okProc, _, _ := procGetProcessMemoryInfo.Call(handle, uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	if okProc == 0 {
		return 0, false
	}
	return uint64(counters.WorkingSetSize), true
}

// sampleCPUMillis reports the cumulative user and kernel CPU time of the process.
func sampleCPUMillis(pid int) (uint64, bool) {
	handle, ok := openProcess(pid)
	if !ok {
		return 0, false
	}
	defer procCloseHandle.Call(handle)
	var creation, exit, kernel, user filetime
	okProc, _, _ := procGetProcessTimes.Call(
		handle,
		uintptr(unsafe.Pointer(&creation)),
		uintptr(unsafe.Pointer(&exit)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if okProc == 0 {
		return 0, false
	}
	return (kernel.ticks100ns() + user.ticks100ns()) / 10000, true
}
