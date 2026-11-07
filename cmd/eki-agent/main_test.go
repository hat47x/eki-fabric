package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	got := strings.TrimSpace(out.String())
	// コミットの値はビルド方法で変わる（go testのバイナリにはVCS情報が入らない）。
	// 形式と、「dev」と名乗らないことだけを確かめる。
	for _, want := range []string{"eki-agent revision=", " modified=", " go="} {
		if !strings.Contains(got, want) {
			t.Fatalf("out = %q, want it to contain %q", got, want)
		}
	}
	if got == "eki-agent dev" || strings.Contains(got, "\n") {
		t.Fatalf("out = %q", got)
	}
}
