package measurement

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func logBuffer() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

func TestEachCountBecomesOneLine(t *testing.T) {
	c := New()
	c.Add(AdmissionRejected, "auth")
	c.Add(AdmissionRejected, "auth")
	c.Add(LimitRefused, "size")
	log, buf := logBuffer()
	c.Log(log)
	out := buf.String()
	if got := strings.Count(out, "msg=measurement"); got != 2 {
		t.Fatalf("lines = %d, want 2: %s", got, out)
	}
	if !strings.Contains(out, "kind=admission.rejected") || !strings.Contains(out, "count=2") {
		t.Fatalf("log = %s", out)
	}
	if !strings.Contains(out, "reason=size") {
		t.Fatalf("log = %s", out)
	}
}

func TestLoggingAnEmptyCounterWritesNothing(t *testing.T) {
	log, buf := logBuffer()
	New().Log(log)
	if buf.Len() != 0 {
		t.Fatalf("log = %s", buf.String())
	}
}

func TestLoggingWithNoLoggerIsHarmless(t *testing.T) {
	c := New()
	c.Add(ClaimRefused, "stale")
	c.Log(nil)
	var nilCounters *Counters
	nilCounters.Log(nil)
}
