package artifactsafety

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// zipEntry describes one entry for buildZip.
type zipEntry struct {
	name   string
	body   []byte
	mode   fs.FileMode // applied with SetMode when non-zero
	method uint16
	flags  uint16
	adjust func(*zip.FileHeader) // applied last, for header fields SetMode cannot express
}

// buildZip writes a zip archive in memory from the given entries.
func buildZip(t testing.TB, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		fh := &zip.FileHeader{Name: e.name, Method: e.method, Flags: e.flags}
		if e.mode != 0 {
			fh.SetMode(e.mode)
		}
		if e.adjust != nil {
			e.adjust(fh)
		}
		fw, err := w.CreateHeader(fh)
		if err != nil {
			t.Fatalf("CreateHeader(%q): %v", e.name, err)
		}
		if len(e.body) > 0 {
			if _, err := fw.Write(e.body); err != nil {
				t.Fatalf("Write(%q): %v", e.name, err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

// limitsWith returns the default limits with the given fields changed.
func limitsWith(mod func(*Limits)) Limits {
	l := DefaultLimits()
	mod(&l)
	return l
}

func mustPolicy(t testing.TB, l Limits) *Policy {
	t.Helper()
	p, err := NewPolicy(l)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return p
}

func defaultPolicy(t testing.TB) *Policy {
	t.Helper()
	return mustPolicy(t, DefaultLimits())
}

// admitBytes admits data against its own digest, so that only the content
// checks decide the result.
func admitBytes(p *Policy, data []byte) Decision {
	return p.Admit(bytes.NewReader(data), Digest(sha256.Sum256(data)))
}

// requireRejected checks the rejection contract: the reason and index match,
// no entries or totals are reported, and the list of non-guarantees is present.
func requireRejected(t *testing.T, d Decision, reason Reason, index int) {
	t.Helper()
	if d.Accepted {
		t.Fatalf("accepted; want rejection %q at index %d", reason, index)
	}
	if d.Reason != reason {
		t.Fatalf("reason = %q, want %q", d.Reason, reason)
	}
	if d.Index != index {
		t.Fatalf("index = %d, want %d", d.Index, index)
	}
	if d.Entries != nil {
		t.Fatalf("rejected decision carries %d entries; want nil", len(d.Entries))
	}
	if d.Totals != (Totals{}) {
		t.Fatalf("rejected decision carries totals %+v; want zero", d.Totals)
	}
	if len(d.NotGuaranteed) == 0 {
		t.Fatal("NotGuaranteed is empty")
	}
}

func TestNewPolicyRejectsNonPositiveLimits(t *testing.T) {
	setters := []struct {
		name string
		set  func(*Limits, int64)
	}{
		{"MaxCompressedBytes", func(l *Limits, v int64) { l.MaxCompressedBytes = v }},
		{"MaxUncompressedBytes", func(l *Limits, v int64) { l.MaxUncompressedBytes = v }},
		{"MaxExpansionRatio", func(l *Limits, v int64) { l.MaxExpansionRatio = v }},
		{"MaxEntries", func(l *Limits, v int64) { l.MaxEntries = v }},
		{"MaxEntryBytes", func(l *Limits, v int64) { l.MaxEntryBytes = v }},
		{"MaxPathDepth", func(l *Limits, v int64) { l.MaxPathDepth = v }},
		{"MaxNameBytes", func(l *Limits, v int64) { l.MaxNameBytes = v }},
	}
	for _, s := range setters {
		for _, v := range []int64{0, -1, math.MinInt64} {
			l := DefaultLimits()
			s.set(&l, v)
			p, err := NewPolicy(l)
			if err == nil || p != nil {
				t.Errorf("NewPolicy with %s=%d: got policy %v, err %v; want error", s.name, v, p, err)
				continue
			}
			if !errors.Is(err, ErrInvalidLimits) {
				t.Errorf("NewPolicy with %s=%d: error %v does not wrap ErrInvalidLimits", s.name, v, err)
			}
		}
	}
}

func TestNewPolicyRejectsCompressedLimitThatOverflows(t *testing.T) {
	l := DefaultLimits()
	l.MaxCompressedBytes = math.MaxInt64
	if _, err := NewPolicy(l); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("MaxCompressedBytes = MaxInt64: err = %v; want ErrInvalidLimits", err)
	}
}

func TestNewPolicyAcceptsDefaults(t *testing.T) {
	if _, err := NewPolicy(DefaultLimits()); err != nil {
		t.Fatalf("NewPolicy(DefaultLimits()): %v", err)
	}
}

func TestAcceptsWellFormedArchive(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	noise := make([]byte, 8192)
	rnd.Read(noise)
	text := []byte("hello world\n")

	data := buildZip(t,
		zipEntry{name: "docs/"},
		zipEntry{name: "docs/readme.txt", body: text},
		zipEntry{name: "bin/data.bin", body: noise, method: zip.Deflate},
		zipEntry{name: "日本語/ファイル.txt", body: []byte("x")},
	)
	d := admitBytes(defaultPolicy(t), data)
	if !d.Accepted {
		t.Fatalf("rejected: %q at index %d", d.Reason, d.Index)
	}
	if d.Reason != "" || d.Index != -1 {
		t.Fatalf("accepted decision has reason %q and index %d", d.Reason, d.Index)
	}
	if d.ArchiveSHA256 != Digest(sha256.Sum256(data)) {
		t.Fatalf("ArchiveSHA256 does not match the archive bytes")
	}

	want := []Entry{
		{Index: 0, Name: "docs", Directory: true, Size: 0, CompressedSize: 0},
		{Index: 1, Name: "docs/readme.txt", Directory: false, Size: int64(len(text)), CompressedSize: int64(len(text))},
		{Index: 2, Name: "bin/data.bin", Directory: false, Size: int64(len(noise))},
		{Index: 3, Name: "日本語/ファイル.txt", Directory: false, Size: 1, CompressedSize: 1},
	}
	if len(d.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(d.Entries), len(want))
	}
	for i, w := range want {
		got := d.Entries[i]
		if got.Index != w.Index || got.Name != w.Name || got.Directory != w.Directory || got.Size != w.Size {
			t.Errorf("entry %d = %+v, want index/name/dir/size of %+v", i, got, w)
		}
		if w.CompressedSize != 0 && got.CompressedSize != w.CompressedSize {
			t.Errorf("entry %d compressed size = %d, want %d", i, got.CompressedSize, w.CompressedSize)
		}
		if got.CompressedSize <= 0 && !got.Directory && got.Size > 0 {
			t.Errorf("entry %d has content but no compressed bytes counted", i)
		}
	}

	var total int64
	for _, e := range d.Entries {
		total += e.Size
	}
	if d.Totals.EntryCount != len(want) {
		t.Errorf("EntryCount = %d, want %d", d.Totals.EntryCount, len(want))
	}
	if d.Totals.ArchiveBytes != int64(len(data)) {
		t.Errorf("ArchiveBytes = %d, want %d", d.Totals.ArchiveBytes, len(data))
	}
	if d.Totals.UncompressedBytes != total {
		t.Errorf("UncompressedBytes = %d, want %d", d.Totals.UncompressedBytes, total)
	}
	wantRatio := float64(total) / float64(len(data))
	if d.Totals.ObservedRatio != wantRatio {
		t.Errorf("ObservedRatio = %v, want %v", d.Totals.ObservedRatio, wantRatio)
	}
}

func TestAcceptedDecisionListsWhatIsNotGuaranteed(t *testing.T) {
	d := admitBytes(defaultPolicy(t), buildZip(t, zipEntry{name: "a.txt", body: []byte("a")}))
	if !d.Accepted {
		t.Fatalf("rejected: %q", d.Reason)
	}
	if len(d.NotGuaranteed) == 0 {
		t.Fatal("NotGuaranteed is empty on an accepted decision")
	}
	// The returned slice must be a copy, so a caller cannot change the
	// package's list.
	d.NotGuaranteed[0] = "changed"
	again := admitBytes(defaultPolicy(t), buildZip(t, zipEntry{name: "a.txt", body: []byte("a")}))
	if again.NotGuaranteed[0] == "changed" {
		t.Fatal("NotGuaranteed shares storage between decisions")
	}
}

func TestAcceptsEmptyArchive(t *testing.T) {
	data := buildZip(t)
	d := admitBytes(defaultPolicy(t), data)
	if !d.Accepted {
		t.Fatalf("empty archive rejected: %q", d.Reason)
	}
	if d.Entries == nil || len(d.Entries) != 0 {
		t.Fatalf("Entries = %#v; want non-nil empty slice", d.Entries)
	}
	if d.Totals.EntryCount != 0 || d.Totals.UncompressedBytes != 0 {
		t.Fatalf("totals = %+v; want zero entries and bytes", d.Totals)
	}
}

func TestDigestMismatchIsRejected(t *testing.T) {
	data := buildZip(t, zipEntry{name: "a.txt", body: []byte("x")})
	var wrong Digest
	d := defaultPolicy(t).Admit(bytes.NewReader(data), wrong)
	requireRejected(t, d, ReasonDigestMismatch, -1)
	if d.ArchiveSHA256 != Digest(sha256.Sum256(data)) {
		t.Fatal("ArchiveSHA256 on a digest mismatch does not match the bytes read")
	}
}

func TestDigestMismatchIsCheckedBeforeParsing(t *testing.T) {
	// Garbage bytes with a wrong digest must report the digest, not the parse
	// failure, which shows that the archive is not parsed before its identity is
	// confirmed.
	d := defaultPolicy(t).Admit(bytes.NewReader([]byte("garbage")), Digest{})
	requireRejected(t, d, ReasonDigestMismatch, -1)
}

func TestMalformedInputIsRejected(t *testing.T) {
	valid := buildZip(t, zipEntry{name: "a.txt", body: []byte("hello")})
	cases := map[string][]byte{
		"empty input":       {},
		"plain text":        []byte("not a zip archive at all, just text"),
		"truncated zip":     valid[:len(valid)-10],
		"local header only": []byte("PK\x03\x04"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			requireRejected(t, admitBytes(defaultPolicy(t), data), ReasonMalformedArchive, -1)
		})
	}
}

func TestDeclaredLimitsRejectBeforeDecoding(t *testing.T) {
	cases := []struct {
		name   string
		limits Limits
		files  []zipEntry
		reason Reason
		index  int
	}{
		{
			name:   "too many entries",
			limits: limitsWith(func(l *Limits) { l.MaxEntries = 2 }),
			files: []zipEntry{
				{name: "a"}, {name: "b"}, {name: "c"},
			},
			reason: ReasonTooManyEntries,
			index:  -1,
		},
		{
			name:   "declared entry size over limit",
			limits: limitsWith(func(l *Limits) { l.MaxEntryBytes = 10 }),
			files:  []zipEntry{{name: "a.txt", body: []byte("12345678901")}},
			reason: ReasonEntryTooLarge,
			index:  0,
		},
		{
			name:   "declared total over limit",
			limits: limitsWith(func(l *Limits) { l.MaxUncompressedBytes = 10 }),
			files: []zipEntry{
				{name: "a.txt", body: []byte("123456")},
				{name: "b.txt", body: []byte("123456")},
			},
			reason: ReasonTotalTooLarge,
			index:  1,
		},
		{
			name:   "name longer than limit",
			limits: limitsWith(func(l *Limits) { l.MaxNameBytes = 8 }),
			files:  []zipEntry{{name: "abcdefghij.txt", body: []byte("x")}},
			reason: ReasonNameTooLong,
			index:  0,
		},
		{
			name:   "path deeper than limit",
			limits: limitsWith(func(l *Limits) { l.MaxPathDepth = 2 }),
			files:  []zipEntry{{name: "a/b/c.txt", body: []byte("x")}},
			reason: ReasonNameDepth,
			index:  0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := buildZip(t, c.files...)
			d := admitBytes(mustPolicy(t, c.limits), data)
			requireRejected(t, d, c.reason, c.index)
		})
	}
}

func TestDepthLimitAllowsTheBoundary(t *testing.T) {
	// Two segments are at the limit of 2, for a directory and for a file.
	p := mustPolicy(t, limitsWith(func(l *Limits) { l.MaxPathDepth = 2 }))
	data := buildZip(t,
		zipEntry{name: "a/b/"},
		zipEntry{name: "a/c.txt", body: []byte("x")},
	)
	if d := admitBytes(p, data); !d.Accepted {
		t.Fatalf("two-segment paths at the limit rejected: %q", d.Reason)
	}
}

func TestFailClosedInputs(t *testing.T) {
	valid := buildZip(t, zipEntry{name: "a.txt", body: []byte("hello")})
	digest := Digest(sha256.Sum256(valid))

	cases := []struct {
		name   string
		admit  func() Decision
		reason Reason
	}{
		{
			name: "nil policy",
			admit: func() Decision {
				var p *Policy
				return p.Admit(bytes.NewReader(valid), digest)
			},
			reason: ReasonNilPolicy,
		},
		{
			name: "zero policy value",
			admit: func() Decision {
				var p Policy
				return p.Admit(bytes.NewReader(valid), digest)
			},
			reason: ReasonInvalidPolicy,
		},
		{
			name: "nil reader",
			admit: func() Decision {
				return defaultPolicy(t).Admit(nil, digest)
			},
			reason: ReasonNilReader,
		},
		{
			name: "reader error at start",
			admit: func() Decision {
				return defaultPolicy(t).Admit(errReader{}, digest)
			},
			reason: ReasonReadError,
		},
		{
			name: "reader error after a valid prefix",
			admit: func() Decision {
				r := io.MultiReader(bytes.NewReader(valid[:len(valid)/2]), errReader{})
				return defaultPolicy(t).Admit(r, digest)
			},
			reason: ReasonReadError,
		},
		{
			name: "reader returns more bytes than requested",
			admit: func() Decision {
				return defaultPolicy(t).Admit(overCountReader{}, digest)
			},
			reason: ReasonReadError,
		},
		{
			name: "reader stalls without progress",
			admit: func() Decision {
				return defaultPolicy(t).Admit(stallReader{}, digest)
			},
			reason: ReasonReadError,
		},
		{
			name: "reader panics",
			admit: func() Decision {
				return defaultPolicy(t).Admit(panicReader{}, digest)
			},
			reason: ReasonInternal,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := c.admit()
			if d.Accepted {
				t.Fatalf("accepted; want rejection %q", c.reason)
			}
			if d.Reason != c.reason {
				t.Fatalf("reason = %q, want %q", d.Reason, c.reason)
			}
			if d.Entries != nil {
				t.Fatal("rejected decision carries entries")
			}
		})
	}
}

// countingReader produces bytes forever and counts how many were requested.
type countingReader struct {
	consumed int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'A'
	}
	c.consumed += int64(len(p))
	return len(p), nil
}

func TestCompressedLimitStopsReadingAtTheBound(t *testing.T) {
	const limit = 1 << 20
	const margin = 64 << 10
	p := mustPolicy(t, limitsWith(func(l *Limits) { l.MaxCompressedBytes = limit }))
	r := &countingReader{}

	d := p.Admit(r, Digest{})
	requireRejected(t, d, ReasonCompressedTooLarge, -1)
	if r.consumed > limit+1+margin {
		t.Fatalf("reader consumed %d bytes for a %d-byte limit; want at most %d", r.consumed, limit, limit+1+margin)
	}
	if r.consumed < limit {
		t.Fatalf("reader consumed only %d bytes; the limit was not reached", r.consumed)
	}
}

func TestCompressedLimitAcceptsArchiveAtTheBound(t *testing.T) {
	data := buildZip(t, zipEntry{name: "a.txt", body: []byte("hello")})
	p := mustPolicy(t, limitsWith(func(l *Limits) { l.MaxCompressedBytes = int64(len(data)) }))
	if d := admitBytes(p, data); !d.Accepted {
		t.Fatalf("archive exactly at the byte limit rejected: %q", d.Reason)
	}
	p = mustPolicy(t, limitsWith(func(l *Limits) { l.MaxCompressedBytes = int64(len(data)) - 1 }))
	requireRejected(t, admitBytes(p, data), ReasonCompressedTooLarge, -1)
}

func TestDecisionIsDeterministic(t *testing.T) {
	p := defaultPolicy(t)
	inputs := map[string][]byte{
		"accepted":    buildZip(t, zipEntry{name: "a/b.txt", body: []byte("hello")}),
		"bad name":    buildZip(t, zipEntry{name: "a", body: []byte("x")}, zipEntry{name: "../escape", body: []byte("y")}),
		"duplicate":   buildZip(t, zipEntry{name: "A", body: []byte("x")}, zipEntry{name: "a", body: []byte("y")}),
		"not a zip":   []byte("PK\x03\x04 broken"),
		"empty input": {},
	}
	for name, data := range inputs {
		first := admitBytes(p, data)
		for i := 0; i < 3; i++ {
			if again := admitBytes(p, data); !reflect.DeepEqual(first, again) {
				t.Fatalf("%s: decision changed between runs:\nfirst %+v\nagain %+v", name, first, again)
			}
		}
	}
}

func FuzzAdmit(f *testing.F) {
	f.Add(buildZip(f, zipEntry{name: "a.txt", body: []byte("hello")}))
	f.Add(buildZip(f, zipEntry{name: "d/"}, zipEntry{name: "d/x", body: []byte("y")}))
	f.Add([]byte("PK\x03\x04 not really"))
	p := mustPolicy(f, limitsWith(func(l *Limits) {
		l.MaxCompressedBytes = 1 << 16
		l.MaxUncompressedBytes = 1 << 16
		l.MaxEntryBytes = 1 << 15
		l.MaxEntries = 64
	}))
	f.Fuzz(func(t *testing.T, data []byte) {
		d := admitBytes(p, data)
		if !d.Accepted {
			if d.Entries != nil {
				t.Fatal("rejected decision carries entries")
			}
			return
		}
		var total int64
		for _, e := range d.Entries {
			if e.Name == "" || e.Size < 0 {
				t.Fatalf("admitted entry %+v is malformed", e)
			}
			total += e.Size
		}
		if total != d.Totals.UncompressedBytes || d.Totals.ArchiveBytes != int64(len(data)) {
			t.Fatalf("totals %+v do not match entries (sum %d) or input (%d bytes)", d.Totals, total, len(data))
		}
		if len(d.Entries) > 64 {
			t.Fatalf("admitted %d entries, limit is 64", len(d.Entries))
		}
	})
}

// errBoom is returned by errReader.
var errBoom = errors.New("boom")

// errReader fails on the first read.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBoom }

// overCountReader reports more bytes than the buffer it was given.
type overCountReader struct{}

func (overCountReader) Read(p []byte) (int, error) { return len(p) + 1, nil }

// stallReader makes no progress and reports no error.
type stallReader struct{}

func (stallReader) Read([]byte) (int, error) { return 0, nil }

// panicReader panics on the first read.
type panicReader struct{}

func (panicReader) Read([]byte) (int, error) { panic("reader exploded") }
