package cbor

import (
	"bytes"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
)

func TestEncodeSign1RoundTripsThroughStrictDecoder(t *testing.T) {
	codec := newCodec(t)
	protected, err := codec.EncodeProtectedHeader(decisiontrust.AlgorithmEdDSA, "kid-lse-1")
	if err != nil {
		t.Fatalf("EncodeProtectedHeader() error = %v", err)
	}
	if !bytes.Equal(protected, mustEnc(t, protectedHeader{
		Algorithm: ptr(int64(-8)),
		Critical:  ptr([]int64{decisiontrust.COSEHeaderLabelEKIProfile}),
		KeyID:     ptr([]byte("kid-lse-1")),
		Profile:   ptr(decisiontrust.SignedDecisionProfileV1),
	})) {
		t.Fatal("protected header is not the canonical EKI profile header")
	}
	payload := validPayload(t)
	signature := bytes.Repeat([]byte{0x5a}, 64)

	raw, err := codec.EncodeSign1(protected, payload, signature)
	if err != nil {
		t.Fatalf("EncodeSign1() error = %v", err)
	}
	envelope, err := codec.DecodeSign1(raw)
	if err != nil {
		t.Fatalf("DecodeSign1() error = %v", err)
	}
	if !bytes.Equal(envelope.ProtectedHeader(), protected) ||
		!bytes.Equal(envelope.Payload(), payload) ||
		!bytes.Equal(envelope.Signature(), signature) ||
		envelope.KeyID() != "kid-lse-1" ||
		envelope.Algorithm() != decisiontrust.AlgorithmEdDSA {
		t.Fatalf("round trip changed envelope: %#v", envelope)
	}
}

func TestEncodeSign1RefusesOutputTheDecoderWouldReject(t *testing.T) {
	codec := newCodec(t)
	protected, err := codec.EncodeProtectedHeader(decisiontrust.AlgorithmEdDSA, "kid-lse-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.EncodeSign1(protected, validPayload(t), make([]byte, 63)); err == nil {
		t.Fatal("short signature produced an envelope")
	}
	if _, err := codec.EncodeSign1(protected, nil, make([]byte, 64)); err == nil {
		t.Fatal("detached payload produced an envelope")
	}
	if _, err := codec.EncodeSign1(protected, []byte{0x01}, make([]byte, 64)); err == nil {
		t.Fatal("non-map payload produced an envelope")
	}
	if _, err := codec.EncodeProtectedHeader("none", "kid"); err == nil {
		t.Fatal("unsupported algorithm produced a protected header")
	}
	if _, err := codec.EncodeProtectedHeader(decisiontrust.AlgorithmEdDSA, ""); err == nil {
		t.Fatal("empty kid produced a protected header")
	}
}
