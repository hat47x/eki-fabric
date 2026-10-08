package federation

import (
	"bytes"
	"crypto/ed25519"
	"testing"
	"time"
)

// testNow is the verification time used by the tests. The tests never read the clock.
var testNow = time.Date(2026, time.October, 8, 12, 0, 0, 0, time.UTC)

// testMaxAge is the maximum bundle age of the test registries.
const testMaxAge = 24 * time.Hour

// testKeyPair derives a deterministic Ed25519 key pair from a one-byte seed.
func testKeyPair(t testing.TB, seed byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	return priv.Public().(ed25519.PublicKey), priv
}

func mustDomain(t testing.TB, name string) TrustDomain {
	t.Helper()
	domain, err := ParseTrustDomain(name)
	if err != nil {
		t.Fatalf("ParseTrustDomain(%q): %v", name, err)
	}
	return domain
}

// party is a trust domain together with its bundle authority key pair.
type party struct {
	domain   TrustDomain
	authPub  ed25519.PublicKey
	authPriv ed25519.PrivateKey
}

func newParty(t testing.TB, name string, seed byte) party {
	t.Helper()
	pub, priv := testKeyPair(t, seed)
	return party{domain: mustDomain(t, name), authPub: pub, authPriv: priv}
}

func mustBundle(t testing.TB, domain TrustDomain, sequence uint64, validatedAt time.Time, keys ...BundleKey) Bundle {
	t.Helper()
	bundle, err := NewBundle(domain, sequence, validatedAt, keys)
	if err != nil {
		t.Fatalf("NewBundle(%s, %d): %v", domain, sequence, err)
	}
	return bundle
}

func mustLink(t testing.TB, domain TrustDomain, authority ed25519.PublicKey, purposes ...Purpose) Link {
	t.Helper()
	link, err := NewLink(domain, authority, purposes...)
	if err != nil {
		t.Fatalf("NewLink(%s): %v", domain, err)
	}
	return link
}

// authoritySign signs a bundle with the party's pinned authority key.
func authoritySign(t testing.TB, p party, bundle Bundle) []byte {
	t.Helper()
	input, err := BundleSigningInput(bundle)
	if err != nil {
		t.Fatalf("BundleSigningInput: %v", err)
	}
	return ed25519.Sign(p.authPriv, input)
}

// identitySign signs an identity whose subject is signedFor's.
func identitySign(t testing.TB, priv ed25519.PrivateKey, signedFor TrustDomain, subject string) []byte {
	t.Helper()
	input, err := IdentitySigningInput(signedFor, subject)
	if err != nil {
		t.Fatalf("IdentitySigningInput(%s, %q): %v", signedFor, subject, err)
	}
	return ed25519.Sign(priv, input)
}

// makeIdentity builds an identity that claims claimed, is signed by priv for
// signedFor, and names keyID.
func makeIdentity(t testing.TB, claimed TrustDomain, keyID string, priv ed25519.PrivateKey, signedFor TrustDomain, subject string) Identity {
	t.Helper()
	return Identity{
		Domain:    claimed,
		Subject:   subject,
		KeyID:     keyID,
		Signature: identitySign(t, priv, signedFor, subject),
	}
}

func mustRegistry(t testing.TB) *Registry {
	t.Helper()
	reg, err := NewRegistry(testMaxAge)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

// expectReason fails the test unless err is a rejection with the given reason.
func expectReason(t testing.TB, err error, want Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("want rejection %s, got success", want)
	}
	got, ok := ReasonOf(err)
	if !ok {
		t.Fatalf("want rejection %s, got a non-rejection error: %v", want, err)
	}
	if got != want {
		t.Fatalf("reason = %s, want %s (error: %v)", got, want, err)
	}
}

// testWorld is the fixed cast of the tests. alpha is federated with two keys,
// beta with one key that alpha also holds, and gamma is never federated.
type testWorld struct {
	alpha, beta, gamma party
	keyIDs             [2]string
	keyPubs            [2]ed25519.PublicKey
	keyPrivs           [2]ed25519.PrivateKey
	outsiderPub        ed25519.PublicKey
	outsiderPriv       ed25519.PrivateKey
}

func newTestWorld(t testing.TB) testWorld {
	t.Helper()
	w := testWorld{
		alpha:  newParty(t, "alpha.example", 0xA1),
		beta:   newParty(t, "beta.example", 0xB1),
		gamma:  newParty(t, "gamma.example", 0xC1),
		keyIDs: [2]string{"k1", "k2"},
	}
	w.keyPubs[0], w.keyPrivs[0] = testKeyPair(t, 0x11)
	w.keyPubs[1], w.keyPrivs[1] = testKeyPair(t, 0x12)
	w.outsiderPub, w.outsiderPriv = testKeyPair(t, 0x13)
	return w
}

// bundleKeys returns the bundle entries for the key bits in mask. Bit 0 is key
// "k1" and bit 1 is key "k2".
func (w testWorld) bundleKeys(mask uint8) []BundleKey {
	var keys []BundleKey
	for i := 0; i < len(w.keyIDs); i++ {
		if mask&(1<<i) != 0 {
			keys = append(keys, BundleKey{ID: w.keyIDs[i], PublicKey: w.keyPubs[i]})
		}
	}
	return keys
}

func (w testWorld) alphaLink(t testing.TB) Link {
	return mustLink(t, w.alpha.domain, w.alpha.authPub, PurposeTaskSubmission, PurposeExecutorParticipation)
}

func (w testWorld) betaLink(t testing.TB) Link {
	return mustLink(t, w.beta.domain, w.beta.authPub, PurposeExecutorParticipation)
}

// federatedRegistry returns a registry in which alpha is bound to a bundle
// with k1 and k2, and beta is bound to a bundle with k1 only. Both bundles were
// validated one hour before testNow.
func (w testWorld) federatedRegistry(t testing.TB) *Registry {
	t.Helper()
	reg := mustRegistry(t)
	validatedAt := testNow.Add(-time.Hour)

	alpha := mustBundle(t, w.alpha.domain, 1, validatedAt, w.bundleKeys(0b11)...)
	if err := reg.Federate(w.alphaLink(t), alpha, authoritySign(t, w.alpha, alpha)); err != nil {
		t.Fatalf("Federate(alpha): %v", err)
	}
	beta := mustBundle(t, w.beta.domain, 1, validatedAt, w.bundleKeys(0b01)...)
	if err := reg.Federate(w.betaLink(t), beta, authoritySign(t, w.beta, beta)); err != nil {
		t.Fatalf("Federate(beta): %v", err)
	}
	return reg
}
