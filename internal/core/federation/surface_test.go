package federation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// packageSourceFiles returns the non-test Go files of the package.
func packageSourceFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var out []string
	for _, name := range files {
		if !strings.HasSuffix(name, "_test.go") {
			out = append(out, name)
		}
	}
	if len(out) == 0 {
		t.Fatal("no package source files found")
	}
	return out
}

// receiverName returns the base type name of a method receiver.
func receiverName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.StarExpr:
		return receiverName(x.X)
	case *ast.Ident:
		return x.Name
	default:
		return "?"
	}
}

// TestExportedSurfaceHasNoMergedPool compares the exported functions and methods
// of the package with an explicit allowlist. A new export must be added here on
// purpose, and a function that returns a union of bundles or a merged root pool
// cannot slip in unnoticed.
func TestExportedSurfaceHasNoMergedPool(t *testing.T) {
	allowed := map[string]bool{
		"ParseTrustDomain":     true,
		"NewLink":              true,
		"NewBundle":            true,
		"BundleSigningInput":   true,
		"IdentitySigningInput": true,
		"NewRegistry":          true,
		"ReasonOf":             true,

		"TrustDomain.String":     true,
		"TrustDomain.Valid":      true,
		"Purpose.Valid":          true,
		"Link.Domain":            true,
		"Link.AuthorityKey":      true,
		"Link.Purposes":          true,
		"Link.Allows":            true,
		"Bundle.Domain":          true,
		"Bundle.Sequence":        true,
		"Bundle.ValidatedAt":     true,
		"Bundle.Digest":          true,
		"Bundle.Keys":            true,
		"Digest.String":          true,
		"Binding.Domain":         true,
		"Binding.Bundle":         true,
		"Binding.Sequence":       true,
		"Binding.Digest":         true,
		"Binding.ValidatedAt":    true,
		"Binding.Purposes":       true,
		"Binding.Allows":         true,
		"Binding.Revoked":        true,
		"Binding.RevokedAt":      true,
		"Rejection.Error":        true,
		"Rejection.Unwrap":       true,
		"Registry.Federate":      true,
		"Registry.InstallBundle": true,
		"Registry.Revoke":        true,
		"Registry.Binding":       true,
		"Registry.Verify":        true,
	}

	fset := token.NewFileSet()
	got := map[string]bool{}
	for _, name := range packageSourceFiles(t) {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			key := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) > 0 {
				key = receiverName(fn.Recv.List[0].Type) + "." + key
			}
			got[key] = true
		}
	}

	for key := range got {
		if !allowed[key] {
			t.Errorf("exported %s is not on the surface allowlist; review it for a merged bundle pool before adding it", key)
		}
	}
	for key := range allowed {
		if !got[key] {
			t.Errorf("allowlisted %s is no longer exported", key)
		}
	}
	for key := range got {
		for _, bad := range []string{"Merge", "Union", "Pool", "Root", "Combined", "Bundles", "Domains"} {
			if strings.Contains(key, bad) {
				t.Errorf("exported %s contains %q, which suggests a merged trust pool", key, bad)
			}
		}
	}
}

// TestNoTypeMethodSuggestsAMergedPool checks the method sets of every exported
// type with reflection, in addition to the source scan above.
func TestNoTypeMethodSuggestsAMergedPool(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf((*Registry)(nil)),
		reflect.TypeOf((*Binding)(nil)),
		reflect.TypeOf((*Bundle)(nil)),
		reflect.TypeOf((*Link)(nil)),
		reflect.TypeOf((*TrustDomain)(nil)),
		reflect.TypeOf((*Digest)(nil)),
		reflect.TypeOf((*Rejection)(nil)),
		reflect.TypeOf((*Purpose)(nil)),
	}
	forbidden := []string{"Merge", "Union", "Pool", "Root", "Combined", "Bundles", "Domains"}
	for _, typ := range types {
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			for _, bad := range forbidden {
				if strings.Contains(name, bad) {
					t.Errorf("%s.%s suggests a merged trust pool", typ, name)
				}
			}
		}
	}
}

// TestPackageUsesOnlyAllowedImportsAndNoClockOrGoroutines enforces the package
// rules: standard library only, no clock reads, no timers, and no goroutines.
func TestPackageUsesOnlyAllowedImportsAndNoClockOrGoroutines(t *testing.T) {
	allowed := map[string]bool{
		"bytes":           true,
		"crypto/ed25519":  true,
		"crypto/sha256":   true,
		"encoding/binary": true,
		"encoding/hex":    true,
		"errors":          true,
		"fmt":             true,
		"slices":          true,
		"strings":         true,
		"sync":            true,
		"time":            true,
	}
	forbiddenTime := map[string]bool{
		"Now": true, "Since": true, "Until": true, "Sleep": true, "After": true,
		"AfterFunc": true, "Tick": true, "NewTimer": true, "NewTicker": true,
	}

	fset := token.NewFileSet()
	for _, name := range packageSourceFiles(t) {
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: import path %s: %v", name, imp.Path.Value, err)
			}
			if !allowed[path] {
				t.Errorf("%s imports %q, which is not on the allowlist", name, path)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s: goroutine started at %s", name, fset.Position(x.Pos()))
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" && forbiddenTime[x.Sel.Name] {
					t.Errorf("%s: time.%s at %s reads the clock or starts a timer", name, x.Sel.Name, fset.Position(x.Pos()))
				}
			}
			return true
		})
	}
}
