package grant

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// moneyTerms are substrings that would suggest a currency, a price, a credit,
// or an exchange rate. Matching is case-insensitive.
var moneyTerms = []string{
	"currency", "price", "pricing", "credit", "exchange", "money", "monetar",
	"cash", "wallet", "coin", "cost", "fee", "token", "rate", "payment",
}

func moneyTermIn(name string) (string, bool) {
	lower := strings.ToLower(name)
	for _, term := range moneyTerms {
		if strings.Contains(lower, term) {
			return term, true
		}
	}
	return "", false
}

// TestExportedTypesCarryNoMoneyFields uses reflection to check every exported
// type's name, field names, field type names, and exported method names.
func TestExportedTypesCarryNoMoneyFields(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(Identity("")),
		reflect.TypeOf(ResourceClass("")),
		reflect.TypeOf(Purpose("")),
		reflect.TypeOf(ReasonCategory("")),
		reflect.TypeOf(ResourceCeiling{}),
		reflect.TypeOf(Spec{}),
		reflect.TypeOf(Revocation{}),
		reflect.TypeOf(RecordedRevocation{}),
		reflect.TypeOf(Ledger{}),
		reflect.TypeOf(Grant{}),
		reflect.TypeOf(Request{}),
		reflect.TypeOf(ReclaimConfig{}),
		reflect.TypeOf(ReclaimAssignment{}),
	}
	check := func(what, name string) {
		t.Helper()
		if term, ok := moneyTermIn(name); ok {
			t.Errorf("%s %q suggests money through %q", what, name, term)
		}
	}
	for _, typ := range types {
		check("type", typ.Name())
		for i := 0; i < typ.NumMethod(); i++ {
			check("method of "+typ.Name(), typ.Method(i).Name)
		}
		if typ.Kind() != reflect.Struct {
			continue
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			check("field of "+typ.Name(), f.Name)
			ft := f.Type
			for ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array || ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			check("field type of "+typ.Name(), ft.Name())
		}
	}
}

// packageSources parses every non-test Go file in the package directory.
func packageSources(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("ParseFile(%s) error = %v", name, err)
		}
		files[name] = f
	}
	if len(files) == 0 {
		t.Fatal("no package sources found")
	}
	return files
}

// TestPackageHasNoClockIOOrGoroutines checks the imports and the call sites in
// the non-test sources. Time comes in as an input, and the package does no I/O.
func TestPackageHasNoClockIOOrGoroutines(t *testing.T) {
	allowedImports := map[string]bool{
		"cmp":             true,
		"crypto/sha256":   true,
		"encoding/binary": true,
		"errors":          true,
		"fmt":             true,
		"slices":          true,
		"time":            true,
		"github.com/hat47x/eki-fabric/internal/core/contract": true,
	}
	bannedTimeCalls := map[string]bool{
		"Now": true, "Since": true, "Until": true, "Sleep": true, "After": true,
		"AfterFunc": true, "NewTimer": true, "NewTicker": true, "Tick": true,
	}

	files := packageSources(t)
	for name, file := range files {
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unreadable import %s", name, imp.Path.Value)
			}
			if !allowedImports[path] {
				t.Errorf("%s imports %q, which this package must not use", name, path)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s starts a goroutine", name)
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" && bannedTimeCalls[x.Sel.Name] {
					t.Errorf("%s calls time.%s, which reads the clock or waits", name, x.Sel.Name)
				}
			}
			return true
		})
	}
}

// TestPackageIdentifiersCarryNoMoneyTerms checks the names of every exported
// top-level identifier in the non-test sources, including constants and
// functions that reflection on types would not reach.
func TestPackageIdentifiersCarryNoMoneyTerms(t *testing.T) {
	files := packageSources(t)
	for name, file := range files {
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name.IsExported() {
					if term, ok := moneyTermIn(d.Name.Name); ok {
						t.Errorf("%s: function %s suggests money through %q", name, d.Name.Name, term)
					}
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							if term, ok := moneyTermIn(s.Name.Name); ok {
								t.Errorf("%s: type %s suggests money through %q", name, s.Name.Name, term)
							}
						}
					case *ast.ValueSpec:
						for _, id := range s.Names {
							if id.IsExported() {
								if term, ok := moneyTermIn(id.Name); ok {
									t.Errorf("%s: %s suggests money through %q", name, id.Name, term)
								}
							}
						}
					}
				}
			}
		}
	}
}

// TestSourceIsASCII keeps every Go file in the package to ASCII, so that
// non-ASCII test values appear only as byte escapes.
func TestSourceIsASCII(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", e.Name(), err)
		}
		for i, b := range data {
			if b >= 0x80 {
				t.Fatalf("%s has a non-ASCII byte at offset %d", e.Name(), i)
			}
		}
	}
}
