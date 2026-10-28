package nativerunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/executor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/decisionpayload"
)

func spec(limit uint64) executor.Spec {
	return executor.Spec{
		Limits:  decisionpayload.ResourceLimits{OutputBytesMaximum: limit},
		Sandbox: decisionpayload.SandboxPolicy{Profile: SandboxProfile},
	}
}

func helper(mode string) Runner {
	return Runner{Command: os.Args[0], Args: []string{"-test.run=TestHelperProcess", "--", mode}}
}

func TestHelperProcess(t *testing.T) {
	if len(os.Args) < 4 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "hello":
		fmt.Fprint(os.Stdout, "hello")
	case "exit7":
		os.Exit(7)
	case "ten":
		fmt.Fprint(os.Stdout, "1234567890")
	case "sleep":
		time.Sleep(5 * time.Second)
	}
	os.Exit(0)
}

func TestRunRejectsUnknownSandbox(t *testing.T) {
	unsupported := spec(100)
	unsupported.Sandbox.Profile = "container"
	if _, err := helper("hello").Run(context.Background(), unsupported); err == nil {
		t.Fatal("unsupported sandbox profile was accepted")
	}
}

func TestRunCapturesOutputAndDigest(t *testing.T) {
	result, err := helper("hello").Run(context.Background(), spec(100))
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.OutputBytes != 5 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.OutputDigest != contract.SHA256([]byte("hello")) {
		t.Fatalf("unexpected digest: %s", result.OutputDigest)
	}
}

func TestRunReportsExitCode(t *testing.T) {
	result, err := helper("exit7").Run(context.Background(), spec(100))
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", result.ExitCode)
	}
}

func TestRunStopsAtOutputCeiling(t *testing.T) {
	result, err := helper("ten").Run(context.Background(), spec(5))
	if !errors.Is(err, ErrOutputCeiling) {
		t.Fatalf("err = %v, want ErrOutputCeiling", err)
	}
	if result.OutputBytes != 5 {
		t.Fatalf("output bytes = %d, want 5", result.OutputBytes)
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := helper("sleep").Run(ctx, spec(100)); err == nil {
		t.Fatal("cancelled context was accepted")
	}
}
