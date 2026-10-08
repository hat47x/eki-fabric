package artifactsafety

import (
	"archive/zip"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

// Non-ASCII names are written as byte escapes so the source stays ASCII. Sigma
// (U+03C3) is CF 83, final sigma (U+03C2) is CF 82, capital sigma (U+03A3) is
// CE A3, long s (U+017F) is C5 BF, the Kelvin sign (U+212A) is E2 84 AA, and
// sharp s (U+00DF) is C3 9F.
const (
	sigma      = "\xcf\x83"
	finalSigma = "\xcf\x82"
	capSigma   = "\xce\xa3"
	longS      = "\xc5\xbf"
	kelvin     = "\xe2\x84\xaa"
	sharpS     = "\xc3\x9f"
)

// TestFoldKeySharesSimpleCaseOrbits checks that the duplicate key treats every
// member of a simple case-folding orbit as the same name, and nothing else.
func TestFoldKeySharesSimpleCaseOrbits(t *testing.T) {
	same := [][]string{
		{sigma, finalSigma, capSigma},
		{"s", "S", longS},
		{"k", "K", kelvin},
	}
	for _, group := range same {
		for _, s := range group[1:] {
			if foldKey(s) != foldKey(group[0]) {
				t.Errorf("foldKey(%q) = %q, want the key of %q (%q)", s, foldKey(s), group[0], foldKey(group[0]))
			}
		}
	}
	if foldKey("a") == foldKey("b") {
		t.Error("distinct letters share a key")
	}
	// The orbit minimum of "a" is "A", and the separator is kept as it is.
	if foldKey("a/b") != "A/B" {
		t.Errorf("foldKey(%q) = %q, want %q", "a/b", foldKey("a/b"), "A/B")
	}
}

// TestUnicodeCaseFoldingDuplicatesAreRejected checks that names that differ only
// by Unicode simple case are a rejection, not two entries that overwrite each
// other on a case-insensitive target.
func TestUnicodeCaseFoldingDuplicatesAreRejected(t *testing.T) {
	cases := []struct {
		name          string
		first, second string
	}{
		{"sigma and final sigma", sigma + ".txt", finalSigma + ".txt"},
		{"capital sigma and final sigma", capSigma + ".txt", finalSigma + ".txt"},
		{"long s and s", longS + ".txt", "s.txt"},
		{"Kelvin sign and k", kelvin + ".txt", "k.txt"},
	}
	p := defaultPolicy(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := buildZip(t,
				zipEntry{name: tc.first, body: []byte("1")},
				zipEntry{name: tc.second, body: []byte("2")},
			)
			requireRejected(t, admitBytes(p, data), ReasonDuplicateName, 1)
		})
	}
}

// TestFoldedAncestorConflictsAreRejected checks that a file cannot take the
// place of a directory that an earlier entry needs, even when the two names
// differ only by simple case.
func TestFoldedAncestorConflictsAreRejected(t *testing.T) {
	p := defaultPolicy(t)
	data := buildZip(t,
		zipEntry{name: capSigma + "/b", body: []byte("1")},
		zipEntry{name: finalSigma, body: []byte("2")},
	)
	requireRejected(t, admitBytes(p, data), ReasonPathConflict, 1)
}

// TestFullCaseFoldingIsNotAppliedAndIsDisclosed documents a known limit. Full
// case folding maps sharp s to "ss", and that is not applied, so the two names
// are admitted. NotGuaranteed states the limit.
func TestFullCaseFoldingIsNotAppliedAndIsDisclosed(t *testing.T) {
	p := defaultPolicy(t)
	data := buildZip(t,
		zipEntry{name: sharpS + ".txt", body: []byte("1")},
		zipEntry{name: "ss.txt", body: []byte("2")},
	)
	if d := admitBytes(p, data); !d.Accepted {
		t.Fatalf("expected the documented limit to admit both names, got %+v", d)
	}
	found := false
	for _, s := range notGuaranteed() {
		if s == "full case folding, such as \"ß\" with \"ss\", is not applied to names; simple case folding is" {
			found = true
		}
	}
	if !found {
		t.Error("NotGuaranteed does not disclose the full case folding limit")
	}
}

// TestCompressedRangesMustFitTheArchive checks the bound on decoder input.
// Each entry reads exactly its declared compressed range, so records that share
// bytes would make the decoder read the same input again for each entry. The
// declared ranges together must fit in the archive.
func TestCompressedRangesMustFitTheArchive(t *testing.T) {
	p := defaultPolicy(t)
	files := []*zip.File{
		{FileHeader: zip.FileHeader{Name: "a", CompressedSize64: 40}},
		{FileHeader: zip.FileHeader{Name: "b", CompressedSize64: 40}},
		{FileHeader: zip.FileHeader{Name: "c", CompressedSize64: 30}},
	}
	_, reason, index := p.plan(files, 100)
	if reason != ReasonCompressedTotal || index != 2 {
		t.Fatalf("reason = %q index = %d, want %q at 2", reason, index, ReasonCompressedTotal)
	}

	// Ranges that exactly fill the archive are accepted.
	files[2].CompressedSize64 = 20
	if _, reason, index := p.plan(files, 100); reason != "" {
		t.Fatalf("exact fit rejected: reason %q at %d", reason, index)
	}
}

// unicodePathExtra builds an Info-ZIP Unicode Path extra field that carries name,
// with the CRC32 of headerName, as an archiver writes it.
func unicodePathExtra(name, headerName string) []byte {
	data := []byte{1}
	data = binary.LittleEndian.AppendUint32(data, crc32.ChecksumIEEE([]byte(headerName)))
	data = append(data, name...)
	out := make([]byte, 4, 4+len(data))
	binary.LittleEndian.PutUint16(out[0:], zipExtraUnicodePath)
	binary.LittleEndian.PutUint16(out[2:], uint16(len(data)))
	return append(out, data...)
}

// TestUnicodePathExtraMustRepeatTheHeaderName checks that a name carried in the
// extra field cannot differ from the header name. An extractor that honours the
// field would otherwise write a different file.
func TestUnicodePathExtraMustRepeatTheHeaderName(t *testing.T) {
	badVersion := unicodePathExtra("safe.txt", "safe.txt")
	badVersion[4] = 2
	cases := []struct {
		name   string
		header string
		extra  []byte
		want   Reason
	}{
		{"same name", "safe.txt", unicodePathExtra("safe.txt", "safe.txt"), ""},
		{"different name", "safe_b.txt", unicodePathExtra("safe_a.txt", "safe_b.txt"), ReasonNameExtraMismatch},
		{"crc of another name", "safe_b.txt", unicodePathExtra("safe_b.txt", "other.txt"), ReasonNameExtraMismatch},
		{"unknown version", "safe.txt", badVersion, ReasonExtraMalformed},
		{"truncated field", "safe.txt", []byte{0x75, 0x70, 0x10, 0x00}, ReasonExtraMalformed},
		{"trailing byte", "safe.txt", append(unicodePathExtra("safe.txt", "safe.txt"), 0x00), ReasonExtraMalformed},
		{"unrelated field passes", "safe.txt", []byte{0x55, 0x54, 0x01, 0x00, 0x01}, ""},
	}
	p := defaultPolicy(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &zip.File{FileHeader: zip.FileHeader{Name: tc.header, Extra: tc.extra}}
			if _, reason, _ := p.plan([]*zip.File{f}, 0); reason != tc.want {
				t.Fatalf("reason = %q, want %q", reason, tc.want)
			}
		})
	}
}
