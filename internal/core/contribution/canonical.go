package contribution

import (
	"encoding/binary"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// Domain tags keep a record digest and a correction digest from ever encoding
// to the same bytes.
const (
	recordDomain     = "eki.contribution.record"
	correctionDomain = "eki.contribution.correction"
)

// canonical returns the bytes that Record.Digest hashes. Fields are written in
// a fixed order. Strings and lists carry a 32-bit length prefix, and integers
// are big-endian. The encoding uses no maps, so equal fields always yield equal
// bytes.
func (r Record) canonical() []byte {
	b := appendString(nil, recordDomain)
	b = binary.BigEndian.AppendUint32(b, r.schemaVersion)
	b = appendString(b, r.providerID)
	b = appendString(b, r.resourceClass)
	b = appendString(b, string(r.metric))
	b = appendString(b, string(r.quantity.Unit))
	b = binary.BigEndian.AppendUint64(b, uint64(r.quantity.Value))
	b = binary.BigEndian.AppendUint32(b, uint32(len(r.observers)))
	for _, o := range r.observers {
		b = appendString(b, string(o.Kind))
		b = appendString(b, o.ID)
	}
	b = appendString(b, string(r.assurance))
	b = appendDigest(b, r.taskDigest)
	b = appendDigest(b, r.resultDigest)
	b = appendString(b, r.provenance)
	b = appendTime(b, r.windowStart)
	return appendTime(b, r.windowEnd)
}

// canonical returns the bytes that Correction.Digest hashes, using the same
// rules as Record.canonical.
func (c Correction) canonical() []byte {
	b := appendString(nil, correctionDomain)
	b = appendString(b, string(c.kind))
	b = appendDigest(b, c.target)
	b = appendDigest(b, c.successor)
	b = appendDigest(b, c.taskDigest)
	return appendDigest(b, c.resultDigest)
}

func appendString(b []byte, s string) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(s)))
	return append(b, s...)
}

func appendDigest(b []byte, d contract.Digest) []byte {
	return appendString(b, d.String())
}

// appendTime writes seconds and nanoseconds since the Unix epoch, not a single
// nanosecond count, so that every representable time encodes without overflow.
func appendTime(b []byte, t time.Time) []byte {
	b = binary.BigEndian.AppendUint64(b, uint64(t.Unix()))
	return binary.BigEndian.AppendUint32(b, uint32(t.Nanosecond()))
}
