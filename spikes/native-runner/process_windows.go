//go:build windows

package nativerunner

import "os/exec"

func prepareProcess(*exec.Cmd) {}

func cancelProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
