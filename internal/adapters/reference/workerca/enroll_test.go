package workerca_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) set(t time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.now = t }

type enrollFixture struct {
	svc   *workerenrollment.Service
	store *memory.Store
	clock *fakeClock
	admin workerenrollment.Admin
}

func newEnrollFixture(t *testing.T) enrollFixture {
	t.Helper()
	ca, _, _ := newCA(t, "example.org", 0)
	store := memory.New()
	clock := &fakeClock{now: t0}
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: clock, Issuer: ca})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := workerenrollment.NewAdmin("operator")
	if err != nil {
		t.Fatal(err)
	}
	return enrollFixture{svc, store, clock, admin}
}

func (f enrollFixture) credential(t *testing.T) string {
	t.Helper()
	c, err := f.svc.IssueEnrollmentCredential(context.Background(), f.admin, workerenrollment.IssueRequest{TrustDomain: "example.org"})
	if err != nil {
		t.Fatal(err)
	}
	return c.Secret.Reveal()
}

func TestEnrollRegistersTheWorkerAndRecordsTheCertificate(t *testing.T) {
	f := newEnrollFixture(t)
	ctx := context.Background()
	key := newKey(t)
	got, err := f.svc.Enroll(ctx, f.credential(t), "example.org", &key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.svc.LookupWorker(ctx, got.Worker.Identity)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Certificate.Serial != got.Certificate.Serial || !stored.Certificate.NotAfter.Equal(got.Certificate.NotAfter) {
		t.Fatalf("registry certificate %+v, issued %+v", stored.Certificate, got.Certificate)
	}
	if !stored.Usable() {
		t.Fatalf("enrolled Worker is %s", stored.State)
	}
}

func TestEnrollRefusesAReusedCredentialIdentically(t *testing.T) {
	f := newEnrollFixture(t)
	ctx := context.Background()
	secret := f.credential(t)
	if _, err := f.svc.Enroll(ctx, secret, "example.org", &newKey(t).PublicKey); err != nil {
		t.Fatal(err)
	}
	_, second := f.svc.Enroll(ctx, secret, "example.org", &newKey(t).PublicKey)
	_, unknown := f.svc.Enroll(ctx, "eki_enroll_v1_"+"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "example.org", &newKey(t).PublicKey)
	_, wrongDomain := f.svc.Enroll(ctx, f.credential(t), "other.example", &newKey(t).PublicKey)
	for name, err := range map[string]error{"reused": second, "unknown": unknown, "other domain": wrongDomain} {
		if !errors.Is(err, workerregistry.ErrCredentialRefused) {
			t.Fatalf("%s: %v, want the uniform refusal", name, err)
		}
		if err.Error() != workerregistry.ErrCredentialRefused.Error() {
			t.Fatalf("%s: refusal text %q differs", name, err.Error())
		}
	}
}

func TestRenewIssuesANewCertificateOnlyForARegisteredWorker(t *testing.T) {
	f := newEnrollFixture(t)
	ctx := context.Background()
	first, err := f.svc.Enroll(ctx, f.credential(t), "example.org", &newKey(t).PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	principal := workerenrollment.WorkerPrincipal{Identity: first.Worker.Identity, TrustDomain: "example.org"}

	f.clock.set(t0.Add(48 * time.Hour))
	renewed, err := f.svc.Renew(ctx, principal, &newKey(t).PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Serial == first.Certificate.Serial || !renewed.NotAfter.After(first.Certificate.NotAfter) {
		t.Fatalf("renewed %+v after %+v", renewed, first.Certificate)
	}
	stored, _ := f.svc.LookupWorker(ctx, principal.Identity)
	if stored.Certificate.Serial != renewed.Serial {
		t.Fatalf("registry holds %s, want the renewed %s", stored.Certificate.Serial, renewed.Serial)
	}

	// Another trust domain's principal does not renew.
	if _, err := f.svc.Renew(ctx, workerenrollment.WorkerPrincipal{Identity: principal.Identity, TrustDomain: "other.example"}, &newKey(t).PublicKey); !errors.Is(err, workerenrollment.ErrWorkerNotUsable) {
		t.Fatalf("other domain renewed: %v", err)
	}
	// A disabled Worker does not renew; enabling it again restores it.
	if _, err := f.svc.SetWorkerState(ctx, f.admin, principal.Identity, workerregistry.StateDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Renew(ctx, principal, &newKey(t).PublicKey); !errors.Is(err, workerenrollment.ErrWorkerNotUsable) {
		t.Fatalf("disabled Worker renewed: %v", err)
	}
	if _, err := f.svc.SetWorkerState(ctx, f.admin, principal.Identity, workerregistry.StateRegistered); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Renew(ctx, principal, &newKey(t).PublicKey); err != nil {
		t.Fatalf("re-enabled Worker: %v", err)
	}
	// A revoked Worker never renews.
	if _, err := f.svc.SetWorkerState(ctx, f.admin, principal.Identity, workerregistry.StateRevoked); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Renew(ctx, principal, &newKey(t).PublicKey); !errors.Is(err, workerenrollment.ErrWorkerNotUsable) {
		t.Fatalf("revoked Worker renewed: %v", err)
	}
}

func TestEnrollWithoutAnIssuerFailsBeforeSpendingTheSecret(t *testing.T) {
	store := memory.New()
	clock := &fakeClock{now: t0}
	svc, _ := workerenrollment.New(workerenrollment.Config{Store: store, Clock: clock})
	admin, _ := workerenrollment.NewAdmin("operator")
	c, _ := svc.IssueEnrollmentCredential(context.Background(), admin, workerenrollment.IssueRequest{TrustDomain: "example.org"})
	if _, err := svc.Enroll(context.Background(), c.Secret.Reveal(), "example.org", &newKey(t).PublicKey); !errors.Is(err, workerenrollment.ErrNoIssuer) {
		t.Fatalf("Enroll = %v, want ErrNoIssuer", err)
	}
	if got, _ := store.LoadEnrollmentCredential(context.Background(), c.ID); got.State != workerregistry.CredentialIssued {
		t.Fatalf("credential is %s after a failed Enroll", got.State)
	}
}
