//go:build windows

package nativerunner

import (
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processKiller keeps one Job Object per running command. Every process the
// runner starts is assigned to that job, and closing the job handle terminates
// the whole tree in the kernel. Job Objects are the race-free way to stop a
// tree on Windows: taskkill /T walks parent to child at call time, so a parent
// that dies before the walk reaches the children can leave them behind — full
// verify runs caught that race with the tree test more than once.
type processKiller struct {
	mu       sync.Mutex
	job      windows.Handle
	assigned bool
}

// assign puts the started process into its own kill-on-close Job Object. A
// failure is not fatal: cancelProcess falls back to taskkill /T /F, which is
// still better than no cancellation.
func (k *processKiller) assign(pid int) error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("set job object limits: %w", err)
	}
	proc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("open process for job assignment: %w", err)
	}
	defer windows.CloseHandle(proc)
	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return fmt.Errorf("assign process to job: %w", err)
	}
	k.mu.Lock()
	k.job, k.assigned = job, true
	k.mu.Unlock()
	return nil
}

// release closes the job handle. The kernel terminates every process left in
// the job, so this is what actually stops the tree — including grandchildren
// the taskkill walk used to miss.
func (k *processKiller) release() {
	k.mu.Lock()
	job := k.job
	k.job, k.assigned = 0, false
	k.mu.Unlock()
	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}

func prepareProcess(*exec.Cmd) {}

func cancelProcess(k *processKiller, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	k.mu.Lock()
	assigned := k.assigned
	k.mu.Unlock()
	if assigned {
		k.release()
		return nil
	}
	// The job could not be assigned at start (for example an access error on
	// an unusual child). Fall back to the enumeration-based stop and accept
	// its race.
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
