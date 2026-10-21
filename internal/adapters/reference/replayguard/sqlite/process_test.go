package sqlite

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const helperDatabaseEnv = "EKI_REPLAY_GUARD_HELPER_DATABASE"

func TestReplayGuardProcessHelper(t *testing.T) {
	path := os.Getenv(helperDatabaseEnv)
	if path == "" {
		t.Skip("subprocess helper")
	}
	g, err := Open(context.Background(), path, clockFunc(func() time.Time { return baseTime }), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	fmt.Println("ready")
	if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	claimed, err := g.Claim(context.Background(), nonce(42), baseTime.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(claimed)
	if os.Getenv("EKI_REPLAY_GUARD_HELPER_EXIT_WITHOUT_CLOSE") == "1" {
		// Deliberately skip Close and all Go test cleanup after a committed claim.
		os.Exit(0)
	}
}

func helperCommand(ctx context.Context, path string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReplayGuardProcessHelper$")
	cmd.Env = append(os.Environ(), helperDatabaseEnv+"="+path)
	return cmd
}

func TestIndependentProcessesHaveExactlyOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "processes.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type child struct {
		cmd    *exec.Cmd
		input  *bufio.Writer
		output *bufio.Reader
		stderr *bytes.Buffer
	}
	children := make([]child, 0, 2)
	for i := 0; i < 2; i++ {
		cmd := helperCommand(ctx, path)
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		stderr := &bytes.Buffer{}
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		children = append(children, child{cmd, bufio.NewWriter(input), bufio.NewReader(output), stderr})
	}
	// Both processes opened the same database before either may claim.
	for _, child := range children {
		ready, err := child.output.ReadString('\n')
		if err != nil || strings.TrimSpace(ready) != "ready" {
			t.Fatalf("child readiness %q, %v", ready, err)
		}
	}
	for _, child := range children {
		if _, err := child.input.WriteString("claim\n"); err != nil {
			t.Fatal(err)
		}
		if err := child.input.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	winners := 0
	for _, child := range children {
		result, err := child.output.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		switch strings.TrimSpace(result) {
		case "true":
			winners++
		case "false":
		default:
			t.Fatalf("unexpected child result %q", result)
		}
		if err := child.cmd.Wait(); err != nil {
			t.Fatalf("child exited: %v, %s", err, child.stderr.String())
		}
	}
	if winners != 1 {
		t.Fatalf("independent processes produced %d winners", winners)
	}
	g := openGuard(t, path, clockFunc(func() time.Time { return baseTime }), 1)
	assertClaim(t, g, nonce(42), baseTime.Add(time.Minute), false)
}

func TestCommittedClaimSurvivesExitWithoutClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abrupt-exit.db")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := helperCommand(ctx, path)
	cmd.Env = append(cmd.Env, "EKI_REPLAY_GUARD_HELPER_EXIT_WITHOUT_CLOSE=1")
	cmd.Stdin = strings.NewReader("claim\n")
	output, err := cmd.CombinedOutput()
	if err != nil || string(output) != "ready\ntrue\n" {
		t.Fatalf("exit without close: %q, %v", output, err)
	}
	g := openGuard(t, path, clockFunc(func() time.Time { return baseTime }), 1)
	assertClaim(t, g, nonce(42), baseTime.Add(time.Minute), false)
}
