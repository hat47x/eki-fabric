package grant

import (
	"encoding/binary"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// grantDomain keeps a grant digest from ever encoding to the same bytes as a
// digest of some other object.
const grantDomain = "eki.grant.compute-grant.v1"

// Digest returns the grant's identity: a SHA-256 digest over the fields fixed
// at issuance. Revocation is not part of it, so revoking a grant does not
// change its identity. A zero Grant has a zero digest.
func (g Grant) Digest() contract.Digest {
	if !g.issued {
		return contract.Digest{}
	}
	return contract.SHA256(g.canonical())
}

// canonical returns the bytes that Digest hashes. Fields are written in a fixed
// order. Strings carry a 32-bit length prefix, and integers are big-endian. The
// encoding uses no maps, and the ceilings are already sorted by class, so equal
// grants always yield equal bytes.
func (g Grant) canonical() []byte {
	b := appendString(nil, grantDomain)
	b = appendString(b, string(g.provider))
	b = appendString(b, string(g.beneficiary))
	b = binary.BigEndian.AppendUint32(b, uint32(len(g.ceilings)))
	for _, c := range g.ceilings {
		b = appendString(b, string(c.Class))
		b = binary.BigEndian.AppendUint32(b, uint32(c.Percent))
	}
	b = appendTime(b, g.issuedAt)
	b = appendTime(b, g.expiresAt)
	b = binary.BigEndian.AppendUint32(b, uint32(g.maxLeases))
	b = appendString(b, string(g.purpose))
	return appendString(b, g.policy.String())
}

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

// appendTime writes seconds and nanoseconds since the Unix epoch, so every
// representable instant encodes without overflow.
func appendTime(b []byte, t time.Time) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(t.Unix()))
	return binary.BigEndian.AppendUint32(b, uint32(t.Nanosecond()))
}
