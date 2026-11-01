package service

import (
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

func TestExecutorApprovalDigestAcceptsHex(t *testing.T) {
	hex := contract.SHA256([]byte("executor approval payload")).Hex()
	c := &ExecutorApprovalConfig{PayloadDigest: hex}
	got, err := c.digest(hex)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hex() != hex {
		t.Fatalf("digest = %s, want %s", got.Hex(), hex)
	}
}

func TestExecutorApprovalDigestRejectsPrefixedValue(t *testing.T) {
	prefixed := contract.SHA256([]byte("executor approval payload")).String()
	c := &ExecutorApprovalConfig{PayloadDigest: prefixed}
	if _, err := c.digest(prefixed); err == nil {
		t.Fatal("a sha256:-prefixed digest was accepted")
	} else if !strings.Contains(err.Error(), "64 hexadecimal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecutorApprovalDigestRejectsShortValue(t *testing.T) {
	c := &ExecutorApprovalConfig{PayloadDigest: "abcd"}
	if _, err := c.digest("abcd"); err == nil {
		t.Fatal("a short digest was accepted")
	}
}
