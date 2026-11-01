//go:build windows

package nativerunner

import (
	"fmt"
	"os/exec"
	"unsafe"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitProcessTime         = 0x00000002
	jobObjectLimitProcessMemory       = 0x00000100
	jobObjectLimitKillOnJobClose      = 0x00002000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

var (
	procCreateJobObject          = modKernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = modKernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = modKernel32.NewProc("AssignProcessToJobObject")
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformationStruct struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func supportedSandboxProfile(profile string) bool {
	return profile == SandboxProfile || profile == SandboxProfileJobObject
}

// applySandbox assigns the started process to a Job Object. The returned
// cleanup closes the job, which kills any remaining process in the tree.
func applySandbox(cmd *exec.Cmd, spec executor.Spec) (func(), error) {
	if spec.Sandbox.Profile != SandboxProfileJobObject {
		return func() {}, nil
	}
	job, _, err := procCreateJobObject.Call(0, 0)
	if job == 0 {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	closeJob := func() { _, _, _ = procCloseHandle.Call(job) }
	var info jobObjectExtendedLimitInformationStruct
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	if spec.Limits.CPUMillis != 0 {
		info.BasicLimitInformation.LimitFlags |= jobObjectLimitProcessTime
		// The Job Object expresses per-process CPU time in 100ns units.
		info.BasicLimitInformation.PerProcessUserTimeLimit = int64(spec.Limits.CPUMillis) * 10000
	}
	if spec.Limits.MemoryBytes != 0 {
		info.BasicLimitInformation.LimitFlags |= jobObjectLimitProcessMemory
		info.ProcessMemoryLimit = uintptr(spec.Limits.MemoryBytes)
	}
	ok, _, err := procSetInformationJobObject.Call(
		job,
		uintptr(jobObjectExtendedLimitInformation),
		uintptr(unsafe.Pointer(&info)),
		uintptr(unsafe.Sizeof(info)),
	)
	if ok == 0 {
		closeJob()
		return nil, fmt.Errorf("set job object limits: %w", err)
	}
	handle, _, err := procOpenProcess.Call(uintptr(processSetQuota|processTerminate), 0, uintptr(uint32(cmd.Process.Pid)))
	if handle == 0 {
		closeJob()
		return nil, fmt.Errorf("open process for job object: %w", err)
	}
	assigned, _, err := procAssignProcessToJobObject.Call(job, handle)
	_, _, _ = procCloseHandle.Call(handle)
	if assigned == 0 {
		closeJob()
		return nil, fmt.Errorf("assign process to job object: %w", err)
	}
	return closeJob, nil
}

func wrapSandboxCommand(_ executor.Spec, executable string, args []string, _ string) (string, []string, error) {
	return executable, args, nil
}
