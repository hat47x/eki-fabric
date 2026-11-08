package transportfixture

import (
    "bytes"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net"
    "path/filepath"
    "runtime"
    "testing"
    "time"
    "os"
)

// These tests exercise OS Unix-domain socket transfer, not EKI's application
// Local IPC Adapter, Lease authority, Connect RPC, or consumer acceptance.
func readWireFixture(t *testing.T) []json.RawMessage {
    t.Helper()
    raw, err := os.ReadFile("fixture.json")
    if err != nil { t.Fatal(err) }
    var f struct { Events []json.RawMessage `json:"events"` }
    if err := json.Unmarshal(raw, &f); err != nil { t.Fatal(err) }
    if len(f.Events) != 6 { t.Fatalf("expected frozen six events, got %d", len(f.Events)) }
    return f.Events
}

func sameWireEvents(want, got []json.RawMessage) error {
    if len(want) != len(got) { return fmt.Errorf("event count: want %d, got %d", len(want), len(got)) }
    for i := range want {
        var a, b bytes.Buffer
        if err := json.Compact(&a, want[i]); err != nil { return err }
        if err := json.Compact(&b, got[i]); err != nil { return err }
        if !bytes.Equal(a.Bytes(), b.Bytes()) { return fmt.Errorf("event %d: raw JSON changed", i) }
    }
    return nil
}

func TestUnixSocketFixtureWireDelivery(t *testing.T) {
    if runtime.GOOS == "windows" { t.Skip("unix sockets are unavailable on Windows") }
    want := readWireFixture(t)
    socketPath := filepath.Join(t.TempDir(), "events.sock")
    listener, err := net.Listen("unix", socketPath)
    if err != nil { t.Skipf("Unix-domain socket unavailable: %v", err) }
    defer listener.Close()
    received := make(chan struct {
        events []json.RawMessage
        err error
    }, 1)
    go func() {
        result := struct{events []json.RawMessage; err error}{}
        conn, err := listener.Accept()
        if err != nil { result.err = err; received <- result; return }
        defer conn.Close()
        if err := conn.SetReadDeadline(time.Now().Add(4*time.Second)); err != nil { result.err = err; received <- result; return }
        decoder := json.NewDecoder(conn)
        for {
            var msg json.RawMessage
            err := decoder.Decode(&msg)
            if errors.Is(err, io.EOF) { break }
            if err != nil { result.err = err; break }
            result.events = append(result.events, msg)
            if len(result.events) > len(want) { result.err = fmt.Errorf("too many events"); break }
        }
        received <- result
    }()
    conn, err := net.DialTimeout("unix", socketPath, 4*time.Second)
    if err != nil { t.Fatal(err) }
    encoder := json.NewEncoder(conn)
    for _, event := range want {
        if err := encoder.Encode(event); err != nil { conn.Close(); t.Fatal(err) }
    }
    if err := conn.Close(); err != nil { t.Fatal(err) }
    select {
    case result := <-received:
        if result.err != nil { t.Fatal(result.err) }
        if err := sameWireEvents(want, result.events); err != nil { t.Fatal(err) }
    case <-time.After(6*time.Second):
        t.Fatal("local socket receive timed out")
    }
}

func TestUnixSocketWireComparisonRejectsMutations(t *testing.T) {
    source := readWireFixture(t)
    clones := func() []json.RawMessage { return append([]json.RawMessage(nil), source...) }
    cases := []struct { name string; change func([]json.RawMessage) []json.RawMessage }{
        {"missing", func(x []json.RawMessage) []json.RawMessage { return x[:len(x)-1] }},
        {"duplicate", func(x []json.RawMessage) []json.RawMessage { return append(x,x[0]) }},
        {"reordered", func(x []json.RawMessage) []json.RawMessage { x[0],x[1]=x[1],x[0]; return x }},
        {"modified-payload", func(x []json.RawMessage) []json.RawMessage { x[0] = json.RawMessage(`{"id":"E1","kind":"observation","generation":"gA","sequence":40,"epoch":"e7","payload":"tampered"}`); return x }},
    }
    for _, tc := range cases { t.Run(tc.name,func(t *testing.T){ if sameWireEvents(source, tc.change(clones()))==nil { t.Fatal("altered wire events were accepted") } }) }
}
