package federation

import (
	"encoding/hex"
	"errors"
	"math/big"
	"testing"
)

// The reference arithmetic below uses math/big and does not share code with the
// production check. It re-derives the torsion subgroup, so the production table
// is not trusted on its own.

var (
	refP = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	// d = -121665 / 121666 mod p, the Edwards curve constant.
	refCurveD = new(big.Int).Mod(new(big.Int).Mul(big.NewInt(-121665), new(big.Int).ModInverse(big.NewInt(121666), refP)), refP)
	// sqrt(-1) = 2^((p-1)/4) mod p.
	refSqrtM1 = new(big.Int).Exp(big.NewInt(2), new(big.Int).Rsh(new(big.Int).Sub(refP, big.NewInt(1)), 2), refP)
	// (p+3)/8, the exponent for a square root when p = 5 mod 8.
	refSqrtExp = new(big.Int).Rsh(new(big.Int).Add(refP, big.NewInt(3)), 3)
	// The prime order of the base point: 2^252 + 27742317777372353535851937790883648493.
	refOrderL = new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 252),
		mustBigDecimal("27742317777372353535851937790883648493"))
)

func mustBigDecimal(s string) *big.Int {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad decimal constant")
	}
	return v
}

type refPoint struct{ x, y *big.Int }

func refIdentity() refPoint { return refPoint{x: big.NewInt(0), y: big.NewInt(1)} }

func refReduce(v *big.Int) *big.Int { return new(big.Int).Mod(v, refP) }

// refAdd is the affine addition law of the twisted Edwards curve with a = -1.
func refAdd(a, b refPoint) refPoint {
	x1x2 := new(big.Int).Mul(a.x, b.x)
	y1y2 := new(big.Int).Mul(a.y, b.y)
	t := refReduce(new(big.Int).Mul(refCurveD, new(big.Int).Mul(x1x2, y1y2)))
	nx := refReduce(new(big.Int).Add(new(big.Int).Mul(a.x, b.y), new(big.Int).Mul(a.y, b.x)))
	ny := refReduce(new(big.Int).Add(y1y2, x1x2))
	dx := refReduce(new(big.Int).Add(big.NewInt(1), t))
	dy := refReduce(new(big.Int).Sub(big.NewInt(1), t))
	return refPoint{
		x: refReduce(new(big.Int).Mul(nx, new(big.Int).ModInverse(dx, refP))),
		y: refReduce(new(big.Int).Mul(ny, new(big.Int).ModInverse(dy, refP))),
	}
}

// refMul returns k times p by double-and-add.
func refMul(k *big.Int, p refPoint) refPoint {
	r := refIdentity()
	for i := k.BitLen() - 1; i >= 0; i-- {
		r = refAdd(r, r)
		if k.Bit(i) == 1 {
			r = refAdd(r, p)
		}
	}
	return r
}

func refIsIdentity(p refPoint) bool {
	return p.x.Sign() == 0 && p.y.Cmp(big.NewInt(1)) == 0
}

// refDecode returns the point that enc encodes. It reports false unless enc is
// the canonical encoding of a point on the curve.
func refDecode(enc []byte) (refPoint, bool) {
	if len(enc) != 32 {
		return refPoint{}, false
	}
	le := append([]byte(nil), enc...)
	sign := le[31] >> 7
	le[31] &= 0x7f
	y := refFromLE(le)
	if y.Cmp(refP) >= 0 {
		return refPoint{}, false
	}
	y2 := refReduce(new(big.Int).Mul(y, y))
	u := refReduce(new(big.Int).Sub(y2, big.NewInt(1)))
	v := refReduce(new(big.Int).Add(new(big.Int).Mul(refCurveD, y2), big.NewInt(1)))
	x2 := refReduce(new(big.Int).Mul(u, new(big.Int).ModInverse(v, refP)))
	if x2.Sign() == 0 {
		if sign == 1 {
			return refPoint{}, false
		}
		return refPoint{x: big.NewInt(0), y: y}, true
	}
	x := new(big.Int).Exp(x2, refSqrtExp, refP)
	if refReduce(new(big.Int).Mul(x, x)).Cmp(x2) != 0 {
		x = refReduce(new(big.Int).Mul(x, refSqrtM1))
		if refReduce(new(big.Int).Mul(x, x)).Cmp(x2) != 0 {
			return refPoint{}, false
		}
	}
	if x.Bit(0) != uint(sign) {
		x = refReduce(new(big.Int).Neg(x))
	}
	return refPoint{x: x, y: y}, true
}

// refEncode returns the canonical 32-byte encoding of p.
func refEncode(p refPoint) []byte {
	out := refToLE(p.y)
	out[31] |= byte(p.x.Bit(0)) << 7
	return out
}

func refToLE(v *big.Int) []byte {
	be := v.FillBytes(make([]byte, 32))
	le := make([]byte, 32)
	for i := range be {
		le[i] = be[len(be)-1-i]
	}
	return le
}

func refFromLE(le []byte) *big.Int {
	be := make([]byte, len(le))
	for i := range le {
		be[i] = le[len(le)-1-i]
	}
	return new(big.Int).SetBytes(be)
}

// TestSmallOrderTableIsTheWholeTorsionSubgroup checks the production table
// against independent arithmetic. Every table entry must have order dividing 8,
// and the entries must be the complete set of such points. The complete set is
// found by taking [l]Q for curve points Q. That product is always a point of
// order dividing 8, and across many Q it covers the whole subgroup.
func TestSmallOrderTableIsTheWholeTorsionSubgroup(t *testing.T) {
	eight := big.NewInt(8)
	table := make(map[string]bool, len(smallOrderKeyHex))
	for _, s := range smallOrderKeyHex {
		enc, err := hex.DecodeString(s)
		if err != nil {
			t.Fatalf("table entry %s: %v", s, err)
		}
		pt, ok := refDecode(enc)
		if !ok {
			t.Fatalf("table entry %s is not a canonical curve point", s)
		}
		if !refIsIdentity(refMul(eight, pt)) {
			t.Errorf("table entry %s does not have order dividing 8", s)
		}
		table[s] = true
	}
	if len(table) != len(smallOrderKeyHex) {
		t.Fatalf("table has %d distinct entries, want %d", len(table), len(smallOrderKeyHex))
	}

	found := make(map[string]bool, 8)
	for y := int64(2); y < 2000 && len(found) < 8; y++ {
		q, ok := refDecode(refToLE(big.NewInt(y)))
		if !ok {
			continue
		}
		torsion := refMul(refOrderL, q)
		found[hex.EncodeToString(refEncode(torsion))] = true
	}
	if len(found) != 8 {
		t.Fatalf("enumeration found %d torsion points, want 8", len(found))
	}
	for s := range found {
		if !table[s] {
			t.Errorf("torsion point %s is missing from the production table", s)
		}
	}
	for s := range table {
		if !found[s] {
			t.Errorf("table entry %s is not a torsion point", s)
		}
	}
}

func TestUsableSigningKeyAcceptsRealKeys(t *testing.T) {
	for seed := byte(1); seed <= 32; seed++ {
		pub, _ := testKeyPair(t, seed)
		pt, ok := refDecode(pub)
		if !ok {
			t.Fatalf("seed %d: the derived public key is not a canonical curve point", seed)
		}
		if refIsIdentity(refMul(big.NewInt(8), pt)) {
			t.Fatalf("seed %d: a real public key has small order", seed)
		}
		if !usableSigningKey(pub) {
			t.Errorf("seed %d: a real public key was refused", seed)
		}
	}
}

func TestUsableSigningKeyRefusesSmallOrderAndNonCanonicalKeys(t *testing.T) {
	for i, s := range smallOrderKeyHex {
		key, err := hex.DecodeString(s)
		if err != nil {
			t.Fatalf("table entry %d: %v", i, err)
		}
		if usableSigningKey(key) {
			t.Errorf("small-order key %d (%s) was accepted", i, s)
		}
	}

	// y = p and y = p+1 are non-canonical. Under a decoder that reduces modulo
	// p, y = p+1 is the identity again, so refusing it closes that alias.
	atP := append([]byte(nil), fieldPrime[:]...)
	pPlusOne := append([]byte(nil), fieldPrime[:]...)
	pPlusOne[0]++
	pPlusOneSigned := append([]byte(nil), pPlusOne...)
	pPlusOneSigned[31] |= 0x80
	for name, key := range map[string][]byte{
		"y = p":             atP,
		"y = p+1":           pPlusOne,
		"y = p+1, sign set": pPlusOneSigned,
	} {
		if usableSigningKey(key) {
			t.Errorf("non-canonical key (%s) was accepted", name)
		}
	}

	for _, n := range []int{0, 31, 33} {
		if usableSigningKey(make([]byte, n)) {
			t.Errorf("a %d-byte key was accepted", n)
		}
	}
}

func TestSmallOrderKeysAreRefusedByBundleAndLink(t *testing.T) {
	identity, err := hex.DecodeString(smallOrderKeyHex[0])
	if err != nil {
		t.Fatal(err)
	}
	domain := mustDomain(t, "example.org")

	_, err = NewBundle(domain, 1, testNow, []BundleKey{{ID: "k1", PublicKey: identity}})
	if !errors.Is(err, ErrInvalidBundle) {
		t.Errorf("NewBundle with the identity key: error = %v, want ErrInvalidBundle", err)
	}

	_, err = NewLink(domain, identity, PurposeTaskSubmission)
	if !errors.Is(err, ErrInvalidLink) {
		t.Errorf("NewLink with the identity authority key: error = %v, want ErrInvalidLink", err)
	}
}
