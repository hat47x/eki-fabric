package federation

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewBundleRejectsNonCanonicalContent(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	pub1, _ := testKeyPair(t, 0x21)
	pub2, _ := testKeyPair(t, 0x22)
	valid := []BundleKey{{ID: "k1", PublicKey: pub1}}

	tooManyKeys := make([]BundleKey, 0, maxBundleKeys+1)
	for i := 0; i <= maxBundleKeys; i++ {
		pub, _ := testKeyPair(t, byte(i+1))
		tooManyKeys = append(tooManyKeys, BundleKey{ID: "k" + strconv.Itoa(i), PublicKey: pub})
	}

	cases := map[string]struct {
		domain   TrustDomain
		sequence uint64
		at       time.Time
		keys     []BundleKey
	}{
		"zero domain":                  {TrustDomain{}, 1, testNow, valid},
		"zero sequence":                {domain, 0, testNow, valid},
		"zero validated-at":            {domain, 1, time.Time{}, valid},
		"no keys":                      {domain, 1, testNow, nil},
		"more than 64 keys":            {domain, 1, testNow, tooManyKeys},
		"empty key ID":                 {domain, 1, testNow, []BundleKey{{ID: "", PublicKey: pub1}}},
		"key ID with a space":          {domain, 1, testNow, []BundleKey{{ID: "k 1", PublicKey: pub1}}},
		"key ID with non-ASCII bytes":  {domain, 1, testNow, []BundleKey{{ID: "k\xc3\xa91", PublicKey: pub1}}},
		"key ID longer than 128 bytes": {domain, 1, testNow, []BundleKey{{ID: strings.Repeat("k", 129), PublicKey: pub1}}},
		"short public key":             {domain, 1, testNow, []BundleKey{{ID: "k1", PublicKey: pub1[:31]}}},
		"long public key":              {domain, 1, testNow, []BundleKey{{ID: "k1", PublicKey: append(slices.Clone(pub1), 0)}}},
		"nil public key":               {domain, 1, testNow, []BundleKey{{ID: "k1", PublicKey: nil}}},
		"duplicate key ID": {domain, 1, testNow, []BundleKey{
			{ID: "k1", PublicKey: pub1},
			{ID: "k1", PublicKey: pub2},
		}},
		"same public key under two IDs": {domain, 1, testNow, []BundleKey{
			{ID: "k1", PublicKey: pub1},
			{ID: "k2", PublicKey: pub1},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			bundle, err := NewBundle(tc.domain, tc.sequence, tc.at, tc.keys)
			if err == nil {
				t.Fatalf("NewBundle succeeded with digest %s; want an error", bundle.Digest())
			}
			if !errors.Is(err, ErrInvalidBundle) {
				t.Fatalf("error %v does not wrap ErrInvalidBundle", err)
			}
		})
	}
}

func TestNewBundleAcceptsSixtyFourKeys(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	keys := make([]BundleKey, 0, maxBundleKeys)
	for i := 0; i < maxBundleKeys; i++ {
		pub, _ := testKeyPair(t, byte(i+1))
		keys = append(keys, BundleKey{ID: "k" + strconv.Itoa(i), PublicKey: pub})
	}
	if _, err := NewBundle(domain, 1, testNow, keys); err != nil {
		t.Fatalf("NewBundle with %d keys: %v", maxBundleKeys, err)
	}
}

func TestBundleDigestIgnoresKeyOrder(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	pub1, _ := testKeyPair(t, 0x11)
	pub2, _ := testKeyPair(t, 0x12)
	a := mustBundle(t, domain, 3, testNow, BundleKey{ID: "k1", PublicKey: pub1}, BundleKey{ID: "k2", PublicKey: pub2})
	b := mustBundle(t, domain, 3, testNow, BundleKey{ID: "k2", PublicKey: pub2}, BundleKey{ID: "k1", PublicKey: pub1})
	if a.Digest() != b.Digest() {
		t.Fatalf("digest depends on key order: %s vs %s", a.Digest(), b.Digest())
	}
}

func TestBundleDigestCoversDomainSequenceAndKeys(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	other := mustDomain(t, "beta.example")
	pub1, _ := testKeyPair(t, 0x11)
	pub2, _ := testKeyPair(t, 0x12)
	k1 := BundleKey{ID: "k1", PublicKey: pub1}
	base := mustBundle(t, domain, 1, testNow, k1)

	variants := map[string]Bundle{
		"different domain":    mustBundle(t, other, 1, testNow, k1),
		"different sequence":  mustBundle(t, domain, 2, testNow, k1),
		"different key ID":    mustBundle(t, domain, 1, testNow, BundleKey{ID: "k9", PublicKey: pub1}),
		"different key bytes": mustBundle(t, domain, 1, testNow, BundleKey{ID: "k1", PublicKey: pub2}),
		"extra key":           mustBundle(t, domain, 1, testNow, k1, BundleKey{ID: "k2", PublicKey: pub2}),
	}
	for name, variant := range variants {
		if variant.Digest() == base.Digest() {
			t.Errorf("%s: digest did not change", name)
		}
	}
}

func TestBundleDigestDoesNotCoverValidatedAt(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	pub1, _ := testKeyPair(t, 0x11)
	k1 := BundleKey{ID: "k1", PublicKey: pub1}
	early := mustBundle(t, domain, 1, testNow.Add(-time.Hour), k1)
	late := mustBundle(t, domain, 1, testNow, k1)
	if early.Digest() != late.Digest() {
		t.Fatal("validated-at time changed the digest of the same content")
	}
}

func TestBundleDigestMatchesCanonicalEncoding(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	pubA, _ := testKeyPair(t, 0x11)
	pubB, _ := testKeyPair(t, 0x12)
	bundle := mustBundle(t, domain, 7, testNow,
		BundleKey{ID: "k2", PublicKey: pubB},
		BundleKey{ID: "k1", PublicKey: pubA},
	)

	// The expected encoding is written out field by field: a length-prefixed tag,
	// the length-prefixed domain, the sequence as 8 bytes, the key count as 4 bytes,
	// and then each (ID, public key) pair in ID order, every field length-prefixed.
	var want []byte
	put := func(field []byte) {
		want = binary.BigEndian.AppendUint32(want, uint32(len(field)))
		want = append(want, field...)
	}
	put([]byte("eki-federation-bundle-digest/v1"))
	put([]byte("alpha.example"))
	want = binary.BigEndian.AppendUint64(want, 7)
	want = binary.BigEndian.AppendUint32(want, 2)
	put([]byte("k1"))
	put(pubA)
	put([]byte("k2"))
	put(pubB)

	if got, expected := bundle.Digest(), Digest(sha256.Sum256(want)); got != expected {
		t.Fatalf("digest = %s, want %s", got, expected)
	}
}

func TestBundleCopiesItsInputsAndOutputs(t *testing.T) {
	domain := mustDomain(t, "alpha.example")
	pub1, _ := testKeyPair(t, 0x11)
	keys := []BundleKey{{ID: "k1", PublicKey: slices.Clone(pub1)}}
	bundle := mustBundle(t, domain, 1, testNow, keys...)
	before := bundle.Digest()

	keys[0].PublicKey[0] ^= 0xff
	keys[0].ID = "changed"
	if bundle.Digest() != before {
		t.Fatal("bundle changed after the caller modified its input keys")
	}

	out := bundle.Keys()
	out[0].PublicKey[0] ^= 0xff
	if bundle.Digest() != before || bundle.Keys()[0].PublicKey[0] != pub1[0] {
		t.Fatal("bundle changed after the caller modified the Keys() copy")
	}
}

func TestZeroBundleIsRejected(t *testing.T) {
	var zero Bundle
	if _, err := BundleSigningInput(zero); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("BundleSigningInput(zero) error = %v, want ErrInvalidBundle", err)
	}
	reg := mustRegistry(t)
	w := newTestWorld(t)
	if err := reg.Federate(w.alphaLink(t), zero, nil); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("Federate with zero bundle error = %v, want ErrInvalidBundle", err)
	}
	if err := reg.InstallBundle(zero, nil); !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("InstallBundle with zero bundle error = %v, want ErrInvalidBundle", err)
	}
}

func TestBundleSigningInputIsDomainSeparated(t *testing.T) {
	w := newTestWorld(t)
	bundle := mustBundle(t, w.alpha.domain, 1, testNow, w.bundleKeys(0b01)...)
	bundleInput, err := BundleSigningInput(bundle)
	if err != nil {
		t.Fatalf("BundleSigningInput: %v", err)
	}
	identityInput, err := IdentitySigningInput(w.alpha.domain, "worker-1")
	if err != nil {
		t.Fatalf("IdentitySigningInput: %v", err)
	}
	if string(bundleInput) == string(identityInput) {
		t.Fatal("bundle and identity signing inputs collide")
	}
	// An identity signature must never verify as a bundle signature, even with
	// the same key. Both sides use a domain-separation tag, so this holds.
	sig := ed25519.Sign(w.keyPrivs[0], identityInput)
	if ed25519.Verify(w.keyPubs[0], bundleInput, sig) {
		t.Fatal("identity signature verified as a bundle signature")
	}
}
