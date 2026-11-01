//go:build windows

package nativerunner

import "syscall"

var (
	modKernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess              = modKernel32.NewProc("OpenProcess")
	procCloseHandle              = modKernel32.NewProc("CloseHandle")
	procGetProcessTimes          = modKernel32.NewProc("GetProcessTimes")
	procCreateToolhelp32Snapshot = modKernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32First           = modKernel32.NewProc("Process32FirstW")
	procProcess32Next            = modKernel32.NewProc("Process32NextW")
	modPsapi                     = syscall.NewLazyDLL("psapi.dll")
	procGetProcessMemoryInfo     = modPsapi.NewProc("GetProcessMemoryInfo")
	processQueryLimitedInfo      = uint32(0x1000)
	toolhelpSnapshotProcess      = uint32(0x2)
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
