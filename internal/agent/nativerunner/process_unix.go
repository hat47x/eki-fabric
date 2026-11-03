//go:build !windows

package nativerunner

import (
	"os/exec"
	"syscall"
)

// processKiller carries no state off Windows: the process group set in
// prepareProcess is the tree, and one signal stops it.
type processKiller struct{}

func (k *processKiller) assign(int) error { return nil }

func (k *processKiller) release() {}

func prepareProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func cancelProcess(k *processKiller, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
