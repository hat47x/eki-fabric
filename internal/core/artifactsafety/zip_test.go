package artifactsafety

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"math"
	"testing"
)

// patchCentralRecords returns a copy of data in which fn has been applied to
// every central directory record. Each record slice starts at the record's
// signature, so fixed header offsets can be used directly.
func patchCentralRecords(t testing.TB, data []byte, fn func(rec []byte)) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	sig := []byte{'P', 'K', 0x01, 0x02}
	patched := 0
	for off := 0; ; {
		i := bytes.Index(out[off:], sig)
		if i < 0 {
			break
		}
		pos := off + i
		fn(out[pos:])
		off = pos + 4
		patched++
	}
	if patched == 0 {
		t.Fatal("no central directory records found")
	}
	return out
}

// setCentralUncompressedSize declares the given uncompressed size in every
// central directory record, so the headers no longer match the content.
func setCentralUncompressedSize(t testing.TB, data []byte, size uint32) []byte {
	t.Helper()
	return patchCentralRecords(t, data, func(rec []byte) {
		// The uncompressed size is at offset 24 of a central directory record.
		binary.LittleEndian.PutUint32(rec[24:], size)
	})
}

// insertAfterEntryBody places junk directly after the compressed body of the
// first entry of a one-entry archive. The central directory size and offset
// are adjusted so that the archive still parses, and the junk counts as part
// of the entry.
func insertAfterEntryBody(t testing.TB, data, junk []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(zr.File) != 1 {
		t.Fatalf("insertAfterEntryBody needs a valid one-entry archive: %v", err)
	}
	// Local header: name length at offset 26, extra length at offset 28, body
	// after the 30-byte fixed part, the name, and the extra field.
	body := 30 + int(binary.LittleEndian.Uint16(data[26:])) + int(binary.LittleEndian.Uint16(data[28:]))
	at := body + int(zr.File[0].CompressedSize64)

	out := make([]byte, 0, len(data)+len(junk))
	out = append(out, data[:at]...)
	out = append(out, junk...)
	out = append(out, data[at:]...)
	out = patchCentralRecords(t, out, func(rec []byte) {
		// The compressed size is at offset 20 of a central directory record.
		size := binary.LittleEndian.Uint32(rec[20:])
		binary.LittleEndian.PutUint32(rec[20:], size+uint32(len(junk)))
	})

	eocd := bytes.LastIndex(out, []byte{'P', 'K', 0x05, 0x06})
	if eocd < 0 {
		t.Fatal("end of central directory not found")
	}
	// The offset of the central directory is at offset 16 of the end record.
	offset := binary.LittleEndian.Uint32(out[eocd+16:])
	binary.LittleEndian.PutUint32(out[eocd+16:], offset+uint32(len(junk)))
	return out
}

func TestPathSafetyRejectsUnsafeNames(t *testing.T) {
	cases := []struct {
		name   string
		reason Reason
	}{
		{"/etc/passwd", ReasonNameAbsolute},
		{"C:/windows/win.ini", ReasonNameDriveLetter},
		{"c:escape.txt", ReasonNameDriveLetter},
		{"..\\..\\escape.txt", ReasonNameBackslash},
		{"dir\\file.txt", ReasonNameBackslash},
		{"../escape.txt", ReasonNameDotDotSegment},
		{"a/../../escape.txt", ReasonNameDotDotSegment},
		{"a/./b.txt", ReasonNameDotSegment},
		{"a//b.txt", ReasonNameEmptySegment},
		{"a\x00b.txt", ReasonNameNUL},
		{"a\x01b.txt", ReasonNameControlChar},
		{"a\x7fb.txt", ReasonNameControlChar},
		{"a\u202eb.txt", ReasonNameControlChar},
		{"a\xffb.txt", ReasonNameInvalidUTF8},
		{"stream:hidden.txt", ReasonNameForbiddenChar},
		{"CON.txt", ReasonNameWindowsReserved},
		{"logs/lpt1", ReasonNameWindowsReserved},
		{"name.", ReasonNameWindowsTrailing},
		{"name ", ReasonNameWindowsTrailing},
		{"", ReasonNameEmpty},
	}
	for _, c := range cases {
		t.Run(string(c.reason)+"/"+c.name, func(t *testing.T) {
			data := buildZip(t, zipEntry{name: c.name, body: []byte("x")})
			requireRejected(t, admitBytes(defaultPolicy(t), data), c.reason, 0)
		})
	}
}

func TestRejectionIndexPointsAtTheOffendingEntry(t *testing.T) {
	data := buildZip(t,
		zipEntry{name: "ok/one.txt", body: []byte("1")},
		zipEntry{name: "ok/two.txt", body: []byte("2")},
		zipEntry{name: "../escape.txt", body: []byte("3")},
	)
	requireRejected(t, admitBytes(defaultPolicy(t), data), ReasonNameDotDotSegment, 2)
}

func TestEntryTypesAreRejected(t *testing.T) {
	unknownType := func(fh *zip.FileHeader) {
		// 0x9000 is not a Unix file type. archive/zip would report it as a
		// regular file, so the classification must read the raw attributes.
		fh.CreatorVersion = zipCreatorUnix<<8 | 20
		fh.ExternalAttrs = (0x9000 | 0o644) << 16
	}
	unknownCreator := func(fh *zip.FileHeader) {
		fh.CreatorVersion = 1<<8 | 20
		fh.ExternalAttrs = 0
	}
	msdosDirWithData := func(fh *zip.FileHeader) {
		fh.CreatorVersion = zipCreatorFAT<<8 | 20
		fh.ExternalAttrs = msdosDirAttr
	}

	cases := []struct {
		name   string
		entry  zipEntry
		reason Reason
	}{
		{"symbolic link", zipEntry{name: "link", body: []byte("/etc/passwd"), mode: fs.ModeSymlink | 0o777}, ReasonLinkEntry},
		{"named pipe", zipEntry{name: "pipe", mode: fs.ModeNamedPipe | 0o644}, ReasonSpecialEntry},
		{"socket", zipEntry{name: "sock", mode: fs.ModeSocket | 0o644}, ReasonSpecialEntry},
		{"block device", zipEntry{name: "blk", mode: fs.ModeDevice | 0o600}, ReasonSpecialEntry},
		{"character device", zipEntry{name: "chr", mode: fs.ModeDevice | fs.ModeCharDevice | 0o600}, ReasonSpecialEntry},
		{"unknown Unix type", zipEntry{name: "odd", body: []byte("x"), adjust: unknownType}, ReasonUnknownEntryType},
		{"unknown creator platform", zipEntry{name: "alien", body: []byte("x"), adjust: unknownCreator}, ReasonUnknownEntryType},
		{"trailing slash with a regular-file type", zipEntry{name: "d/", mode: 0o644}, ReasonUnknownEntryType},
		{"directory with content (Unix type)", zipEntry{name: "d", body: []byte("x"), mode: fs.ModeDir | 0o755}, ReasonDirectoryWithData},
		{"directory with content (MS-DOS attribute)", zipEntry{name: "d", body: []byte("x"), adjust: msdosDirWithData}, ReasonDirectoryWithData},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := buildZip(t, c.entry)
			requireRejected(t, admitBytes(defaultPolicy(t), data), c.reason, 0)
		})
	}
}

func TestDirectoriesAndRegularFilesAreAdmitted(t *testing.T) {
	data := buildZip(t,
		zipEntry{name: "d/", mode: fs.ModeDir | 0o755},
		zipEntry{name: "d/f.txt", body: []byte("content"), mode: 0o644},
		zipEntry{name: "msdos/", adjust: func(fh *zip.FileHeader) {
			fh.CreatorVersion = zipCreatorFAT<<8 | 20
			fh.ExternalAttrs = msdosDirAttr
		}},
	)
	d := admitBytes(defaultPolicy(t), data)
	if !d.Accepted {
		t.Fatalf("rejected: %q at %d", d.Reason, d.Index)
	}
	wantDir := []bool{true, false, true}
	for i, e := range d.Entries {
		if e.Directory != wantDir[i] {
			t.Errorf("entry %d (%q) Directory = %v, want %v", i, e.Name, e.Directory, wantDir[i])
		}
	}
}

func TestEncryptionAndUnknownFlagsAreRejected(t *testing.T) {
	cases := []struct {
		name   string
		entry  zipEntry
		reason Reason
	}{
		{"encrypted", zipEntry{name: "secret.txt", body: []byte("x"), flags: 0x0001}, ReasonEncrypted},
		{"unknown flag bit", zipEntry{name: "f.txt", body: []byte("x"), flags: 0x0020}, ReasonUnsupportedFlags},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := buildZip(t, c.entry)
			requireRejected(t, admitBytes(defaultPolicy(t), data), c.reason, 0)
		})
	}
}

func TestUnsupportedMethodIsRejectedWithoutDecoding(t *testing.T) {
	// Method 99 is not one archive/zip can write, so the method is changed in
	// the central directory. The entry must be rejected before any decoding.
	data := buildZip(t, zipEntry{name: "f.txt", body: []byte("x")})
	patched := patchCentralRecords(t, data, func(rec []byte) {
		binary.LittleEndian.PutUint16(rec[10:], 99)
	})
	requireRejected(t, admitBytes(defaultPolicy(t), patched), ReasonUnsupportedMethod, 0)
}

func TestDuplicateAndConflictingNamesAreRejected(t *testing.T) {
	cases := []struct {
		name    string
		entries []zipEntry
		reason  Reason
		index   int
	}{
		{
			name:    "same name differing by case",
			entries: []zipEntry{{name: "Readme.txt", body: []byte("1")}, {name: "README.TXT", body: []byte("2")}},
			reason:  ReasonDuplicateName, index: 1,
		},
		{
			name:    "same directory twice",
			entries: []zipEntry{{name: "d/"}, {name: "d/"}},
			reason:  ReasonDuplicateName, index: 1,
		},
		{
			name:    "file then directory with the same name",
			entries: []zipEntry{{name: "a", body: []byte("1")}, {name: "a/"}},
			reason:  ReasonDuplicateName, index: 1,
		},
		{
			name:    "file then child under it",
			entries: []zipEntry{{name: "a", body: []byte("1")}, {name: "a/b", body: []byte("2")}},
			reason:  ReasonPathConflict, index: 1,
		},
		{
			name:    "child then file taking its parent's place",
			entries: []zipEntry{{name: "a/b", body: []byte("1")}, {name: "a", body: []byte("2")}},
			reason:  ReasonPathConflict, index: 1,
		},
		{
			name:    "case-insensitive parent conflict",
			entries: []zipEntry{{name: "Dir/x", body: []byte("1")}, {name: "dir", body: []byte("2")}},
			reason:  ReasonPathConflict, index: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := buildZip(t, c.entries...)
			requireRejected(t, admitBytes(defaultPolicy(t), data), c.reason, c.index)
		})
	}
}

func TestDirectoryAfterItsChildrenIsAdmitted(t *testing.T) {
	data := buildZip(t,
		zipEntry{name: "a/b", body: []byte("1")},
		zipEntry{name: "a/"},
	)
	if d := admitBytes(defaultPolicy(t), data); !d.Accepted {
		t.Fatalf("directory listed after its child rejected: %q", d.Reason)
	}
}

func TestCorruptEntryContentIsRejected(t *testing.T) {
	const payload = "hello-world-data"
	data := buildZip(t, zipEntry{name: "a.txt", body: []byte(payload)})
	i := bytes.Index(data, []byte(payload))
	if i < 0 {
		t.Fatal("payload not found in stored archive")
	}
	corrupt := append([]byte(nil), data...)
	corrupt[i] ^= 0x01
	requireRejected(t, admitBytes(defaultPolicy(t), corrupt), ReasonEntryCorrupt, 0)
}

func TestCompressedBytesLeftInsideAnEntryAreRejected(t *testing.T) {
	// A deflate stream with junk after its end, counted in the entry's declared
	// compressed size. The control has no junk and must be admitted.
	data := buildZip(t, zipEntry{name: "a.txt", body: []byte("hello hello hello hello"), method: zip.Deflate})
	if d := admitBytes(defaultPolicy(t), data); !d.Accepted {
		t.Fatalf("control rejected: %q", d.Reason)
	}
	withJunk := insertAfterEntryBody(t, data, []byte{0xAA, 0xBB, 0xCC})
	requireRejected(t, admitBytes(defaultPolicy(t), withJunk), ReasonEntryCorrupt, 0)
}

func TestExpansionRatioStopsDecodingEarly(t *testing.T) {
	// Four mebibytes of zeros compress to a few kilobytes, far beyond 100:1.
	data := buildZip(t, zipEntry{name: "zeros.bin", body: make([]byte, 4<<20), method: zip.Deflate})
	p := defaultPolicy(t)
	d, st := p.run(bytes.NewReader(data), Digest(sha256.Sum256(data)))
	requireRejected(t, d, ReasonExpansionRatio, 0)
	// The check runs after each chunk of at most streamChunkSize bytes, so the
	// decoder should stop after the first chunk or two, not after all 4 MiB.
	if st.producedBytes > 2*streamChunkSize {
		t.Fatalf("decoded %d bytes before stopping; want at most %d", st.producedBytes, 2*streamChunkSize)
	}
}

func TestMeasuredSizesEnforceLimitsWhenHeadersLie(t *testing.T) {
	// One mebibyte of content whose headers claim one byte. Phase one accepts
	// the claim, so the limits must be enforced while decoding.
	body := bytes.Repeat([]byte("A"), 1<<20)
	data := setCentralUncompressedSize(t, buildZip(t, zipEntry{name: "big.bin", body: body}), 1)

	t.Run("entry size stops decoding", func(t *testing.T) {
		p := mustPolicy(t, limitsWith(func(l *Limits) { l.MaxEntryBytes = 1000 }))
		d, st := p.run(bytes.NewReader(data), Digest(sha256.Sum256(data)))
		requireRejected(t, d, ReasonEntryTooLarge, 0)
		if st.producedBytes > 1000+streamChunkSize {
			t.Fatalf("decoded %d bytes; want at most %d", st.producedBytes, 1000+streamChunkSize)
		}
	})

	t.Run("total size stops decoding", func(t *testing.T) {
		p := mustPolicy(t, limitsWith(func(l *Limits) { l.MaxUncompressedBytes = 1000 }))
		d, st := p.run(bytes.NewReader(data), Digest(sha256.Sum256(data)))
		requireRejected(t, d, ReasonTotalTooLarge, 0)
		if st.producedBytes > 1000+streamChunkSize {
			t.Fatalf("decoded %d bytes; want at most %d", st.producedBytes, 1000+streamChunkSize)
		}
	})

	t.Run("declared size that does not match is corrupt", func(t *testing.T) {
		requireRejected(t, admitBytes(defaultPolicy(t), data), ReasonEntryCorrupt, 0)
	})
}

func TestRatioExceededBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		out      uint64
		consumed int64
		limit    int64
		want     bool
	}{
		{"nothing produced", 0, 0, 1, false},
		{"output without input", 1, 0, 1, true},
		{"exactly at the ratio", 100, 1, 100, false},
		{"one byte over the ratio", 101, 1, 100, true},
		{"ratio over a larger input", 1000, 10, 100, false},
		{"ratio over a larger input, over", 1001, 10, 100, true},
		{"product overflows 64 bits", math.MaxUint64, math.MaxInt64, math.MaxInt64, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ratioExceeded(c.out, c.consumed, c.limit); got != c.want {
				t.Fatalf("ratioExceeded(%d, %d, %d) = %v, want %v", c.out, c.consumed, c.limit, got, c.want)
			}
		})
	}
}

func TestInsecureZipPathReachesNameCheck(t *testing.T) {
	// With GODEBUG=zipinsecurepath=0, archive/zip returns ErrInsecurePath
	// together with a usable reader. The package must still give the precise
	// name reason, not a malformed-archive reason.
	t.Setenv("GODEBUG", "zipinsecurepath=0")
	data := buildZip(t, zipEntry{name: "../escape.txt", body: []byte("x")})
	if _, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Skip("GODEBUG=zipinsecurepath=0 is not in effect; ErrInsecurePath path not exercised")
	}
	requireRejected(t, admitBytes(defaultPolicy(t), data), ReasonNameDotDotSegment, 0)
}
