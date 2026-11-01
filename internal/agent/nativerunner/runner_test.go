package nativerunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

func helper(mode string, extra ...string) Runner {
	args := []string{"-test.run=TestHelperProcess", "--", mode}
	args = append(args, extra...)
	return Runner{Command: os.Args[0], Args: args}
}

func TestHelperProcess(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i
			break
		}
	}
	if index < 0 || index+1 >= len(os.Args) {
		return
	}
	mode := os.Args[index+1]
	extra := os.Args[index+2:]
	switch mode {
	case "hello":
		fmt.Fprint(os.Stdout, "hello")
	case "exit7":
		os.Exit(7)
	case "ten":
		fmt.Fprint(os.Stdout, "1234567890")
	case "env":
		fmt.Fprint(os.Stdout, os.Getenv("TAI_SECRET"))
	case "spam":
		for {
			fmt.Fprint(os.Stdout, "x")
		}
	case "tree":
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "write", extra[0])
		cmd.Env = []string{}
		if err := cmd.Start(); err != nil {
			os.Exit(2)
		}
		time.Sleep(5 * time.Second)
	case "write":
		time.Sleep(1 * time.Second)
		_ = os.WriteFile(extra[0], []byte("survived"), 0o644)
	case "sleep":
		time.Sleep(5 * time.Second)
	case "burn":
		// Spend CPU in this process so a resource watch sampling its own PID
		// can observe the usage. The bound only keeps a broken watch from
		// running forever; a working watch stops the process much sooner.
		until := time.Now().Add(10 * time.Second)
		var sink int
		for time.Now().Before(until) {
			for i := 0; i < 10000; i++ {
				sink += i * i
			}
		}
		fmt.Fprint(os.Stdout, sink%10)
	}
	os.Exit(0)
}

func TestRunDropsInheritedEnvironment(t *testing.T) {
	t.Setenv("TAI_SECRET", "leak")
	result, err := helper("env").Run(context.Background(), spec(100))
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputBytes != 0 {
		t.Fatalf("inherited environment leaked %d bytes", result.OutputBytes)
	}
}

func TestRunPassesExplicitEnvironment(t *testing.T) {
	runner := helper("env")
	runner.Env = []string{"TAI_SECRET=explicit"}
	result, err := runner.Run(context.Background(), spec(100))
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputDigest != contract.SHA256([]byte("explicit")) {
		t.Fatalf("explicit environment was not passed: %+v", result)
	}
}

func TestRunChecksExecutableDigest(t *testing.T) {
	digest, err := executableDigest(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	runner := helper("hello")
	runner.ExecutableSHA256 = digest
	if _, err := runner.Run(context.Background(), spec(100)); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsExecutableDigestMismatch(t *testing.T) {
	runner := helper("hello")
	runner.ExecutableSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := runner.Run(context.Background(), spec(100)); err == nil {
		t.Fatal("executable digest mismatch was accepted")
	}
}

func TestRunRejectsGPULimit(t *testing.T) {
	limited := spec(100)
	limited.Sandbox.GPU = &decisionpayload.GPULimit{DeviceCount: 1}
	if _, err := helper("hello").Run(context.Background(), limited); err == nil {
		t.Fatal("unenforced GPU limit was accepted")
	}
}

func TestRunRejectsArgumentOutsideAllowlist(t *testing.T) {
	runner := helper("hello")
	runner.AllowedArgs = []string{"-test.run=TestHelperProcess", "--"}
	if _, err := runner.Run(context.Background(), spec(100)); err == nil {
		t.Fatal("argument outside allowlist was accepted")
	}
}

func TestRunAllowsListedArguments(t *testing.T) {
	runner := helper("hello")
	runner.AllowedArgs = append([]string(nil), runner.Args...)
	if _, err := runner.Run(context.Background(), spec(100)); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsAllArgumentsWhenAllowlistIsEmpty(t *testing.T) {
	runner := helper("hello")
	runner.AllowedArgs = []string{}
	if _, err := runner.Run(context.Background(), spec(100)); err == nil {
		t.Fatal("empty allowlist allowed arguments")
	}
}

func TestRunRejectsUnenforcedNetworkLimit(t *testing.T) {
	limited := spec(100)
	limited.Limits.NetworkBytesMax = 1
	if _, err := helper("hello").Run(context.Background(), limited); err == nil {
		t.Fatal("unenforced network limit was accepted")
	}
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

func TestRunStopsProducerAtCeiling(t *testing.T) {
	started := time.Now()
	if _, err := helper("spam").Run(context.Background(), spec(10)); !errors.Is(err, ErrOutputCeiling) {
		t.Fatalf("err = %v, want ErrOutputCeiling", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("producer was not stopped promptly: %s", elapsed)
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := helper("sleep").Run(ctx, spec(100)); err == nil {
		t.Fatal("cancelled context was accepted")
	}
}
