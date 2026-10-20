// Package cbor is the reference strict COSE_Sign1 decoder for the EKI signed
// decision profile (ADR-0005).
//
// It is hostile-input parsing code. It uses fxamacker/cbor/v2 with every
// resource and form restriction turned on, then re-encodes what it decoded
// with Core Deterministic Encoding and compares bytes, because a successful
// decode alone does not prove the bytes are canonical.
package cbor

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	fxcbor "github.com/fxamacker/cbor/v2"

	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/spi/cosedecoder"
)

const (
	// cborTag18 is the single initial byte of CBOR tag 18 (COSE_Sign1) in its
	// shortest form: major type 6 with additional information 18.
	cborTag18 = 0xd2
	// emptyMap is the only accepted unprotected header: a definite empty map.
	emptyMap = 0xa0
	// cborNull is the CBOR encoding of a detached payload.
	cborNull = 0xf6

	signature1Context = "Signature1"

	// Hard limits. The baseline structures are small, so these are far below
	// library defaults and bound the work a hostile input can cause.
	maxNestedLevels  = 8
	maxArrayElements = 64
	maxMapPairs      = 32
)

// Codec decodes strict COSE_Sign1 envelopes and builds Signature1 bytes.
type Codec struct {
	outer     fxcbor.DecMode
	protected fxcbor.DecMode
	payload   fxcbor.DecMode
	encode    fxcbor.EncMode
}

var _ cosedecoder.SigningCodec = (*Codec)(nil)

// New returns a Codec. Construction fails only if the CBOR library rejects an
// option set, which would be a programming error.
func New() (*Codec, error) {
	base := fxcbor.DecOptions{
		DupMapKey:        fxcbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels:  maxNestedLevels,
		MaxArrayElements: maxArrayElements,
		MaxMapPairs:      maxMapPairs,
		IndefLength:      fxcbor.IndefLengthForbidden,
		TagsMd:           fxcbor.TagsForbidden,
	}

	outerOpts := base
	outer, err := outerOpts.DecMode()
	if err != nil {
		return nil, fmt.Errorf("create COSE outer decoder: %w", err)
	}

	protectedOpts := base
	protectedOpts.ExtraReturnErrors = fxcbor.ExtraDecErrorUnknownField
	protected, err := protectedOpts.DecMode()
	if err != nil {
		return nil, fmt.Errorf("create COSE protected-header decoder: %w", err)
	}

	payload, err := base.DecMode()
	if err != nil {
		return nil, fmt.Errorf("create COSE payload decoder: %w", err)
	}

	encode, err := fxcbor.CoreDetEncOptions().EncMode()
	if err != nil {
		return nil, fmt.Errorf("create deterministic CBOR encoder: %w", err)
	}
	return &Codec{outer: outer, protected: protected, payload: payload, encode: encode}, nil
}

// sign1 is the COSE_Sign1 array after the tag has been removed.
type sign1 struct {
	_           struct{} `cbor:",toarray"`
	Protected   []byte
	Unprotected fxcbor.RawMessage
	Payload     fxcbor.RawMessage
	Signature   []byte
}

// protectedHeader is the closed set of protected header labels the baseline
// allows. Pointer fields distinguish "absent" from a zero value.
type protectedHeader struct {
	Algorithm *int64   `cbor:"1,keyasint"`
	Critical  *[]int64 `cbor:"2,keyasint"`
	KeyID     *[]byte  `cbor:"4,keyasint"`
	Profile   *string  `cbor:"-70000,keyasint"`
}

func fail(reason decisiontrust.VerificationReason, format string, args ...any) error {
	return &decisiontrust.VerificationFailure{Reason: reason, Err: fmt.Errorf(format, args...)}
}

// DecodeSign1 parses raw into an Envelope or returns a
// *decisiontrust.VerificationFailure. It never returns a partially populated
// Envelope. Library errors are normalized and not exposed as a contract.
func (c *Codec) DecodeSign1(raw []byte) (cosedecoder.Envelope, error) {
	envelope, err := c.decodeSign1(raw)
	if err != nil {
		return cosedecoder.Envelope{}, err
	}
	return envelope, nil
}

func (c *Codec) decodeSign1(raw []byte) (cosedecoder.Envelope, error) {
	if c == nil || c.outer == nil {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "codec is not initialized")
	}

	// 1. Transport-level byte limit.
	if len(raw) == 0 || len(raw) > decisiontrust.MaxCOSESign1Bytes {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "COSE_Sign1 must contain 1..%d bytes", decisiontrust.MaxCOSESign1Bytes)
	}

	// 2-4. Shape: tag 18, then a definite four-element array.
	if raw[0] != cborTag18 {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "COSE_Sign1 must carry CBOR tag 18")
	}
	var msg sign1
	if err := c.outer.Unmarshal(raw[1:], &msg); err != nil {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "COSE_Sign1 array: %v", err)
	}
	if len(msg.Unprotected) != 1 || msg.Unprotected[0] != emptyMap {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "unprotected header must be an empty map")
	}
	if len(msg.Payload) == 0 || msg.Payload[0] == cborNull {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "payload must be attached")
	}
	var payload []byte
	if err := c.outer.Unmarshal(msg.Payload, &payload); err != nil || payload == nil {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "payload must be a byte string")
	}
	if len(msg.Signature) != 64 {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "signature must contain exactly 64 bytes")
	}

	// 5-7. Protected header: strict decode, profile checks, canonical bytes.
	algorithm, keyID, err := c.checkProtected(msg.Protected)
	if err != nil {
		return cosedecoder.Envelope{}, err
	}

	// 8. Payload must be one deterministic CBOR map.
	if err := c.checkPayload(payload); err != nil {
		return cosedecoder.Envelope{}, err
	}

	envelope, err := cosedecoder.NewEnvelope(cosedecoder.EnvelopeConfig{
		ProtectedHeader: msg.Protected,
		Payload:         payload,
		Signature:       msg.Signature,
		Algorithm:       algorithm,
		KeyID:           keyID,
	})
	if err != nil {
		return cosedecoder.Envelope{}, fail(decisiontrust.ReasonMalformedCOSE, "%v", err)
	}
	return envelope, nil
}

func (c *Codec) checkProtected(encoded []byte) (decisiontrust.Algorithm, decisiontrust.KeyID, error) {
	if len(encoded) == 0 || len(encoded) > decisiontrust.MaxProtectedHeaderBytes {
		return "", "", fail(decisiontrust.ReasonProtectedHeaderInvalid, "protected header must contain 1..%d bytes", decisiontrust.MaxProtectedHeaderBytes)
	}

	var h protectedHeader
	if err := c.protected.Unmarshal(encoded, &h); err != nil {
		return "", "", fail(decisiontrust.ReasonProtectedHeaderInvalid, "protected header: %v", err)
	}
	if h.Algorithm == nil || h.Critical == nil || h.KeyID == nil || h.Profile == nil {
		return "", "", fail(decisiontrust.ReasonProtectedHeaderInvalid, "protected header must contain alg, crit, kid, and the EKI profile label")
	}

	// Check in a fixed order so the reason is stable: algorithm first, then
	// crit, profile, and key ID.
	if _, ok := decisiontrust.AlgorithmFromCOSEIdentifier(*h.Algorithm); !ok {
		return "", "", fail(decisiontrust.ReasonAlgorithmNotAllowed, "unsupported algorithm identifier %d", *h.Algorithm)
	}
	if len(*h.Critical) != 1 || (*h.Critical)[0] != decisiontrust.COSEHeaderLabelEKIProfile {
		return "", "", fail(decisiontrust.ReasonProtectedHeaderInvalid, "crit must contain only the EKI profile label")
	}
	if *h.Profile != decisiontrust.SignedDecisionProfileV1 {
		return "", "", fail(decisiontrust.ReasonUnsupportedProfile, "profile %q is unsupported", *h.Profile)
	}
	if n := len(*h.KeyID); n == 0 || n > decisiontrust.MaxKeyIDBytes {
		return "", "", fail(decisiontrust.ReasonProtectedHeaderInvalid, "kid must contain 1..%d bytes", decisiontrust.MaxKeyIDBytes)
	}

	algorithm, keyID, err := decisiontrust.ProtectedHeaderProfile{
		AlgorithmIdentifier: *h.Algorithm,
		CriticalLabels:      *h.Critical,
		KeyID:               *h.KeyID,
		Profile:             *h.Profile,
	}.Validate()
	if err != nil {
		return "", "", fail(decisiontrust.ReasonProtectedHeaderInvalid, "%v", err)
	}

	// Decoding succeeding is not enough: the same value must re-encode to the
	// exact bytes we were given.
	canonical, err := c.encode.Marshal(h)
	if err != nil {
		return "", "", fail(decisiontrust.ReasonProtectedHeaderInvalid, "canonical re-encode: %v", err)
	}
	if !bytes.Equal(canonical, encoded) {
		return "", "", fail(decisiontrust.ReasonProtectedHeaderInvalid, "protected header is not Core Deterministic Encoding")
	}
	return algorithm, keyID, nil
}

// checkPayload verifies that payload is a single well-formed CBOR map in Core
// Deterministic Encoding. It does not interpret the Decision; that is the
// payload decoder's job (ADR-0005 step 9).
func (c *Codec) checkPayload(payload []byte) error {
	// A CBOR map is major type 5: the top three bits of the first byte are 101.
	if len(payload) == 0 || payload[0]&0xe0 != 0xa0 {
		return fail(decisiontrust.ReasonMalformedCOSE, "payload must be a CBOR map")
	}

	var value any
	if err := c.payload.Unmarshal(payload, &value); err != nil {
		var syntax *fxcbor.SyntaxError
		var extra *fxcbor.ExtraneousDataError
		if errors.As(err, &syntax) || errors.As(err, &extra) ||
			errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return fail(decisiontrust.ReasonMalformedCOSE, "payload is not well-formed CBOR: %v", err)
		}
		// Well-formed but outside the deterministic profile: indefinite
		// length, duplicate keys, tags, or over a resource limit.
		return fail(decisiontrust.ReasonPayloadNotDeterministic, "payload: %v", err)
	}
	canonical, err := c.encode.Marshal(value)
	if err != nil {
		return fail(decisiontrust.ReasonPayloadNotDeterministic, "payload canonical re-encode: %v", err)
	}
	if !bytes.Equal(canonical, payload) {
		return fail(decisiontrust.ReasonPayloadNotDeterministic, "payload is not Core Deterministic Encoding")
	}
	return nil
}

// EncodeSignature1 builds the COSE Sig_structure for Signature1 with an empty
// external AAD:
//
//	["Signature1", protected, h'', payload]
//
// The protected header must be the exact bytes taken from the envelope.
func (c *Codec) EncodeSignature1(protectedHeader, payload []byte) ([]byte, error) {
	if c == nil || c.encode == nil {
		return nil, errors.New("COSE codec is not initialized")
	}
	if len(protectedHeader) == 0 || len(protectedHeader) > decisiontrust.MaxProtectedHeaderBytes {
		return nil, fmt.Errorf("protected header must contain 1..%d bytes", decisiontrust.MaxProtectedHeaderBytes)
	}
	if payload == nil {
		return nil, errors.New("detached payload is prohibited")
	}
	if len(payload) > decisiontrust.MaxCOSESign1Bytes {
		return nil, fmt.Errorf("payload exceeds the %d-byte bound", decisiontrust.MaxCOSESign1Bytes)
	}
	out, err := c.encode.Marshal([]any{signature1Context, protectedHeader, []byte{}, payload})
	if err != nil {
		return nil, fmt.Errorf("encode Signature1: %w", err)
	}
	return out, nil
}

// EncodeProtectedHeader builds the canonical EKI signed-decision protected
// header for one signing key: alg, crit = [EKI profile], kid, and the profile.
func (c *Codec) EncodeProtectedHeader(algorithm decisiontrust.Algorithm, keyID decisiontrust.KeyID) ([]byte, error) {
	if c == nil || c.encode == nil {
		return nil, errors.New("COSE codec is not initialized")
	}
	identifier, ok := algorithm.COSEIdentifier()
	if !ok {
		return nil, fmt.Errorf("unsupported algorithm %q", algorithm)
	}
	critical := []int64{decisiontrust.COSEHeaderLabelEKIProfile}
	kid := []byte(string(keyID))
	profile := decisiontrust.SignedDecisionProfileV1
	if _, _, err := (decisiontrust.ProtectedHeaderProfile{
		AlgorithmIdentifier: identifier,
		CriticalLabels:      critical,
		KeyID:               kid,
		Profile:             profile,
	}).Validate(); err != nil {
		return nil, err
	}
	out, err := c.encode.Marshal(protectedHeader{
		Algorithm: &identifier,
		Critical:  &critical,
		KeyID:     &kid,
		Profile:   &profile,
	})
	if err != nil {
		return nil, fmt.Errorf("encode protected header: %w", err)
	}
	if len(out) > decisiontrust.MaxProtectedHeaderBytes {
		return nil, fmt.Errorf("protected header exceeds %d bytes", decisiontrust.MaxProtectedHeaderBytes)
	}
	return out, nil
}

// EncodeSign1 assembles tag 18 [protected, {}, payload, signature] with an
// attached payload and an empty unprotected header, then checks that the
// result passes the strict decoder. A value this codec would reject on the
// relying side is never returned to an issuer.
func (c *Codec) EncodeSign1(protected, payload, signature []byte) ([]byte, error) {
	if c == nil || c.encode == nil {
		return nil, errors.New("COSE codec is not initialized")
	}
	if payload == nil {
		return nil, errors.New("detached payload is prohibited")
	}
	body, err := c.encode.Marshal([]any{protected, map[int64]any{}, payload, signature})
	if err != nil {
		return nil, fmt.Errorf("encode COSE_Sign1: %w", err)
	}
	out := append([]byte{cborTag18}, body...)
	if _, err := c.decodeSign1(out); err != nil {
		return nil, fmt.Errorf("encoded COSE_Sign1 violates the EKI profile: %w", err)
	}
	return out, nil
}
