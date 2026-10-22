// Package native is a reference Runner that executes a locally registered
// executable.
//
// The WorkOrder never names a path or an argument vector. It carries the digest
// of an approved Executor artifact. The operator registers the digest together
// with the local path and arguments, and this Runner hashes the file again
// before every start. A changed file, a missing registration, or a digest that
// does not match stops the start. This keeps a malicious Client from choosing
// what runs.
//
// The Runner enforces the wall-clock deadline through the context and bounds
// the output bytes. It does not provide OS-level sandboxing: CPU, memory, and
// filesystem isolation remain the responsibility of the platform profile.
package native

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
	"sync"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
)

var (
	ErrInvalidConfig    = errors.New("invalid native executor configuration")
	ErrInvalidSpec      = errors.New("invalid execution spec")
	ErrExecutorNotKnown = errors.New("executor digest is not registered")
	ErrExecutorChanged  = errors.New("executor file does not match the approved digest")
	ErrExecutorStart    = errors.New("executor could not be started")
)

// Executor is one locally approved artifact. Args and Env come from local
// configuration, not from the WorkOrder.
type Executor struct {
	Path    string
	Args    []string
	Env     []string
	WorkDir string
}

type Config struct {
	Executors map[contract.Digest]Executor
}

// Runner executes registered artifacts. It is safe for concurrent use.
type Runner struct {
	executors map[contract.Digest]Executor
}

var _ executor.Runner = (*Runner)(nil)

func New(cfg Config) (*Runner, error) {
	if len(cfg.Executors) == 0 {
		return nil, fmt.Errorf("%w: at least one executor must be registered", ErrInvalidConfig)
	}
	copied := make(map[contract.Digest]Executor, len(cfg.Executors))
	for digest, entry := range cfg.Executors {
		if digest.IsZero() {
			return nil, fmt.Errorf("%w: executor digest is required", ErrInvalidConfig)
		}
		if entry.Path == "" {
			return nil, fmt.Errorf("%w: executor path is required", ErrInvalidConfig)
		}
		copied[digest] = Executor{
			Path:    entry.Path,
			Args:    append([]string(nil), entry.Args...),
			Env:     append([]string(nil), entry.Env...),
			WorkDir: entry.WorkDir,
		}
	}
	return &Runner{executors: copied}, nil
}

// Run starts the registered artifact and waits for it.
//
// A non-zero exit code is returned as a Result with a nil error: the Agent must
// record it as a failed execution, not as a Runner malfunction. Output beyond
// the WorkOrder limit stops the process and is reported as an oversized Result.
// Cancellation of ctx (a Local Governor stop or the deadline) returns ctx.Err().
func (r *Runner) Run(ctx context.Context, spec executor.Spec) (executor.Result, error) {
	if r == nil || r.executors == nil {
		return executor.Result{}, fmt.Errorf("%w: runner is not initialized", ErrInvalidConfig)
	}
	if ctx == nil {
		return executor.Result{}, fmt.Errorf("%w: context is required", ErrInvalidSpec)
	}
	if spec.ExecutorDigest.IsZero() {
		return executor.Result{}, fmt.Errorf("%w: executor digest is required", ErrInvalidSpec)
	}
	entry, ok := r.executors[spec.ExecutorDigest]
	if !ok {
		return executor.Result{}, fmt.Errorf("%w: %s", ErrExecutorNotKnown, spec.ExecutorDigest)
	}
	if err := verifyFile(spec.ExecutorDigest, entry.Path); err != nil {
		return executor.Result{}, err
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	limit := spec.Limits.OutputBytesMaximum
	output := &boundedBuffer{limit: limit, onOverflow: cancel}
	cmd := exec.CommandContext(runCtx, entry.Path, entry.Args...)
	cmd.Dir = entry.WorkDir
	cmd.Env = entry.Env
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return executor.Result{}, fmt.Errorf("%w: %v", ErrExecutorStart, err)
	}
	err := cmd.Wait()
	if output.overflowed {
		return executor.Result{ExitCode: -1, OutputBytes: output.written}, nil
	}
	if err != nil {
		if runCtx.Err() != nil {
			return executor.Result{}, runCtx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return executor.Result{ExitCode: exitErr.ExitCode(), OutputBytes: output.written}, nil
		}
		return executor.Result{}, fmt.Errorf("%w: %v", ErrExecutorStart, err)
	}
	return executor.Result{ExitCode: 0, OutputBytes: output.written, OutputDigest: output.digest()}, nil
}

// verifyFile hashes the file again, so a registration cannot outlive the
// artifact it named.
func verifyFile(want contract.Digest, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExecutorStart, err)
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("%w: %v", ErrExecutorStart, err)
	}
	got, err := contract.ParseSHA256(hex.EncodeToString(hasher.Sum(nil)))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExecutorStart, err)
	}
	if got != want {
		return fmt.Errorf("%w: %s", ErrExecutorChanged, path)
	}
	return nil
}

// boundedBuffer keeps at most limit bytes and cancels the process on overflow.
type boundedBuffer struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	limit      uint64
	written    uint64
	overflowed bool
	onOverflow func()
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.written += uint64(len(p))
	remaining := b.limit - minUint64(b.written-uint64(len(p)), b.limit)
	// Write only what fits. The count still records everything observed, so the
	// Agent can prove that the limit was exceeded.
	if uint64(len(p)) > remaining {
		if remaining > 0 {
			b.buf.Write(p[:remaining])
		}
		if !b.overflowed {
			b.overflowed = true
			if b.onOverflow != nil {
				b.onOverflow()
			}
		}
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// digest returns the SHA-256 of everything the process wrote. In this Runner the
// output of an execution is its combined standard output and standard error, so
// this is the digest a completed ResultClaim reports. It is only meaningful when
// the buffer did not overflow, which the caller checks first.
func (b *boundedBuffer) digest() contract.Digest {
	b.mu.Lock()
	defer b.mu.Unlock()
	return contract.SHA256(b.buf.Bytes())
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
