package rejectedresult

import (
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
)

var digest = contract.SHA256([]byte("output"))

func validConfig() Config {
	return Config{
		TaskID: "tsk_1", AttemptID: "atm_1",
		ClaimDigest: contract.SHA256([]byte("claim")), OutputDigest: digest,
		PolicyDigest: contract.SHA256([]byte("policy")), Reason: resultaccept.ReasonAuthorityRejected,
		Dissent: []Dissent{
			{VerifierID: "v-2", Check: resultaccept.CheckDiffers, ObservedDigest: contract.SHA256([]byte("other")).Hex()},
			{VerifierID: "authority", Check: resultaccept.CheckValidatorRejected, ObservedDigest: digest.Hex()},
		},
		Verifiers:  []string{"v-2", "authority"},
		Excluded:   []Exclusion{{VerifierID: "v-0", Reason: "verifier is the producing worker"}},
		RejectedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.FixedZone("x", 3600)),
	}
}

func TestNewSortsListsSoTheSameDecisionCompareEqual(t *testing.T) {
	a, err := New(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.Dissent[0], cfg.Dissent[1] = cfg.Dissent[1], cfg.Dissent[0]
	cfg.Verifiers = []string{"authority", "v-2"}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Equal(b) || a.Dissent()[0].VerifierID != "authority" || a.RejectedAt().Location() != time.UTC {
		t.Fatalf("records differ: %+v vs %+v", a, b)
	}
	other := validConfig()
	other.Excluded = nil
	c, err := New(other)
	if err != nil || a.Equal(c) {
		t.Fatalf("records with different exclusions compare equal: %v", err)
	}
}

func TestNewRefusesRecordsNoRejectionCouldProduce(t *testing.T) {
	cases := map[string]func(*Config){
		"no task":              func(c *Config) { c.TaskID = "" },
		"no attempt":           func(c *Config) { c.AttemptID = "" },
		"no claim":             func(c *Config) { c.ClaimDigest = contract.Digest{} },
		"no output":            func(c *Config) { c.OutputDigest = contract.Digest{} },
		"no policy":            func(c *Config) { c.PolicyDigest = contract.Digest{} },
		"held reason":          func(c *Config) { c.Reason = resultaccept.ReasonVerificationsDisagree },
		"no dissent":           func(c *Config) { c.Dissent = nil },
		"agreeing dissent":     func(c *Config) { c.Dissent[0].Check = resultaccept.CheckReproduced },
		"repeated dissenter":   func(c *Config) { c.Dissent[1].VerifierID = "v-2" },
		"non-canonical digest": func(c *Config) { c.Dissent[0].ObservedDigest = "ABC" },
		"repeated verifier":    func(c *Config) { c.Verifiers = []string{"v-2", "v-2"} },
		"empty exclusion":      func(c *Config) { c.Excluded[0].Reason = "" },
		"no time":              func(c *Config) { c.RejectedAt = time.Time{} },
	}
	for name, mutate := range cases {
		cfg := validConfig()
		cfg.Dissent = append([]Dissent(nil), cfg.Dissent...)
		cfg.Excluded = append([]Exclusion(nil), cfg.Excluded...)
		mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("%s: New() error = %v, want %v", name, err, ErrInvalidRecord)
		}
	}
}

func TestFromDecisionKeepsTheEvidenceOfARejection(t *testing.T) {
	cfg := validConfig()
	decision := resultaccept.Decision{
		Outcome: resultaccept.OutcomeRejected, Reason: resultaccept.ReasonAuthorityRejected,
		PolicyDigest: cfg.PolicyDigest, Verifiers: cfg.Verifiers,
		Dissent: []resultaccept.Observation{
			{VerifierID: "authority", Check: resultaccept.CheckValidatorRejected, ObservedDigest: digest.Hex()},
		},
		Excluded: []resultaccept.Exclusion{{VerifierID: "v-0", Reason: "verifier is the producing worker"}},
	}
	r, err := FromDecision(cfg.TaskID, cfg.AttemptID, cfg.ClaimDigest, cfg.OutputDigest, decision, cfg.RejectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Dissent()) != 1 || len(r.Excluded()) != 1 || r.PolicyDigest() != cfg.PolicyDigest {
		t.Fatalf("record = %+v", r)
	}
	decision.Outcome = resultaccept.OutcomeHeld
	if _, err := FromDecision(cfg.TaskID, cfg.AttemptID, cfg.ClaimDigest, cfg.OutputDigest, decision, cfg.RejectedAt); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("held decision error = %v, want %v", err, ErrInvalidRecord)
	}
}

func TestAccessorsDoNotShareStoredLists(t *testing.T) {
	r, err := New(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	r.Dissent()[0].VerifierID = "changed"
	r.Verifiers()[0] = "changed"
	r.Excluded()[0].Reason = "changed"
	again, _ := New(validConfig())
	if !r.Equal(again) {
		t.Fatal("a caller changed the stored record through an accessor")
	}
}
