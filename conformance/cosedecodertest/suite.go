package cosedecodertest

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/vectors"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/spi/cosedecoder"
)

const manifestMaxBytes = 64 * 1024

type Factory func(t testing.TB) cosedecoder.Codec

type manifest struct {
	Profile string   `json:"profile"`
	TestKey testKey  `json:"test_key"`
	Vectors []vector `json:"vectors"`
}

type testKey struct {
	KeyID        string `json:"kid"`
	Algorithm    string `json:"algorithm"`
	PublicKeyHex string `json:"public_key_hex"`
}

type vector struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	COSEHex        string `json:"cose_hex"`
	ExpectedReason string `json:"expected_reason"`
	Phase          string `json:"phase"`
}

// RunCodecContract applies the repository's language-neutral signed-decision
// COSE vectors to a concrete decoder/Signature1 encoder implementation.
func RunCodecContract(t *testing.T, newCodec Factory) {
	t.Helper()
	if newCodec == nil {
		t.Fatal("cosedecodertest Factory is nil")
	}

	m, err := loadManifest()
	if err != nil {
		t.Fatalf("load COSE conformance manifest: %v", err)
	}
	publicKey, err := validateManifestIdentity(m)
	if err != nil {
		t.Fatalf("invalid COSE conformance manifest identity: %v", err)
	}

	for _, v := range m.Vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			codec := newCodec(t)
			if codec == nil {
				t.Fatal("Factory returned nil codec")
			}

			raw, err := hex.DecodeString(v.COSEHex)
			if err != nil {
				t.Fatalf("decode vector hex: %v", err)
			}

			envelope, decodeErr := codec.DecodeSign1(raw)
			switch v.Phase {
			case "envelope", "protected", "payload":
				assertDecoderFailure(t, envelope, decodeErr, decisiontrust.VerificationReason(v.ExpectedReason))
				return
			case "crypto", "complete":
				if decodeErr != nil {
					t.Fatalf("DecodeSign1() error = %v; vector must reach %s phase", decodeErr, v.Phase)
				}
			default:
				t.Fatalf("unsupported vector phase %q", v.Phase)
			}

			if envelope.IsZero() {
				t.Fatal("DecodeSign1() returned zero envelope without error")
			}
			if envelope.Algorithm() != decisiontrust.AlgorithmEdDSA {
				t.Fatalf("Algorithm() = %q, want %q", envelope.Algorithm(), decisiontrust.AlgorithmEdDSA)
			}
			if envelope.KeyID() != decisiontrust.KeyID(m.TestKey.KeyID) {
				t.Fatalf("KeyID() = %q, want %q", envelope.KeyID(), m.TestKey.KeyID)
			}

			sigStructure, err := codec.EncodeSignature1(envelope.ProtectedHeader(), envelope.Payload())
			if err != nil {
				t.Fatalf("EncodeSignature1() error = %v", err)
			}
			if len(sigStructure) == 0 {
				t.Fatal("EncodeSignature1() returned empty bytes")
			}

			signatureValid := ed25519.Verify(publicKey, sigStructure, envelope.Signature())
			switch {
			case v.Phase == "complete" && v.ExpectedReason == "OK":
				if !signatureValid {
					t.Fatal("valid vector signature did not verify against generated Sig_structure")
				}
			case v.Phase == "crypto" && v.ExpectedReason == string(decisiontrust.ReasonSignatureInvalid):
				if signatureValid {
					t.Fatal("tampered signature unexpectedly verified")
				}
			default:
				t.Fatalf(
					"vector reached unsupported post-decode state phase=%q reason=%q",
					v.Phase,
					v.ExpectedReason,
				)
			}
		})
	}
}

func assertDecoderFailure(
	t testing.TB,
	envelope cosedecoder.Envelope,
	err error,
	want decisiontrust.VerificationReason,
) {
	t.Helper()
	if err == nil {
		t.Fatalf("DecodeSign1() succeeded, want failure %q", want)
	}
	if !envelope.IsZero() {
		t.Fatal("DecodeSign1() returned partially populated envelope with error")
	}
	got, ok := decisiontrust.FailureReason(err)
	if !ok {
		t.Fatalf("DecodeSign1() error %v does not carry VerificationFailure", err)
	}
	if got != want {
		t.Fatalf("DecodeSign1() reason = %q, want %q (err=%v)", got, want, err)
	}
}

func validateManifestIdentity(m manifest) (ed25519.PublicKey, error) {
	if m.Profile != decisiontrust.SignedDecisionProfileV1 {
		return nil, fmt.Errorf("profile = %q, want %q", m.Profile, decisiontrust.SignedDecisionProfileV1)
	}
	if m.TestKey.KeyID == "" {
		return nil, errors.New("test key ID is empty")
	}
	if m.TestKey.Algorithm != string(decisiontrust.AlgorithmEdDSA) {
		return nil, fmt.Errorf("test key algorithm = %q, want EdDSA", m.TestKey.Algorithm)
	}
	publicKey, err := hex.DecodeString(m.TestKey.PublicKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode test public key: %w", err)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("test public key length = %d, want %d", len(publicKey), ed25519.PublicKeySize)
	}
	if len(m.Vectors) == 0 {
		return nil, errors.New("vector list is empty")
	}
	return ed25519.PublicKey(bytes.Clone(publicKey)), nil
}

func loadManifest() (manifest, error) {
	raw := vectors.SignedDecisionCOSEV1()
	if len(raw) == 0 || len(raw) > manifestMaxBytes {
		return manifest{}, fmt.Errorf("manifest size = %d, want 1..%d", len(raw), manifestMaxBytes)
	}

	var m manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return manifest{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return manifest{}, fmt.Errorf("trailing JSON value or invalid tail: %v", err)
	}
	return m, nil
}
