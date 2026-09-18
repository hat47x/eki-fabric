package safezip

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeZip(t *testing.T, entries map[string]string, symlink string) *zip.Reader {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if symlink != "" {
		h := &zip.FileHeader{Name: "link"}
		h.SetMode(os.ModeSymlink | 0777)
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(symlink)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func TestExtractValid(t *testing.T) {
	zr := makeZip(t, map[string]string{"a/b.txt": "hello"}, "")
	dest := filepath.Join(t.TempDir(), "out")
	rep, err := Extract(zr, dest, Limits{MaxFiles: 10, MaxTotalBytes: 1024, MaxFileBytes: 1024, MaxDepth: 4})
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalExtracted != 5 {
		t.Fatalf("extracted=%d", rep.TotalExtracted)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a", "b.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestRejectTraversal(t *testing.T) {
	zr := makeZip(t, map[string]string{"../../escape": "x"}, "")
	_, err := Inspect(zr, Limits{})
	if err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestRejectBackslashTraversal(t *testing.T) {
	zr := makeZip(t, map[string]string{"..\\..\\escape": "x"}, "")
	_, err := Inspect(zr, Limits{})
	if err == nil {
		t.Fatal("expected backslash rejection")
	}
}

func TestRejectDriveQualifiedPath(t *testing.T) {
	zr := makeZip(t, map[string]string{"C:/escape": "x"}, "")
	if _, err := Inspect(zr, Limits{}); err == nil {
		t.Fatal("expected drive path rejection")
	}
}

func TestRejectSymlink(t *testing.T) {
	zr := makeZip(t, nil, "/etc/passwd")
	_, err := Inspect(zr, Limits{})
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestRejectFileCount(t *testing.T) {
	zr := makeZip(t, map[string]string{"a": "1", "b": "2", "c": "3"}, "")
	_, err := Inspect(zr, Limits{MaxFiles: 2})
	if err == nil {
		t.Fatal("expected file count rejection")
	}
}

func TestRejectExpandedSize(t *testing.T) {
	zr := makeZip(t, map[string]string{"big": strings.Repeat("0", 4096)}, "")
	_, err := Inspect(zr, Limits{MaxTotalBytes: 1024})
	if err == nil {
		t.Fatal("expected total size rejection")
	}
}

func TestRejectCompressionRatio(t *testing.T) {
	zr := makeZip(t, map[string]string{"dense": strings.Repeat("0", 64*1024)}, "")
	_, err := Inspect(zr, Limits{MaxCompressionRatio: 5})
	if err == nil {
		t.Fatal("expected ratio rejection")
	}
}

func TestDestinationMustBeNew(t *testing.T) {
	zr := makeZip(t, map[string]string{"a": "1"}, "")
	dest := t.TempDir()
	_, err := Extract(zr, dest, Limits{MaxFiles: 10, MaxTotalBytes: 1024, MaxFileBytes: 1024, MaxDepth: 4})
	if err == nil {
		t.Fatal("expected destination creation failure")
	}
}

func TestExtractRequiresFiniteLimits(t *testing.T) {
	zr := makeZip(t, map[string]string{"a": "1"}, "")
	dest := filepath.Join(t.TempDir(), "out")
	if _, err := Extract(zr, dest, Limits{}); err == nil {
		t.Fatal("expected finite limit requirement")
	}
}

func TestRejectNonCanonicalPath(t *testing.T) {
	zr := makeZip(t, map[string]string{"a/../b": "x"}, "")
	if _, err := Inspect(zr, Limits{}); err == nil {
		t.Fatal("expected non-canonical path rejection")
	}
}
