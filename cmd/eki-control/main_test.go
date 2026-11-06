package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/control/clientapi"
)

func TestVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	got := strings.TrimSpace(out.String())
	// コミットの値はビルド方法で変わる（go testのバイナリにはVCS情報が入らない）。
	// 形式と、「dev」と名乗らないことだけを確かめる。
	for _, want := range []string{"eki-control revision=", " modified=", " go="} {
		if !strings.Contains(got, want) {
			t.Fatalf("out = %q, want it to contain %q", got, want)
		}
	}
	if got == "eki-control dev" || strings.Contains(got, "\n") {
		t.Fatalf("out = %q", got)
	}
}

func TestWithoutConfigNothingIsStarted(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "no listeners configured") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestBadConfigIsRefusedWithoutServing(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--config", "does-not-exist.json"}, &out, &errOut); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if errOut.Len() == 0 {
		t.Fatal("no error was reported")
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--no-such-flag"}, &out, &errOut); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestGenTokenPrintsATokenAndItsHash(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"gen-token"}, &out, &errOut); code != 0 {
		t.Fatalf("code = %d, err = %q", code, errOut.String())
	}
	var token, hash string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		switch {
		case strings.HasPrefix(line, "token:"):
			token = strings.TrimSpace(strings.TrimPrefix(line, "token:"))
		case strings.HasPrefix(line, "sha256:"):
			hash = strings.TrimSpace(strings.TrimPrefix(line, "sha256:"))
		}
	}
	if len(token) < clientapi.MinTokenBytes {
		t.Fatalf("token %q is too short to be accepted", token)
	}
	if hash != clientapi.HashToken(token) {
		t.Fatalf("printed hash does not match the token")
	}
	if strings.Contains(errOut.String(), token) {
		t.Fatal("the token was written to the diagnostic stream")
	}

	var again bytes.Buffer
	run([]string{"gen-token"}, &again, &bytes.Buffer{})
	if again.String() == out.String() {
		t.Fatal("two generated tokens are identical")
	}
}
