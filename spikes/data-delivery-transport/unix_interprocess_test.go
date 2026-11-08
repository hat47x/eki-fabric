package transportfixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestUnixFixtureChildSender executes only as a subprocess of the test below.
// It transports the frozen event bytes, but confers no EKI authorization.
func TestUnixFixtureChildSender(t *testing.T) {
	if os.Getenv("EKI_FIXTURE_CHILD") != "1" {
		t.Skip("helper process only")
	}
	socket := os.Getenv("EKI_FIXTURE_SOCKET")
	if socket == "" {
		t.Fatal("missing socket")
	}
	events := readWireFixture(t)
	if os.Getenv("EKI_FIXTURE_DROP_LAST") == "1" {
		events = events[:len(events)-1]
	}
	conn, err := net.DialTimeout("unix", socket, 4*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(conn)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
}

func runFixtureAcrossProcesses(t *testing.T, dropLast bool) ([]json.RawMessage, []json.RawMessage) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix-domain sockets not supported")
	}
	wanted := readWireFixture(t)
	socket := filepath.Join(t.TempDir(), "events.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("Unix-domain sockets unavailable: %v", err)
	}
	defer listener.Close()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(self, "-test.run=^TestUnixFixtureChildSender$", "-test.v")
	child.Env = append(os.Environ(), "EKI_FIXTURE_CHILD=1", "EKI_FIXTURE_SOCKET="+socket)
	if dropLast {
		child.Env = append(child.Env, "EKI_FIXTURE_DROP_LAST=1")
	}
	child.Dir = "."
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	if child.Process.Pid == os.Getpid() {
		t.Fatal("not a separate process")
	}
	if err := listener.(*net.UnixListener).SetDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err != nil {
		t.Fatalf("accept: %v; child output: %s", err, output.String())
	}
	if err := conn.SetReadDeadline(time.Now().Add(6 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// A malicious sender cannot force the test to buffer unbounded input.
	dec := json.NewDecoder(io.LimitReader(conn, 1<<20))
	var got []json.RawMessage
	for {
		var event json.RawMessage
		err := dec.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			conn.Close()
			t.Fatalf("decode: %v", err)
		}
		got = append(got, event)
		if len(got) > len(wanted) {
			conn.Close()
			t.Fatal("unexpected extra event")
		}
	}
	_ = conn.Close()
	if err := child.Wait(); err != nil {
		t.Fatalf("child failed: %v; output: %s", err, output.String())
	}
	t.Logf("separate process PID=%d sent %d JSON events over Unix-domain socket; no EKI adapter or authority checked", child.Process.Pid, len(got))
	return wanted, got
}

func TestUnixFixtureAcrossProcesses(t *testing.T) {
	want, got := runFixtureAcrossProcesses(t, false)
	if err := sameWireEvents(want, got); err != nil {
		t.Fatal(err)
	}
}

func TestUnixFixtureAcrossProcessesRejectsPartial(t *testing.T) {
	want, got := runFixtureAcrossProcesses(t, true)
	if len(got) != len(want)-1 {
		t.Fatalf("missing frame injection did not happen: got=%d want=%d", len(got), len(want)-1)
	}
	if sameWireEvents(want, got) == nil {
		t.Fatal("incomplete delivery trace incorrectly accepted")
	}
}
