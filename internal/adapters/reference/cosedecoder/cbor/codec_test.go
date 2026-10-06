package cbor

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	fxcbor "github.com/fxamacker/cbor/v2"

	"github.com/hat47x/eki-fabric/conformance/cosedecodertest"
	"github.com/hat47x/eki-fabric/internal/core/decisiontrust"
	"github.com/hat47x/eki-fabric/internal/spi/cosedecoder"
)

func newCodec(t testing.TB) *Codec {
	t.Helper()
	codec, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return codec
}

func TestCodecConformance(t *testing.T) {
	cosedecodertest.RunCodecContract(t, func(t testing.TB) cosedecoder.Codec {
		return newCodec(t)
	})
}

// The helpers below build envelopes from scratch so each test changes exactly
// one property and the failure it expects is not an accident of fixture bytes.

func mustEnc(t testing.TB, v any) []byte {
	t.Helper()
	mode, err := fxcbor.CoreDetEncOptions().EncMode()
	if err != nil {
		t.Fatalf("EncMode() error = %v", err)
	}
	out, err := mode.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return out
}

func validProtected(t testing.TB) []byte {
	t.Helper()
	return mustEnc(t, protectedHeader{
		Algorithm: ptr(int64(-8)),
		Critical:  ptr([]int64{decisiontrust.COSEHeaderLabelEKIProfile}),
		KeyID:     ptr([]byte("kid-1")),
		Profile:   ptr(decisiontrust.SignedDecisionProfileV1),
	})
}

func validPayload(t testing.TB) []byte {
	t.Helper()
	return mustEnc(t, map[uint64]any{1: uint64(1), 2: "lease"})
}

func ptr[T any](v T) *T { return &v }

// envelope assembles tag 18 + array(4) from raw CBOR items.
func envelope(protected, unprotected, payload, signature []byte) []byte {
	out := []byte{0xd2, 0x84}
	out = append(out, cborBytes(protected)...)
	out = append(out, unprotected...)
	out = append(out, payload...)
	out = append(out, cborBytes(signature)...)
	return out
}

func cborBytes(b []byte) []byte {
	out := mustEncNoT(b)
	return out
}

func mustEncNoT(v any) []byte {
	mode, _ := fxcbor.CoreDetEncOptions().EncMode()
	out, _ := mode.Marshal(v)
	return out
}

func validEnvelope(t testing.TB) []byte {
	t.Helper()
	return envelope(validProtected(t), []byte{0xa0}, cborBytes(validPayload(t)), bytes.Repeat([]byte{7}, 64))
}

func wantReason(t *testing.T, raw []byte, want decisiontrust.VerificationReason) {
	t.Helper()
	got, err := newCodec(t).DecodeSign1(raw)
	if err == nil {
		t.Fatalf("DecodeSign1() succeeded, want %s", want)
	}
	if !got.IsZero() {
		t.Fatal("DecodeSign1() returned a populated envelope with an error")
	}
	reason, ok := decisiontrust.FailureReason(err)
	if !ok {
		t.Fatalf("error %v does not carry a VerificationFailure", err)
	}
	if reason != want {
		t.Fatalf("reason = %s, want %s (err=%v)", reason, want, err)
	}
}

func TestValidEnvelopeDecodes(t *testing.T) {
	got, err := newCodec(t).DecodeSign1(validEnvelope(t))
	if err != nil {
		t.Fatalf("DecodeSign1() error = %v", err)
	}
	if got.Algorithm() != decisiontrust.AlgorithmEdDSA || got.KeyID() != "kid-1" {
		t.Fatalf("unexpected envelope: alg=%s kid=%s", got.Algorithm(), got.KeyID())
	}
}

func TestEmptyAndOversizedInput(t *testing.T) {
	wantReason(t, nil, decisiontrust.ReasonMalformedCOSE)
	wantReason(t, []byte{}, decisiontrust.ReasonMalformedCOSE)
	wantReason(t, bytes.Repeat([]byte{0xd2}, decisiontrust.MaxCOSESign1Bytes+1), decisiontrust.ReasonMalformedCOSE)
}

func TestTruncatedInputIsRejectedAtEveryLength(t *testing.T) {
	raw := validEnvelope(t)
	for n := 0; n < len(raw); n++ {
		if _, err := newCodec(t).DecodeSign1(raw[:n]); err == nil {
			t.Fatalf("truncated input of %d bytes was accepted", n)
		}
	}
}

func TestEverySingleByteMutationIsContained(t *testing.T) {
	// A mutated input may be rejected or, rarely, still be a valid envelope
	// (for example a changed signature byte). It must never panic, and any
	// error must be a normalized VerificationFailure.
	raw := validEnvelope(t)
	codec := newCodec(t)
	for i := range raw {
		for _, delta := range []byte{0x01, 0x80, 0xff} {
			mutated := bytes.Clone(raw)
			mutated[i] ^= delta
			got, err := codec.DecodeSign1(mutated)
			if err != nil {
				if _, ok := decisiontrust.FailureReason(err); !ok {
					t.Fatalf("byte %d ^%#x: error %v is not a VerificationFailure", i, delta, err)
				}
				if !got.IsZero() {
					t.Fatalf("byte %d ^%#x: populated envelope with error", i, delta)
				}
			}
		}
	}
}

func TestNonShortestTagIsRejected(t *testing.T) {
	// Tag 18 encoded as 0xd8 0x12 is valid CBOR but not the shortest form.
	raw := validEnvelope(t)
	raw = append([]byte{0xd8, 0x12}, raw[1:]...)
	wantReason(t, raw, decisiontrust.ReasonMalformedCOSE)
}

func TestPayloadMustBeAByteString(t *testing.T) {
	text := mustEnc(t, "not a byte string")
	wantReason(t, envelope(validProtected(t), []byte{0xa0}, text, bytes.Repeat([]byte{7}, 64)), decisiontrust.ReasonMalformedCOSE)
}

func TestEmptyAttachedPayloadIsNotAMap(t *testing.T) {
	// An attached empty bstr is not detached, but it is not a CBOR map either.
	wantReason(t, envelope(validProtected(t), []byte{0xa0}, []byte{0x40}, bytes.Repeat([]byte{7}, 64)), decisiontrust.ReasonMalformedCOSE)
}

func TestSignatureMustBeExactly64Bytes(t *testing.T) {
	for _, n := range []int{0, 1, 63, 65, 128} {
		raw := envelope(validProtected(t), []byte{0xa0}, cborBytes(validPayload(t)), bytes.Repeat([]byte{7}, n))
		wantReason(t, raw, decisiontrust.ReasonMalformedCOSE)
	}
}

func TestPayloadMustBeAMap(t *testing.T) {
	for name, payload := range map[string][]byte{
		"array":    mustEnc(t, []any{1, 2}),
		"integer":  mustEnc(t, 7),
		"text":     mustEnc(t, "x"),
		"bool":     {0xf5},
		"null":     {0xf6},
		"tagged":   {0xc1, 0x01},
		"one byte": {0x00},
	} {
		raw := envelope(validProtected(t), []byte{0xa0}, cborBytes(payload), bytes.Repeat([]byte{7}, 64))
		if _, err := newCodec(t).DecodeSign1(raw); err == nil {
			t.Errorf("%s payload was accepted", name)
		}
	}
}

func TestPayloadStrictnessReasons(t *testing.T) {
	sig := bytes.Repeat([]byte{7}, 64)
	cases := []struct {
		name    string
		payload []byte
		want    decisiontrust.VerificationReason
	}{
		// {1:1, 1:2}: duplicate key.
		{"duplicate key", []byte{0xa2, 0x01, 0x01, 0x01, 0x02}, decisiontrust.ReasonPayloadNotDeterministic},
		// {_ 1:1}: indefinite-length map.
		{"indefinite map", []byte{0xbf, 0x01, 0x01, 0xff}, decisiontrust.ReasonPayloadNotDeterministic},
		// {1: 1_(non-shortest)} uses a two-byte integer for 1.
		{"non-shortest integer", []byte{0xa1, 0x01, 0x18, 0x01}, decisiontrust.ReasonPayloadNotDeterministic},
		// map(1) with a missing value: not well-formed.
		{"truncated map", []byte{0xa1, 0x01}, decisiontrust.ReasonMalformedCOSE},
		// map(1) whose value is a tag.
		{"tag in value", []byte{0xa1, 0x01, 0xc1, 0x01}, decisiontrust.ReasonPayloadNotDeterministic},
		// Trailing bytes after one map.
		{"trailing data", []byte{0xa1, 0x01, 0x01, 0x00}, decisiontrust.ReasonMalformedCOSE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantReason(t, envelope(validProtected(t), []byte{0xa0}, cborBytes(tc.payload), sig), tc.want)
		})
	}
}

func TestPayloadResourceLimits(t *testing.T) {
	sig := bytes.Repeat([]byte{7}, 64)

	// Nesting beyond the hard limit: [[[[[[[[[[0]]]]]]]]]] as a map value.
	deep := any(uint64(0))
	for i := 0; i < maxNestedLevels+2; i++ {
		deep = []any{deep}
	}
	wantAny := func(name string, payload any) {
		t.Helper()
		raw := envelope(validProtected(t), []byte{0xa0}, cborBytes(mustEnc(t, payload)), sig)
		if _, err := newCodec(t).DecodeSign1(raw); err == nil {
			t.Errorf("%s: input over a resource limit was accepted", name)
		}
	}
	wantAny("deep nesting", map[uint64]any{1: deep})

	wide := make([]any, maxArrayElements+1)
	wantAny("wide array", map[uint64]any{1: wide})

	many := map[uint64]any{}
	for i := uint64(0); i <= maxMapPairs; i++ {
		many[i] = i
	}
	wantAny("many map pairs", many)
}

func TestProtectedHeaderStrictness(t *testing.T) {
	sig := bytes.Repeat([]byte{7}, 64)
	build := func(h protectedHeader) []byte {
		return envelope(mustEnc(t, h), []byte{0xa0}, cborBytes(validPayload(t)), sig)
	}
	base := func() protectedHeader {
		return protectedHeader{
			Algorithm: ptr(int64(-8)),
			Critical:  ptr([]int64{decisiontrust.COSEHeaderLabelEKIProfile}),
			KeyID:     ptr([]byte("kid-1")),
			Profile:   ptr(decisiontrust.SignedDecisionProfileV1),
		}
	}

	t.Run("missing each label", func(t *testing.T) {
		for name, mutate := range map[string]func(*protectedHeader){
			"alg":     func(h *protectedHeader) { h.Algorithm = nil },
			"crit":    func(h *protectedHeader) { h.Critical = nil },
			"kid":     func(h *protectedHeader) { h.KeyID = nil },
			"profile": func(h *protectedHeader) { h.Profile = nil },
		} {
			h := base()
			mutate(&h)
			t.Run(name, func(t *testing.T) {
				wantReason(t, build(h), decisiontrust.ReasonProtectedHeaderInvalid)
			})
		}
	})

	t.Run("crit with extra label", func(t *testing.T) {
		h := base()
		h.Critical = ptr([]int64{decisiontrust.COSEHeaderLabelEKIProfile, 1})
		wantReason(t, build(h), decisiontrust.ReasonProtectedHeaderInvalid)
	})
	t.Run("crit empty", func(t *testing.T) {
		h := base()
		h.Critical = ptr([]int64{})
		wantReason(t, build(h), decisiontrust.ReasonProtectedHeaderInvalid)
	})
	t.Run("alg es256 is allowed", func(t *testing.T) {
		h := base()
		h.Algorithm = ptr(int64(-7))
		got, err := newCodec(t).DecodeSign1(build(h))
		if err != nil {
			t.Fatalf("DecodeSign1() error = %v", err)
		}
		if got.Algorithm() != decisiontrust.AlgorithmES256 {
			t.Fatalf("Algorithm() = %s", got.Algorithm())
		}
	})
	t.Run("alg none", func(t *testing.T) {
		h := base()
		h.Algorithm = ptr(int64(0))
		wantReason(t, build(h), decisiontrust.ReasonAlgorithmNotAllowed)
	})
	t.Run("kid at the bound", func(t *testing.T) {
		h := base()
		h.KeyID = ptr(bytes.Repeat([]byte("k"), decisiontrust.MaxKeyIDBytes))
		if _, err := newCodec(t).DecodeSign1(build(h)); err != nil {
			t.Fatalf("DecodeSign1() error = %v", err)
		}
	})
	t.Run("kid over the bound", func(t *testing.T) {
		h := base()
		h.KeyID = ptr(bytes.Repeat([]byte("k"), decisiontrust.MaxKeyIDBytes+1))
		wantReason(t, build(h), decisiontrust.ReasonProtectedHeaderInvalid)
	})
	t.Run("kid invalid utf-8", func(t *testing.T) {
		h := base()
		h.KeyID = ptr([]byte{0xff, 0xfe})
		wantReason(t, build(h), decisiontrust.ReasonProtectedHeaderInvalid)
	})
	t.Run("empty protected", func(t *testing.T) {
		raw := envelope(nil, []byte{0xa0}, cborBytes(validPayload(t)), sig)
		wantReason(t, raw, decisiontrust.ReasonProtectedHeaderInvalid)
	})
	t.Run("protected over the bound", func(t *testing.T) {
		raw := envelope(bytes.Repeat([]byte{0xa0}, decisiontrust.MaxProtectedHeaderBytes+1), []byte{0xa0}, cborBytes(validPayload(t)), sig)
		wantReason(t, raw, decisiontrust.ReasonProtectedHeaderInvalid)
	})
	t.Run("protected is not a map", func(t *testing.T) {
		raw := envelope(mustEnc(t, []any{1, 2}), []byte{0xa0}, cborBytes(validPayload(t)), sig)
		wantReason(t, raw, decisiontrust.ReasonProtectedHeaderInvalid)
	})
	t.Run("trailing byte after protected map", func(t *testing.T) {
		raw := envelope(append(validProtected(t), 0x00), []byte{0xa0}, cborBytes(validPayload(t)), sig)
		wantReason(t, raw, decisiontrust.ReasonProtectedHeaderInvalid)
	})
	t.Run("tag inside protected header", func(t *testing.T) {
		// {1: tag(1, -8)}
		protected := []byte{0xa1, 0x01, 0xc1, 0x27}
		raw := envelope(protected, []byte{0xa0}, cborBytes(validPayload(t)), sig)
		wantReason(t, raw, decisiontrust.ReasonProtectedHeaderInvalid)
	})
}

func TestUnprotectedHeaderMustBeADefiniteEmptyMap(t *testing.T) {
	sig := bytes.Repeat([]byte{7}, 64)
	for name, unprotected := range map[string][]byte{
		"one entry":       {0xa1, 0x04, 0x41, 0x6b},
		"indefinite":      {0xbf, 0xff},
		"two-byte length": {0xb8, 0x00},
		"array":           {0x80},
		"null":            {0xf6},
	} {
		raw := envelope(validProtected(t), unprotected, cborBytes(validPayload(t)), sig)
		if _, err := newCodec(t).DecodeSign1(raw); err == nil {
			t.Errorf("%s: unprotected header was accepted", name)
		}
	}
}

func TestOuterArrayArity(t *testing.T) {
	sig := bytes.Repeat([]byte{7}, 64)
	three := append([]byte{0xd2, 0x83}, cborBytes(validProtected(t))...)
	three = append(three, 0xa0)
	three = append(three, cborBytes(validPayload(t))...)
	wantReason(t, three, decisiontrust.ReasonMalformedCOSE)

	five := envelope(validProtected(t), []byte{0xa0}, cborBytes(validPayload(t)), sig)
	five[1] = 0x85
	five = append(five, 0x00)
	wantReason(t, five, decisiontrust.ReasonMalformedCOSE)
}

func TestSignature1Structure(t *testing.T) {
	codec := newCodec(t)
	protected := validProtected(t)
	payload := validPayload(t)

	got, err := codec.EncodeSignature1(protected, payload)
	if err != nil {
		t.Fatalf("EncodeSignature1() error = %v", err)
	}

	// array(4), text(10) "Signature1", then bstr protected, bstr empty, bstr payload.
	want := []byte{0x84, 0x6a}
	want = append(want, []byte(signature1Context)...)
	want = append(want, cborBytes(protected)...)
	want = append(want, 0x40)
	want = append(want, cborBytes(payload)...)
	if !bytes.Equal(got, want) {
		t.Fatalf("Sig_structure mismatch:\n got: %x\nwant: %x", got, want)
	}

	// The bytes depend on the exact protected header and payload.
	other, err := codec.EncodeSignature1(protected, append(bytes.Clone(payload), 0))
	if err != nil {
		t.Fatalf("EncodeSignature1() error = %v", err)
	}
	if bytes.Equal(got, other) {
		t.Fatal("different payloads produced the same Sig_structure")
	}
}

func TestSignature1RejectsInvalidInputs(t *testing.T) {
	codec := newCodec(t)
	protected := validProtected(t)
	payload := validPayload(t)

	if _, err := codec.EncodeSignature1(nil, payload); err == nil {
		t.Error("empty protected header was accepted")
	}
	if _, err := codec.EncodeSignature1(bytes.Repeat([]byte{1}, decisiontrust.MaxProtectedHeaderBytes+1), payload); err == nil {
		t.Error("oversized protected header was accepted")
	}
	if _, err := codec.EncodeSignature1(protected, nil); err == nil {
		t.Error("detached (nil) payload was accepted")
	}
	if _, err := codec.EncodeSignature1(protected, []byte{}); err != nil {
		t.Errorf("attached empty payload error = %v, want accepted", err)
	}
	if _, err := codec.EncodeSignature1(protected, make([]byte, decisiontrust.MaxCOSESign1Bytes+1)); err == nil {
		t.Error("oversized payload was accepted")
	}
}

func TestSignedEnvelopeRoundTripVerifies(t *testing.T) {
	// Sign what EncodeSignature1 produces, decode the envelope, and verify the
	// signature over the Sig_structure rebuilt from the decoded parts. This
	// ties decoding and the Signature1 encoder together end to end.
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	codec := newCodec(t)
	protected := validProtected(t)
	payload := validPayload(t)

	sigStructure, err := codec.EncodeSignature1(protected, payload)
	if err != nil {
		t.Fatalf("EncodeSignature1() error = %v", err)
	}
	signature := ed25519.Sign(priv, sigStructure)

	decoded, err := codec.DecodeSign1(envelope(protected, []byte{0xa0}, cborBytes(payload), signature))
	if err != nil {
		t.Fatalf("DecodeSign1() error = %v", err)
	}
	rebuilt, err := codec.EncodeSignature1(decoded.ProtectedHeader(), decoded.Payload())
	if err != nil {
		t.Fatalf("EncodeSignature1() error = %v", err)
	}
	if !ed25519.Verify(pub, rebuilt, decoded.Signature()) {
		t.Fatal("signature did not verify over the rebuilt Sig_structure")
	}

	// Changing one payload byte must break verification.
	tampered := bytes.Clone(decoded.Payload())
	tampered[len(tampered)-1] ^= 1
	forged, err := codec.EncodeSignature1(decoded.ProtectedHeader(), tampered)
	if err != nil {
		t.Fatalf("EncodeSignature1() error = %v", err)
	}
	if ed25519.Verify(pub, forged, decoded.Signature()) {
		t.Fatal("signature verified over a tampered payload")
	}
}

func TestNilCodecFailsClosed(t *testing.T) {
	var codec *Codec
	if _, err := codec.DecodeSign1(validEnvelope(t)); err == nil {
		t.Fatal("nil codec decoded an envelope")
	}
	if _, err := codec.EncodeSignature1([]byte{1}, []byte{2}); err == nil {
		t.Fatal("nil codec encoded a Sig_structure")
	}
}

func TestErrorsDoNotLeakLibraryTypesAsContract(t *testing.T) {
	_, err := newCodec(t).DecodeSign1([]byte{0xd2, 0xff})
	if err == nil {
		t.Fatal("malformed input was accepted")
	}
	var failure *decisiontrust.VerificationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("error %T is not a *VerificationFailure", err)
	}
	if strings.TrimSpace(failure.Error()) == "" {
		t.Fatal("failure has an empty message")
	}
}

// FuzzDecodeSign1 feeds arbitrary bytes to the decoder. Whatever the input,
// the decoder must not panic, must return only normalized failures, and any
// envelope it accepts must be usable to build a Sig_structure.
func FuzzDecodeSign1(f *testing.F) {
	codec, err := New()
	if err != nil {
		f.Fatalf("New() error = %v", err)
	}
	f.Add([]byte{})
	f.Add([]byte{0xd2})
	f.Add([]byte{0xd2, 0x84, 0x40, 0xa0, 0x40, 0x40})
	f.Add(validEnvelope(f))

	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := codec.DecodeSign1(raw)
		if err != nil {
			if !got.IsZero() {
				t.Fatal("populated envelope returned with an error")
			}
			if _, ok := decisiontrust.FailureReason(err); !ok {
				t.Fatalf("error %v is not a VerificationFailure", err)
			}
			return
		}
		if got.IsZero() {
			t.Fatal("zero envelope returned without an error")
		}
		if _, err := codec.EncodeSignature1(got.ProtectedHeader(), got.Payload()); err != nil {
			t.Fatalf("accepted envelope could not build a Sig_structure: %v", err)
		}
	})
}
