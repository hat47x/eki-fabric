package workerplane_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/memory"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	server "github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/core/workerregistry"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/testprofile"
)

type mtlsClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mtlsClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *mtlsClock) set(t time.Time)     { c.mu.Lock(); defer c.mu.Unlock(); c.now = t }
func (c *mtlsClock) add(d time.Duration) { c.set(c.Now().Add(d)) }

type mtlsFixture struct {
	srv     *httptest.Server
	svc     *workerenrollment.Service
	admin   workerenrollment.Admin
	claims  *fakeClaims
	assign  *fakeAssignments
	clock   *mtlsClock
	caCert  *x509.Certificate
	caKey   *ecdsa.PrivateKey
	logs    *bytes.Buffer
	mu      sync.Mutex
	conns   map[net.Conn]http.ConnState
	closedN int
}

func newMTLSFixture(t *testing.T) *mtlsFixture {
	t.Helper()
	clock := &mtlsClock{now: time.Now().UTC().Truncate(time.Second)}
	certPEM, keyPEM, err := workerca.GenerateCA("example.org", 0, clock.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := workerca.New(certPEM, keyPEM, "example.org", 0)
	if err != nil {
		t.Fatal(err)
	}
	store := memory.New()
	svc, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: clock, Issuer: ca})
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := workerenrollment.NewAdmin("operator")
	f := &mtlsFixture{svc: svc, admin: admin, claims: &fakeClaims{}, assign: &fakeAssignments{byWorker: map[string][]workerplane.Assignment{}}, clock: clock, caCert: ca.Certificate(), logs: &bytes.Buffer{}, conns: map[net.Conn]http.ConnState{}}
	kb, _ := pem.Decode(keyPEM)
	k, _ := x509.ParsePKCS8PrivateKey(kb.Bytes)
	f.caKey = k.(*ecdsa.PrivateKey)

	h, err := server.NewMTLSHandler(server.MTLSConfig{
		Claims: f.claims, Assignments: f.assign, Registry: svc, WorkerCA: ca.Certificate(), TrustDomain: "example.org",
		Clock: clock.Now, Logger: slog.New(slog.NewTextHandler(&syncWriter{w: f.logs}, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.srv = httptest.NewUnstartedServer(h)
	f.srv.TLS = &tls.Config{ClientAuth: tls.RequestClientCert, MinVersion: tls.VersionTLS13}
	f.srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.conns[c] = s
		if s == http.StateClosed {
			f.closedN++
		}
	}
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (f *mtlsFixture) closedConnections() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closedN
}

func (f *mtlsFixture) secret(t *testing.T) string {
	t.Helper()
	c, err := f.svc.IssueEnrollmentCredential(context.Background(), f.admin, workerenrollment.IssueRequest{TrustDomain: "example.org"})
	if err != nil {
		t.Fatal(err)
	}
	return c.Secret.Reveal()
}

// client returns an HTTP client; cert may be nil.
func (f *mtlsFixture) client(cert *tls.Certificate) *http.Client {
	tr := f.srv.Client().Transport.(*http.Transport).Clone()
	if cert != nil {
		tr.TLSClientConfig.Certificates = []tls.Certificate{*cert}
	}
	return &http.Client{Transport: tr}
}

func csrDER(t *testing.T, key *ecdsa.PrivateKey, tmpl *x509.CertificateRequest) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func (f *mtlsFixture) post(t *testing.T, c *http.Client, path, contentType, auth string, body []byte) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, string(raw)
}

type enrolledWorker struct {
	id   ids.WorkerIdentity
	key  *ecdsa.PrivateKey
	cert tls.Certificate
	leaf *x509.Certificate
}

func (f *mtlsFixture) enroll(t *testing.T, tmpl *x509.CertificateRequest) enrolledWorker {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if tmpl == nil {
		tmpl = &x509.CertificateRequest{}
	}
	resp, body := f.post(t, f.client(nil), enrollwire.EnrollPath, enrollwire.CSRContentType, "Bearer "+f.secret(t), csrDER(t, key, tmpl))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enroll = %d %s", resp.StatusCode, body)
	}
	return f.parseEnrolled(t, key, body)
}

func (f *mtlsFixture) parseEnrolled(t *testing.T, key *ecdsa.PrivateKey, body string) enrolledWorker {
	t.Helper()
	out, err := enrollwire.DecodeResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(out.CertificatePEM))
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return enrolledWorker{
		id: ids.WorkerIdentity(out.WorkerIdentity), key: key, leaf: leaf,
		cert: tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key},
	}
}

func (f *mtlsFixture) claim(t *testing.T, w enrolledWorker, c *http.Client) (*http.Response, string) {
	t.Helper()
	if c == nil {
		c = f.client(&w.cert)
	}
	return f.post(t, c, testprofile.Path, testprofile.ContentType, "", httpClaimBody(t, string(w.id)))
}

func TestMTLSEnrollThenClaimUsesTheRegistryIdentity(t *testing.T) {
	f := newMTLSFixture(t)
	w := f.enroll(t, nil)
	resp, body := f.claim(t, w, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d %s", resp.StatusCode, body)
	}
	if f.claims.calls != 1 || f.claims.principal.WorkerIdentity != string(w.id) || f.claims.principal.TrustDomain != "example.org" {
		t.Fatalf("claim service saw %+v after %d calls", f.claims.principal, f.claims.calls)
	}
}

func TestMTLSForgedCSRSubjectStillGetsTheRegistryIdentity(t *testing.T) {
	f := newMTLSFixture(t)
	victim := f.enroll(t, nil)
	forged := f.enroll(t, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: string(victim.id)},
		URIs:     []*url.URL{{Scheme: "eki", Host: "example.org", Path: "/worker/" + string(victim.id)}},
		DNSNames: []string{"eki-control"},
	})
	if forged.id == victim.id {
		t.Fatal("a forged CSR obtained another Worker's identity")
	}
	if len(forged.leaf.URIs) != 1 || !strings.HasSuffix(forged.leaf.URIs[0].Path, string(forged.id)) || len(forged.leaf.DNSNames) != 0 {
		t.Fatalf("certificate carries requester names: %v %v", forged.leaf.URIs, forged.leaf.DNSNames)
	}
	if resp, body := f.claim(t, forged, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d %s", resp.StatusCode, body)
	}
	if f.claims.principal.WorkerIdentity != string(forged.id) {
		t.Fatalf("principal = %q, want the registry identity %q", f.claims.principal.WorkerIdentity, forged.id)
	}
}

func TestMTLSIgnoresTheIdentityInTheBody(t *testing.T) {
	f := newMTLSFixture(t)
	a, b := f.enroll(t, nil), f.enroll(t, nil)
	// A claim that names b, sent with a's certificate, reaches the claim
	// service as a. claimplane (unchanged) refuses the mismatch.
	resp, _ := f.post(t, f.client(&a.cert), testprofile.Path, testprofile.ContentType, "", httpClaimBody(t, string(b.id)))
	if resp.StatusCode != http.StatusOK || f.claims.principal.WorkerIdentity != string(a.id) {
		t.Fatalf("status %d, principal %q; want the certificate's identity %q", resp.StatusCode, f.claims.principal.WorkerIdentity, a.id)
	}
}

func TestMTLSEnrollmentRefusalsAreUniformAndLeakNothing(t *testing.T) {
	f := newMTLSFixture(t)
	csr := csrDER(t, mustKey(t), &x509.CertificateRequest{})
	used := f.secret(t)
	if resp, body := f.post(t, f.client(nil), enrollwire.EnrollPath, enrollwire.CSRContentType, "Bearer "+used, csr); resp.StatusCode != http.StatusOK {
		t.Fatalf("first use = %d %s", resp.StatusCode, body)
	}
	expired := f.secret(t)
	revoked, _ := f.svc.IssueEnrollmentCredential(context.Background(), f.admin, workerenrollment.IssueRequest{TrustDomain: "example.org"})
	if err := f.svc.RevokeEnrollmentCredential(context.Background(), f.admin, revoked.ID); err != nil {
		t.Fatal(err)
	}
	otherDomain, _ := f.svc.IssueEnrollmentCredential(context.Background(), f.admin, workerenrollment.IssueRequest{TrustDomain: "other.example"})
	unknown, _ := workerregistry.NewSecret()

	type answer struct {
		status int
		body   string
		hdr    string
	}
	ask := func(auth string) answer {
		resp, body := f.post(t, f.client(nil), enrollwire.EnrollPath, enrollwire.CSRContentType, auth, csr)
		return answer{resp.StatusCode, body, resp.Header.Get("WWW-Authenticate") + resp.Header.Get("Content-Type")}
	}
	var want *answer
	for name, auth := range map[string]string{
		"reused":       "Bearer " + used,
		"revoked":      "Bearer " + revoked.Secret.Reveal(),
		"other domain": "Bearer " + otherDomain.Secret.Reveal(),
		"unknown":      "Bearer " + unknown.Reveal(),
		"malformed":    "Bearer not-a-secret",
		"no header":    "",
		"wrong scheme": "Basic " + expired,
		"truncated":    "Bearer " + expired[:len(expired)-3],
	} {
		got := ask(auth)
		if got.status != http.StatusUnauthorized {
			t.Fatalf("%s: status %d %s", name, got.status, got.body)
		}
		if want == nil {
			want = &got
		} else if got != *want {
			t.Fatalf("%s: answer %+v differs from %+v", name, got, *want)
		}
	}
	// The valid-but-unspent credential still works: refusals spent nothing.
	if resp, body := f.post(t, f.client(nil), enrollwire.EnrollPath, enrollwire.CSRContentType, "Bearer "+expired, csr); resp.StatusCode != http.StatusOK {
		t.Fatalf("unspent credential = %d %s", resp.StatusCode, body)
	}
	// An expired credential gets the same answer.
	late := f.secret(t)
	f.clock.add(workerregistry.DefaultCredentialTTL + time.Second)
	got := ask("Bearer " + late)
	if got != *want {
		t.Fatalf("expired: answer %+v differs from %+v", got, *want)
	}
	if strings.Contains(f.logs.String(), used) || strings.Contains(f.logs.String(), unknown.Reveal()) {
		t.Fatalf("log contains an enrollment secret:\n%s", f.logs.String())
	}
	if !strings.Contains(f.logs.String(), "reason=consumed") {
		t.Fatalf("the audit log lacks the refusal reason:\n%s", f.logs.String())
	}
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestMTLSEnrollmentRejectsSecretsInTheQueryAndBadRequests(t *testing.T) {
	f := newMTLSFixture(t)
	csr := csrDER(t, mustKey(t), &x509.CertificateRequest{})
	secret := f.secret(t)
	resp, _ := f.post(t, f.client(nil), enrollwire.EnrollPath+"?token="+secret, enrollwire.CSRContentType, "Bearer "+secret, csr)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("query string status = %d, want 400", resp.StatusCode)
	}
	if strings.Contains(f.logs.String(), secret) {
		t.Fatal("the secret from the query string reached the log")
	}
	for name, tc := range map[string]struct {
		ct   string
		body []byte
		want int
	}{
		"wrong content type": {"text/plain", csr, http.StatusUnsupportedMediaType},
		"garbage CSR":        {enrollwire.CSRContentType, []byte("nope"), http.StatusBadRequest},
		"oversize":           {enrollwire.CSRContentType, make([]byte, enrollwire.MaxCSRBytes+1), http.StatusRequestEntityTooLarge},
	} {
		resp, _ := f.post(t, f.client(nil), enrollwire.EnrollPath, tc.ct, "Bearer "+secret, tc.body)
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", name, resp.StatusCode, tc.want)
		}
	}
	// None of the rejected requests spent the secret.
	if resp, body := f.post(t, f.client(nil), enrollwire.EnrollPath, enrollwire.CSRContentType, "Bearer "+secret, csr); resp.StatusCode != http.StatusOK {
		t.Fatalf("secret was spent by a rejected request: %d %s", resp.StatusCode, body)
	}
	// GET is not allowed.
	r, err := f.client(nil).Get(f.srv.URL + enrollwire.EnrollPath)
	if err != nil || r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %v %v", r, err)
	}
	r.Body.Close()
}

func TestMTLSRevokedWorkerIsRefusedOnTheExistingConnection(t *testing.T) {
	f := newMTLSFixture(t)
	w := f.enroll(t, nil)
	c := f.client(&w.cert)
	if resp, body := f.claim(t, w, c); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d %s", resp.StatusCode, body)
	}
	before := f.closedConnections()
	if _, err := f.svc.SetWorkerState(context.Background(), f.admin, w.id, workerregistry.StateRevoked); err != nil {
		t.Fatal(err)
	}
	calls := f.claims.calls
	resp, _ := f.claim(t, w, c)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked Worker's next request = %d, want 401", resp.StatusCode)
	}
	if f.claims.calls != calls {
		t.Fatal("a claim of a revoked Worker reached the claim service")
	}
	deadline := time.Now().Add(5 * time.Second)
	for f.closedConnections() == before {
		if time.Now().After(deadline) {
			t.Fatal("the session was not closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMTLSDisabledWorkerIsRefusedUntilEnabledAgain(t *testing.T) {
	f := newMTLSFixture(t)
	w := f.enroll(t, nil)
	if _, err := f.svc.SetWorkerState(context.Background(), f.admin, w.id, workerregistry.StateDisabled); err != nil {
		t.Fatal(err)
	}
	if resp, _ := f.claim(t, w, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("disabled Worker = %d, want 401", resp.StatusCode)
	}
	if _, err := f.svc.SetWorkerState(context.Background(), f.admin, w.id, workerregistry.StateRegistered); err != nil {
		t.Fatal(err)
	}
	if resp, body := f.claim(t, w, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("re-enabled Worker = %d %s", resp.StatusCode, body)
	}
}

func TestMTLSRefusesCertificatesThatAreNotThisCAsWorkerCertificates(t *testing.T) {
	f := newMTLSFixture(t)
	w := f.enroll(t, nil)

	// Expired.
	f.clock.add(workerenrollmentDefaultValidity() + time.Minute)
	if resp, _ := f.claim(t, w, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired certificate = %d, want 401", resp.StatusCode)
	}
	f.clock.add(-workerenrollmentDefaultValidity() - time.Minute)
	if resp, _ := f.claim(t, w, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("control: the certificate is valid again = %d", resp.StatusCode)
	}

	sign := func(parent *x509.Certificate, parentKey *ecdsa.PrivateKey, uri string, eku []x509.ExtKeyUsage) tls.Certificate {
		key := mustKey(t)
		u, _ := url.Parse(uri)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "x"},
			NotBefore: f.clock.Now(), NotAfter: f.clock.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: eku, URIs: []*url.URL{u},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	id := string(w.id)
	client := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}

	// A CA the Control Plane does not trust.
	otherPEM, otherKeyPEM, _ := workerca.GenerateCA("example.org", 0, f.clock.Now().Add(-time.Hour))
	ob, _ := pem.Decode(otherPEM)
	otherCA, _ := x509.ParseCertificate(ob.Bytes)
	okb, _ := pem.Decode(otherKeyPEM)
	ok, _ := x509.ParsePKCS8PrivateKey(okb.Bytes)

	// Another trust domain, signed by a CA that is constrained to that domain.
	foreignPEM, foreignKeyPEM, _ := workerca.GenerateCA("other.example", 0, f.clock.Now().Add(-time.Hour))
	fb, _ := pem.Decode(foreignPEM)
	foreignCA, _ := x509.ParseCertificate(fb.Bytes)
	fkb, _ := pem.Decode(foreignKeyPEM)
	fk, _ := x509.ParsePKCS8PrivateKey(fkb.Bytes)

	cases := map[string]tls.Certificate{
		"unknown CA":                   sign(otherCA, ok.(*ecdsa.PrivateKey), "eki://example.org/worker/"+id, client),
		"other trust domain, other CA": sign(foreignCA, fk.(*ecdsa.PrivateKey), "eki://other.example/worker/"+id, client),
		"sub-domain, right CA":         sign(f.caCert, f.caKey, "eki://sub.example.org/worker/"+id, client),
		"not a Worker name, right CA":  sign(f.caCert, f.caKey, "eki://example.org/admin/"+id, client),
		"unknown Worker, right CA":     sign(f.caCert, f.caKey, "eki://example.org/worker/wrk_00000000000000000000000000000000", client),
		"server-auth only, right CA":   sign(f.caCert, f.caKey, "eki://example.org/worker/"+id, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}),
	}
	for name, cert := range cases {
		cert := cert
		resp, _ := f.post(t, f.client(&cert), testprofile.Path, testprofile.ContentType, "", httpClaimBody(t, id))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}
	if f.claims.calls != 1 {
		t.Fatalf("claim service called %d times, want only the control request", f.claims.calls)
	}

	// No certificate, and a Client-API style bearer token, never reach the claim route.
	for name, auth := range map[string]string{"no credential": "", "client bearer token": "Bearer client-token-0123456789-abcdefgh"} {
		resp, _ := f.post(t, f.client(nil), testprofile.Path, testprofile.ContentType, auth, httpClaimBody(t, id))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
	}
}

func workerenrollmentDefaultValidity() time.Duration { return 72 * time.Hour }

func TestMTLSRenewal(t *testing.T) {
	f := newMTLSFixture(t)
	w := f.enroll(t, nil)
	newKey := mustKey(t)
	csr := csrDER(t, newKey, &x509.CertificateRequest{})

	// The old key cannot be reused.
	reuse := csrDER(t, w.key, &x509.CertificateRequest{})
	if resp, _ := f.post(t, f.client(&w.cert), enrollwire.RenewPath, enrollwire.CSRContentType, "", reuse); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("key reuse = %d, want 400", resp.StatusCode)
	}
	// Without a client certificate, renewal is refused.
	if resp, _ := f.post(t, f.client(nil), enrollwire.RenewPath, enrollwire.CSRContentType, "", csr); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("renew without certificate = %d, want 401", resp.StatusCode)
	}
	f.clock.add(48 * time.Hour)
	resp, body := f.post(t, f.client(&w.cert), enrollwire.RenewPath, enrollwire.CSRContentType, "", csr)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("renew = %d %s", resp.StatusCode, body)
	}
	renewed := f.parseEnrolled(t, newKey, body)
	if renewed.id != w.id || renewed.leaf.SerialNumber.Cmp(w.leaf.SerialNumber) == 0 {
		t.Fatalf("renewed identity %s serial %s, was %s %s", renewed.id, renewed.leaf.SerialNumber, w.id, w.leaf.SerialNumber)
	}
	if got, _ := f.svc.LookupWorker(context.Background(), w.id); got.Certificate.Serial != renewed.leaf.SerialNumber.Text(16) {
		t.Fatalf("the registry records %s, not the renewed serial", got.Certificate.Serial)
	}
	if resp, body := f.claim(t, renewed, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim with the renewed certificate = %d %s", resp.StatusCode, body)
	}

	// An expired certificate cannot renew.
	f.clock.add(workerenrollmentDefaultValidity() + time.Hour)
	if resp, _ := f.post(t, f.client(&renewed.cert), enrollwire.RenewPath, enrollwire.CSRContentType, "", csrDER(t, mustKey(t), &x509.CertificateRequest{})); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expired certificate renewed: %d", resp.StatusCode)
	}
}

func TestMTLSDisabledAndRevokedWorkersCannotRenew(t *testing.T) {
	f := newMTLSFixture(t)
	for _, state := range []workerregistry.State{workerregistry.StateDisabled, workerregistry.StateRevoked} {
		w := f.enroll(t, nil)
		if _, err := f.svc.SetWorkerState(context.Background(), f.admin, w.id, state); err != nil {
			t.Fatal(err)
		}
		before, _ := f.svc.LookupWorker(context.Background(), w.id)
		resp, _ := f.post(t, f.client(&w.cert), enrollwire.RenewPath, enrollwire.CSRContentType, "", csrDER(t, mustKey(t), &x509.CertificateRequest{}))
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s Worker renewal = %d, want 401", state, resp.StatusCode)
		}
		after, _ := f.svc.LookupWorker(context.Background(), w.id)
		if after.Certificate != before.Certificate {
			t.Fatalf("%s Worker's recorded certificate changed", state)
		}
	}
}

func TestMTLSHandlerConfigIsChecked(t *testing.T) {
	f := newMTLSFixture(t)
	for name, cfg := range map[string]server.MTLSConfig{
		"no claims":   {Registry: f.svc, WorkerCA: f.caCert, TrustDomain: "example.org"},
		"no registry": {Claims: f.claims, WorkerCA: f.caCert, TrustDomain: "example.org"},
		"no CA":       {Claims: f.claims, Registry: f.svc, TrustDomain: "example.org"},
		"bad domain":  {Claims: f.claims, Registry: f.svc, WorkerCA: f.caCert, TrustDomain: "Example.ORG"},
	} {
		if _, err := server.NewMTLSHandler(cfg); err == nil {
			t.Errorf("%s: handler built", name)
		}
	}
}
