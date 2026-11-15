package teideleg

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeTree(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"workspace/a.json":       `{"a":1}`,
		"workspace/nested/b.txt": "hello\n",
		"top.md":                 "# top\n",
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBundleDirIsDeterministicAcrossLocations(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	writeTree(t, a)
	writeTree(t, b)

	rawA, digestA, err := BundleDir(a)
	if err != nil {
		t.Fatal(err)
	}
	_, digestA2, err := BundleDir(a)
	if err != nil {
		t.Fatal(err)
	}
	rawB, digestB, err := BundleDir(b)
	if err != nil {
		t.Fatal(err)
	}
	if digestA != digestA2 {
		t.Errorf("the same tree produced %s then %s", digestA, digestA2)
	}
	if digestA != digestB {
		t.Errorf("two copies produced %s and %s; the digest must not depend on the location", digestA, digestB)
	}
	if len(rawA) == 0 || len(rawB) == 0 {
		t.Fatal("empty bundle")
	}

	if err := os.WriteFile(filepath.Join(a, "top.md"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, changed, err := BundleDir(a)
	if err != nil {
		t.Fatal(err)
	}
	if changed == digestA {
		t.Error("editing a file must change the input digest")
	}
}

func TestBundleDirRefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating a symbolic link on Windows needs a privilege this test does not assume")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symbolic links are not available here:", err)
	}
	if _, _, err := BundleDir(root); err == nil {
		t.Fatal("a symbolic link must be refused")
	}
}

func TestBundleDirRefusesEmptyDirectory(t *testing.T) {
	if _, _, err := BundleDir(t.TempDir()); err == nil {
		t.Fatal("an empty directory must be refused")
	}
}
