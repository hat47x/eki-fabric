// Package nativerunner is a feasibility spike for a fixed-command native
// Runner. It does not provide a sandbox and must not be used with untrusted
// tasks.
package nativerunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var ErrOutputCeiling = errors.New("native runner output ceiling exceeded")

// SandboxProfile is the only profile this spike claims to handle. It does not
// provide isolation; the name makes that limitation explicit at the boundary.
const SandboxProfile = "native-fixed-spike"

// Runner executes one fixed command. The command and arguments are trusted
// configuration, not task input.
type Runner struct {
	Command string
	Args    []string
}

func (r Runner) Run(ctx context.Context, spec executor.Spec) (executor.Result, error) {
	if r.Command == "" {
		return executor.Result{}, errors.New("native runner command is empty")
	}
	if spec.Sandbox.Profile != SandboxProfile {
		return executor.Result{}, fmt.Errorf("native runner does not implement sandbox profile %q", spec.Sandbox.Profile)
	}
	if spec.Sandbox.NetworkProfile != "" && spec.Sandbox.NetworkProfile != "none" {
		return executor.Result{}, fmt.Errorf("native runner does not implement network profile %q", spec.Sandbox.NetworkProfile)
	}
	if spec.Limits.NetworkBytesMax != 0 {
		return executor.Result{}, errors.New("native runner does not enforce network limits")
	}
	if spec.Limits.TemporaryDiskBytes != 0 {
		return executor.Result{}, errors.New("native runner does not enforce temporary-disk limits")
	}
	limit := spec.Limits.OutputBytesMaximum
	if limit == 0 {
		limit = 1 << 20
	}
	var output bytes.Buffer
	writer := &limitWriter{writer: &output, limit: limit}
	cmd := exec.CommandContext(ctx, r.Command, r.Args...)
	cmd.Stdout = writer
	cmd.Stderr = writer
	err := cmd.Run()
	result := executor.Result{OutputBytes: uint64(output.Len())}
	if writer.exceeded {
		return result, fmt.Errorf("%w: limit=%d", ErrOutputCeiling, limit)
	}
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			result.ExitCode = exitError.ExitCode()
			return result, nil
		}
		return result, err
	}
	result.OutputDigest = contract.SHA256(output.Bytes())
	return result, nil
}

type limitWriter struct {
	writer   *bytes.Buffer
	limit    uint64
	exceeded bool
}

func (w *limitWriter) Write(p []byte) (int, error) {
	remaining := w.limit - uint64(w.writer.Len())
	if remaining == 0 {
		w.exceeded = true
		return len(p), nil
	}
	if uint64(len(p)) > remaining {
		_, _ = w.writer.Write(p[:remaining])
		w.exceeded = true
		return len(p), nil
	}
	_, _ = w.writer.Write(p)
	return len(p), nil
}
