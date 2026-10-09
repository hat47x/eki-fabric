// Package federationregistrytest is the contract that every durable federation
// registry must pass. An adapter calls RunDurabilityContract from its tests, and
// RunPersistFailureContract when it can inject write failures.
//
// The contract uses the exported API of the core package only. Times are fixed
// values, so the contract never reads a clock.
package federationregistrytest

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/federation"
)

// MaxBundleAge is the maximum bundle age that a registry under test must be
// opened with. Every bundle in the contract is validated within this age of the
// probe times.
const MaxBundleAge = 24 * time.Hour

// Registry is the behavior that the contract exercises. The core Registry has the
// same method set, and so does a durable adapter.
type Registry interface {
	Federate(link federation.Link, bundle federation.Bundle, authoritySignature []byte) error
	InstallBundle(bundle federation.Bundle, authoritySignature []byte) error
	Revoke(domain federation.TrustDomain, effectiveAt time.Time) error
	Binding(domain federation.TrustDomain) (federation.Binding, error)
	Verify(id federation.Identity, purpose federation.Purpose, now time.Time) (federation.Verified, error)
}

// DurableFactory opens the registry stored at location. After closeRegistry is
// called, opening the same location again must show every change that was
// committed before the close.
type DurableFactory struct {
	Open func(t testing.TB, location string) (reg Registry, closeRegistry func() error)
}

// Faulty is a durable registry whose writes can be made to fail on demand.
type Faulty interface {
	Registry
	Close() error
	// FailWrites makes every later write fail until RestoreWrites is called.
	// Reads keep working.
	FailWrites(t testing.TB)
	// RestoreWrites ends the failure made by FailWrites.
	RestoreWrites(t testing.TB)
}

// FaultyFactory opens the registry stored at location, with failure injection.
type FaultyFactory struct {
	Open func(t testing.TB, location string) Faulty
}

// RunDurabilityContract checks that committed changes survive a close and a reopen
// of the same location, and that the rules still hold after the reopen.
func RunDurabilityContract(t *testing.T, factory DurableFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("DurableFactory.Open is nil")
	}

	t.Run("bindings-and-bundles-survive-reopen", func(t *testing.T) {
		f := newFixture(t)
		location := filepath.Join(t.TempDir(), "federation.db")
		reg, closeReg := factory.Open(t, location)
		f.seed(t, reg)
		before := f.answers(t, reg)
		mustNil(t, "close", closeReg())

		reopened, closeReopened := factory.Open(t, location)
		defer func() { mustNil(t, "close reopened", closeReopened()) }()
		assertSame(t, "after reopen", f.answers(t, reopened), before)
	})

	t.Run("revoked-domain-stays-revoked-and-refuses-federate", func(t *testing.T) {
		f := newFixture(t)
		location := filepath.Join(t.TempDir(), "federation.db")
		reg, closeReg := factory.Open(t, location)
		f.seed(t, reg)
		mustNil(t, "close", closeReg())

		reopened, closeReopened := factory.Open(t, location)
		defer func() { mustNil(t, "close reopened", closeReopened()) }()

		b, err := reopened.Binding(f.beta.domain)
		mustNil(t, "Binding(beta)", err)
		if !b.Revoked() || !b.RevokedAt().Equal(f.at(-5*time.Minute)) {
			t.Fatalf("beta after reopen: revoked %t at %s, want revoked at %s", b.Revoked(), b.RevokedAt(), f.at(-5*time.Minute))
		}
		next, sig := f.signedBundle(t, f.beta, 2, f.at(-time.Minute), keyMask1)
		expectError(t, reopened.Federate(f.link(f.beta, federation.PurposeExecutorParticipation), next, sig), federation.ErrAlreadyFederated)
		expectError(t, reopened.InstallBundle(next, sig), federation.ErrDomainRevoked)
		expectError(t, reopened.Revoke(f.beta.domain, f.at(0)), federation.ErrAlreadyRevoked)
		_, err = reopened.Verify(f.identity(t, f.beta, 0, "worker-after-reopen"), federation.PurposeExecutorParticipation, f.at(0))
		expectReason(t, err, federation.ReasonDomainRevoked)
	})

	t.Run("rollback-refused-after-reopen", func(t *testing.T) {
		f := newFixture(t)
		location := filepath.Join(t.TempDir(), "federation.db")
		reg, closeReg := factory.Open(t, location)
		f.seed(t, reg)
		mustNil(t, "close", closeReg())

		reopened, closeReopened := factory.Open(t, location)
		defer func() { mustNil(t, "close reopened", closeReopened()) }()

		older, sig := f.signedBundle(t, f.alpha, 1, f.at(-time.Hour), keyMask12)
		expectError(t, reopened.InstallBundle(older, sig), federation.ErrRollback)
		assertSequence(t, reopened, f.alpha.domain, 2)
	})

	t.Run("equivocation-refused-after-reopen", func(t *testing.T) {
		f := newFixture(t)
		location := filepath.Join(t.TempDir(), "federation.db")
		reg, closeReg := factory.Open(t, location)
		f.seed(t, reg)
		mustNil(t, "close", closeReg())

		reopened, closeReopened := factory.Open(t, location)
		defer func() { mustNil(t, "close reopened", closeReopened()) }()

		// Sequence 2 is bound with k1 alone. The same sequence with k2 alone is a different bundle.
		other, sig := f.signedBundle(t, f.alpha, 2, f.at(-30*time.Minute), keyMask2)
		expectError(t, reopened.InstallBundle(other, sig), federation.ErrEquivocation)
		assertSequence(t, reopened, f.alpha.domain, 2)
	})

	t.Run("reaccepted-bundle-and-new-sequence-persist-across-reopens", func(t *testing.T) {
		f := newFixture(t)
		location := filepath.Join(t.TempDir(), "federation.db")
		reg, closeReg := factory.Open(t, location)
		f.seed(t, reg)
		mustNil(t, "close", closeReg())

		// Re-accepting the same bundle with a later validated-at time moves the time forward.
		reopened, closeReopened := factory.Open(t, location)
		same, sig := f.signedBundle(t, f.alpha, 2, f.at(-10*time.Minute), keyMask1)
		mustNil(t, "re-accept sequence 2 later", reopened.InstallBundle(same, sig))
		// An earlier validated-at time changes nothing.
		earlier, sigEarlier := f.signedBundle(t, f.alpha, 2, f.at(-20*time.Minute), keyMask1)
		mustNil(t, "re-accept sequence 2 earlier", reopened.InstallBundle(earlier, sigEarlier))
		mustNil(t, "close reopened", closeReopened())

		again, closeAgain := factory.Open(t, location)
		b, err := again.Binding(f.alpha.domain)
		mustNil(t, "Binding(alpha)", err)
		if !b.ValidatedAt().Equal(f.at(-10 * time.Minute)) {
			t.Fatalf("alpha validated at %s after reopen, want %s", b.ValidatedAt(), f.at(-10*time.Minute))
		}
		next, sigNext := f.signedBundle(t, f.alpha, 3, f.at(-5*time.Minute), keyMask1)
		mustNil(t, "install sequence 3", again.InstallBundle(next, sigNext))
		mustNil(t, "close again", closeAgain())

		final, closeFinal := factory.Open(t, location)
		defer func() { mustNil(t, "close final", closeFinal()) }()
		assertSequence(t, final, f.alpha.domain, 3)
	})

	t.Run("pinned-authority-key-stays-unique-after-reopen", func(t *testing.T) {
		f := newFixture(t)
		location := filepath.Join(t.TempDir(), "federation.db")
		reg, closeReg := factory.Open(t, location)
		f.seed(t, reg)
		mustNil(t, "close", closeReg())

		reopened, closeReopened := factory.Open(t, location)
		defer func() { mustNil(t, "close reopened", closeReopened()) }()

		// Gamma pins the authority key that alpha already pins.
		gamma, sig := f.signedBundleWith(t, f.alpha, f.gamma.domain, 1, f.at(-time.Hour), keyMask1)
		link := mustLink(t, f.gamma.domain, f.alpha.authPub, federation.PurposeTaskSubmission)
		expectError(t, reopened.Federate(link, gamma, sig), federation.ErrAuthorityKeyReused)
		_, err := reopened.Binding(f.gamma.domain)
		expectError(t, err, federation.ErrNotFederated)
	})
}

// RunPersistFailureContract checks that a write which cannot be saved changes
// nothing, neither in memory nor after a reopen.
func RunPersistFailureContract(t *testing.T, factory FaultyFactory) {
	t.Helper()
	if factory.Open == nil {
		t.Fatal("FaultyFactory.Open is nil")
	}

	t.Run("failed-writes-leave-memory-unchanged", func(t *testing.T) {
		f := newFixture(t)
		location := filepath.Join(t.TempDir(), "federation.db")
		reg := factory.Open(t, location)
		defer func() { _ = reg.Close() }()
		f.seed(t, reg)
		before := f.answers(t, reg)

		reg.FailWrites(t)
		for i, change := range f.pendingChanges(t) {
			if err := change(reg); err == nil {
				t.Fatalf("pending change %d succeeded while writes fail", i)
			}
		}
		assertSame(t, "in memory while writes fail", f.answers(t, reg), before)

		// Once writes work again, the same changes are not refused by stale memory.
		reg.RestoreWrites(t)
		for i, change := range f.pendingChanges(t) {
			mustNil(t, fmt.Sprintf("pending change %d after restore", i), change(reg))
		}
	})

	t.Run("failed-writes-leave-storage-unchanged", func(t *testing.T) {
		f := newFixture(t)
		location := filepath.Join(t.TempDir(), "federation.db")
		reg := factory.Open(t, location)
		f.seed(t, reg)
		before := f.answers(t, reg)

		reg.FailWrites(t)
		for i, change := range f.pendingChanges(t) {
			if err := change(reg); err == nil {
				t.Fatalf("pending change %d succeeded while writes fail", i)
			}
		}
		reg.RestoreWrites(t)
		mustNil(t, "close", reg.Close())

		reopened := factory.Open(t, location)
		defer func() { mustNil(t, "close reopened", reopened.Close()) }()
		assertSame(t, "after reopen", f.answers(t, reopened), before)
	})
}

// Key masks select the bundle keys: bit 0 is k1 and bit 1 is k2.
const (
	keyMask1  uint8 = 0b01
	keyMask2  uint8 = 0b10
	keyMask12 uint8 = 0b11
)

// fixture is the cast of the contract. Alpha is federated with two keys and moves
// to k1 alone. Beta is federated with k1, then revoked. Gamma is never federated
// before the failure tests.
type fixture struct {
	alpha, beta, gamma party
	keyPub             [2]ed25519.PublicKey
	keyPriv            [2]ed25519.PrivateKey
	outsiderPub        ed25519.PublicKey
	outsiderPriv       ed25519.PrivateKey
}

// party is a trust domain together with its bundle authority key pair.
type party struct {
	domain   federation.TrustDomain
	authPub  ed25519.PublicKey
	authPriv ed25519.PrivateKey
}

// testNow is the reference time of the contract.
var testNow = time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)

func newFixture(t testing.TB) fixture {
	t.Helper()
	f := fixture{
		alpha: newParty(t, "alpha.example", 0xA1),
		beta:  newParty(t, "beta.example", 0xB1),
		gamma: newParty(t, "gamma.example", 0xC1),
	}
	f.keyPub[0], f.keyPriv[0] = seededKey(0x11)
	f.keyPub[1], f.keyPriv[1] = seededKey(0x12)
	f.outsiderPub, f.outsiderPriv = seededKey(0x13)
	return f
}

func newParty(t testing.TB, name string, seed byte) party {
	t.Helper()
	domain, err := federation.ParseTrustDomain(name)
	mustNil(t, "ParseTrustDomain "+name, err)
	pub, priv := seededKey(seed)
	return party{domain: domain, authPub: pub, authPriv: priv}
}

func seededKey(seed byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	return priv.Public().(ed25519.PublicKey), priv
}

func (f fixture) at(d time.Duration) time.Time {
	return testNow.Add(d)
}

// keys returns the bundle keys selected by mask.
func (f fixture) keys(mask uint8) []federation.BundleKey {
	var out []federation.BundleKey
	names := [2]string{"k1", "k2"}
	for i := range names {
		if mask&(1<<i) != 0 {
			out = append(out, federation.BundleKey{ID: names[i], PublicKey: f.keyPub[i]})
		}
	}
	return out
}

// signedBundle returns a bundle of p and its authority signature.
func (f fixture) signedBundle(t testing.TB, p party, sequence uint64, validatedAt time.Time, mask uint8) (federation.Bundle, []byte) {
	t.Helper()
	return f.signedBundleWith(t, p, p.domain, sequence, validatedAt, mask)
}

// signedBundleWith returns a bundle for domain, signed with the authority key of
// signer. It differs from signedBundle only when a test needs a bundle that a key
// of another party signed.
func (f fixture) signedBundleWith(t testing.TB, signer party, domain federation.TrustDomain, sequence uint64, validatedAt time.Time, mask uint8) (federation.Bundle, []byte) {
	t.Helper()
	bundle, err := federation.NewBundle(domain, sequence, validatedAt, f.keys(mask))
	mustNil(t, "NewBundle", err)
	input, err := federation.BundleSigningInput(bundle)
	mustNil(t, "BundleSigningInput", err)
	return bundle, ed25519.Sign(signer.authPriv, input)
}

func (f fixture) link(p party, purposes ...federation.Purpose) federation.Link {
	if len(purposes) == 0 {
		purposes = []federation.Purpose{federation.PurposeTaskSubmission, federation.PurposeExecutorParticipation}
	}
	link, err := federation.NewLink(p.domain, p.authPub, purposes...)
	if err != nil {
		panic(fmt.Sprintf("NewLink(%s): %v", p.domain, err))
	}
	return link
}

func mustLink(t testing.TB, domain federation.TrustDomain, authority ed25519.PublicKey, purposes ...federation.Purpose) federation.Link {
	t.Helper()
	link, err := federation.NewLink(domain, authority, purposes...)
	mustNil(t, "NewLink", err)
	return link
}

// identity returns an identity of p that names key k (0 for k1, 1 for k2).
func (f fixture) identity(t testing.TB, p party, k int, subject string) federation.Identity {
	t.Helper()
	input, err := federation.IdentitySigningInput(p.domain, subject)
	mustNil(t, "IdentitySigningInput", err)
	return federation.Identity{
		Domain:    p.domain,
		Subject:   subject,
		KeyID:     []string{"k1", "k2"}[k],
		Signature: ed25519.Sign(f.keyPriv[k], input),
	}
}

// seed brings reg to the reference state. Alpha is federated with k1 and k2 at
// sequence 1, then moves to k1 alone at sequence 2. Beta is federated with k1 at
// sequence 1, and then revoked five minutes before testNow.
func (f fixture) seed(t testing.TB, reg Registry) {
	t.Helper()
	alpha1, sig := f.signedBundle(t, f.alpha, 1, f.at(-time.Hour), keyMask12)
	mustNil(t, "Federate(alpha)", reg.Federate(f.link(f.alpha, federation.PurposeTaskSubmission, federation.PurposeExecutorParticipation), alpha1, sig))

	beta1, sigBeta := f.signedBundle(t, f.beta, 1, f.at(-time.Hour), keyMask1)
	mustNil(t, "Federate(beta)", reg.Federate(f.link(f.beta, federation.PurposeExecutorParticipation), beta1, sigBeta))

	alpha2, sig2 := f.signedBundle(t, f.alpha, 2, f.at(-30*time.Minute), keyMask1)
	mustNil(t, "InstallBundle(alpha, 2)", reg.InstallBundle(alpha2, sig2))

	mustNil(t, "Revoke(beta)", reg.Revoke(f.beta.domain, f.at(-5*time.Minute)))
}

// pendingChanges returns changes that the seeded state refuses while writes fail.
// Each change succeeds on a registry that is not stale.
func (f fixture) pendingChanges(t testing.TB) []func(Registry) error {
	t.Helper()
	gammaBundle, gammaSig := f.signedBundle(t, f.gamma, 1, f.at(-time.Hour), keyMask1)
	gammaLink := f.link(f.gamma, federation.PurposeTaskSubmission)
	alpha3, sig3 := f.signedBundle(t, f.alpha, 3, f.at(-10*time.Minute), keyMask1)
	return []func(Registry) error{
		func(r Registry) error { return r.Federate(gammaLink, gammaBundle, gammaSig) },
		func(r Registry) error { return r.InstallBundle(alpha3, sig3) },
		func(r Registry) error { return r.Revoke(f.alpha.domain, f.at(-time.Minute)) },
	}
}

// answers describes what the registry says about the cast: each binding, and the
// verdict for each probe identity. Two registries that answer the same way have
// the same state, as far as the contract can observe.
func (f fixture) answers(t testing.TB, reg Registry) []string {
	t.Helper()
	var out []string
	for _, p := range []party{f.alpha, f.beta, f.gamma} {
		b, err := reg.Binding(p.domain)
		if err != nil {
			out = append(out, fmt.Sprintf("binding %s: %v", p.domain, err))
			continue
		}
		out = append(out, "binding "+describeBinding(b))
	}
	probes := []struct {
		name    string
		id      federation.Identity
		purpose federation.Purpose
		now     time.Time
	}{
		{"alpha-k1", f.identity(t, f.alpha, 0, "alpha-worker"), federation.PurposeTaskSubmission, f.at(0)},
		{"alpha-k2", f.identity(t, f.alpha, 1, "alpha-worker"), federation.PurposeTaskSubmission, f.at(0)},
		{"alpha-purpose", f.identity(t, f.alpha, 0, "alpha-worker"), federation.PurposeDataClassAccess, f.at(0)},
		{"beta-before-revocation", f.identity(t, f.beta, 0, "beta-worker"), federation.PurposeExecutorParticipation, f.at(-10 * time.Minute)},
		{"beta-after-revocation", f.identity(t, f.beta, 0, "beta-worker"), federation.PurposeExecutorParticipation, f.at(0)},
		{"gamma", f.identity(t, f.gamma, 0, "gamma-worker"), federation.PurposeTaskSubmission, f.at(0)},
	}
	for _, p := range probes {
		v, err := reg.Verify(p.id, p.purpose, p.now)
		out = append(out, fmt.Sprintf("verify %s: %s", p.name, describeVerdict(v, err)))
	}
	return out
}

func describeBinding(b federation.Binding) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "domain=%s sequence=%d digest=%s validated=%s purposes=%v revoked=%t revokedAt=%s",
		b.Domain(), b.Sequence(), b.Digest(), b.ValidatedAt().UTC().Format(time.RFC3339Nano),
		b.Purposes(), b.Revoked(), b.RevokedAt().UTC().Format(time.RFC3339Nano))
	for _, k := range b.Bundle().Keys() {
		fmt.Fprintf(&sb, " key=%s:%x", k.ID, k.PublicKey)
	}
	return sb.String()
}

func describeVerdict(v federation.Verified, err error) string {
	if err != nil {
		reason, _ := federation.ReasonOf(err)
		return "rejected " + string(reason)
	}
	return fmt.Sprintf("accepted domain=%s subject=%s key=%s purpose=%s sequence=%d digest=%s",
		v.Domain, v.Subject, v.KeyID, v.Purpose, v.BundleSequence, v.BundleDigest)
}

func assertSame(t testing.TB, label string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: answers differ\n got:\n  %s\nwant:\n  %s", label, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func assertSequence(t testing.TB, reg Registry, domain federation.TrustDomain, want uint64) {
	t.Helper()
	b, err := reg.Binding(domain)
	mustNil(t, "Binding("+domain.String()+")", err)
	if b.Sequence() != want {
		t.Fatalf("%s bound sequence = %d, want %d", domain, b.Sequence(), want)
	}
}

func mustNil(t testing.TB, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func expectError(t testing.TB, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func expectReason(t testing.TB, err error, want federation.Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("want rejection %s, got success", want)
	}
	got, ok := federation.ReasonOf(err)
	if !ok || got != want {
		t.Fatalf("reason = %q (rejection %t), want %s (error: %v)", got, ok, want, err)
	}
}
