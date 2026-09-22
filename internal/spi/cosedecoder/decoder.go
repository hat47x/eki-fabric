package cosedecoder

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
)

var ErrInvalidDecodedEnvelope = errors.New("invalid decoded COSE_Sign1 envelope")

// Envelope is the result of a decoder that has already enforced the EKI
// signed-decision COSE profile at the envelope, protected-header, and payload
// canonicality layers.
//
// Constructing Envelope does not prove that the signature is valid and does
// not authorize execution.
type Envelope struct {
	protectedHeader []byte
	payload         []byte
	signature       []byte
	algorithm       decisiontrust.Algorithm
	keyID           decisiontrust.KeyID
}

type EnvelopeConfig struct {
	ProtectedHeader []byte
	Payload         []byte
	Signature       []byte
	Algorithm       decisiontrust.Algorithm
	KeyID           decisiontrust.KeyID
}

func NewEnvelope(cfg EnvelopeConfig) (Envelope, error) {
	if len(cfg.ProtectedHeader) == 0 || len(cfg.ProtectedHeader) > decisiontrust.MaxProtectedHeaderBytes {
		return Envelope{}, fmt.Errorf(
			"%w: protected header must contain 1..%d bytes",
			ErrInvalidDecodedEnvelope,
			decisiontrust.MaxProtectedHeaderBytes,
		)
	}
	if len(cfg.Payload) > decisiontrust.MaxCOSESign1Bytes {
		return Envelope{}, fmt.Errorf(
			"%w: payload exceeds defensive %d-byte bound",
			ErrInvalidDecodedEnvelope,
			decisiontrust.MaxCOSESign1Bytes,
		)
	}
	if len(cfg.Signature) != 64 {
		return Envelope{}, fmt.Errorf(
			"%w: signature must contain exactly 64 bytes",
			ErrInvalidDecodedEnvelope,
		)
	}
	if _, ok := cfg.Algorithm.COSEIdentifier(); !ok {
		return Envelope{}, fmt.Errorf(
			"%w: unsupported algorithm %q",
			ErrInvalidDecodedEnvelope,
			cfg.Algorithm,
		)
	}
	keyID := string(cfg.KeyID)
	if len(keyID) == 0 || len(keyID) > decisiontrust.MaxKeyIDBytes || !utf8.ValidString(keyID) {
		return Envelope{}, fmt.Errorf(
			"%w: key ID must contain 1..%d valid UTF-8 bytes",
			ErrInvalidDecodedEnvelope,
			decisiontrust.MaxKeyIDBytes,
		)
	}

	return Envelope{
		protectedHeader: append([]byte(nil), cfg.ProtectedHeader...),
		payload:         append([]byte(nil), cfg.Payload...),
		signature:       append([]byte(nil), cfg.Signature...),
		algorithm:       cfg.Algorithm,
		keyID:           cfg.KeyID,
	}, nil
}

func (e Envelope) ProtectedHeader() []byte {
	return append([]byte(nil), e.protectedHeader...)
}

func (e Envelope) Payload() []byte {
	return append([]byte(nil), e.payload...)
}

func (e Envelope) Signature() []byte {
	return append([]byte(nil), e.signature...)
}

func (e Envelope) Algorithm() decisiontrust.Algorithm {
	return e.algorithm
}

func (e Envelope) KeyID() decisiontrust.KeyID {
	return e.keyID
}

func (e Envelope) IsZero() bool {
	return len(e.protectedHeader) == 0 &&
		len(e.payload) == 0 &&
		len(e.signature) == 0 &&
		e.algorithm == "" &&
		e.keyID == ""
}

// Decoder parses hostile raw COSE_Sign1 bytes and enforces the EKI baseline
// envelope, protected-header, and deterministic-payload contract.
//
// Rejections caused by hostile input MUST be normalized to
// decisiontrust.VerificationFailure. Implementations MUST return the zero
// Envelope when err is non-nil and MUST NOT expose library-specific parser
// errors as a stable external contract.
type Decoder interface {
	DecodeSign1(raw []byte) (Envelope, error)
}

// Signature1Encoder constructs the exact deterministic CBOR Sig_structure for
// COSE Signature1 using empty external AAD.
//
// This is an internal cryptographic framing capability, not a generic CBOR
// encoder and not an authorization primitive.
type Signature1Encoder interface {
	EncodeSignature1(protectedHeader, payload []byte) ([]byte, error)
}

type Codec interface {
	Decoder
	Signature1Encoder
}
