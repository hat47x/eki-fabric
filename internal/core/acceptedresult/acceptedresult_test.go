package acceptedresult

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
)

var baseTime = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func validConfig() Config {
	return Config{
		TaskID:       "tsk_00000000000000000000000000000001",
		AttemptID:    "atm_00000000000000000000000000000001",
		ClaimDigest:  contract.SHA256([]byte("claim")),
		OutputDigest: contract.SHA256([]byte("output")),
		Level:        resultaccept.LevelRA2,
		PolicyDigest: contract.SHA256([]byte("policy")),
		Verifiers:    []string{"verifier-b", "verifier-a"},
		AcceptedAt:   baseTime,
	}
}

func TestNewBuildsAnImmutableRecord(t *testing.T) {
	cfg := validConfig()
	record, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if !record.Valid() || record.TaskID() != cfg.TaskID || record.AttemptID() != cfg.AttemptID ||
		record.ClaimDigest() != cfg.ClaimDigest || record.OutputDigest() != cfg.OutputDigest ||
		record.Level() != resultaccept.LevelRA2 || record.PolicyDigest() != cfg.PolicyDigest ||
		!record.AcceptedAt().Equal(baseTime) {
		t.Fatalf("record = %+v", record)
	}

	// The caller's slice is copied in, and a copy is handed out.
	cfg.Verifiers[0] = "changed"
	verifiers := record.Verifiers()
	if len(verifiers) != 2 || verifiers[0] != "verifier-a" || verifiers[1] != "verifier-b" {
		t.Fatalf("verifiers = %v, want them sorted and unaffected by the caller", verifiers)
	}
	verifiers[0] = "changed"
	if record.Verifiers()[0] != "verifier-a" {
		t.Fatal("Verifiers() exposes the record's own slice")
	}
}

func TestNewRejectsAnIncompleteOrUnjustifiedRecord(t *testing.T) {
	cases := map[string]func(*Config){
		"task":               func(c *Config) { c.TaskID = "" },
		"attempt":            func(c *Config) { c.AttemptID = "" },
		"claim digest":       func(c *Config) { c.ClaimDigest = contract.Digest{} },
		"output digest":      func(c *Config) { c.OutputDigest = contract.Digest{} },
		"policy digest":      func(c *Config) { c.PolicyDigest = contract.Digest{} },
		"no assurance level": func(c *Config) { c.Level = resultaccept.LevelRA0 },
		"an unproven level":  func(c *Config) { c.Level = resultaccept.AssuranceLevel("RA-3") },
		"no verifiers":       func(c *Config) { c.Verifiers = nil },
		"empty verifier":     func(c *Config) { c.Verifiers = []string{"verifier-a", ""} },
		"duplicate verifier": func(c *Config) { c.Verifiers = []string{"verifier-a", "verifier-a"} },
		"overlong verifier":  func(c *Config) { c.Verifiers = []string{strings.Repeat("v", maxVerifierName+1)} },
		"invalid utf-8":      func(c *Config) { c.Verifiers = []string{"verifier-\xff"} },
		"too many verifiers": func(c *Config) { c.Verifiers = manyVerifiers(maxVerifiers + 1) },
		"no acceptance time": func(c *Config) { c.AcceptedAt = time.Time{} },
	}
	for name, mutate := range cases {
		cfg := validConfig()
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("%s: New() error = %v, want %v", name, err, ErrInvalidRecord)
		}
	}
}

func manyVerifiers(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("verifier-%04d", i)
	}
	return out
}

func TestEqualComparesTheWholeDecision(t *testing.T) {
	base, err := New(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	same, err := New(validConfig())
	if err != nil || !base.Equal(same) {
		t.Fatalf("identical records are not Equal: %v", err)
	}
	cases := map[string]func(*Config){
		"task":      func(c *Config) { c.TaskID = "tsk_00000000000000000000000000000002" },
		"attempt":   func(c *Config) { c.AttemptID = "atm_00000000000000000000000000000002" },
		"claim":     func(c *Config) { c.ClaimDigest = contract.SHA256([]byte("another claim")) },
		"output":    func(c *Config) { c.OutputDigest = contract.SHA256([]byte("another output")) },
		"level":     func(c *Config) { c.Level = resultaccept.LevelRA1 },
		"policy":    func(c *Config) { c.PolicyDigest = contract.SHA256([]byte("another policy")) },
		"verifiers": func(c *Config) { c.Verifiers = []string{"verifier-a", "verifier-c"} },
		"time":      func(c *Config) { c.AcceptedAt = baseTime.Add(time.Second) },
	}
	for name, mutate := range cases {
		cfg := validConfig()
		mutate(&cfg)
		other, err := New(cfg)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if base.Equal(other) {
			t.Fatalf("records that differ in %s are Equal", name)
		}
	}
}

func TestAcceptanceTimeIsKeptInUTC(t *testing.T) {
	cfg := validConfig()
	cfg.AcceptedAt = baseTime.In(time.FixedZone("JST", 9*3600))
	record, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if record.AcceptedAt().Location() != time.UTC || !record.AcceptedAt().Equal(baseTime) {
		t.Fatalf("AcceptedAt() = %s", record.AcceptedAt())
	}
}
