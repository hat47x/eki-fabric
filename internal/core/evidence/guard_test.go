package evidence

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

// forbiddenMemberFragments are name fragments that would mean an exported type
// carries money, credit, or a score. Matching is case-insensitive and covers fields
// and methods, including unexported ones.
var forbiddenMemberFragments = []string{
	"currency", "price", "rate", "exchange", "score", "credit", "money",
	"monetary", "cash", "amount", "reputation", "payment", "payout", "reward",
	"yen", "dollar", "usd", "market", "point",
}

// secondaryMemberFragments name metrics that ADR-0008 keeps out of the primary set.
// A primary type must not carry them, so a flops or utilization number cannot
// appear as a primary metric.
var secondaryMemberFragments = []string{
	"flops", "utilization", "throughput", "workercount", "secondary",
}

// allExportedTypes lists every exported type in the package, so the guards below
// cover the whole surface rather than a hand-picked subset.
func allExportedTypes() []any {
	return []any{
		GovernorReclaim{}, ResultDecision{}, SuitabilityDecision{}, RetryEvent{}, ContributionRecord{},
		Primary{}, ReclaimLatency{}, ReclaimSample{}, VerifiedResults{}, SuitabilityCounts{},
		RetryAuthorityGrowth{}, VerifiedContributions{}, Secondary{},
	}
}

// TestExportedTypesCarryNoMonetaryOrScoreMembers checks every exported type by
// reflection, including the method set of the pointer type.
func TestExportedTypesCarryNoMonetaryOrScoreMembers(t *testing.T) {
	check := func(typeName, member string) {
		lower := strings.ToLower(member)
		for _, fragment := range forbiddenMemberFragments {
			if strings.Contains(lower, fragment) {
				t.Errorf("%s has member %q, which matches forbidden fragment %q", typeName, member, fragment)
			}
		}
	}
	for _, v := range allExportedTypes() {
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

// TestPrimaryMetricsCarryNoSecondaryMembers checks that the primary metric tree
// contains no secondary-style members and does not embed the Secondary type.
func TestPrimaryMetricsCarryNoSecondaryMembers(t *testing.T) {
	secondaryType := reflect.TypeOf(Secondary{})
	var visit func(typ reflect.Type, path string, seen map[reflect.Type]bool)
	visit = func(typ reflect.Type, path string, seen map[reflect.Type]bool) {
		for typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		if typ == secondaryType {
			t.Errorf("%s embeds Secondary, which must stay a separate type", path)
			return
		}
		if seen[typ] || typ.Kind() != reflect.Struct {
			return
		}
		seen[typ] = true
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			lower := strings.ToLower(field.Name)
			for _, fragment := range secondaryMemberFragments {
				if strings.Contains(lower, fragment) {
					t.Errorf("%s has field %q, which matches secondary fragment %q", path, field.Name, fragment)
				}
			}
			visit(field.Type, path+"."+field.Name, seen)
		}
	}
	visit(reflect.TypeOf(Primary{}), "Primary", map[reflect.Type]bool{})
}

// forbiddenStdlib lists standard-library packages that perform I/O, read the
// environment, start concurrency, or introduce nondeterminism.
var forbiddenStdlib = map[string]bool{
	"os":            true,
	"os/exec":       true,
	"os/signal":     true,
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
	"math/rand":     true,
	"math/rand/v2":  true,
	"crypto/rand":   true,
	"plugin":        true,
}

// forbiddenTimeFuncs are functions in package time that read the clock, wait on
// it, or start timers. Timestamps arrive as input, so none of these may appear.
var forbiddenTimeFuncs = map[string]bool{
	"Now":       true,
	"Since":     true,
	"Until":     true,
	"Sleep":     true,
	"After":     true,
	"AfterFunc": true,
	"Tick":      true,
	"NewTimer":  true,
	"NewTicker": true,
}

// TestPackageSourceStaysPureAndDependencyFree parses the non-test source files and
// checks four things. The package imports only the standard library, and it does not
// import any package of this module, including statestore, control, or adapters.
// It must not touch the I/O, environment, or randomness packages. It must not read
// or wait on the clock. It must not start goroutines.
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
			if strings.HasPrefix(path, modulePath) {
				t.Errorf("%s imports %q, but evidence must not depend on any package of this module", name, path)
			}
			if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				t.Errorf("%s imports non-standard-library package %q", name, path)
			}
			if forbiddenStdlib[path] {
				t.Errorf("%s imports %q, which performs I/O, reads the environment, or manages concurrency or randomness", name, path)
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s starts a goroutine", name)
			case *ast.SelectorExpr:
				if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" && forbiddenTimeFuncs[x.Sel.Name] {
					t.Errorf("%s uses time.%s, which reads or waits on the clock", name, x.Sel.Name)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatalf("no non-test source files were checked")
	}
}
