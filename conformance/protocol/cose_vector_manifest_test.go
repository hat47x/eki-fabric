package protocol

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/authority"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
)

const coseVectorManifestMaxBytes = 64 * 1024

type coseVectorManifest struct {
	Profile string            `json:"profile"`
	TestKey coseVectorTestKey `json:"test_key"`
	Vectors []coseVector      `json:"vectors"`
}

type coseVectorTestKey struct {
	KeyID        string `json:"kid"`
	Algorithm    string `json:"algorithm"`
	PublicKeyHex string `json:"public_key_hex"`
}

type coseVector struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	COSEHex        string `json:"cose_hex"`
	ExpectedReason string `json:"expected_reason"`
	Phase          string `json:"phase"`
}

func TestCOSEVectorManifestIsClosedAndComplete(t *testing.T) {
	manifest := loadCOSEVectorManifest(t)

	if manifest.Profile != decisiontrust.SignedDecisionProfileV1 {
		t.Fatalf("profile = %q, want %q", manifest.Profile, decisiontrust.SignedDecisionProfileV1)
	}
	if manifest.TestKey.KeyID != "kid-test-ed25519" {
		t.Fatalf("test key ID = %q, want kid-test-ed25519", manifest.TestKey.KeyID)
	}
	if manifest.TestKey.Algorithm != string(decisiontrust.AlgorithmEdDSA) {
		t.Fatalf("test key algorithm = %q, want %q", manifest.TestKey.Algorithm, decisiontrust.AlgorithmEdDSA)
	}
	publicKey, err := hex.DecodeString(manifest.TestKey.PublicKeyHex)
	if err != nil {
		t.Fatalf("decode public key: %v", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		t.Fatalf("public key length = %d, want %d", len(publicKey), ed25519.PublicKeySize)
	}

	required := []string{
		"detached_payload",
		"duplicate_alg_key",
		"empty_kid",
		"indefinite_outer_array",
		"indefinite_protected_map",
		"kid_too_long",
		"missing_crit",
		"nondeterministic_alg_integer",
		"nondeterministic_payload_order",
		"nondeterministic_protected_order",
		"nonempty_unprotected",
		"signature_63_bytes",
		"tampered_signature",
		"trailing_data",
		"unknown_algorithm",
		"unknown_protected_header",
		"untagged_sign1",
		"valid_eddsa_lease",
		"wrong_cbor_tag",
		"wrong_crit",
		"wrong_profile",
	}
	if len(manifest.Vectors) != len(required) {
		t.Fatalf("vector count = %d, want %d", len(manifest.Vectors), len(required))
	}

	seen := make(map[string]struct{}, len(manifest.Vectors))
	names := make([]string, 0, len(manifest.Vectors))
	okCount := 0
	for _, vector := range manifest.Vectors {
		if vector.Name == "" {
			t.Fatal("vector with empty name")
		}
		if vector.Description == "" {
			t.Fatalf("vector %q has empty description", vector.Name)
		}
		if _, exists := seen[vector.Name]; exists {
			t.Fatalf("duplicate vector name %q", vector.Name)
		}
		seen[vector.Name] = struct{}{}
		names = append(names, vector.Name)

		raw, err := hex.DecodeString(vector.COSEHex)
		if err != nil {
			t.Fatalf("vector %q COSE hex: %v", vector.Name, err)
		}
		if len(raw) == 0 {
			t.Fatalf("vector %q has empty COSE bytes", vector.Name)
		}
		if len(raw) > decisiontrust.MaxCOSESign1Bytes {
			t.Fatalf(
				"vector %q has %d bytes, exceeds %d-byte COSE limit",
				vector.Name,
				len(raw),
				decisiontrust.MaxCOSESign1Bytes,
			)
		}
		if hex.EncodeToString(raw) != vector.COSEHex {
			t.Fatalf("vector %q COSE hex must be canonical lowercase hex", vector.Name)
		}

		if err := validateVectorOutcome(vector); err != nil {
			t.Fatalf("vector %q: %v", vector.Name, err)
		}
		if vector.ExpectedReason == "OK" {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("OK vector count = %d, want 1", okCount)
	}

	sort.Strings(names)
	sort.Strings(required)
	if !equalStrings(names, required) {
		t.Fatalf("vector names:\n got: %v\nwant: %v", names, required)
	}
}

func TestValidCOSEVectorIsByteForByteReproducible(t *testing.T) {
	manifest := loadCOSEVectorManifest(t)

	var valid *coseVector
	for i := range manifest.Vectors {
		if manifest.Vectors[i].Name == "valid_eddsa_lease" {
			valid = &manifest.Vectors[i]
			break
		}
	}
	if valid == nil {
		t.Fatal("valid_eddsa_lease vector not found")
	}

	got, err := hex.DecodeString(valid.COSEHex)
	if err != nil {
		t.Fatalf("decode valid vector: %v", err)
	}

	want, publicKey := buildValidEdDSALeaseVector(t)
	if !bytes.Equal(got, want) {
		t.Fatalf(
			"valid vector does not match deterministic regeneration\n got: %x\nwant: %x",
			got,
			want,
		)
	}
	if hex.EncodeToString(publicKey) != manifest.TestKey.PublicKeyHex {
		t.Fatalf(
			"regenerated public key = %x, manifest = %s",
			publicKey,
			manifest.TestKey.PublicKeyHex,
		)
	}
}

func loadCOSEVectorManifest(t testing.TB) coseVectorManifest {
	t.Helper()
	path := filepath.Join("..", "vectors", "signed-decision-cose-v1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read COSE vector manifest: %v", err)
	}
	if len(raw) == 0 || len(raw) > coseVectorManifestMaxBytes {
		t.Fatalf(
			"COSE vector manifest size = %d, want 1..%d bytes",
			len(raw),
			coseVectorManifestMaxBytes,
		)
	}

	var manifest coseVectorManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode COSE vector manifest: %v", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("COSE vector manifest has trailing JSON value or invalid tail: %v", err)
	}
	return manifest
}

func validateVectorOutcome(vector coseVector) error {
	var wantPhase string
	switch vector.ExpectedReason {
	case "OK":
		wantPhase = "complete"
	case string(decisiontrust.ReasonMalformedCOSE):
		wantPhase = "envelope"
	case string(decisiontrust.ReasonProtectedHeaderInvalid),
		string(decisiontrust.ReasonUnsupportedProfile),
		string(decisiontrust.ReasonAlgorithmNotAllowed):
		wantPhase = "protected"
	case string(decisiontrust.ReasonPayloadNotDeterministic):
		wantPhase = "payload"
	case string(decisiontrust.ReasonSignatureInvalid):
		wantPhase = "crypto"
	default:
		return fmt.Errorf("unsupported expected reason %q", vector.ExpectedReason)
	}
	if vector.Phase != wantPhase {
		return fmt.Errorf(
			"phase = %q, want %q for reason %q",
			vector.Phase,
			wantPhase,
			vector.ExpectedReason,
		)
	}
	return nil
}

func buildValidEdDSALeaseVector(t testing.TB) ([]byte, ed25519.PublicKey) {
	t.Helper()

	seed := make([]byte, ed25519.SeedSize)
	seed[0] = 1
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	algorithmID, ok := decisiontrust.AlgorithmEdDSA.COSEIdentifier()
	if !ok {
		t.Fatal("EdDSA lacks COSE identifier")
	}

	kid := []byte("kid-test-ed25519")
	protected := cborMap(
		cborInt(decisiontrust.COSEHeaderLabelAlgorithm), cborInt(algorithmID),
		cborInt(decisiontrust.COSEHeaderLabelCritical),
		cborArray(cborInt(decisiontrust.COSEHeaderLabelEKIProfile)),
		cborInt(decisiontrust.COSEHeaderLabelKeyID), cborBytes(kid),
		cborInt(decisiontrust.COSEHeaderLabelEKIProfile),
		cborText(decisiontrust.SignedDecisionProfileV1),
	)

	payload := cborMap(
		cborInt(1), cborUint(1),
		cborInt(2), cborText(string(authority.DecisionLease)),
		cborInt(3), cborText("d"),
		cborInt(4), cborMap(
			cborInt(1), cborText(string(authority.RoleLease)),
			cborInt(2), cborText("p"),
		),
		cborInt(5), cborUint(1000),
		cborInt(6), cborUint(2000),
		cborInt(7), cborText("r"),
		cborInt(8), cborMap(
			cborInt(1), cborText("t"),
			cborInt(2), cborMap(
				cborInt(1), cborUint(1),
				cborInt(2), cborBytes(make([]byte, 32)),
			),
			cborInt(3), cborText("a"),
			cborInt(4), cborText("w"),
			cborInt(5), cborText("l"),
			cborInt(6), cborUint(1),
			cborInt(7), cborUint(0),
		),
	)

	sigStructure := cborArray(
		cborText("Signature1"),
		cborBytes(protected),
		cborBytes(nil),
		cborBytes(payload),
	)
	signature := ed25519.Sign(privateKey, sigStructure)

	envelope := cborTag(
		decisiontrust.COSESign1Tag,
		cborArray(
			cborBytes(protected),
			[]byte{0xa0},
			cborBytes(payload),
			cborBytes(signature),
		),
	)
	return envelope, publicKey
}

func cborHead(major byte, value uint64) []byte {
	switch {
	case value < 24:
		return []byte{major<<5 | byte(value)}
	case value <= 0xff:
		return []byte{major<<5 | 24, byte(value)}
	case value <= 0xffff:
		return []byte{major<<5 | 25, byte(value >> 8), byte(value)}
	case value <= 0xffffffff:
		return []byte{
			major<<5 | 26,
			byte(value >> 24),
			byte(value >> 16),
			byte(value >> 8),
			byte(value),
		}
	default:
		return []byte{
			major<<5 | 27,
			byte(value >> 56),
			byte(value >> 48),
			byte(value >> 40),
			byte(value >> 32),
			byte(value >> 24),
			byte(value >> 16),
			byte(value >> 8),
			byte(value),
		}
	}
}

func cborUint(value uint64) []byte {
	return cborHead(0, value)
}

func cborInt(value int64) []byte {
	if value >= 0 {
		return cborUint(uint64(value))
	}
	return cborHead(1, uint64(-1-value))
}

func cborBytes(value []byte) []byte {
	return append(cborHead(2, uint64(len(value))), value...)
}

func cborText(value string) []byte {
	return append(cborHead(3, uint64(len(value))), []byte(value)...)
}

func cborArray(items ...[]byte) []byte {
	out := cborHead(4, uint64(len(items)))
	for _, item := range items {
		out = append(out, item...)
	}
	return out
}

func cborMap(items ...[]byte) []byte {
	if len(items)%2 != 0 {
		panic("test CBOR map requires key/value pairs")
	}
	out := cborHead(5, uint64(len(items)/2))
	for _, item := range items {
		out = append(out, item...)
	}
	return out
}

func cborTag(number uint64, item []byte) []byte {
	return append(cborHead(6, number), item...)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
