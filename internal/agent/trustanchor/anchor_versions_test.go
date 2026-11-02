package trustanchor

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane/enrollwire"
)

func wireVersion(t *testing.T, version uint64) enrollwire.Bundle {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	bundle := trustbundle.Bundle{
		Version: version,
		Issuers: []trustbundle.Issuer{{
			Fingerprint: contract.SHA256([]byte("ca")), NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour),
		}},
		NotAfter: now.Add(90*24*time.Hour + time.Duration(version)*24*time.Hour),
	}
	signed, err := trustbundle.Sign(bundle, "control-key-1", priv)
	if err != nil {
		t.Fatal(err)
	}
	return enrollwire.EncodeBundle(signed)
}

func TestAnAnchorWithoutVersionsStillLoads(t *testing.T) {
	if err := checkVersions(File{BundleRevision: 7}); err != nil {
		t.Fatalf("checkVersions() error = %v", err)
	}
}

func TestTheNewestStoredVersionMustBeTheBundleRevision(t *testing.T) {
	doc := File{BundleRevision: 2, TrustBundleVersions: []enrollwire.Bundle{wireVersion(t, 1), wireVersion(t, 2)}}
	if err := checkVersions(doc); err != nil {
		t.Fatalf("checkVersions() error = %v", err)
	}
	doc.BundleRevision = 1
	if err := checkVersions(doc); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want %v", err, ErrInvalid)
	}
}

func TestABrokenVersionInTheHistoryIsRefused(t *testing.T) {
	doc := File{BundleRevision: 1, TrustBundleVersions: []enrollwire.Bundle{{}}}
	if err := checkVersions(doc); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want %v", err, ErrInvalid)
	}
}
