// Package artifactsafety decides whether an uploaded archive Artifact may be
// admitted, and reads the archive under hard bounds.
//
// An Artifact comes from an untrusted party. Admit reads it through an
// io.Reader, checks the bytes against a digest the caller declares, and
// examines every entry before any content is handed on. The package never
// writes to disk, never reads the clock, starts no goroutines, and imports only
// the standard library. An accepted result lists entries by normalized name and
// measured size; nothing is extracted.
//
// Only the zip format is supported, through archive/zip. tar and tar.gz are not
// supported. The zip central directory sits at the end of the file and
// archive/zip needs random access, so the input is buffered in memory up to
// MaxCompressedBytes before any entry is examined. Peak memory is therefore
// proportional to MaxCompressedBytes. Only the stored and deflate methods are
// decoded; every other method is rejected.
//
// Ambiguous cases fail closed. Unknown entry types, creator platforms, and
// compression methods are rejections. Sizes declared in entry headers can only
// cause a rejection; acceptance depends on sizes and CRC32 values measured
// while decoding.
package artifactsafety

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
)

const (
	// streamChunkSize bounds how much decoded output one Read call can add
	// before the limits are checked again.
	streamChunkSize = 32 << 10

	// maxIdleReads is the number of consecutive Read calls that return no data
	// and no error before the input is treated as stalled.
	maxIdleReads = 100
)

// ErrInvalidLimits is wrapped by the error returned from NewPolicy.
var ErrInvalidLimits = errors.New("artifactsafety: invalid limits")

// Limits bounds the work done for one Artifact. Every field must be positive.
// NewPolicy rejects zero and negative values instead of replacing them with a
// default.
type Limits struct {
	// MaxCompressedBytes is the largest archive size in bytes. Reading stops
	// at MaxCompressedBytes+1 bytes.
	MaxCompressedBytes int64

	// MaxUncompressedBytes is the largest total of measured entry sizes.
	MaxUncompressedBytes int64

	// MaxExpansionRatio is the largest allowed ratio of decoded bytes to
	// compressed bytes consumed for a single entry, as an integer: 100 means
	// 100:1. The ratio is checked after every decoded chunk, so a long run of
	// highly compressible data can trip it even if the final ratio would be
	// lower.
	MaxExpansionRatio int64

	// MaxEntries is the largest number of central directory entries.
	MaxEntries int64

	// MaxEntryBytes is the largest measured size of one entry.
	MaxEntryBytes int64

	// MaxPathDepth is the largest number of path segments in one entry name.
	// A directory name with a trailing slash counts the same as without it.
	MaxPathDepth int64

	// MaxNameBytes is the largest length of one raw entry name in bytes.
	MaxNameBytes int64
}

// DefaultLimits returns the documented default limits:
//
//	MaxCompressedBytes   16 MiB
//	MaxUncompressedBytes 256 MiB
//	MaxExpansionRatio    100
//	MaxEntries           4096
//	MaxEntryBytes        64 MiB
//	MaxPathDepth         16
//	MaxNameBytes         255
//
// The name limit matches a common single-component file name limit, so admitted
// names fit ordinary file systems. Deployments may raise any value through
// NewPolicy.
func DefaultLimits() Limits {
	return Limits{
		MaxCompressedBytes:   16 << 20,
		MaxUncompressedBytes: 256 << 20,
		MaxExpansionRatio:    100,
		MaxEntries:           4096,
		MaxEntryBytes:        64 << 20,
		MaxPathDepth:         16,
		MaxNameBytes:         255,
	}
}

// Policy is a validated set of Limits. Create it with NewPolicy. The zero value
// is not usable, and Admit rejects it.
type Policy struct {
	limits Limits
	valid  bool
}

// NewPolicy validates the limits and returns a Policy. Any field that is zero
// or negative causes an error wrapping ErrInvalidLimits.
func NewPolicy(l Limits) (*Policy, error) {
	fields := []struct {
		name  string
		value int64
	}{
		{"MaxCompressedBytes", l.MaxCompressedBytes},
		{"MaxUncompressedBytes", l.MaxUncompressedBytes},
		{"MaxExpansionRatio", l.MaxExpansionRatio},
		{"MaxEntries", l.MaxEntries},
		{"MaxEntryBytes", l.MaxEntryBytes},
		{"MaxPathDepth", l.MaxPathDepth},
		{"MaxNameBytes", l.MaxNameBytes},
	}
	for _, f := range fields {
		if f.value <= 0 {
			return nil, fmt.Errorf("%w: %s must be positive, got %d", ErrInvalidLimits, f.name, f.value)
		}
	}
	if l.MaxCompressedBytes == math.MaxInt64 {
		return nil, fmt.Errorf("%w: MaxCompressedBytes must be below %d", ErrInvalidLimits, int64(math.MaxInt64))
	}
	return &Policy{limits: l, valid: true}, nil
}

// Digest is a SHA-256 digest of the archive bytes.
type Digest [sha256.Size]byte

// Reason is a stable machine-readable code for a rejection. Callers may match
// on these values; they do not change between releases.
type Reason string

// Rejection reasons. A rejected Decision carries exactly one of these.
const (
	ReasonNilPolicy          Reason = "nil_policy"
	ReasonInvalidPolicy      Reason = "invalid_policy"
	ReasonNilReader          Reason = "nil_reader"
	ReasonReadError          Reason = "read_error"
	ReasonCompressedTooLarge Reason = "compressed_too_large"
	ReasonDigestMismatch     Reason = "digest_mismatch"
	ReasonMalformedArchive   Reason = "malformed_archive"
	ReasonTooManyEntries     Reason = "too_many_entries"

	ReasonNameEmpty           Reason = "name_empty"
	ReasonNameTooLong         Reason = "name_too_long"
	ReasonNameInvalidUTF8     Reason = "name_invalid_utf8"
	ReasonNameNUL             Reason = "name_nul"
	ReasonNameControlChar     Reason = "name_control_char"
	ReasonNameBackslash       Reason = "name_backslash"
	ReasonNameAbsolute        Reason = "name_absolute"
	ReasonNameDriveLetter     Reason = "name_drive_letter"
	ReasonNameForbiddenChar   Reason = "name_forbidden_char"
	ReasonNameEmptySegment    Reason = "name_empty_segment"
	ReasonNameDotSegment      Reason = "name_dot_segment"
	ReasonNameDotDotSegment   Reason = "name_dotdot_segment"
	ReasonNameDepth           Reason = "name_depth_exceeded"
	ReasonNameWindowsReserved Reason = "name_windows_reserved"
	ReasonNameWindowsTrailing Reason = "name_windows_trailing_dot_or_space"
	ReasonNameOutsideRoot     Reason = "name_outside_root"
	ReasonDuplicateName       Reason = "duplicate_name"
	ReasonNameExtraMismatch   Reason = "name_extra_mismatch"
	ReasonExtraMalformed      Reason = "extra_field_malformed"
	ReasonCompressedTotal     Reason = "compressed_total_exceeded"
	ReasonPathConflict        Reason = "path_conflict"

	ReasonLinkEntry         Reason = "entry_link"
	ReasonSpecialEntry      Reason = "entry_special"
	ReasonUnknownEntryType  Reason = "entry_unknown_type"
	ReasonEncrypted         Reason = "entry_encrypted"
	ReasonUnsupportedMethod Reason = "entry_unsupported_method"
	ReasonUnsupportedFlags  Reason = "entry_unsupported_flags"
	ReasonDirectoryWithData Reason = "directory_with_data"

	ReasonEntryTooLarge  Reason = "entry_too_large"
	ReasonTotalTooLarge  Reason = "total_too_large"
	ReasonExpansionRatio Reason = "expansion_ratio_exceeded"
	ReasonEntryCorrupt   Reason = "entry_corrupt"

	ReasonInternal Reason = "internal_error"
)

// Entry describes one admitted entry. Sizes are measured while decoding and are
// not taken from the archive headers.
type Entry struct {
	// Index is the position of the entry in the central directory.
	Index int
	// Name is the normalized name: no leading slash, no trailing slash, and
	// no "." or ".." segments. The case is the one the archive used.
	Name string
	// Directory reports whether the entry is a directory. A directory has no
	// content.
	Directory bool
	// Size is the number of decoded bytes.
	Size int64
	// CompressedSize is the number of compressed bytes the decoder consumed.
	CompressedSize int64
}

// Totals summarizes an accepted archive. All fields are zero for a rejection.
type Totals struct {
	// EntryCount is the number of admitted entries.
	EntryCount int
	// ArchiveBytes is the number of bytes read from the input.
	ArchiveBytes int64
	// UncompressedBytes is the sum of the measured entry sizes.
	UncompressedBytes int64
	// ObservedRatio is UncompressedBytes divided by ArchiveBytes.
	ObservedRatio float64
}

// Decision is the result of Admit.
//
// An accepted Decision has Accepted set, an empty Reason, Index -1, and a
// non-nil Entries slice. A rejected Decision has Entries set to nil, so a
// partial list is never mistaken for an accepted one.
//
// ArchiveSHA256 is the digest of every byte read from the input. It is set
// whenever reading finished within the limits, including for some rejections
// that happen later. It is zero when reading did not finish.
//
// NotGuaranteed lists properties that an accepted archive does not establish.
// A matching digest shows that the bytes are the ones the caller declared. It
// does not show that the declared value is trustworthy, that the content is
// safe to interpret, or that the content is free of malware.
type Decision struct {
	Accepted      bool
	Reason        Reason
	Index         int
	ArchiveSHA256 Digest
	Entries       []Entry
	Totals        Totals
	NotGuaranteed []string
}

// runStats records what the run did, for tests and for the caller's own
// accounting.
type runStats struct {
	// inputBytes is the number of bytes read from the input.
	inputBytes int64
	// producedBytes is the number of decoded bytes produced so far.
	producedBytes uint64
}

// Admit reads r, checks that its SHA-256 equals expect, and decides whether the
// archive is admitted. Admit does not return an error. Every failure is a
// rejection with a reason:
//
//   - a nil Policy, a zero Policy, or a nil reader is rejected;
//   - an error from the reader, or a reader that stalls or misreports counts, is
//     rejected;
//   - a panic while reading or parsing is converted to a rejection.
//
// The conversion of panics does not isolate memory or CPU. The package offers
// no process-level isolation.
func (p *Policy) Admit(r io.Reader, expect Digest) Decision {
	d, _ := p.run(r, expect)
	return d
}

// run performs the admission and returns the decision with its statistics.
func (p *Policy) run(r io.Reader, expect Digest) (d Decision, st runStats) {
	defer func() {
		if recover() != nil {
			d = reject(ReasonInternal, -1, Digest{})
		}
	}()

	if p == nil {
		return reject(ReasonNilPolicy, -1, Digest{}), st
	}
	if !p.valid {
		return reject(ReasonInvalidPolicy, -1, Digest{}), st
	}
	if r == nil {
		return reject(ReasonNilReader, -1, Digest{}), st
	}

	data, err := readBounded(r, p.limits.MaxCompressedBytes)
	if err != nil {
		return reject(ReasonReadError, -1, Digest{}), st
	}
	st.inputBytes = int64(len(data))
	if st.inputBytes > p.limits.MaxCompressedBytes {
		return reject(ReasonCompressedTooLarge, -1, Digest{}), st
	}

	digest := Digest(sha256.Sum256(data))
	if digest != expect {
		return reject(ReasonDigestMismatch, -1, digest), st
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	// ErrInsecurePath is returned only when the GODEBUG setting asks for it,
	// and the reader is still usable. The entry name checks below reject
	// insecure names with a precise reason, so the error is ignored here.
	if zr == nil || (err != nil && !errors.Is(err, zip.ErrInsecurePath)) {
		return reject(ReasonMalformedArchive, -1, digest), st
	}
	if int64(len(zr.File)) > p.limits.MaxEntries {
		return reject(ReasonTooManyEntries, -1, digest), st
	}

	planned, reason, index := p.plan(zr.File, uint64(len(data)))
	if reason != "" {
		return reject(reason, index, digest), st
	}
	entries, reason, index := p.stream(planned, &st)
	if reason != "" {
		return reject(reason, index, digest), st
	}

	var ratio float64
	if st.inputBytes > 0 {
		ratio = float64(st.producedBytes) / float64(st.inputBytes)
	}
	return Decision{
		Accepted:      true,
		Index:         -1,
		ArchiveSHA256: digest,
		Entries:       entries,
		Totals: Totals{
			EntryCount:        len(entries),
			ArchiveBytes:      st.inputBytes,
			UncompressedBytes: int64(st.producedBytes),
			ObservedRatio:     ratio,
		},
		NotGuaranteed: notGuaranteed(),
	}, st
}

// reject builds a rejected Decision.
func reject(reason Reason, index int, digest Digest) Decision {
	return Decision{
		Accepted:      false,
		Reason:        reason,
		Index:         index,
		ArchiveSHA256: digest,
		NotGuaranteed: notGuaranteed(),
	}
}

// notGuaranteed returns a fresh copy of the properties this package does not
// establish.
func notGuaranteed() []string {
	return []string{
		"semantic content safety: entry contents are decoded and checked by CRC32 and size only, never interpreted",
		"absence of malware or hostile payloads in admitted entries",
		"provenance: who produced the archive and from which build",
		"trustworthiness of the caller-supplied expected digest itself",
		"parser isolation: decoding runs in this process without OS-level CPU, memory, or wall-time limits",
		"Unicode normalization equivalence of names, which the standard library cannot check; names that differ only by normalization form are not detected as duplicates",
		"full case folding, such as \"ß\" with \"ss\", is not applied to names; simple case folding is",
		"extra fields in local file headers are not inspected, because archive/zip does not expose them; central directory extra fields are checked",
		"bytes the zip container parser does not interpret, such as data before the first local header or after the central directory, are covered by the digest only",
		"CRC32 detects accidental corruption, not deliberate tampering",
		"write-time behavior on the target file system (existing files, mount points, link races), because this package writes nothing",
	}
}

// readBounded reads r until EOF, or until limit+1 bytes have been read,
// whichever comes first. It never asks r for more than the bytes it still
// needs, so the input is not read past limit+1.
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	var data []byte
	var chunk [streamChunkSize]byte
	idle := 0
	for {
		room := limit + 1 - int64(len(data))
		if room <= 0 {
			return data, nil
		}
		want := len(chunk)
		if int64(want) > room {
			want = int(room)
		}
		n, err := r.Read(chunk[:want])
		if n < 0 || n > want {
			return nil, errInvalidRead
		}
		if n > 0 {
			data = append(data, chunk[:n]...)
			idle = 0
		} else if err == nil {
			idle++
			if idle >= maxIdleReads {
				return nil, errNoProgress
			}
		}
		if err == io.EOF {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

var (
	errInvalidRead = errors.New("artifactsafety: reader returned an invalid count")
	errNoProgress  = errors.New("artifactsafety: reader made no progress")
)
