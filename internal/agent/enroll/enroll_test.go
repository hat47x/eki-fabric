package enroll_test

import (
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

type wall struct{}

func (wall) Now() time.Time { return time.Now().UTC() }

type fixture struct {
	srv        *httptest.Server
	svc        *workerenrollment.Service
	admin      workerenrollment.Admin
	requests   atomic.Int64
	serverCA   *x509.Certificate
	serverLeaf *x509.Certificate
}

func newFixture(t *testing.T, chain bool, validity time.Duration) *fixture {
	t.Helper()
	f := &fixture{}
	certPEM, keyPEM, err := workerca.GenerateCA("example.org", 0, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := workerca.New(certPEM, keyPEM, "example.org", validity)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := workerenrollment.New(workerenrollment.Config{Store: memory.New(), Clock: wall{}, Issuer: ca})
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	f.admin, _ = workerenrollment.NewAdmin("operator")
	h, err := server.NewMTLSHandler(server.MTLSConfig{
		Claims: noClaims{}, Registry: svc, WorkerCA: ca.Certificate(), TrustDomain: "example.org",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The Control Plane's own TLS certificate: a CA and a leaf for 127.0.0.1.
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test server CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	f.serverCA, _ = x509.ParseCertificate(caDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "eki-control"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
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
		Secret: f.secret(t), StateDir: filepath.Join(t.TempDir(), "state"),
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
	next, err := enroll.Renew(context.Background(), p.ControlURL, p.StateDir, first)
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
	if _, err := enroll.Renew(context.Background(), p.ControlURL, p.StateDir, next); err == nil {
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

func TestRenewLoopRenewsWhenAThirdRemains(t *testing.T) {
	f := newFixture(t, true, 12*time.Second)
	p := f.params(t, f.serverCA)
	first, err := enroll.Enroll(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	holder := enroll.NewHolder(first)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	renewed := make(chan struct{}, 1)
	go func() {
		defer close(done)
		enroll.RenewLoop(ctx, holder, p.ControlURL, p.StateDir, 100*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)), func() { renewed <- struct{}{} })
	}()
	select {
	case <-renewed:
	case <-time.After(20 * time.Second):
		t.Fatal("the certificate was not renewed")
	}
	cancel()
	<-done
	next := holder.Get()
	if next.Leaf.SerialNumber.Cmp(first.Leaf.SerialNumber) == 0 {
		t.Fatal("the holder still has the first certificate")
	}
	// It renewed once a third of the lifetime remained, not much earlier.
	if at := next.Leaf.NotBefore; at.Sub(first.Leaf.NotBefore) < first.Leaf.NotAfter.Sub(first.Leaf.NotBefore)/2 {
		t.Fatalf("renewed after only %v of a %v lifetime", at.Sub(first.Leaf.NotBefore), first.Leaf.NotAfter.Sub(first.Leaf.NotBefore))
	}
}
