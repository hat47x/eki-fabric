package trustbundle

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// The fingerprint is the SHA-256 of the certificate's DER form: the value
// `openssl x509 -fingerprint -sha256` prints and the one a bundle lists. It is not
// the digest of the digest (#466).
func TestTheIssuerFingerprintIsTheDigestOfTheDER(t *testing.T) {
	ca := testCA(t, "test-root", base.Add(24*time.Hour))
	sum := sha256.Sum256(ca.Raw)
	want := hex.EncodeToString(sum[:])

	got := IssuerFingerprint(ca)
	if got.Hex() != want {
		t.Fatalf("IssuerFingerprint() = %s, want the sha256 of the DER %s", got.Hex(), want)
	}

	// The mistake this test exists to catch: hashing the digest again.
	double := sha256.Sum256(sum[:])
	if got.Hex() == hex.EncodeToString(double[:]) {
		t.Fatal("IssuerFingerprint() hashed the digest twice")
	}
}

// A nil certificate has no fingerprint, and the zero Digest says so without
// pretending to be one.
func TestNoCertificateHasNoFingerprint(t *testing.T) {
	if got := IssuerFingerprint(nil); !got.IsZero() {
		t.Fatalf("IssuerFingerprint(nil) = %s, want the zero digest", got.Hex())
	}
}

// The durable form agrees with the computed one: what a bundle stores is what a
// later check compares against.
func TestTheStoredFingerprintMatchesTheComputedOne(t *testing.T) {
	ca := testCA(t, "test-root", base.Add(24*time.Hour))
	stored, err := contract.ParseSHA256(IssuerFingerprint(ca).Hex())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Hex() != IssuerFingerprint(ca).Hex() {
		t.Fatal("the stored fingerprint does not equal the computed one")
	}
}
