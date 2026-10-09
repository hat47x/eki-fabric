// Package productboundary turns two product-value boundaries into tests.
//
//   - ADR-0010 / RQ-038: EKI Core and its external contracts carry no
//     balance, transfer-of-value, price, exchange-rate, or credit-issuance
//     vocabulary. A Contribution Record states observed facts only.
//   - product/value.md "adjacent products": EKI Core depends on none of SUI,
//     SEI, or TEI, neither as a Go dependency nor as application vocabulary in
//     its language-neutral contracts.
//
// Nothing here is a style check. Each failure means a boundary the product
// promises has been crossed and the crossing should be reviewed, not silenced.
package productboundary

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

const repoRoot = "../.."

// valueTokens are word-level tokens that denote money-like semantics. They are
// matched against identifiers split into words, so "ExchangeRate",
// "exchange_rate" and "exchange-rate" all contain "exchange".
var valueTokens = []string{
	"currency", "price", "pricing", "exchange", "wallet", "balance", "money",
	"monetary", "cash", "payout", "reward", "bounty", "credit", "credits",
	"fiat", "invoice", "coin", "token_supply",
}

// valueAllowed lists identifiers that legitimately contain a value token.
// Each entry needs a reason; an empty list is the goal.
var valueAllowed = map[string]string{}

// adjacentTokens are application vocabulary of adjacent products. They must not
// appear in language-neutral contracts (product/value.md, Principles 7).
var adjacentTokens = []string{"hypothesis", "hypotheses", "sensemaking", "cognition"}

// forbiddenImports are module path fragments EKI Core must not depend on.
var forbiddenImports = []string{"sei-cognition", "sui-sensemaking", "/tei", "hat47x/sei", "hat47x/sui"}

var wordSplit = regexp.MustCompile(`[A-Z]+[a-z0-9]*|[a-z0-9]+`)

func words(identifier string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(identifier, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		for _, w := range wordSplit.FindAllString(part, -1) {
			out = append(out, strings.ToLower(w))
		}
	}
	return out
}

func containsToken(identifier string, tokens []string) (string, bool) {
	ws := words(identifier)
	for _, w := range ws {
		for _, t := range tokens {
			if w == t {
				return t, true
			}
		}
	}
	return "", false
}

func readFile(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(raw)
}

// contractFiles are the language-neutral contracts. Each must exist; a missing
// file would otherwise let this test pass vacuously.
var contractFiles = []string{
	"spec/external/openapi.json",
	"spec/worker/v1/worker.proto",
	"spec/decisions/v1/decisions.cddl",
	"spec/decisions/v1/cose-profile.cddl",
}

var identifierPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_\-]*`)

func TestContractsCarryNoValueVocabulary(t *testing.T) {
	for _, rel := range contractFiles {
		body := readFile(t, rel)
		for _, id := range identifierPattern.FindAllString(body, -1) {
			if _, ok := valueAllowed[id]; ok {
				continue
			}
			if tok, bad := containsToken(id, valueTokens); bad {
				t.Errorf("%s: identifier %q carries value vocabulary %q; Core contracts may state observed facts only (ADR-0010)", rel, id, tok)
			}
		}
	}
}

func TestContractsCarryNoAdjacentProductVocabulary(t *testing.T) {
	for _, rel := range contractFiles {
		body := readFile(t, rel)
		for _, id := range identifierPattern.FindAllString(body, -1) {
			if tok, bad := containsToken(id, adjacentTokens); bad {
				t.Errorf("%s: identifier %q carries adjacent-product vocabulary %q; EKI Core states compute semantics only", rel, id, tok)
			}
		}
	}
}

// goFiles returns non-test Go files under the given repo-relative roots.
func goFiles(t *testing.T, roots ...string) []string {
	t.Helper()
	var files []string
	for _, root := range roots {
		base := filepath.Join(repoRoot, root)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no Go files under %v; the test would pass vacuously", roots)
	}
	return files
}

// TestExportedAPIAndWireNamesCarryNoValueVocabulary covers what the contract
// files cannot: exported Go declarations and JSON/struct tags in Core and the
// Control Plane API.
func TestExportedAPIAndWireNamesCarryNoValueVocabulary(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range goFiles(t, "internal/core", "internal/control") {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.TypeSpec:
				check(t, path, v.Name.Name)
			case *ast.FuncDecl:
				check(t, path, v.Name.Name)
			case *ast.ValueSpec:
				for _, name := range v.Names {
					check(t, path, name.Name)
				}
			case *ast.Field:
				for _, name := range v.Names {
					check(t, path, name.Name)
				}
				if v.Tag != nil {
					tag, err := strconv.Unquote(v.Tag.Value)
					if err == nil {
						check(t, path, tag)
					}
				}
			}
			return true
		})
	}
}

func check(t *testing.T, path, identifier string) {
	t.Helper()
	if !ast.IsExported(identifier) && !strings.Contains(identifier, ":") {
		return
	}
	if _, ok := valueAllowed[identifier]; ok {
		return
	}
	for _, id := range identifierPattern.FindAllString(identifier, -1) {
		if tok, bad := containsToken(id, valueTokens); bad {
			t.Errorf("%s: %q carries value vocabulary %q; Core states observed facts only (ADR-0010)", path, identifier, tok)
			return
		}
	}
}

func TestCoreImportsNoAdjacentProduct(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range goFiles(t, "internal", "cmd") {
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			lower := strings.ToLower(p)
			for _, bad := range forbiddenImports {
				if strings.Contains(lower, bad) {
					t.Errorf("%s imports %q; EKI must not depend on an adjacent product (dependency runs from the product to EKI only)", path, p)
				}
			}
		}
	}
}

func TestModuleRequiresNoAdjacentProduct(t *testing.T) {
	mod := strings.ToLower(readFile(t, "go.mod"))
	for _, bad := range forbiddenImports {
		if strings.Contains(mod, bad) {
			t.Errorf("go.mod mentions %q; EKI must not depend on an adjacent product", bad)
		}
	}
}
