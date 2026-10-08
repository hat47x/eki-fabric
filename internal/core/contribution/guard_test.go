package contribution

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

const modulePath = "github.com/hat47x/eki-fabric"

// forbiddenMemberFragments are name fragments that would mean the record carries
// money, credit, or a score. Matching is case-insensitive and applies to field
// and method names, including unexported ones.
var forbiddenMemberFragments = []string{
	"currency", "price", "rate", "exchange", "score", "credit", "money",
	"monetary", "cash", "amount", "reputation", "payment", "payout", "reward",
	"yen", "dollar", "usd", "market", "point",
}

// TestExportedTypesCarryNoMonetaryOrScoreMembers checks every exported type by
// reflection. It covers fields and the method set of the pointer type, which
// includes value methods.
func TestExportedTypesCarryNoMonetaryOrScoreMembers(t *testing.T) {
	values := []any{
		Record{}, RecordInput{}, Observation{}, Observer{}, Quantity{},
		AcceptedResultRef{}, Correction{}, AuditView{}, MetricDefinition{},
	}
	check := func(typeName, member string) {
		lower := strings.ToLower(member)
		for _, fragment := range forbiddenMemberFragments {
			if strings.Contains(lower, fragment) {
				t.Errorf("%s has member %q, which matches forbidden fragment %q", typeName, member, fragment)
			}
		}
	}
	for _, v := range values {
		typ := reflect.TypeOf(v)
		for i := 0; i < typ.NumField(); i++ {
			check(typ.Name(), typ.Field(i).Name)
		}
		ptr := reflect.New(typ).Type()
		for i := 0; i < ptr.NumMethod(); i++ {
			check(typ.Name(), ptr.Method(i).Name)
		}
	}
}

// TestPackageSourceStaysPureAndDependencyFree parses the non-test source files and
// checks three things. Imports must be standard library or internal/core only.
// The package must not touch I/O packages. It must not read the clock or start
// goroutines.
func TestPackageSourceStaysPureAndDependencyFree(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: unquote import: %v", name, err)
			}
			if strings.HasPrefix(path, modulePath+"/") && !strings.HasPrefix(path, modulePath+"/internal/core/") {
				t.Errorf("%s imports %q, which is outside internal/core", name, path)
			}
			if !strings.HasPrefix(path, modulePath+"/") && strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				t.Errorf("%s imports non-standard-library package %q", name, path)
			}
			if forbiddenStdlib[path] {
				t.Errorf("%s imports %q, which performs I/O, reads the environment, or manages concurrency", name, path)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s starts a goroutine", name)
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" && x.Sel.Name == "Now" {
					t.Errorf("%s reads the clock with time.Now", name)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatalf("no non-test source files were checked")
	}
}

var forbiddenStdlib = map[string]bool{
	"os":            true,
	"os/exec":       true,
	"io":            true,
	"io/fs":         true,
	"net":           true,
	"net/http":      true,
	"path/filepath": true,
	"log":           true,
	"context":       true,
	"sync":          true,
	"runtime":       true,
	"syscall":       true,
	"database/sql":  true,
}
