package teideleg

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The adapter's job depends on EKI's published contract, not on EKI's
// implementation. Importing EKI's Go packages would make TEI depend on
// internals that are not part of that contract, so the boundary is checked as
// a test rather than left to review.
func TestSpikeDoesNotImportEKIGoPackages(t *testing.T) {
	const (
		ekiModule = "github.com/hat47x/eki-fabric"
		self      = ekiModule + "/spikes/tei-eki-minimal-delegation"
	)

	var files []string
	for _, pattern := range []string{"*.go", filepath.Join("cmd", "*", "*.go")} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, matches...)
	}
	if len(files) == 0 {
		t.Fatal("no Go files found; the boundary check would be vacuous")
	}
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, imp := range file.Imports {
			imported, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			// The CLI may import this package; nothing may import another EKI
			// Go package.
			if imported == self {
				continue
			}
			if imported == ekiModule || strings.HasPrefix(imported, ekiModule+"/") {
				t.Errorf("%s imports %q; the adapter must use only EKI's external contract", path, imported)
			}
		}
	}
	// A check that silently found no files would pass. Keep the file list
	// meaningful.
	for _, path := range files {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s disappeared during the check: %v", path, err)
		}
	}
}
