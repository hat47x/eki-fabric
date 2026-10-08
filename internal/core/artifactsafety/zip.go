package artifactsafety

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math/bits"
	"strings"
)

// Creator platform values from the high byte of the "version made by" field.
// The values follow the zip specification.
const (
	zipCreatorFAT   = 0
	zipCreatorUnix  = 3
	zipCreatorNTFS  = 11
	zipCreatorVFAT  = 14
	zipCreatorMacOS = 19
)

// Unix file type values in the S_IFMT bits of the high 16 bits of the external
// attributes.
const (
	unixTypeMask    = 0xF000
	unixTypeFIFO    = 0x1000
	unixTypeChar    = 0x2000
	unixTypeDir     = 0x4000
	unixTypeBlock   = 0x6000
	unixTypeRegular = 0x8000
	unixTypeSymlink = 0xA000
	unixTypeSocket  = 0xC000
)

// msdosDirAttr is the directory bit of the MS-DOS attributes in the low byte of
// the external attributes.
const msdosDirAttr = 0x10

// General purpose flags. Bit 0 marks encryption. The other allowed bits are the
// deflate option bits (0x0002, 0x0004), the data descriptor (0x0008), and the
// UTF-8 name flag (0x0800). Any other bit is rejected.
const (
	zipFlagEncrypted = 0x0001
	zipAllowedFlags  = 0x0002 | 0x0004 | 0x0008 | 0x0800
)

// plannedEntry is an entry that passed the metadata checks and is ready to be
// decoded.
type plannedEntry struct {
	index int
	file  *zip.File
	name  string
	dir   bool
}

// plan checks the metadata of every entry without decoding any content. It
// returns the planned entries in central directory order, or the first
// rejection with its index. archiveBytes is the size of the whole archive.
//
// Declared sizes are used here only to reject early. Acceptance is decided by
// stream, which measures the sizes.
func (p *Policy) plan(files []*zip.File, archiveBytes uint64) ([]plannedEntry, Reason, int) {
	out := make([]plannedEntry, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	fileKeys := make(map[string]struct{})
	ancestorKeys := make(map[string]struct{})
	var declared uint64
	var compressed uint64

	for i, f := range files {
		name, reason := normalizeName(f.Name, p.limits.MaxNameBytes, p.limits.MaxPathDepth)
		if reason != "" {
			return nil, reason, i
		}
		dir, reason := classify(f)
		if reason != "" {
			return nil, reason, i
		}
		if reason := checkHeader(f); reason != "" {
			return nil, reason, i
		}
		if reason := checkExtraFields(f); reason != "" {
			return nil, reason, i
		}
		// Each entry reads exactly its declared compressed range. The ranges of
		// well-formed entries do not overlap, so together they fit in the archive.
		// Records that share bytes would make the decoder read the same input
		// again for each entry, so the sum is bounded here. compressed never
		// exceeds archiveBytes, so the subtraction is safe.
		if f.CompressedSize64 > archiveBytes-compressed {
			return nil, ReasonCompressedTotal, i
		}
		compressed += f.CompressedSize64
		if f.UncompressedSize64 > uint64(p.limits.MaxEntryBytes) {
			return nil, ReasonEntryTooLarge, i
		}
		// declared never exceeds MaxUncompressedBytes, so the subtraction is safe.
		if f.UncompressedSize64 > uint64(p.limits.MaxUncompressedBytes)-declared {
			return nil, ReasonTotalTooLarge, i
		}
		declared += f.UncompressedSize64

		// Two entries that map to the same name, ignoring ASCII and Unicode
		// simple case, are a rejection, not an overwrite.
		key := foldKey(name)
		if _, dup := seen[key]; dup {
			return nil, ReasonDuplicateName, i
		}
		ancestors := ancestorsOf(key)
		for _, anc := range ancestors {
			// An earlier file cannot also be a directory that this entry needs.
			if _, isFile := fileKeys[anc]; isFile {
				return nil, ReasonPathConflict, i
			}
		}
		// A later file cannot take the place of a directory that an earlier
		// entry needs. A directory entry may be the parent of earlier entries.
		if _, needed := ancestorKeys[key]; needed && !dir {
			return nil, ReasonPathConflict, i
		}

		seen[key] = struct{}{}
		if !dir {
			fileKeys[key] = struct{}{}
		}
		for _, anc := range ancestors {
			ancestorKeys[anc] = struct{}{}
		}
		out = append(out, plannedEntry{index: i, file: f, name: name, dir: dir})
	}
	return out, "", -1
}

// classify reports whether an entry is a directory, or the reason it must be
// rejected. It reads the raw creator and attribute fields, not
// zip.FileHeader.Mode, because archive/zip maps unrecognized Unix file types to
// regular files.
func classify(f *zip.File) (bool, Reason) {
	var dir bool
	switch f.CreatorVersion >> 8 {
	case zipCreatorUnix, zipCreatorMacOS:
		switch (f.ExternalAttrs >> 16) & unixTypeMask {
		case unixTypeRegular:
			dir = false
		case unixTypeDir:
			dir = true
		case unixTypeSymlink:
			return false, ReasonLinkEntry
		case unixTypeFIFO, unixTypeChar, unixTypeBlock, unixTypeSocket:
			return false, ReasonSpecialEntry
		default:
			return false, ReasonUnknownEntryType
		}
		// A trailing slash means directory. Attributes that call the entry a
		// regular file contradict it, so the entry is rejected.
		if strings.HasSuffix(f.Name, "/") && !dir {
			return false, ReasonUnknownEntryType
		}
	case zipCreatorFAT, zipCreatorNTFS, zipCreatorVFAT:
		dir = f.ExternalAttrs&msdosDirAttr != 0 || strings.HasSuffix(f.Name, "/")
	default:
		return false, ReasonUnknownEntryType
	}
	return dir, ""
}

// checkHeader rejects encryption, unknown flag bits, and methods other than
// stored and deflate.
func checkHeader(f *zip.File) Reason {
	if f.Flags&zipFlagEncrypted != 0 {
		return ReasonEncrypted
	}
	if f.Flags&^zipAllowedFlags != 0 {
		return ReasonUnsupportedFlags
	}
	if f.Method != zip.Store && f.Method != zip.Deflate {
		return ReasonUnsupportedMethod
	}
	return ""
}

// zipExtraUnicodePath is the Info-ZIP Unicode Path extra field (tag 0x7075). It
// carries a UTF-8 name, and some extractors write that name in place of the
// header name.
const zipExtraUnicodePath = 0x7075

// checkExtraFields parses the extra fields of an entry. The Unicode Path field
// must repeat the header name exactly, with a matching CRC32 of that name, so an
// extractor that honours the field writes the same file as one that does not.
// Malformed extra data is rejected, because a malformed block could hide such a
// field from this check. Local file header extra fields are not visible through
// archive/zip and are not inspected; see the NotGuaranteed list.
func checkExtraFields(f *zip.File) Reason {
	extra := f.Extra
	for len(extra) > 0 {
		if len(extra) < 4 {
			return ReasonExtraMalformed
		}
		tag := binary.LittleEndian.Uint16(extra[0:2])
		size := int(binary.LittleEndian.Uint16(extra[2:4]))
		if size > len(extra)-4 {
			return ReasonExtraMalformed
		}
		data := extra[4 : 4+size]
		if tag == zipExtraUnicodePath {
			if len(data) < 5 || data[0] != 1 {
				return ReasonExtraMalformed
			}
			if binary.LittleEndian.Uint32(data[1:5]) != crc32.ChecksumIEEE([]byte(f.Name)) ||
				string(data[5:]) != f.Name {
				return ReasonNameExtraMismatch
			}
		}
		extra = extra[4+size:]
	}
	return ""
}

// stream decodes each planned entry in order and measures it. It checks the
// per-entry size, the total, and the expansion ratio after every chunk, so
// decoding stops at the first violated limit. After the last chunk it checks
// the measured size, the CRC32, and that all declared compressed bytes were
// used.
func (p *Policy) stream(files []plannedEntry, st *runStats) ([]Entry, Reason, int) {
	entries := make([]Entry, 0, len(files))
	chunk := make([]byte, streamChunkSize)
	maxEntry := uint64(p.limits.MaxEntryBytes)
	maxTotal := uint64(p.limits.MaxUncompressedBytes)

	for _, pe := range files {
		f := pe.file
		raw, err := f.OpenRaw()
		if err != nil {
			return nil, ReasonEntryCorrupt, pe.index
		}
		m := &meter{br: bufio.NewReader(raw)}

		var src io.Reader = m
		var fl io.ReadCloser
		if f.Method == zip.Deflate {
			fl = flate.NewReader(m)
			src = fl
		}

		crc := crc32.NewIEEE()
		var out uint64
		idle := 0
		var reason Reason
		for {
			n, rerr := src.Read(chunk)
			if n < 0 || n > len(chunk) {
				reason = ReasonEntryCorrupt
				break
			}
			if n > 0 {
				idle = 0
				u := uint64(n)
				out += u
				if out > maxEntry {
					reason = ReasonEntryTooLarge
					break
				}
				// st.producedBytes never exceeds maxTotal, so the subtraction is safe.
				if u > maxTotal-st.producedBytes {
					reason = ReasonTotalTooLarge
					break
				}
				st.producedBytes += u
				crc.Write(chunk[:n])
				if ratioExceeded(out, m.consumed, p.limits.MaxExpansionRatio) {
					reason = ReasonExpansionRatio
					break
				}
			} else if rerr == nil {
				idle++
				if idle >= maxIdleReads {
					reason = ReasonEntryCorrupt
					break
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				reason = ReasonEntryCorrupt
				break
			}
		}
		if fl != nil {
			_ = fl.Close()
		}
		if reason != "" {
			return nil, reason, pe.index
		}

		if pe.dir && out > 0 {
			return nil, ReasonDirectoryWithData, pe.index
		}
		if out != f.UncompressedSize64 {
			return nil, ReasonEntryCorrupt, pe.index
		}
		if crc.Sum32() != f.CRC32 {
			return nil, ReasonEntryCorrupt, pe.index
		}
		// Compressed bytes left unused inside the entry are not inspected, so
		// they are a rejection.
		if m.consumed != int64(f.CompressedSize64) {
			return nil, ReasonEntryCorrupt, pe.index
		}

		entries = append(entries, Entry{
			Index:          pe.index,
			Name:           pe.name,
			Directory:      pe.dir,
			Size:           int64(out),
			CompressedSize: m.consumed,
		})
	}
	return entries, "", -1
}

// meter counts the compressed bytes that a decoder has taken. Only bytes handed
// to the caller are counted. Read-ahead inside bufio is not, and the decoder
// reads through ReadByte, so it does not read ahead of its own needs.
type meter struct {
	br       *bufio.Reader
	consumed int64
}

// Read implements io.Reader and counts the bytes returned.
func (m *meter) Read(p []byte) (int, error) {
	n, err := m.br.Read(p)
	m.consumed += int64(n)
	return n, err
}

// ReadByte implements io.ByteReader, so flate uses it directly without adding
// its own buffer.
func (m *meter) ReadByte() (byte, error) {
	b, err := m.br.ReadByte()
	if err == nil {
		m.consumed++
	}
	return b, err
}

// ratioExceeded reports whether out decoded bytes from consumed compressed
// bytes exceed limit:1. It does not overflow, and zero compressed input with
// any output is a violation.
func ratioExceeded(out uint64, consumed int64, limit int64) bool {
	if out == 0 {
		return false
	}
	if consumed <= 0 {
		return true
	}
	hi, lo := bits.Mul64(uint64(limit), uint64(consumed))
	if hi != 0 {
		return false
	}
	return out > lo
}
