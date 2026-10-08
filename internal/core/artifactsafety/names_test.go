package artifactsafety

import (
	"path"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestNormalizeNameAdmits(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"a", "a"},
		{"a/b.txt", "a/b.txt"},
		{"dir/", "dir"},
		{".hidden", ".hidden"},
		{"...txt", "...txt"},
		{"a.b.c", "a.b.c"},
		{"a b/c d.txt", "a b/c d.txt"},
		{"日本語/ファイル.txt", "日本語/ファイル.txt"},
		{"CONSOLE.txt", "CONSOLE.txt"},
		{"com10", "com10"},
		{"file-name_v1.2.tar", "file-name_v1.2.tar"},
	}
	for _, c := range cases {
		got, reason := normalizeName(c.raw, 255, 16)
		if reason != "" || got != c.want {
			t.Errorf("normalizeName(%q) = %q, %q; want %q admitted", c.raw, got, reason, c.want)
		}
	}
}

func TestNormalizeNameRejects(t *testing.T) {
	cases := []struct {
		raw    string
		reason Reason
	}{
		{"", ReasonNameEmpty},
		{"/", ReasonNameAbsolute},
		{"//", ReasonNameAbsolute},
		{"/etc/passwd", ReasonNameAbsolute},
		{"a//", ReasonNameEmptySegment},
		{"a//b", ReasonNameEmptySegment},
		{"./a", ReasonNameDotSegment},
		{"a/.", ReasonNameDotSegment},
		{"..", ReasonNameDotDotSegment},
		{"a/..", ReasonNameDotDotSegment},
		{"a/b/../c", ReasonNameDotDotSegment},
		{`a\b`, ReasonNameBackslash},
		{`..\x`, ReasonNameBackslash},
		{"C:", ReasonNameDriveLetter},
		{"d:/x", ReasonNameDriveLetter},
		{"1:x", ReasonNameForbiddenChar},
		{"a:b", ReasonNameDriveLetter},
		{"stream:hidden", ReasonNameForbiddenChar},
		{"a?b", ReasonNameForbiddenChar},
		{"a*b", ReasonNameForbiddenChar},
		{`a"b`, ReasonNameForbiddenChar},
		{"a<b", ReasonNameForbiddenChar},
		{"a>b", ReasonNameForbiddenChar},
		{"a|b", ReasonNameForbiddenChar},
		{"CON", ReasonNameWindowsReserved},
		{"con.txt", ReasonNameWindowsReserved},
		{"Aux", ReasonNameWindowsReserved},
		{"a/nul", ReasonNameWindowsReserved},
		{"com1.log", ReasonNameWindowsReserved},
		{"COM0", ReasonNameWindowsReserved},
		{"lpt¹", ReasonNameWindowsReserved},
		{"x.", ReasonNameWindowsTrailing},
		{"x ", ReasonNameWindowsTrailing},
		{"a/x.../", ReasonNameWindowsTrailing},
		{"a\x00b", ReasonNameNUL},
		{"a\tb", ReasonNameControlChar},
		{"a\x7fb", ReasonNameControlChar},
		{"a\u200bb", ReasonNameControlChar},
		{"a\u202eb", ReasonNameControlChar},
		{"\xc3", ReasonNameInvalidUTF8},
	}
	for _, c := range cases {
		got, reason := normalizeName(c.raw, 255, 16)
		if reason != c.reason {
			t.Errorf("normalizeName(%q) = %q, %q; want reason %q", c.raw, got, reason, c.reason)
		}
		if got != "" {
			t.Errorf("normalizeName(%q) returned %q with a rejection", c.raw, got)
		}
	}
}

func TestNormalizeNameLimits(t *testing.T) {
	if _, reason := normalizeName("abcdefgh", 8, 16); reason != "" {
		t.Errorf("name at the byte limit rejected: %q", reason)
	}
	if _, reason := normalizeName("abcdefghi", 8, 16); reason != ReasonNameTooLong {
		t.Errorf("name over the byte limit: reason %q, want %q", reason, ReasonNameTooLong)
	}
	if _, reason := normalizeName("a/b", 255, 2); reason != "" {
		t.Errorf("two segments at a depth limit of 2 rejected: %q", reason)
	}
	if _, reason := normalizeName("a/b/c", 255, 2); reason != ReasonNameDepth {
		t.Errorf("three segments at a depth limit of 2: reason %q, want %q", reason, ReasonNameDepth)
	}
}

func TestNormalizeNameKeepsCaseForDisplay(t *testing.T) {
	got, reason := normalizeName("Dir/File.TXT", 255, 16)
	if reason != "" || got != "Dir/File.TXT" {
		t.Fatalf("normalizeName = %q, %q; want the name as written", got, reason)
	}
}

// FuzzNormalizeName checks the invariants of every admitted name. The seeds run
// as ordinary subtests in go test.
func FuzzNormalizeName(f *testing.F) {
	for _, seed := range []string{
		"a", "a/b", "../x", "C:/x", `a\b`, "dir/", "a//b", "CON.txt", "a\x00b", "名前/ファイル",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		name, reason := normalizeName(raw, 255, 16)
		if reason != "" {
			return
		}
		if name == "" || !utf8.ValidString(name) {
			t.Fatalf("admitted %q as %q: empty or not valid UTF-8", raw, name)
		}
		if strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
			t.Fatalf("admitted %q as %q: leading or trailing slash", raw, name)
		}
		if strings.ContainsAny(name, `\:*?"<>|`) {
			t.Fatalf("admitted %q as %q: forbidden character", raw, name)
		}
		segments := strings.Split(name, "/")
		if len(segments) > 16 {
			t.Fatalf("admitted %q with %d segments", raw, len(segments))
		}
		for _, seg := range segments {
			if seg == "" || seg == "." || seg == ".." {
				t.Fatalf("admitted %q as %q: segment %q", raw, name, seg)
			}
		}
		if path.Clean(name) != name {
			t.Fatalf("admitted %q as %q: not clean", raw, name)
		}
		for _, r := range name {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("admitted %q as %q: control or format character %U", raw, name, r)
			}
		}
	})
}
