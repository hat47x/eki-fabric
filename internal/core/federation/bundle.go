package federation

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// maxBundleKeys bounds the number of key identities in one bundle.
	maxBundleKeys = 64
	// maxKeyIDBytes bounds the length of one key ID.
	maxKeyIDBytes = 128

	// bundleDigestTag separates bundle digests from every other hashed encoding.
	bundleDigestTag = "eki-federation-bundle-digest/v1"
	// bundleAuthorityTag separates authority signatures over bundles from identity signatures.
	bundleAuthorityTag = "eki-federation-bundle-authority/v1"
)

// BundleKey is one key identity in a trust bundle: an ID that identities name,
// and the Ed25519 public key that the ID stands for.
type BundleKey struct {
	ID        string
	PublicKey ed25519.PublicKey
}

// Digest is the SHA-256 digest of a bundle's canonical encoding.
type Digest [sha256.Size]byte

// String returns the digest as lowercase hexadecimal.
func (d Digest) String() string {
	return hex.EncodeToString(d[:])
}

// Bundle is the set of keys that may vouch for identities of one trust domain at
// one sequence number. Its digest covers the domain, the sequence, and the keys,
// and it does not cover the validated-at time. The validated-at time records when
// the caller accepted the bundle, so re-accepting the same content later does not
// change its identity.
type Bundle struct {
	domain      TrustDomain
	sequence    uint64
	validatedAt time.Time
	keys        []BundleKey
	digest      Digest
}

// NewBundle returns the bundle for domain at sequence. The sequence must be at
// least 1 and must increase with each new bundle of the domain. The keys are
// copied and sorted by ID. A bundle holds 1 to 64 keys, every key ID is unique,
// and every public key is distinct and exactly 32 bytes. The caller supplies
// validatedAt, and the package never reads a clock.
func NewBundle(domain TrustDomain, sequence uint64, validatedAt time.Time, keys []BundleKey) (Bundle, error) {
	b := Bundle{
		domain:      domain,
		sequence:    sequence,
		validatedAt: validatedAt.Round(0).UTC(),
		keys:        cloneBundleKeys(keys),
	}
	slices.SortFunc(b.keys, func(x, y BundleKey) int {
		return strings.Compare(x.ID, y.ID)
	})
	if err := b.checkContent(); err != nil {
		return Bundle{}, err
	}
	b.digest = b.computeDigest()
	return b, nil
}

// Domain returns the trust domain this bundle belongs to.
func (b Bundle) Domain() TrustDomain {
	return b.domain
}

// Sequence returns the bundle's sequence number.
func (b Bundle) Sequence() uint64 {
	return b.sequence
}

// ValidatedAt returns the time at which the caller accepted the bundle.
func (b Bundle) ValidatedAt() time.Time {
	return b.validatedAt
}

// Digest returns the digest of the bundle's canonical encoding.
func (b Bundle) Digest() Digest {
	return b.digest
}

// Keys returns a copy of the bundle's keys, sorted by ID.
func (b Bundle) Keys() []BundleKey {
	return cloneBundleKeys(b.keys)
}

// lookupKey returns the key with the given ID. Callers must not modify the result.
func (b Bundle) lookupKey(id string) (BundleKey, bool) {
	for _, k := range b.keys {
		if k.ID == id {
			return k, true
		}
	}
	return BundleKey{}, false
}

// checkContent validates everything except the digest. It also covers zero
// values, because a zero Bundle has no domain.
func (b Bundle) checkContent() error {
	if !b.domain.Valid() {
		return fmt.Errorf("%w: trust domain is not set", ErrInvalidBundle)
	}
	if b.sequence == 0 {
		return fmt.Errorf("%w: sequence must be at least 1", ErrInvalidBundle)
	}
	if b.validatedAt.IsZero() {
		return fmt.Errorf("%w: validated-at time is required", ErrInvalidBundle)
	}
	if len(b.keys) == 0 || len(b.keys) > maxBundleKeys {
		return fmt.Errorf("%w: a bundle holds 1..%d keys, got %d", ErrInvalidBundle, maxBundleKeys, len(b.keys))
	}
	ids := make(map[string]struct{}, len(b.keys))
	publics := make(map[string]struct{}, len(b.keys))
	for _, k := range b.keys {
		if !isASCIIToken(k.ID, maxKeyIDBytes) {
			return fmt.Errorf("%w: key ID must be 1..%d printable ASCII bytes with no spaces", ErrInvalidBundle, maxKeyIDBytes)
		}
		if len(k.PublicKey) != ed25519.PublicKeySize {
			return fmt.Errorf("%w: key %q is not a %d-byte Ed25519 public key", ErrInvalidBundle, k.ID, ed25519.PublicKeySize)
		}
		if _, dup := ids[k.ID]; dup {
			return fmt.Errorf("%w: key ID %q appears twice", ErrInvalidBundle, k.ID)
		}
		ids[k.ID] = struct{}{}
		if _, dup := publics[string(k.PublicKey)]; dup {
			return fmt.Errorf("%w: key %q repeats the public key of another key", ErrInvalidBundle, k.ID)
		}
		publics[string(k.PublicKey)] = struct{}{}
	}
	return nil
}

// validate re-checks a bundle that did not come from NewBundle, such as a zero
// value or a value whose digest no longer matches its content.
func (b Bundle) validate() error {
	if err := b.checkContent(); err != nil {
		return err
	}
	if b.digest != b.computeDigest() {
		return fmt.Errorf("%w: digest does not match the content", ErrInvalidBundle)
	}
	return nil
}

func (b Bundle) computeDigest() Digest {
	return sha256.Sum256(bundleEncoding(b.domain.String(), b.sequence, b.keys))
}

// bundleEncoding is the canonical byte encoding hashed into a bundle digest. Keys
// must already be sorted by ID. Every field is length-prefixed, and the sequence
// and key count are fixed-width integers, so the encoding is unambiguous.
func bundleEncoding(domain string, sequence uint64, keys []BundleKey) []byte {
	buf := appendField(nil, []byte(bundleDigestTag))
	buf = appendField(buf, []byte(domain))
	buf = binary.BigEndian.AppendUint64(buf, sequence)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(keys)))
	for _, k := range keys {
		buf = appendField(buf, []byte(k.ID))
		buf = appendField(buf, k.PublicKey)
	}
	return buf
}

// BundleSigningInput returns the bytes that the bundle authority signs with
// Ed25519 to vouch for b. The input is a domain-separated tag followed by the
// bundle digest. It is an error if b is not a valid bundle.
func BundleSigningInput(b Bundle) ([]byte, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	buf := appendField(nil, []byte(bundleAuthorityTag))
	return append(buf, b.digest[:]...), nil
}

func cloneBundleKeys(keys []BundleKey) []BundleKey {
	out := make([]BundleKey, len(keys))
	for i, k := range keys {
		out[i] = BundleKey{ID: k.ID, PublicKey: slices.Clone(k.PublicKey)}
	}
	return out
}
