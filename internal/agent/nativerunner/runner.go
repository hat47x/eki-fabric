// Package nativerunner implements executor.Runner for one fixed command.
//
// This is a production adapter with an explicit limit: it is not a sandbox and
// must not be used with untrusted tasks. The command and arguments are trusted
// Worker configuration, never Task input. CPU and memory are polled while the
// process runs on Linux and Windows; when a limit is exceeded the process tree
// is stopped. The post-exit checks remain as a second line of defence.
package nativerunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

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
	Command          string
	Args             []string
	AllowedArgs      []string
	Env              []string
	ExecutableSHA256 string
}

func (r Runner) Run(ctx context.Context, spec executor.Spec) (executor.Result, error) {
	if r.Command == "" {
		return executor.Result{}, errors.New("native runner command is empty")
	}
	if r.AllowedArgs != nil {
		allowed := make(map[string]struct{}, len(r.AllowedArgs))
		for _, arg := range r.AllowedArgs {
			allowed[arg] = struct{}{}
		}
		for _, arg := range r.Args {
			if _, ok := allowed[arg]; !ok {
				return executor.Result{}, fmt.Errorf("native runner argument is not allowed: %q", arg)
			}
		}
	}
	if spec.Sandbox.Profile != SandboxProfile {
		return executor.Result{}, fmt.Errorf("native runner does not implement sandbox profile %q", spec.Sandbox.Profile)
	}
	if spec.Sandbox.NetworkProfile != "" && spec.Sandbox.NetworkProfile != "none" {
		return executor.Result{}, fmt.Errorf("native runner does not implement network profile %q", spec.Sandbox.NetworkProfile)
	}
	if spec.Sandbox.GPU != nil {
		return executor.Result{}, errors.New("native runner does not enforce GPU limits")
	}
	if spec.Limits.NetworkBytesMax != 0 {
		return executor.Result{}, errors.New("native runner does not enforce network limits")
	}
	limit := spec.Limits.OutputBytesMaximum
	if limit == 0 {
		limit = 1 << 20
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var output bytes.Buffer
	writer := &limitWriter{writer: &output, limit: limit, onExceed: cancel}
	executable, err := exec.LookPath(r.Command)
	if err != nil {
		return executor.Result{}, err
	}
	if r.ExecutableSHA256 != "" {
		actual, err := executableDigest(executable)
		if err != nil {
			return executor.Result{}, err
		}
		if !strings.EqualFold(actual, r.ExecutableSHA256) {
			return executor.Result{}, fmt.Errorf("executable digest mismatch: got %s", actual)
		}
	}
	tempDir := ""
	if spec.Limits.TemporaryDiskBytes != 0 {
		tempDir, err = os.MkdirTemp("", "eki-run-*")
		if err != nil {
			return executor.Result{}, fmt.Errorf("native runner temporary directory: %w", err)
		}
		defer os.RemoveAll(tempDir)
	}
	cmd := exec.CommandContext(runCtx, executable, r.Args...)
	prepareProcess(cmd)
	cmd.Cancel = func() error { return cancelProcess(cmd) }
	// An empty non-nil Env means the child receives no inherited environment.
	cmd.Env = append([]string{}, r.Env...)
	if tempDir != "" {
		cmd.Env = append(cmd.Env, "TMP="+tempDir, "TEMP="+tempDir, "TMPDIR="+tempDir)
		// Relative writes land in the per-run directory. This is not a
		// filesystem sandbox: absolute paths are still reachable.
		cmd.Dir = tempDir
	}
	cmd.Stdout = writer
	cmd.Stderr = writer
	result := executor.Result{}
	if err = cmd.Start(); err != nil {
		return result, err
	}
	var watch *resourceWatch
	if spec.Limits.MemoryBytes != 0 || spec.Limits.CPUMillis != 0 || spec.Limits.TemporaryDiskBytes != 0 {
		watch = watchResources(cmd.Process.Pid, spec.Limits.MemoryBytes, spec.Limits.CPUMillis, spec.Limits.TemporaryDiskBytes, tempDir, func() { _ = cancelProcess(cmd) })
	}
	err = cmd.Wait()
	watch.stop()
	result.OutputBytes = uint64(output.Len())
	if watch.memoryWasExceeded() {
		return result, fmt.Errorf("native runner memory limit %d exceeded while running", spec.Limits.MemoryBytes)
	}
	if watch.cpuWasExceeded() {
		return result, fmt.Errorf("native runner CPU limit %dms exceeded while running", spec.Limits.CPUMillis)
	}
	if watch.diskWasExceeded() {
		return result, fmt.Errorf("native runner temporary disk limit %d exceeded while running", spec.Limits.TemporaryDiskBytes)
	}
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
	maxRSSBytes := processUsage(cmd.ProcessState)
	cpuMillis := uint64(cmd.ProcessState.UserTime()+cmd.ProcessState.SystemTime()) / uint64(time.Millisecond)
	if spec.Limits.CPUMillis != 0 && cpuMillis > spec.Limits.CPUMillis {
		return result, fmt.Errorf("native runner CPU time %dms exceeds %dms", cpuMillis, spec.Limits.CPUMillis)
	}
	if spec.Limits.MemoryBytes != 0 && maxRSSBytes > spec.Limits.MemoryBytes {
		return result, fmt.Errorf("native runner memory %d exceeds %d bytes", maxRSSBytes, spec.Limits.MemoryBytes)
	}
	result.OutputDigest = contract.SHA256(output.Bytes())
	return result, nil
}

func executableDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type limitWriter struct {
	writer   *bytes.Buffer
	limit    uint64
	exceeded bool
	onExceed func()
}

func (w *limitWriter) stop() {
	if w.exceeded {
		return
	}
	w.exceeded = true
	if w.onExceed != nil {
		w.onExceed()
	}
}

func (w *limitWriter) Write(p []byte) (int, error) {
	remaining := w.limit - uint64(w.writer.Len())
	if remaining == 0 {
		w.stop()
		return len(p), nil
	}
	if uint64(len(p)) > remaining {
		_, _ = w.writer.Write(p[:remaining])
		w.stop()
		return len(p), nil
	}
	_, _ = w.writer.Write(p)
	return len(p), nil
}
