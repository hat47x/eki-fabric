package cosedecoder

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
)

func TestNewEnvelopePreservesValidatedIdentityAndDefensivelyCopiesBytes(t *testing.T) {
	protected := []byte{0xa4, 0x01, 0x27}
	payload := []byte{0xa1, 0x01, 0x01}
	signature := make([]byte, 64)
	signature[0] = 1

	envelope, err := NewEnvelope(EnvelopeConfig{
		ProtectedHeader: protected,
		Payload:         payload,
		Signature:       signature,
		Algorithm:       decisiontrust.AlgorithmEdDSA,
		KeyID:           decisiontrust.KeyID("kid-test"),
	})
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}

	protected[0] = 0
	payload[0] = 0
	signature[0] = 0

	if got := envelope.ProtectedHeader(); !bytes.Equal(got, []byte{0xa4, 0x01, 0x27}) {
		t.Fatalf("ProtectedHeader() = %x", got)
	}
	if got := envelope.Payload(); !bytes.Equal(got, []byte{0xa1, 0x01, 0x01}) {
		t.Fatalf("Payload() = %x", got)
	}
	if got := envelope.Signature(); len(got) != 64 || got[0] != 1 {
		t.Fatalf("Signature() = %x", got)
	}
	if envelope.Algorithm() != decisiontrust.AlgorithmEdDSA {
		t.Fatalf("Algorithm() = %q", envelope.Algorithm())
	}
	if envelope.KeyID() != decisiontrust.KeyID("kid-test") {
		t.Fatalf("KeyID() = %q", envelope.KeyID())
	}

	gotProtected := envelope.ProtectedHeader()
	gotPayload := envelope.Payload()
	gotSignature := envelope.Signature()
	gotProtected[0] = 0
	gotPayload[0] = 0
	gotSignature[0] = 0
	if envelope.ProtectedHeader()[0] != 0xa4 || envelope.Payload()[0] != 0xa1 || envelope.Signature()[0] != 1 {
		t.Fatal("accessor returned mutable internal storage")
	}
}

func TestNewEnvelopeAllowsAttachedEmptyPayload(t *testing.T) {
	envelope, err := NewEnvelope(EnvelopeConfig{
		ProtectedHeader: []byte{0xa0},
		Payload:         []byte{},
		Signature:       make([]byte, 64),
		Algorithm:       decisiontrust.AlgorithmEdDSA,
		KeyID:           decisiontrust.KeyID("kid-empty-payload"),
	})
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	if envelope.Payload() == nil {
		t.Fatal("Payload() = nil; attached empty bstr must remain distinguishable at decoder boundary")
	}
}

func TestNewEnvelopeRejectsBrokenInternalInvariants(t *testing.T) {
	valid := EnvelopeConfig{
		ProtectedHeader: []byte{0xa0},
		Payload:         []byte{0x01},
		Signature:       make([]byte, 64),
		Algorithm:       decisiontrust.AlgorithmEdDSA,
		KeyID:           decisiontrust.KeyID("kid-valid"),
	}

	tests := []struct {
		name   string
		mutate func(*EnvelopeConfig)
	}{
		{
			name: "empty protected header",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.ProtectedHeader = nil
			},
		},
		{
			name: "oversized protected header",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.ProtectedHeader = make([]byte, decisiontrust.MaxProtectedHeaderBytes+1)
			},
		},
		{
			name: "detached nil payload",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.Payload = nil
			},
		},
		{
			name: "oversized payload",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.Payload = make([]byte, decisiontrust.MaxCOSESign1Bytes+1)
			},
		},
		{
			name: "wrong signature size",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.Signature = make([]byte, 63)
			},
		},
		{
			name: "unknown algorithm",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.Algorithm = decisiontrust.Algorithm("unknown")
			},
		},
		{
			name: "empty key ID",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.KeyID = ""
			},
		},
		{
			name: "oversized key ID",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.KeyID = decisiontrust.KeyID(strings.Repeat("k", decisiontrust.MaxKeyIDBytes+1))
			},
		},
		{
			name: "invalid UTF-8 key ID",
			mutate: func(cfg *EnvelopeConfig) {
				cfg.KeyID = decisiontrust.KeyID(string([]byte{0xff}))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			got, err := NewEnvelope(cfg)
			if !errors.Is(err, ErrInvalidDecodedEnvelope) {
				t.Fatalf("NewEnvelope() error = %v, want ErrInvalidDecodedEnvelope", err)
			}
			if !got.IsZero() {
				t.Fatalf("NewEnvelope() returned non-zero envelope on error: %#v", got)
			}
		})
	}
}

func TestZeroEnvelopeReportsZero(t *testing.T) {
	if !(Envelope{}).IsZero() {
		t.Fatal("zero Envelope must report IsZero")
	}
}
