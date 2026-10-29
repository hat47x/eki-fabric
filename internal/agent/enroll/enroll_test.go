package enroll_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	"github.com/hat47x/eki-fabric/internal/agent/enroll"
	server "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/workercert"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type noClaims struct{}

func (noClaims) SubmitClaim(context.Context, workerplane.Principal, resultclaim.Claim) (workerplane.ClaimAck, error) {
	return workerplane.ClaimAck{}, errors.New("unused")
}

type fixture struct {
	clock      *fakeClock
	srv        *httptest.Server
	svc        *workerenrollment.Service
	admin      workerenrollment.Admin
	requests   atomic.Int64
	serverCA   *x509.Certificate
	serverLeaf *x509.Certificate
}

func newFixture(t *testing.T, chain bool, validity time.Duration) *fixture {
	t.Helper()
	// Every certificate the Control Plane issues or verifies, and every check
	// the Agent makes, reads this clock. The host's wall clock is not used, so a
	// step of it (WSL steps by seconds) cannot make a fresh certificate "not yet
	// valid" or an unexpired one expired.
	f := &fixture{clock: newFakeClock(time.Now().UTC().Truncate(time.Second))}
	certPEM, keyPEM, err := workerca.GenerateCA("example.org", 0, f.clock.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := workerca.New(certPEM, keyPEM, "example.org", validity)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := workerenrollment.New(workerenrollment.Config{Store: memory.New(), Clock: f.clock, Issuer: ca})
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	f.admin, _ = workerenrollment.NewAdmin("operator")
	h, err := server.NewMTLSHandler(server.MTLSConfig{
		Claims: noClaims{}, Registry: svc, WorkerCA: ca.Certificate(), TrustDomain: "example.org",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: f.clock.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The Control Plane's own TLS certificate: a CA and a leaf for 127.0.0.1.
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test server CA"},
		NotBefore: f.clock.Now().Add(-time.Hour), NotAfter: f.clock.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	f.serverCA, _ = x509.ParseCertificate(caDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "eki-control"},
		NotBefore: f.clock.Now().Add(-time.Hour), NotAfter: f.clock.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, f.serverCA, &leafKey.PublicKey, caKey)
	f.serverLeaf, _ = x509.ParseCertificate(leafDER)
	chainDER := [][]byte{leafDER}
	if chain {
		chainDER = append(chainDER, caDER)
	}

	f.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		h.ServeHTTP(w, r)
	}))
	f.srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: chainDER, PrivateKey: leafKey}},
		ClientAuth:   tls.RequestClientCert, MinVersion: tls.VersionTLS13,
	}
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) secret(t *testing.T) string {
	t.Helper()
	c, err := f.svc.IssueEnrollmentCredential(context.Background(), f.admin, workerenrollment.IssueRequest{TrustDomain: "example.org"})
	if err != nil {
		t.Fatal(err)
	}
	return c.Secret.Reveal()
}

func (f *fixture) params(t *testing.T, pin *x509.Certificate) enroll.Params {
	t.Helper()
	return enroll.Params{
		ControlURL: f.srv.URL, Fingerprint: workercert.Fingerprint(pin.Raw),
		Secret: f.secret(t), StateDir: filepath.Join(t.TempDir(), "state"), Clock: f.clock,
	}
}

func TestEnrollGeneratesTheKeyLocallyAndSavesTheIdentity(t *testing.T) {
	for name, tc := range map[string]struct {
		chain bool
		pin   func(*fixture) *x509.Certificate
	}{
		"pin the CA the server sends": {true, func(f *fixture) *x509.Certificate { return f.serverCA }},
		"pin the leaf":                {false, func(f *fixture) *x509.Certificate { return f.serverLeaf }},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, tc.chain, 0)
			p := f.params(t, tc.pin(f))
			id, err := enroll.Enroll(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			if id.TrustDomain != "example.org" || id.Leaf.NotAfter.Sub(id.Leaf.NotBefore) != workercert.DefaultValidity {
				t.Fatalf("identity %+v", id)
			}
			// What was saved is what was enrolled, and reloads.
			loaded, err := enroll.Load(p.StateDir)
			if err != nil || loaded.WorkerIdentity != id.WorkerIdentity || !loaded.Key.Equal(id.Key) {
				t.Fatalf("Load = %+v, %v", loaded, err)
			}
			anchor, err := enroll.LoadAnchor(p.StateDir)
			if err != nil || workercert.Fingerprint(anchor.Raw) != p.Fingerprint {
				t.Fatalf("saved anchor %v, %v", anchor, err)
			}
			if runtime.GOOS != "windows" {
				for _, name := range []string{enroll.IdentityFile, enroll.ControlAnchorFile} {
					info, err := os.Stat(filepath.Join(p.StateDir, name))
					if err != nil || info.Mode().Perm()&0o077 != 0 {
						t.Fatalf("%s mode %v, %v", name, info.Mode(), err)
					}
				}
			}
			// The Control Plane has the registry identity and a recorded serial.
			w, err := f.svc.LookupWorker(context.Background(), id.WorkerIdentity)
			if err != nil || w.Certificate.Serial != id.Leaf.SerialNumber.Text(16) {
				t.Fatalf("registry %+v, %v", w, err)
			}
			// The private key is not in anything the Control Plane received: the
			// leaf carries the public half.
			if pub, _ := id.Leaf.PublicKey.(*ecdsa.PublicKey); !pub.Equal(&id.Key.PublicKey) {
				t.Fatal("certificate is not for the locally generated key")
			}
		})
	}
}

func TestEnrollWithAWrongFingerprintSendsNothing(t *testing.T) {
	f := newFixture(t, true, 0)
	p := f.params(t, f.serverCA)
	p.Fingerprint = workercert.Fingerprint([]byte("some other certificate"))
	_, err := enroll.Enroll(context.Background(), p)
	if !errors.Is(err, enroll.ErrNotPinned) {
		t.Fatalf("Enroll = %v, want ErrNotPinned", err)
	}
	if n := f.requests.Load(); n != 0 {
		t.Fatalf("the Control Plane received %d HTTP requests before the pin was proved", n)
	}
	if _, statErr := os.Stat(filepath.Join(p.StateDir, enroll.IdentityFile)); statErr == nil {
		t.Fatal("an identity was written")
	}
	// The secret is unspent: the right pin enrolls with it.
	p.Fingerprint = workercert.Fingerprint(f.serverCA.Raw)
	if _, err := enroll.Enroll(context.Background(), p); err != nil {
		t.Fatalf("the secret was spent by an aborted enrollment: %v", err)
	}
}

func TestEnrollRefusesAPinnedCertificateThatDoesNotVouchForTheHost(t *testing.T) {
	f := newFixture(t, true, 0)
	p := f.params(t, f.serverCA)
	// The same server, reached by a name its certificate does not carry.
	_, port, _ := net.SplitHostPort(f.srv.Listener.Addr().String())
	p.ControlURL = "https://localhost:" + port
	if _, err := enroll.Enroll(context.Background(), p); !errors.Is(err, enroll.ErrNotPinned) {
		t.Fatalf("Enroll = %v, want ErrNotPinned", err)
	}
	if f.requests.Load() != 0 {
		t.Fatal("a request was sent to a host the certificate does not name")
	}
}

func TestEnrollSecondUseIsRefusedAndRefusalLeavesNoIdentity(t *testing.T) {
	f := newFixture(t, true, 0)
	p := f.params(t, f.serverCA)
	if _, err := enroll.Enroll(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	again := p
	again.StateDir = filepath.Join(t.TempDir(), "other")
	if _, err := enroll.Enroll(context.Background(), again); !errors.Is(err, enroll.ErrRefused) {
		t.Fatalf("second use = %v, want ErrRefused", err)
	}
	if _, err := os.Stat(filepath.Join(again.StateDir, enroll.IdentityFile)); err == nil {
		t.Fatal("a refused enrollment left an identity")
	}
	// An existing identity is never replaced.
	p.Secret = f.secret(t)
	if _, err := enroll.Enroll(context.Background(), p); !errors.Is(err, enroll.ErrIdentityExists) {
		t.Fatalf("over an existing identity = %v, want ErrIdentityExists", err)
	}
}

func TestEnrollRejectsBadParametersWithoutTouchingTheNetwork(t *testing.T) {
	f := newFixture(t, true, 0)
	good := f.params(t, f.serverCA)
	for name, mutate := range map[string]func(*enroll.Params){
		"http URL":          func(p *enroll.Params) { p.ControlURL = "http://127.0.0.1:1" },
		"URL with query":    func(p *enroll.Params) { p.ControlURL += "/?token=x" },
		"URL with userinfo": func(p *enroll.Params) { p.ControlURL = "https://u:p@127.0.0.1:1" },
		"bad fingerprint":   func(p *enroll.Params) { p.Fingerprint = "abc" },
		"bad secret":        func(p *enroll.Params) { p.Secret = "hunter2" },
		"no state dir":      func(p *enroll.Params) { p.StateDir = "" },
	} {
		p := good
		mutate(&p)
		_, err := enroll.Enroll(context.Background(), p)
		if !errors.Is(err, enroll.ErrInvalidParams) {
			t.Errorf("%s: %v, want ErrInvalidParams", name, err)
			continue
		}
		if name == "bad secret" && strings.Contains(err.Error(), "hunter2") {
			t.Error("the rejected secret appears in the error")
		}
	}
	if f.requests.Load() != 0 {
		t.Fatal("a parameter error reached the network")
	}
}

func TestRenewUsesANewKeyAndSavesIt(t *testing.T) {
	f := newFixture(t, true, 0)
	p := f.params(t, f.serverCA)
	first, err := enroll.Enroll(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	next, err := enroll.Renew(context.Background(), p.ControlURL, p.StateDir, first, enroll.WithClock(f.clock))
	if err != nil {
		t.Fatal(err)
	}
	if next.WorkerIdentity != first.WorkerIdentity || next.Key.Equal(first.Key) || next.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) == 0 {
		t.Fatalf("renewed identity %+v from %+v", next, first)
	}
	loaded, err := enroll.Load(p.StateDir)
	if err != nil || !loaded.Key.Equal(next.Key) {
		t.Fatalf("the renewed identity was not saved: %v", err)
	}
	// A disabled Worker cannot renew, and the saved identity stays.
	if _, err := f.svc.SetWorkerState(context.Background(), f.admin, first.WorkerIdentity, workerregistry.StateDisabled); err != nil {
		t.Fatal(err)
	}
	if _, err := enroll.Renew(context.Background(), p.ControlURL, p.StateDir, next, enroll.WithClock(f.clock)); err == nil {
		t.Fatal("a disabled Worker renewed")
	}
	if again, _ := enroll.Load(p.StateDir); !again.Key.Equal(next.Key) {
		t.Fatal("a failed renewal replaced the saved identity")
	}
}

func TestLoadRefusesAKeyOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	f := newFixture(t, true, 0)
	p := f.params(t, f.serverCA)
	if _, err := enroll.Enroll(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(p.StateDir, enroll.IdentityFile)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := enroll.Load(p.StateDir); !errors.Is(err, enroll.ErrInvalidIdentity) {
		t.Fatalf("Load = %v, want ErrInvalidIdentity", err)
	}
	if _, err := enroll.Load(t.TempDir()); !errors.Is(err, enroll.ErrNoIdentity) {
		t.Fatalf("Load of an empty directory = %v", err)
	}
}

func TestNewIdentityRefusesCertificatesThatAreNotThisKeysWorkerCertificate(t *testing.T) {
	f := newFixture(t, true, 0)
	p := f.params(t, f.serverCA)
	id, err := enroll.Enroll(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := enroll.NewIdentity(other, id.Leaf); err == nil {
		t.Fatal("a certificate was accepted for another key")
	}
	if _, err := enroll.NewIdentity(id.Key, f.serverLeaf); err == nil {
		t.Fatal("the server certificate was accepted as a Worker identity")
	}
}

// fakeClock is a Clock that moves only when the test says so. A timer fires
// when Advance carries the clock to or past its time.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*fakeTimer]struct{}
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start, timers: map[*fakeTimer]struct{}{}}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

type fakeTimer struct {
	c    *fakeClock
	when time.Time
	ch   chan time.Time
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	_, active := t.c.timers[t]
	delete(t.c.timers, t)
	return active
}

func (c *fakeClock) NewTimer(d time.Duration) enroll.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{c: c, when: c.now.Add(d), ch: make(chan time.Time, 1)}
	if d <= 0 {
		t.ch <- c.now
		return t
	}
	c.timers[t] = struct{}{}
	return t
}

// Advance moves the clock forward and fires the timers that are due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for t := range c.timers {
		if !t.when.After(c.now) {
			t.ch <- c.now
			delete(c.timers, t)
		}
	}
}

// untilNextTimer is how far the clock is from the earliest timer that waits,
// and false when no timer waits.
func (c *fakeClock) untilNextTimer() (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var first time.Time
	for t := range c.timers {
		if first.IsZero() || t.when.Before(first) {
			first = t.when
		}
	}
	if first.IsZero() {
		return 0, false
	}
	return first.Sub(c.now), true
}

// waitForTimer blocks until the loop sleeps on a timer. The real-time bound
// only guards against a stuck loop; a correct run returns at once.
func (c *fakeClock) waitForTimer(t *testing.T, logs *safeBuffer) time.Duration {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		if d, waiting := c.untilNextTimer(); waiting {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("the renewal loop sleeps on no timer; fake now=%s logs=%q", c.Now().Format(time.RFC3339), logs.String())
		}
		time.Sleep(time.Millisecond)
	}
}

// safeBuffer collects the loop's log lines, which another goroutine writes.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// loopRun is RenewLoop running on the fixture's fake clock. The loop sleeps
// on fake timers only, so the test steps the clock from timer to timer and the
// host's wall clock decides nothing.
type loopRun struct {
	t    *testing.T
	f    *fixture
	logs *safeBuffer
	done chan struct{}
	// renewedAt receives the fake time of each renewal.
	renewedAt chan time.Time
}

func startLoop(t *testing.T, f *fixture, p enroll.Params, holder *enroll.Holder) *loopRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &loopRun{t: t, f: f, logs: &safeBuffer{}, done: make(chan struct{}), renewedAt: make(chan time.Time, 16)}
	t.Cleanup(func() { cancel(); <-r.done })
	go func() {
		defer close(r.done)
		enroll.RenewLoop(ctx, holder, p.ControlURL, p.StateDir, 100*time.Millisecond,
			slog.New(slog.NewTextHandler(r.logs, nil)),
			func() { r.renewedAt <- f.clock.Now() },
			enroll.WithClock(f.clock))
	}()
	return r
}

// untilRenewed advances the fake clock to each timer the loop waits on, until
// the loop renews (ok) or returns (not ok). The real-time bound only guards
// against a stuck loop.
func (r *loopRun) untilRenewed() (renewedAt time.Time, ok bool) {
	r.t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		select {
		case at := <-r.renewedAt:
			return at, true
		case <-r.done:
			return time.Time{}, false
		default:
		}
		select {
		case at := <-r.renewedAt:
			return at, true
		case <-r.done:
			return time.Time{}, false
		case <-time.After(time.Millisecond):
		}
		if d, waiting := r.f.clock.untilNextTimer(); waiting {
			r.f.clock.Advance(d)
		}
	}
	r.t.Fatalf("the renewal loop did not renew or return in a minute; fake now=%s logs=%q", r.f.clock.Now().Format(time.RFC3339), r.logs.String())
	return time.Time{}, false
}

func TestRenewLoopRenewsWhenAThirdRemains(t *testing.T) {
	f := newFixture(t, true, time.Minute)
	p := f.params(t, f.serverCA)
	first, err := enroll.Enroll(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	lifetime := first.Leaf.NotAfter.Sub(first.Leaf.NotBefore)
	renewAt := first.Leaf.NotAfter.Add(-lifetime / 3)
	holder := enroll.NewHolder(first)
	run := startLoop(t, f, p, holder)

	at, ok := run.untilRenewed()
	if !ok {
		t.Fatalf("the loop returned without renewing; logs=%q", run.logs.String())
	}
	// It renews once a third of the lifetime remains: not before, and within
	// one check interval after.
	if at.Before(renewAt) || at.Sub(renewAt) > 5*time.Second {
		t.Fatalf("renewed at %s; renewal is due at %s (NotBefore %s, NotAfter %s)",
			at.Format(time.RFC3339), renewAt.Format(time.RFC3339), first.Leaf.NotBefore.Format(time.RFC3339), first.Leaf.NotAfter.Format(time.RFC3339))
	}
	next := holder.Get()
	if next.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) == 0 {
		t.Fatal("the holder still has the first certificate")
	}
	// The Control Plane issued the new certificate at the time of the renewal.
	if !next.Leaf.NotBefore.Equal(at.Truncate(time.Second)) {
		t.Fatalf("new certificate NotBefore %s, renewal at %s", next.Leaf.NotBefore.Format(time.RFC3339), at.Format(time.RFC3339))
	}
	if loaded, err := enroll.Load(p.StateDir); err != nil || loaded.Leaf.SerialNumber.Cmp(next.Leaf.SerialNumber) != 0 {
		t.Fatalf("the renewed identity was not saved: %v", err)
	}
}

func TestRenewLoopDoesNotRenewBeforeAThirdRemains(t *testing.T) {
	f := newFixture(t, true, time.Minute)
	p := f.params(t, f.serverCA)
	first, err := enroll.Enroll(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	holder := enroll.NewHolder(first)
	run := startLoop(t, f, p, holder)
	before := f.requests.Load()

	// Step the clock through the loop's own timers up to one second before
	// the renewal is due.
	due := first.RenewAt()
	for {
		d := f.clock.waitForTimer(t, run.logs)
		if f.clock.Now().Add(d).After(due.Add(-time.Second)) {
			break
		}
		f.clock.Advance(d)
	}
	select {
	case at := <-run.renewedAt:
		t.Fatalf("renewed at %s, before it was due at %s", at.Format(time.RFC3339), due.Format(time.RFC3339))
	default:
	}
	if n := f.requests.Load(); n != before {
		t.Fatalf("%d requests were sent before the renewal was due", n-before)
	}
	if holder.Get().Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) != 0 {
		t.Fatal("the certificate was replaced before it was due")
	}
}

func TestRenewLoopStopsOnceTheCertificateHasExpired(t *testing.T) {
	f := newFixture(t, true, time.Minute)
	p := f.params(t, f.serverCA)
	first, err := enroll.Enroll(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	holder := enroll.NewHolder(first)
	run := startLoop(t, f, p, holder)
	f.clock.waitForTimer(t, run.logs)
	before := f.requests.Load()

	// The clock passes NotAfter while the loop sleeps. An expired certificate
	// cannot renew, so the loop reports it and ends without asking.
	f.clock.Advance(first.Leaf.NotAfter.Sub(f.clock.Now()) + time.Second)
	if _, ok := run.untilRenewed(); ok {
		t.Fatal("an expired certificate was renewed")
	}
	if !strings.Contains(run.logs.String(), "expired and cannot renew") {
		t.Fatalf("logs=%q", run.logs.String())
	}
	if f.requests.Load() != before {
		t.Fatal("a request was sent with an expired certificate")
	}
}
