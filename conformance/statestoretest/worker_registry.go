package statestoretest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

type WorkerRegistryFactory func(t testing.TB) statestore.WorkerRegistryStore

type DurableWorkerRegistryFactory struct {
	Open func(t testing.TB, location string) (store statestore.WorkerRegistryStore, close func() error)
}

const registryDomain = "team-a.example"

var registryNow = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

type registryClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *registryClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *registryClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

type registryFixture struct {
	store statestore.WorkerRegistryStore
	svc   *workerenrollment.Service
	clock *registryClock
	admin workerenrollment.Admin
}

func newRegistryFixture(t testing.TB, store statestore.WorkerRegistryStore) registryFixture {
	t.Helper()
	clock := &registryClock{now: registryNow}
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: clock})
	if err != nil {
		t.Fatalf("workerenrollment.New() error = %v", err)
	}
	admin, err := workerenrollment.NewAdmin("root")
	if err != nil {
		t.Fatalf("NewAdmin() error = %v", err)
	}
	return registryFixture{store: store, svc: svc, clock: clock, admin: admin}
}

func (f registryFixture) issue(t testing.TB, domain string, ttl time.Duration) workerenrollment.IssuedCredential {
	t.Helper()
	got, err := f.svc.IssueEnrollmentCredential(context.Background(), f.admin, workerenrollment.IssueRequest{TrustDomain: domain, TTL: ttl})
	if err != nil {
		t.Fatalf("IssueEnrollmentCredential() error = %v", err)
	}
	return got
}

func (f registryFixture) enroll(t testing.TB) workerregistry.Worker {
	t.Helper()
	cred := f.issue(t, registryDomain, 0)
	w, err := f.svc.ConsumeEnrollmentCredential(context.Background(), cred.Secret.Reveal(), registryDomain)
	if err != nil {
		t.Fatalf("ConsumeEnrollmentCredential() error = %v", err)
	}
	return w
}

func assertRefusal(t testing.TB, err error, want workerregistry.Reason) {
	t.Helper()
	if !errors.Is(err, workerregistry.ErrCredentialRefused) {
		t.Fatalf("error = %v, want ErrCredentialRefused", err)
	}
	got, ok := workerregistry.RefusalReason(err)
	if !ok || got != want {
		t.Fatalf("refusal reason = %q (ok=%v), want %q", got, ok, want)
	}
}

func RunWorkerRegistryContract(t *testing.T, newStore WorkerRegistryFactory) {
	t.Helper()
	ctx := context.Background()
	runWorkerListingContract(t, newStore)
	newFixture := func(t *testing.T) registryFixture {
		t.Helper()
		return newRegistryFixture(t, newStore(t))
	}

	t.Run("issue-and-consume-creates-a-registered-worker", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, 0)
		if !strings.HasPrefix(cred.Secret.Reveal(), workerregistry.SecretPrefix) {
			t.Fatalf("secret lacks the scanner prefix")
		}
		if got := cred.ExpiresAt.Sub(cred.IssuedAt); got != workerregistry.DefaultCredentialTTL {
			t.Fatalf("default lifetime = %v, want %v", got, workerregistry.DefaultCredentialTTL)
		}

		w, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain)
		if err != nil {
			t.Fatalf("ConsumeEnrollmentCredential() error = %v", err)
		}
		if w.State != workerregistry.StateRegistered || w.TrustDomain != registryDomain || w.Revision != 1 || w.CredentialID != cred.ID {
			t.Fatalf("worker = %+v", w)
		}
		if _, err := ids.ParseWorkerIdentity(string(w.Identity)); err != nil {
			t.Fatalf("identity %q is not registry-issued: %v", w.Identity, err)
		}
		if !w.CreatedAt.Equal(registryNow) || w.Certificate.Serial != "" {
			t.Fatalf("worker times/certificate = %+v", w)
		}
		loaded, err := f.svc.LookupWorker(ctx, w.Identity)
		if err != nil || loaded != w {
			t.Fatalf("LookupWorker() = %+v, %v; want %+v", loaded, err, w)
		}
		stored, err := f.store.LoadEnrollmentCredential(ctx, cred.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.State != workerregistry.CredentialConsumed || stored.ConsumedWorker != w.Identity || stored.Status(registryNow) != workerregistry.StatusConsumed {
			t.Fatalf("stored credential = %+v", stored)
		}
	})

	t.Run("two-credentials-give-two-distinct-identities", func(t *testing.T) {
		f := newFixture(t)
		a, b := f.enroll(t), f.enroll(t)
		if a.Identity == b.Identity {
			t.Fatalf("two Workers share identity %s", a.Identity)
		}
	})

	t.Run("only-the-secret-digest-is-stored", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, 0)
		stored, err := f.store.LoadEnrollmentCredential(ctx, cred.ID)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(cred.Secret.Reveal()))
		if stored.SecretSHA256.Hex() != hex.EncodeToString(sum[:]) {
			t.Fatalf("stored digest = %s", stored.SecretSHA256.Hex())
		}
		for _, shown := range []string{cred.Secret.String(), cred.Secret.GoString()} {
			if strings.Contains(shown, cred.Secret.Reveal()) {
				t.Fatalf("Secret prints itself: %q", shown)
			}
		}
	})

	t.Run("double-consume-is-refused", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, 0)
		if _, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain); err != nil {
			t.Fatal(err)
		}
		_, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain)
		assertRefusal(t, err, workerregistry.ReasonConsumed)
	})

	t.Run("concurrent-consume-creates-exactly-one-worker", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, 0)

		const racers = 16
		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			winners  []workerregistry.Worker
			refusals int
			others   []error
			start    = make(chan struct{})
		)
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				w, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					winners = append(winners, w)
				case errors.Is(err, workerregistry.ErrCredentialRefused):
					refusals++
				default:
					others = append(others, err)
				}
			}()
		}
		close(start)
		wg.Wait()

		if len(winners) != 1 || refusals != racers-1 || len(others) != 0 {
			t.Fatalf("winners=%d refusals=%d other errors=%v, want 1/%d/none", len(winners), refusals, others, racers-1)
		}
		stored, err := f.store.LoadEnrollmentCredential(ctx, cred.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.ConsumedWorker != winners[0].Identity {
			t.Fatalf("credential names %s, the winner is %s", stored.ConsumedWorker, winners[0].Identity)
		}
	})

	t.Run("unusable-credentials-are-refused-alike", func(t *testing.T) {
		f := newFixture(t)

		expired := f.issue(t, registryDomain, time.Hour)
		revoked := f.issue(t, registryDomain, 0)
		if err := f.svc.RevokeEnrollmentCredential(ctx, f.admin, revoked.ID); err != nil {
			t.Fatal(err)
		}
		consumed := f.issue(t, registryDomain, 0)
		if _, err := f.svc.ConsumeEnrollmentCredential(ctx, consumed.Secret.Reveal(), registryDomain); err != nil {
			t.Fatal(err)
		}
		otherDomain := f.issue(t, "team-b.example", 0)
		neverIssued, err := workerregistry.NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		f.clock.set(registryNow.Add(2 * time.Hour))

		cases := []struct {
			name      string
			presented string
			domain    string
			reason    workerregistry.Reason
		}{
			{"expired", expired.Secret.Reveal(), registryDomain, workerregistry.ReasonExpired},
			{"revoked", revoked.Secret.Reveal(), registryDomain, workerregistry.ReasonRevoked},
			{"consumed", consumed.Secret.Reveal(), registryDomain, workerregistry.ReasonConsumed},
			{"wrong-domain", otherDomain.Secret.Reveal(), registryDomain, workerregistry.ReasonWrongTrustDomain},
			{"unknown", neverIssued.Reveal(), registryDomain, workerregistry.ReasonUnknown},
			{"malformed", "not-a-secret", registryDomain, workerregistry.ReasonMalformed},
			{"empty", "", registryDomain, workerregistry.ReasonMalformed},
		}
		var text string
		for _, tc := range cases {
			_, err := f.svc.ConsumeEnrollmentCredential(ctx, tc.presented, tc.domain)
			assertRefusal(t, err, tc.reason)
			if text == "" {
				text = err.Error()
			}
			if err.Error() != text {
				t.Fatalf("%s: error text %q differs from %q", tc.name, err.Error(), text)
			}
			if strings.Contains(err.Error(), tc.presented) && tc.presented != "" {
				t.Fatalf("%s: error repeats the presented secret", tc.name)
			}
		}
	})

	t.Run("expiry-is-at-the-deadline", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, time.Hour)
		f.clock.set(cred.ExpiresAt.Add(-time.Nanosecond))
		if _, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain); err != nil {
			t.Fatalf("just before the deadline error = %v", err)
		}
		again := f.issue(t, registryDomain, time.Hour)
		f.clock.set(again.ExpiresAt)
		_, err := f.svc.ConsumeEnrollmentCredential(ctx, again.Secret.Reveal(), registryDomain)
		assertRefusal(t, err, workerregistry.ReasonExpired)
	})

	t.Run("wrong-domain-does-not-spend-the-credential", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, 0)
		_, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), "team-b.example")
		assertRefusal(t, err, workerregistry.ReasonWrongTrustDomain)
		if _, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain); err != nil {
			t.Fatalf("credential was spent by a refused attempt: %v", err)
		}
	})

	t.Run("lifetime-over-seven-days-is-refused", func(t *testing.T) {
		f := newFixture(t)
		for _, ttl := range []time.Duration{workerregistry.MaxCredentialTTL + time.Nanosecond, 30 * 24 * time.Hour, -time.Hour} {
			_, err := f.svc.IssueEnrollmentCredential(ctx, f.admin, workerenrollment.IssueRequest{TrustDomain: registryDomain, TTL: ttl})
			if !errors.Is(err, workerenrollment.ErrInvalidRequest) {
				t.Fatalf("ttl %v error = %v, want ErrInvalidRequest", ttl, err)
			}
		}
		got := f.issue(t, registryDomain, workerregistry.MaxCredentialTTL)
		if got.ExpiresAt.Sub(got.IssuedAt) != workerregistry.MaxCredentialTTL {
			t.Fatalf("seven-day lifetime = %v", got.ExpiresAt.Sub(got.IssuedAt))
		}

		// The store enforces the cap too, for a caller that skips the service.
		secret, err := workerregistry.NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		id, _ := ids.NewEnrollmentCredentialID()
		long := workerregistry.Credential{
			ID: id, SecretSHA256: secret.Hash(), TrustDomain: registryDomain, IssuedBy: "admin:root",
			IssuedAt: registryNow, ExpiresAt: registryNow.Add(workerregistry.MaxCredentialTTL + time.Hour),
			State: workerregistry.CredentialIssued,
		}
		if err := f.store.IssueEnrollmentCredential(ctx, long); !errors.Is(err, workerregistry.ErrInvalidCredential) {
			t.Fatalf("store accepted an eight-day credential: %v", err)
		}
	})

	t.Run("invalid-trust-domain-is-refused-at-issue", func(t *testing.T) {
		f := newFixture(t)
		for _, domain := range []string{"", "Team-A", "a/b", "a b", strings.Repeat("a", 254)} {
			_, err := f.svc.IssueEnrollmentCredential(ctx, f.admin, workerenrollment.IssueRequest{TrustDomain: domain})
			if !errors.Is(err, workerenrollment.ErrInvalidRequest) {
				t.Fatalf("domain %q error = %v, want ErrInvalidRequest", domain, err)
			}
		}
	})

	t.Run("only-an-administrator-changes-the-registry", func(t *testing.T) {
		f := newFixture(t)
		w := f.enroll(t)
		cred := f.issue(t, registryDomain, 0)
		var none workerenrollment.Admin

		if _, err := f.svc.IssueEnrollmentCredential(ctx, none, workerenrollment.IssueRequest{TrustDomain: registryDomain}); !errors.Is(err, workerenrollment.ErrNotAdmin) {
			t.Fatalf("issue without admin error = %v", err)
		}
		if err := f.svc.RevokeEnrollmentCredential(ctx, none, cred.ID); !errors.Is(err, workerenrollment.ErrNotAdmin) {
			t.Fatalf("revoke credential without admin error = %v", err)
		}
		if _, err := f.svc.SetWorkerState(ctx, none, w.Identity, workerregistry.StateDisabled); !errors.Is(err, workerenrollment.ErrNotAdmin) {
			t.Fatalf("set state without admin error = %v", err)
		}
		if got, _ := f.svc.LookupWorker(ctx, w.Identity); got.State != workerregistry.StateRegistered {
			t.Fatalf("worker state = %s after refused calls", got.State)
		}
		if _, err := workerenrollment.NewAdmin(""); err == nil {
			t.Fatal("NewAdmin(\"\") succeeded")
		}
		if issued, _ := f.store.LoadEnrollmentCredential(ctx, cred.ID); issued.State != workerregistry.CredentialIssued || issued.IssuedBy != "admin:root" {
			t.Fatalf("credential = %+v", issued)
		}
	})

	t.Run("revoked-credential-cannot-enroll", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, 0)
		for i := 0; i < 2; i++ { // revoking twice is harmless
			if err := f.svc.RevokeEnrollmentCredential(ctx, f.admin, cred.ID); err != nil {
				t.Fatalf("revoke #%d error = %v", i+1, err)
			}
		}
		_, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain)
		assertRefusal(t, err, workerregistry.ReasonRevoked)

		missing, _ := ids.NewEnrollmentCredentialID()
		if err := f.svc.RevokeEnrollmentCredential(ctx, f.admin, missing); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("revoke unknown credential error = %v", err)
		}
	})

	t.Run("consumed-credential-cannot-be-revoked", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, 0)
		if _, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.RevokeEnrollmentCredential(ctx, f.admin, cred.ID); !errors.Is(err, workerregistry.ErrCredentialConsumed) {
			t.Fatalf("revoke consumed error = %v, want ErrCredentialConsumed", err)
		}
	})

	t.Run("duplicate-credential-is-refused", func(t *testing.T) {
		f := newFixture(t)
		cred := f.issue(t, registryDomain, 0)
		stored, err := f.store.LoadEnrollmentCredential(ctx, cred.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.IssueEnrollmentCredential(ctx, stored); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("same ID error = %v", err)
		}
		other, _ := ids.NewEnrollmentCredentialID()
		stored.ID = other
		if err := f.store.IssueEnrollmentCredential(ctx, stored); !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("same secret digest error = %v", err)
		}
	})

	t.Run("worker-states-and-revoked-is-terminal", func(t *testing.T) {
		f := newFixture(t)
		w := f.enroll(t)
		f.clock.set(registryNow.Add(time.Minute))

		disabled, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, workerregistry.StateDisabled)
		if err != nil || disabled.State != workerregistry.StateDisabled || disabled.Revision != 2 || disabled.StateChangedBy != "admin:root" {
			t.Fatalf("disable = %+v, %v", disabled, err)
		}
		enabled, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, workerregistry.StateRegistered)
		if err != nil || enabled.State != workerregistry.StateRegistered || enabled.Revision != 3 {
			t.Fatalf("enable = %+v, %v", enabled, err)
		}
		same, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, workerregistry.StateRegistered)
		if err != nil || same.Revision != 3 {
			t.Fatalf("no-op change = %+v, %v", same, err)
		}
		revoked, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, workerregistry.StateRevoked)
		if err != nil || revoked.State != workerregistry.StateRevoked {
			t.Fatalf("revoke = %+v, %v", revoked, err)
		}

		for _, to := range []workerregistry.State{workerregistry.StateRegistered, workerregistry.StateDisabled, workerregistry.StateRevoked} {
			_, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, to)
			if !errors.Is(err, workerregistry.ErrWorkerRevoked) {
				t.Fatalf("revoked -> %s error = %v, want ErrWorkerRevoked", to, err)
			}
		}
		got, err := f.svc.LookupWorker(ctx, w.Identity)
		if err != nil || got != revoked {
			t.Fatalf("revoked worker changed: %+v, %v", got, err)
		}

		if _, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, "frozen"); err == nil {
			t.Fatal("unknown state accepted")
		}
		missing, _ := ids.NewWorkerIdentity()
		if _, err := f.svc.SetWorkerState(ctx, f.admin, missing, workerregistry.StateDisabled); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("unknown worker error = %v", err)
		}
	})

	t.Run("worker-can-revoke-only-itself", func(t *testing.T) {
		f := newFixture(t)
		a, b := f.enroll(t), f.enroll(t)
		asA := workerenrollment.WorkerPrincipal{Identity: a.Identity, TrustDomain: a.TrustDomain}

		if _, err := f.svc.WorkerSelfRevoke(ctx, asA, b.Identity); !errors.Is(err, workerenrollment.ErrNotSelf) {
			t.Fatalf("A revokes B error = %v, want ErrNotSelf", err)
		}
		if got, _ := f.svc.LookupWorker(ctx, b.Identity); got.State != workerregistry.StateRegistered {
			t.Fatalf("B is %s after A's attempt", got.State)
		}
		wrongDomain := workerenrollment.WorkerPrincipal{Identity: a.Identity, TrustDomain: "team-b.example"}
		if _, err := f.svc.WorkerSelfRevoke(ctx, wrongDomain, a.Identity); !errors.Is(err, workerenrollment.ErrNotSelf) {
			t.Fatalf("revoke from another domain error = %v", err)
		}
		if _, err := f.svc.WorkerSelfRevoke(ctx, workerenrollment.WorkerPrincipal{}, a.Identity); !errors.Is(err, workerenrollment.ErrNotSelf) {
			t.Fatalf("empty principal error = %v", err)
		}

		got, err := f.svc.WorkerSelfRevoke(ctx, asA, a.Identity)
		if err != nil || got.State != workerregistry.StateRevoked || got.StateChangedBy != "worker:"+string(a.Identity) {
			t.Fatalf("self revoke = %+v, %v", got, err)
		}
		again, err := f.svc.WorkerSelfRevoke(ctx, asA, a.Identity)
		if err != nil || again.State != workerregistry.StateRevoked || again.Revision != got.Revision {
			t.Fatalf("second self revoke = %+v, %v", again, err)
		}
		if _, err := f.svc.SetWorkerState(ctx, f.admin, a.Identity, workerregistry.StateRegistered); !errors.Is(err, workerregistry.ErrWorkerRevoked) {
			t.Fatalf("admin re-enables a self-revoked worker: %v", err)
		}
	})

	t.Run("revoked-identity-is-never-issued-again", func(t *testing.T) {
		f := newFixture(t)
		w := f.enroll(t)
		if _, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, workerregistry.StateRevoked); err != nil {
			t.Fatal(err)
		}
		cred := f.issue(t, registryDomain, 0)
		hash, err := workerregistry.HashPresentedSecret(cred.Secret.Reveal())
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.store.ConsumeEnrollmentCredential(ctx, statestore.ConsumeEnrollment{
			SecretSHA256: hash, TrustDomain: registryDomain, Identity: w.Identity, At: registryNow,
		})
		if !errors.Is(err, statestore.ErrAlreadyExists) {
			t.Fatalf("reusing a revoked identity error = %v, want ErrAlreadyExists", err)
		}
		// The failed attempt left the credential unspent.
		got, err := f.svc.ConsumeEnrollmentCredential(ctx, cred.Secret.Reveal(), registryDomain)
		if err != nil {
			t.Fatalf("credential was spent by the failed attempt: %v", err)
		}
		if got.Identity == w.Identity {
			t.Fatal("registry issued the revoked identity again")
		}
		if old, _ := f.svc.LookupWorker(ctx, w.Identity); old.State != workerregistry.StateRevoked || old.CredentialID == got.CredentialID {
			t.Fatalf("revoked worker record changed: %+v", old)
		}
	})

	t.Run("per-request-check", func(t *testing.T) {
		f := newFixture(t)
		w := f.enroll(t)
		p := workerenrollment.WorkerPrincipal{Identity: w.Identity, TrustDomain: w.TrustDomain}
		if _, err := f.svc.Authorize(ctx, p); err != nil {
			t.Fatalf("registered worker error = %v", err)
		}
		unknown, _ := ids.NewWorkerIdentity()
		checks := map[string]workerenrollment.WorkerPrincipal{
			"unknown":      {Identity: unknown, TrustDomain: registryDomain},
			"wrong-domain": {Identity: w.Identity, TrustDomain: "team-b.example"},
		}
		for name, principal := range checks {
			if _, err := f.svc.Authorize(ctx, principal); !errors.Is(err, workerenrollment.ErrWorkerNotUsable) {
				t.Fatalf("%s error = %v", name, err)
			}
		}
		for _, to := range []workerregistry.State{workerregistry.StateDisabled, workerregistry.StateRevoked} {
			if _, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, to); err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.Authorize(ctx, p); !errors.Is(err, workerenrollment.ErrWorkerNotUsable) {
				t.Fatalf("%s worker error = %v", to, err)
			}
		}
	})

	t.Run("update-time-never-moves-backwards", func(t *testing.T) {
		f := newFixture(t)
		w := f.enroll(t)
		f.clock.set(registryNow.Add(-10 * time.Second)) // the wall clock stepped back
		got, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, workerregistry.StateDisabled)
		if err != nil {
			t.Fatalf("SetWorkerState() with the clock behind error = %v", err)
		}
		if got.UpdatedAt.Before(w.CreatedAt) || got.UpdatedAt.Before(w.UpdatedAt) {
			t.Fatalf("updated %v precedes recorded %v", got.UpdatedAt, w.UpdatedAt)
		}

		cred := f.issue(t, registryDomain, 0)
		f.clock.set(registryNow.Add(-time.Hour))
		if err := f.svc.RevokeEnrollmentCredential(ctx, f.admin, cred.ID); err != nil {
			t.Fatalf("revoke with the clock behind error = %v", err)
		}
		stored, err := f.store.LoadEnrollmentCredential(ctx, cred.ID)
		if err != nil || stored.RevokedAt.Before(stored.IssuedAt) {
			t.Fatalf("revoked at %v precedes issue %v (%v)", stored.RevokedAt, stored.IssuedAt, err)
		}
	})

	t.Run("certificate-bookkeeping", func(t *testing.T) {
		f := newFixture(t)
		w := f.enroll(t)
		cert := workerregistry.Certificate{Serial: "0a1b2c", NotAfter: registryNow.Add(72 * time.Hour)}
		got, err := f.store.RecordWorkerCertificate(ctx, w.Identity, cert, registryNow.Add(time.Minute))
		if err != nil || got.Certificate != (workerregistry.Certificate{Serial: "0a1b2c", NotAfter: cert.NotAfter.UTC()}) || got.Revision != 2 {
			t.Fatalf("RecordWorkerCertificate() = %+v, %v", got, err)
		}
		if _, err := f.store.RecordWorkerCertificate(ctx, w.Identity, workerregistry.Certificate{}, registryNow); err == nil {
			t.Fatal("an empty certificate was recorded")
		}
		if _, err := f.svc.SetWorkerState(ctx, f.admin, w.Identity, workerregistry.StateRevoked); err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.RecordWorkerCertificate(ctx, w.Identity, cert, registryNow); !errors.Is(err, workerregistry.ErrWorkerRevoked) {
			t.Fatalf("certificate for a revoked worker error = %v", err)
		}
	})

	t.Run("missing-records", func(t *testing.T) {
		f := newFixture(t)
		worker, _ := ids.NewWorkerIdentity()
		if _, err := f.store.LoadWorker(ctx, worker); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("LoadWorker() error = %v", err)
		}
		cred, _ := ids.NewEnrollmentCredentialID()
		if _, err := f.store.LoadEnrollmentCredential(ctx, cred); !errors.Is(err, statestore.ErrNotFound) {
			t.Fatalf("LoadEnrollmentCredential() error = %v", err)
		}
	})

	t.Run("cancelled-context-is-respected", func(t *testing.T) {
		f := newFixture(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := f.store.LoadWorker(cctx, "wrk_00000000000000000000000000000000"); !errors.Is(err, context.Canceled) {
			t.Fatalf("LoadWorker() error = %v", err)
		}
		if _, err := f.svc.IssueEnrollmentCredential(cctx, f.admin, workerenrollment.IssueRequest{TrustDomain: registryDomain}); !errors.Is(err, context.Canceled) {
			t.Fatalf("IssueEnrollmentCredential() error = %v", err)
		}
	})
}

// RunWorkerRegistryDurabilityContract checks that the registry survives
// closing and reopening the store.
func RunWorkerRegistryDurabilityContract(t *testing.T, factory DurableWorkerRegistryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("registry-survives-restart", func(t *testing.T) {
		location := filepath.Join(t.TempDir(), "state.db")
		store, closeStore := factory.Open(t, location)
		f := newRegistryFixture(t, store)

		spent := f.issue(t, registryDomain, 0)
		worker, err := f.svc.ConsumeEnrollmentCredential(ctx, spent.Secret.Reveal(), registryDomain)
		if err != nil {
			t.Fatal(err)
		}
		unused := f.issue(t, registryDomain, 0)
		revokedCred := f.issue(t, registryDomain, 0)
		if err := f.svc.RevokeEnrollmentCredential(ctx, f.admin, revokedCred.ID); err != nil {
			t.Fatal(err)
		}
		gone := f.enroll(t)
		revokedWorker, err := f.svc.SetWorkerState(ctx, f.admin, gone.Identity, workerregistry.StateRevoked)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.RecordWorkerCertificate(ctx, worker.Identity, workerregistry.Certificate{Serial: "ff", NotAfter: registryNow.Add(time.Hour)}, registryNow); err != nil {
			t.Fatal(err)
		}
		worker, err = f.svc.LookupWorker(ctx, worker.Identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := closeStore(); err != nil {
			t.Fatal(err)
		}

		reopened, closeReopened := factory.Open(t, location)
		defer closeReopened()
		g := newRegistryFixture(t, reopened)

		got, err := g.svc.LookupWorker(ctx, worker.Identity)
		if err != nil || got != worker {
			t.Fatalf("worker after restart = %+v, %v; want %+v", got, err, worker)
		}
		got, err = g.svc.LookupWorker(ctx, gone.Identity)
		if err != nil || got != revokedWorker {
			t.Fatalf("revoked worker after restart = %+v, %v", got, err)
		}
		if _, err := g.svc.SetWorkerState(ctx, g.admin, gone.Identity, workerregistry.StateRegistered); !errors.Is(err, workerregistry.ErrWorkerRevoked) {
			t.Fatalf("re-enable after restart error = %v", err)
		}

		_, err = g.svc.ConsumeEnrollmentCredential(ctx, spent.Secret.Reveal(), registryDomain)
		assertRefusal(t, err, workerregistry.ReasonConsumed)
		_, err = g.svc.ConsumeEnrollmentCredential(ctx, revokedCred.Secret.Reveal(), registryDomain)
		assertRefusal(t, err, workerregistry.ReasonRevoked)

		fresh, err := g.svc.ConsumeEnrollmentCredential(ctx, unused.Secret.Reveal(), registryDomain)
		if err != nil {
			t.Fatalf("unused credential after restart error = %v", err)
		}
		if fresh.Identity == worker.Identity || fresh.Identity == gone.Identity {
			t.Fatalf("identity %s issued twice", fresh.Identity)
		}
		stored, err := g.store.LoadEnrollmentCredential(ctx, spent.ID)
		if err != nil {
			t.Fatal(err)
		}
		digest := contract.SHA256([]byte(spent.Secret.Reveal()))
		if stored.SecretSHA256 != digest {
			t.Fatalf("stored digest changed across restart")
		}
	})
}
