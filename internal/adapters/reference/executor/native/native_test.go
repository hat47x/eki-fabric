package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
)

// TestHelperProcess is not a real test. The Runner starts the test binary again
// with -test.run=TestHelperProcess and EK_NATIVE_MODE set, so the tests can
// exercise a real child process without shipping another executable.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("EK_NATIVE_HELPER") != "1" {
		return
	}
	switch os.Getenv("EK_NATIVE_MODE") {
	case "ok":
		fmt.Fprint(os.Stdout, "hello")
	case "fail":
		os.Exit(3)
	case "big":
		for i := 0; i < 10; i++ {
			fmt.Fprint(os.Stdout, "0123456789")
		}
	case "sleep":
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func testBinary(t *testing.T) (string, contract.Digest) {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, contract.SHA256(data)
}

func newRunner(t *testing.T, digest contract.Digest, path, mode string) *Runner {
	t.Helper()
	env := append(os.Environ(), "EK_NATIVE_HELPER=1", "EK_NATIVE_MODE="+mode)
	runner, err := New(Config{Executors: map[contract.Digest]Executor{
		digest: {Path: path, Args: []string{"-test.run=TestHelperProcess", "--"}, Env: env},
	}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return runner
}

func specFor(digest contract.Digest, outputLimit uint64) executor.Spec {
	return executor.Spec{
		TaskID:          "tsk_00000000000000000000000000000001",
		AttemptID:       "atm_00000000000000000000000000000001",
		OrderDecisionID: "wor-decision-001",
		ExecutorDigest:  digest,
		Limits: decisionpayload.ResourceLimits{
			CPUMillis: 4000, MemoryBytes: 1 << 30, WallTimeMillis: 20_000,
			OutputBytesMaximum: outputLimit,
		},
		Sandbox:  decisionpayload.SandboxPolicy{Profile: "sandbox/native/v1"},
		Deadline: time.Now().Add(20 * time.Second),
		Nonce:    []byte("0123456789abcdef"),
	}
}

func TestRunExecutesTheRegisteredArtifact(t *testing.T) {
	path, digest := testBinary(t)
	runner := newRunner(t, digest, path, "ok")
	result, err := runner.Run(context.Background(), specFor(digest, 1024))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != 0 || result.OutputBytes != 5 {
		t.Fatalf("result = %+v, want exit 0 and 5 bytes", result)
	}
}

func TestRunReportsANonZeroExitAsAResult(t *testing.T) {
	path, digest := testBinary(t)
	runner := newRunner(t, digest, path, "fail")
	result, err := runner.Run(context.Background(), specFor(digest, 1024))
	if err != nil {
		t.Fatalf("Run() error = %v, want a Result with the exit code", err)
	}
	if result.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", result.ExitCode)
	}
}

func TestRunStopsWhenOutputExceedsTheLimit(t *testing.T) {
	path, digest := testBinary(t)
	runner := newRunner(t, digest, path, "big")
	result, err := runner.Run(context.Background(), specFor(digest, 10))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.ExitCode != -1 || result.OutputBytes <= 10 {
		t.Fatalf("result = %+v, want an oversized output with exit -1", result)
	}
}

func TestRunRejectsAnUnregisteredDigest(t *testing.T) {
	path, _ := testBinary(t)
	unknown := contract.SHA256([]byte("not-registered"))
	runner := newRunner(t, unknown, path, "ok")
	_, err := runner.Run(context.Background(), specFor(contract.SHA256([]byte("other")), 1024))
	if !errors.Is(err, ErrExecutorNotKnown) {
		t.Fatalf("Run() error = %v, want %v", err, ErrExecutorNotKnown)
	}
}

func TestRunRejectsAChangedArtifact(t *testing.T) {
	path, _ := testBinary(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(t.TempDir(), "executor.bin")
	if err := os.WriteFile(copyPath, data, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := contract.SHA256(data)
	runner := newRunner(t, digest, copyPath, "ok")
	if err := os.WriteFile(copyPath, append(data, 0x00), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), specFor(digest, 1024))
	if !errors.Is(err, ErrExecutorChanged) {
		t.Fatalf("Run() error = %v, want %v", err, ErrExecutorChanged)
	}
}

func TestRunHonoursContextCancellation(t *testing.T) {
	path, digest := testBinary(t)
	runner := newRunner(t, digest, path, "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := runner.Run(ctx, specFor(digest, 1024))
	if err == nil {
		t.Fatal("Run() returned without an error for a cancelled context")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("Run() took %s to stop", elapsed)
	}
}

func TestRunRejectsInvalidInput(t *testing.T) {
	path, digest := testBinary(t)
	runner := newRunner(t, digest, path, "ok")
	if _, err := runner.Run(nil, specFor(digest, 1024)); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("nil context error = %v, want %v", err, ErrInvalidSpec)
	}
	badSpec := specFor(digest, 1024)
	badSpec.ExecutorDigest = contract.Digest{}
	if _, err := runner.Run(context.Background(), badSpec); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("zero digest error = %v, want %v", err, ErrInvalidSpec)
	}
	var uninitialized *Runner
	if _, err := uninitialized.Run(context.Background(), specFor(digest, 1024)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("uninitialized error = %v, want %v", err, ErrInvalidConfig)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	if _, err := New(Config{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty config error = %v, want %v", err, ErrInvalidConfig)
	}
	if _, err := New(Config{Executors: map[contract.Digest]Executor{{}: {Path: "x"}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero digest error = %v, want %v", err, ErrInvalidConfig)
	}
	digest := contract.SHA256([]byte("digest"))
	if _, err := New(Config{Executors: map[contract.Digest]Executor{digest: {}}}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("empty path error = %v, want %v", err, ErrInvalidConfig)
	}
}
