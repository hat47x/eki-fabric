package workerregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/ids"
)

var now = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func mustCredential(t *testing.T, ttl time.Duration) (Credential, Secret) {
	t.Helper()
	id, err := ids.NewEnrollmentCredentialID()
	if err != nil {
		t.Fatal(err)
	}
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCredential(id, secret, "team-a.example", "admin:root", ttl, now)
	if err != nil {
		t.Fatalf("NewCredential() error = %v", err)
	}
	return c, secret
}

func TestSecretShapeAndRedaction(t *testing.T) {
	a, _ := NewSecret()
	b, _ := NewSecret()
	if a.Reveal() == b.Reveal() {
		t.Fatal("two secrets are equal")
	}
	if !strings.HasPrefix(a.Reveal(), SecretPrefix) {
		t.Fatalf("secret %q lacks prefix", a.Reveal())
	}
	if _, err := HashPresentedSecret(a.Reveal()); err != nil {
		t.Fatalf("a generated secret is malformed: %v", err)
	}
	encoded, _ := json.Marshal(map[string]any{"s": a})
	var logged strings.Builder
	slog.New(slog.NewTextHandler(&logged, nil)).Info("x", "secret", a)
	random := strings.TrimPrefix(a.Reveal(), SecretPrefix)
	for _, shown := range []string{fmt.Sprintf("%v %+v %#v %s", a, a, a, a), string(encoded), logged.String()} {
		if strings.Contains(shown, random) {
			t.Fatalf("secret leaked: %q", shown)
		}
	}
}

func TestHashPresentedSecretRejectsMalformedText(t *testing.T) {
	good, _ := NewSecret()
	body := strings.TrimPrefix(good.Reveal(), SecretPrefix)
	for _, in := range []string{"", "x", SecretPrefix, SecretPrefix + "short", good.Reveal() + "A", good.Reveal()[:len(good.Reveal())-1], "other_" + body, SecretPrefix + strings.Repeat("!", len(body))} {
		_, err := HashPresentedSecret(in)
		if !errors.Is(err, ErrCredentialRefused) {
			t.Fatalf("HashPresentedSecret(%q) error = %v", in, err)
		}
		if reason, _ := RefusalReason(err); reason != ReasonMalformed {
			t.Fatalf("reason = %q", reason)
		}
	}
}

func TestCredentialLifetimeBounds(t *testing.T) {
	c, _ := mustCredential(t, 0)
	if c.ExpiresAt.Sub(c.IssuedAt) != DefaultCredentialTTL {
		t.Fatalf("default lifetime = %v", c.ExpiresAt.Sub(c.IssuedAt))
	}
	id, _ := ids.NewEnrollmentCredentialID()
	secret, _ := NewSecret()
	for _, ttl := range []time.Duration{-1, MaxCredentialTTL + 1} {
		if _, err := NewCredential(id, secret, "team-a.example", "admin:root", ttl, now); !errors.Is(err, ErrInvalidCredential) {
			t.Fatalf("ttl %v error = %v", ttl, err)
		}
	}
	if _, err := NewCredential(id, secret, "team-a.example", "admin:root", MaxCredentialTTL, now); err != nil {
		t.Fatalf("seven days error = %v", err)
	}
}

func TestCheckUsableReportsTheInternalReason(t *testing.T) {
	c, secret := mustCredential(t, time.Hour)
	other, _ := NewSecret()
	consumed, err := c.Consume("wrk_00000000000000000000000000000001", now)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := c.Revoke("admin:root", now)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		cred   Credential
		hash   Secret
		domain string
		at     time.Time
		want   Reason
	}{
		{"usable", c, secret, "team-a.example", now, ""},
		{"expired at deadline", c, secret, "team-a.example", c.ExpiresAt, ReasonExpired},
		{"wrong domain", c, secret, "team-b.example", now, ReasonWrongTrustDomain},
		{"consumed", consumed, secret, "team-a.example", now, ReasonConsumed},
		{"revoked", revoked, secret, "team-a.example", now, ReasonRevoked},
		{"row of another secret", c, other, "team-a.example", now, ReasonInconsistentStore},
	}
	var text string
	for _, tc := range cases {
		err := tc.cred.CheckUsable(tc.hash.Hash(), tc.domain, tc.at)
		if tc.want == "" {
			if err != nil {
				t.Fatalf("%s: error = %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrCredentialRefused) {
			t.Fatalf("%s: error = %v", tc.name, err)
		}
		if reason, _ := RefusalReason(err); reason != tc.want {
			t.Fatalf("%s: reason = %q, want %q", tc.name, reason, tc.want)
		}
		if text == "" {
			text = err.Error()
		} else if err.Error() != text {
			t.Fatalf("%s: text %q differs from %q", tc.name, err.Error(), text)
		}
	}
}

func TestCredentialStatusFollowsTheClock(t *testing.T) {
	c, _ := mustCredential(t, time.Hour)
	if c.Status(now) != StatusIssued || c.Status(c.ExpiresAt) != StatusExpired {
		t.Fatalf("status = %s / %s", c.Status(now), c.Status(c.ExpiresAt))
	}
}

func TestCredentialRevokeRules(t *testing.T) {
	c, _ := mustCredential(t, 0)
	r, err := c.Revoke("admin:root", now)
	if err != nil || r.State != CredentialRevoked {
		t.Fatalf("Revoke() = %+v, %v", r, err)
	}
	again, err := r.Revoke("admin:other", now.Add(time.Hour))
	if err != nil || again != r {
		t.Fatalf("second Revoke() changed the record: %+v, %v", again, err)
	}
	spent, _ := c.Consume("wrk_00000000000000000000000000000001", now)
	if _, err := spent.Revoke("admin:root", now); !errors.Is(err, ErrCredentialConsumed) {
		t.Fatalf("Revoke(consumed) error = %v", err)
	}
	if _, err := r.Consume("wrk_00000000000000000000000000000001", now); !errors.Is(err, ErrCredentialRefused) {
		t.Fatalf("Consume(revoked) error = %v", err)
	}
}

func newWorker(t *testing.T) Worker {
	t.Helper()
	c, _ := mustCredential(t, 0)
	id, _ := ids.NewWorkerIdentity()
	w, err := NewWorker(id, c, now)
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	return w
}

func TestWorkerTransitions(t *testing.T) {
	w := newWorker(t)
	allowed := []struct{ from, to State }{
		{StateRegistered, StateDisabled},
		{StateDisabled, StateRegistered},
		{StateRegistered, StateRevoked},
		{StateDisabled, StateRevoked},
	}
	for _, tc := range allowed {
		start := w
		start.State = tc.from
		start.Revision = 2
		start.StateChangedBy = "admin:root"
		next, err := start.Transition(tc.to, "admin:root", now.Add(time.Minute))
		if err != nil || next.State != tc.to || next.Revision != 3 {
			t.Fatalf("%s -> %s = %+v, %v", tc.from, tc.to, next, err)
		}
	}
	revoked, _ := w.Transition(StateRevoked, "admin:root", now)
	for _, to := range []State{StateRegistered, StateDisabled, StateRevoked} {
		if _, err := revoked.Transition(to, "admin:root", now); !errors.Is(err, ErrWorkerRevoked) {
			t.Fatalf("revoked -> %s error = %v", to, err)
		}
	}
	if _, err := w.Transition("frozen", "admin:root", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unknown state error = %v", err)
	}
	if _, err := w.Transition(StateDisabled, "", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("empty actor error = %v", err)
	}
}

func TestWorkerUpdateTimeNeverMovesBackwards(t *testing.T) {
	w := newWorker(t)
	next, err := w.Transition(StateDisabled, "admin:root", now.Add(-time.Hour))
	if err != nil || next.UpdatedAt.Before(w.UpdatedAt) {
		t.Fatalf("Transition() = %+v, %v", next, err)
	}
	cert, err := next.WithCertificate(Certificate{Serial: "ab", NotAfter: now.Add(time.Hour)}, now.Add(-time.Hour))
	if err != nil || cert.UpdatedAt.Before(next.UpdatedAt) {
		t.Fatalf("WithCertificate() = %+v, %v", cert, err)
	}
}

func TestNewWorkerIsRegisteredAndFresh(t *testing.T) {
	w := newWorker(t)
	if w.State != StateRegistered || !w.Usable() || w.Revision != 1 || w.StateChangedBy != "" {
		t.Fatalf("worker = %+v", w)
	}
	bad := w
	bad.Identity = "wrk_x"
	if err := bad.Validate(); !errors.Is(err, ErrInvalidWorker) {
		t.Fatalf("Validate() error = %v", err)
	}
	disabled, _ := w.Transition(StateDisabled, "admin:root", now)
	if disabled.Usable() {
		t.Fatal("a disabled worker is usable")
	}
}
