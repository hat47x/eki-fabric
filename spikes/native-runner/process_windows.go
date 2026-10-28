//go:build windows

package nativerunner

import (
	"os/exec"
	"strconv"
)

func prepareProcess(*exec.Cmd) {}

func cancelProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
