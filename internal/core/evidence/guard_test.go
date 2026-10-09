package evidence

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The guards in this file read the source of the package with go/ast. Imports are
// resolved by import path, so an alias cannot hide a package, and every package-level
// type, field, interface method, and method is enumerated, exported or not. Reflection
// misses unexported methods, so it is used only for the primary metric tree. A reviewer
// can read the classification below and see every type and member that the package
// declares. The fragment check is a name check, and it is a second line of defense
// behind the reviewed classification, not the main one.

// forbiddenMemberFragments are name fragments that would mean the package carries
// money, credit, or a score. Matching is case-insensitive. The fragments are checked
// against every package-level name, struct field, interface method, and method,
// exported or not.
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

// allowedImports is the import allowlist for the non-test files of this package. It is
// exactly the set of packages the package uses today. Any other import fails the guard,
// including standard-library packages that do I/O, read the environment, or start
// concurrency, such as mime, expvar, net/http/pprof, and go/build. Adding a package is a
// review decision. The allowed packages are then checked member by member; see
// forbiddenTimeMembers, forbiddenSelectorNames, and allowedFmtMembers.
var allowedImports = map[string]bool{
	"cmp":          true,
	"errors":       true,
	"fmt":          true,
	"slices":       true,
	"strings":      true,
	"time":         true,
	"unicode":      true,
	"unicode/utf8": true,
}

// forbiddenTimeMembers are the members of package time that read the clock, wait on it,
// start timers, or load zone data from disk. Timestamps arrive as input, so none of
// these may be used. Package time itself stays importable for its value types.
var forbiddenTimeMembers = map[string]bool{
	"Now":          true,
	"Since":        true,
	"Until":        true,
	"Sleep":        true,
	"After":        true,
	"AfterFunc":    true,
	"Tick":         true,
	"NewTimer":     true,
	"NewTicker":    true,
	"LoadLocation": true,
}

// forbiddenSelectorNames are selector names that are forbidden on any receiver, not
// only on package time. time.Local and the method time.Time.Local both switch a value
// to the process-local zone, which the environment decides.
var forbiddenSelectorNames = map[string]bool{
	"Local": true,
}

// allowedFmtMembers are the members of package fmt that only build strings or parse
// in-memory input. Printing to a stream and scanning from a reader are not allowed.
// The list is an allowlist, so a new fmt function stays forbidden until it is reviewed.
var allowedFmtMembers = map[string]bool{
	"Append":     true,
	"Appendf":    true,
	"Appendln":   true,
	"Errorf":     true,
	"Sprint":     true,
	"Sprintf":    true,
	"Sprintln":   true,
	"Sscan":      true,
	"Sscanf":     true,
	"Sscanln":    true,
	"Formatter":  true,
	"GoStringer": true,
	"State":      true,
	"Stringer":   true,
}

// forbiddenBuiltins are builtin functions that write to standard error.
var forbiddenBuiltins = map[string]bool{
	"print":   true,
	"println": true,
}

// typeRole says what a declared type is for. Each role is a review decision recorded by
// hand in classifiedTypes.
type typeRole int

const (
	roleInterface   typeRole = iota + 1 // the closed Observation interface
	roleObservation                     // a kind of record in the append-only log
	roleValue                           // a closed string set
	rolePrimary                         // part of the primary metric tree
	roleSecondary                       // the auxiliary metric
	roleInternal                        // a derivation helper that callers never receive
)

// typeSpec is the reviewed description of one package-level type. members lists every
// field, embedded type, interface method, and method of the type, exported or not. Any
// member that is not listed fails the guard, so a new member cannot enter the package
// without a review.
type typeSpec struct {
	role    typeRole
	members []string
}

// classifiedTypes is the hand-kept classification. Every package-level type, exported or
// not, must appear here. A type that is missing, or whose members differ from the list,
// fails TestEveryPackageLevelTypeIsClassifiedAndItsMembersAreKnown.
var classifiedTypes = map[string]typeSpec{
	"Observation": {roleInterface, []string{"ObservationID", "observation"}},

	"GovernorReclaim": {roleObservation, []string{
		"ID", "WorkerID", "ReclaimStart", "ReclaimCompleted", "ObservationID", "observation",
	}},
	"ResultDecision": {roleObservation, []string{
		"ID", "ResultID", "Accepted", "Verified", "VerificationRecordID", "ObservationID", "observation",
	}},
	"SuitabilityDecision": {roleObservation, []string{"ID", "Outcome", "ObservationID", "observation"}},
	"RetryEvent": {roleObservation, []string{
		"ID", "Cause", "AuthorityGrew", "DuplicateExecution", "ObservationID", "observation",
	}},
	"ContributionRecord": {roleObservation, []string{
		"ID", "WorkerID", "Verified", "SupportingFactIDs", "ObservationID", "observation",
	}},

	"SuitabilityOutcome": {roleValue, []string{"Valid"}},
	"RetryCause":         {roleValue, []string{"Valid"}},

	"buckets": {roleInternal, []string{"reclaims", "results", "outcomes", "retries", "contribs"}},

	"Primary": {rolePrimary, []string{
		"ReclaimLatency", "VerifiedResults", "SuitabilityOutcomes", "RetryAuthorityGrowth", "VerifiedContributions",
	}},
	"ReclaimLatency":  {rolePrimary, []string{"SourceIDs", "Samples", "Pending", "Percentile"}},
	"ReclaimSample":   {rolePrimary, []string{"ObservationID", "WorkerID", "Latency"}},
	"EvidenceLink":    {rolePrimary, []string{"ObservationID", "EvidenceID"}},
	"VerifiedResults": {rolePrimary, []string{"SourceIDs", "Accepted", "Verified", "UnverifiedAccepted", "VerificationRecords", "Share"}},
	"SuitabilityCounts": {rolePrimary, []string{
		"SourceIDs", "Justified", "NotJustified", "NeedsMeasurement", "Count",
	}},
	"RetryAuthorityGrowth": {rolePrimary, []string{
		"SourceIDs", "AuthorityGrew", "DuplicateExecution", "Harmful", "ByCause", "Count", "HarmfulCount", "InvariantHolds",
	}},
	"RetryCauseCounts":      {rolePrimary, []string{"Reclaim", "WorkerDeparture", "Other", "Count"}},
	"VerifiedContributions": {rolePrimary, []string{"SourceIDs", "Verified", "SupportingFacts", "Count"}},

	"Secondary": {roleSecondary, []string{"SourceIDs", "WorkerCount"}},
}

// namedAt is one declared name with its position, for the fragment check.
type namedAt struct {
	name string
	pos  token.Pos
}

// declarations is what go/ast finds in the non-test files of the package.
type declarations struct {
	// types maps each package-level type name to the position of its declaration.
	types map[string]token.Pos
	// members maps each type name to its fields, embedded types, interface methods, and
	// methods, whatever their export status.
	members map[string]map[string]bool
	// observation marks each type that declares the observation marker method, which
	// closes the Observation set.
	observation map[string]bool
	// names lists every package-level name, field, interface method, and method.
	names []namedAt
}

// parseSourceFiles parses every non-test Go file of the package. The guard reads source,
// not compiled code, so it sees the same text that a reviewer sees.
func parseSourceFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatalf("no non-test source files were checked")
	}
	return fset, files
}

// defaultImportName returns the name that a package binds when it is imported without an
// alias. The last path element is used, except for a major-version suffix such as "v2",
// where the element before it is used.
func defaultImportName(path string) string {
	elems := strings.Split(path, "/")
	last := elems[len(elems)-1]
	if len(elems) > 1 && isMajorVersion(last) {
		return elems[len(elems)-2]
	}
	return last
}

func isMajorVersion(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// checkImports checks each import of one file against allowedImports and returns the
// package name that each import binds in that file. Names come from the import path, so an
// alias cannot hide a package. A dot import would make package members look unqualified,
// so it is refused.
func checkImports(t *testing.T, fset *token.FileSet, file *ast.File) map[string]string {
	t.Helper()
	bound := map[string]string{}
	for _, imp := range file.Imports {
		where := fset.Position(imp.Pos())
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("%s: unquote import: %v", where, err)
		}
		if !allowedImports[path] {
			t.Errorf("%s: imports %q, which is not on the import allowlist; review it for I/O, environment, clock, concurrency, and randomness before adding it to allowedImports", where, path)
		}
		name := defaultImportName(path)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch name {
		case "_":
			continue // a side-effect import binds no name
		case ".":
			t.Errorf("%s: dot-imports %q, which hides the package qualifier from the name checks", where, path)
		}
		bound[name] = path
	}
	return bound
}

// checkUses walks one file and rejects the constructs that would add a side effect or
// read the clock or the environment. A selector whose base name is bound to an import is
// resolved through that import's path, so an aliased import is checked like any other.
func checkUses(t *testing.T, fset *token.FileSet, file *ast.File, bound map[string]string) {
	t.Helper()
	selected := map[*ast.Ident]bool{} // identifiers that are the .Sel of a selector
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GoStmt:
			t.Errorf("%s: starts a goroutine", fset.Position(x.Pos()))
		case *ast.FuncDecl:
			if x.Recv == nil && x.Name.Name == "init" {
				t.Errorf("%s: declares init, which runs code when the package is imported", fset.Position(x.Pos()))
			}
		case *ast.SelectorExpr:
			selected[x.Sel] = true
			where := fset.Position(x.Pos())
			if forbiddenSelectorNames[x.Sel.Name] {
				t.Errorf("%s: uses .%s, which depends on the process-local time zone", where, x.Sel.Name)
			}
			base, ok := x.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch bound[base.Name] {
			case "time":
				if forbiddenTimeMembers[x.Sel.Name] {
					t.Errorf("%s: uses time.%s, which reads or waits on the clock or loads zone data", where, x.Sel.Name)
				}
			case "fmt":
				if !allowedFmtMembers[x.Sel.Name] {
					t.Errorf("%s: uses fmt.%s, which is not on the allowlist of string-only functions", where, x.Sel.Name)
				}
			}
		case *ast.Ident:
			if forbiddenBuiltins[x.Name] && !selected[x] {
				t.Errorf("%s: uses builtin %s, which writes to standard error", fset.Position(x.Pos()), x.Name)
			}
		}
		return true
	})
}

// typeExprName returns the name a type expression declares, without a package qualifier
// or a pointer. It returns "" for an expression it does not recognize.
func typeExprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return typeExprName(x.X)
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// fieldNames returns the names that a struct field or an interface element declares. An
// embedded field has no name of its own, so its type name is used.
func fieldNames(f *ast.Field) []string {
	if len(f.Names) == 0 {
		return []string{typeExprName(f.Type)}
	}
	out := make([]string, 0, len(f.Names))
	for _, n := range f.Names {
		out = append(out, n.Name)
	}
	return out
}

// enumerate walks every package-level declaration with go/ast. It records each type with
// its members, the methods of every receiver, and every name for the fragment check. It
// does not decide anything itself, so the checks stay in the tests.
func enumerate(files []*ast.File) declarations {
	d := declarations{
		types:       map[string]token.Pos{},
		members:     map[string]map[string]bool{},
		observation: map[string]bool{},
	}
	addMember := func(typeName, member string, pos token.Pos) {
		if d.members[typeName] == nil {
			d.members[typeName] = map[string]bool{}
		}
		d.members[typeName][member] = true
		d.names = append(d.names, namedAt{member, pos})
	}
	for _, file := range files {
		for _, decl := range file.Decls {
			switch x := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range x.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						name := s.Name.Name
						d.types[name] = s.Name.Pos()
						d.names = append(d.names, namedAt{name, s.Name.Pos()})
						if d.members[name] == nil {
							d.members[name] = map[string]bool{}
						}
						switch typ := s.Type.(type) {
						case *ast.StructType:
							for _, f := range typ.Fields.List {
								for _, m := range fieldNames(f) {
									addMember(name, m, f.Pos())
								}
							}
						case *ast.InterfaceType:
							for _, f := range typ.Methods.List {
								for _, m := range fieldNames(f) {
									addMember(name, m, f.Pos())
								}
							}
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							d.names = append(d.names, namedAt{n.Name, n.Pos()})
						}
					}
				}
			case *ast.FuncDecl:
				if x.Recv == nil {
					d.names = append(d.names, namedAt{x.Name.Name, x.Name.Pos()})
					continue
				}
				// A method is recorded once, as a member of its receiver type.
				recv := typeExprName(x.Recv.List[0].Type)
				addMember(recv, x.Name.Name, x.Name.Pos())
				if x.Name.Name == "observation" {
					d.observation[recv] = true
				}
			}
		}
	}
	return d
}

// TestPackageSourceStaysPureAndDependencyFree checks the non-test files of the package in
// two steps. First, every import must be on allowedImports. Then, within the allowed
// packages, package time may not use the members in forbiddenTimeMembers, package fmt may
// use only the members in allowedFmtMembers, no selector may name a member in
// forbiddenSelectorNames, the print and println builtins are refused, and the package may
// not declare init or start a goroutine.
func TestPackageSourceStaysPureAndDependencyFree(t *testing.T) {
	fset, files := parseSourceFiles(t)
	for _, file := range files {
		bound := checkImports(t, fset, file)
		checkUses(t, fset, file, bound)
	}
}

// TestDeclaredNamesCarryNoMonetaryOrScoreFragments checks every package-level name, every
// struct field, every interface method, and every method, exported or not, against the
// forbidden fragments.
func TestDeclaredNamesCarryNoMonetaryOrScoreFragments(t *testing.T) {
	fset, files := parseSourceFiles(t)
	d := enumerate(files)
	for _, n := range d.names {
		lower := strings.ToLower(n.name)
		for _, fragment := range forbiddenMemberFragments {
			if strings.Contains(lower, fragment) {
				t.Errorf("%s: %q matches forbidden fragment %q", fset.Position(n.pos), n.name, fragment)
			}
		}
	}
}

// TestEveryPackageLevelTypeIsClassifiedAndItsMembersAreKnown requires every package-level
// type, exported or not, to be classified. It also requires the members of each type to
// match the reviewed list exactly. Finally, the set of types that declare the observation
// marker must equal the set of observation kinds, so the Observation set stays closed.
func TestEveryPackageLevelTypeIsClassifiedAndItsMembersAreKnown(t *testing.T) {
	fset, files := parseSourceFiles(t)
	d := enumerate(files)

	for _, name := range slices.Sorted(maps.Keys(d.types)) {
		where := fset.Position(d.types[name])
		spec, ok := classifiedTypes[name]
		if !ok {
			kind := "unexported"
			if ast.IsExported(name) {
				kind = "exported"
			}
			t.Errorf("%s: %s type %s is not classified; review it and add it to classifiedTypes", where, kind, name)
			continue
		}
		got := slices.Sorted(maps.Keys(d.members[name]))
		want := slices.Clone(spec.members)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: members of %s are %v, want %v; review any new member for purity, then update classifiedTypes", where, name, got, want)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(classifiedTypes)) {
		if _, ok := d.types[name]; !ok {
			t.Errorf("classified type %s is not declared in the package; remove the stale entry", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(d.members)) {
		if _, ok := d.types[name]; !ok {
			t.Errorf("members %v are declared on %q, which is not a package-level type", slices.Sorted(maps.Keys(d.members[name])), name)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(d.observation)) {
		if classifiedTypes[name].role != roleObservation {
			t.Errorf("type %s declares the observation marker but is not classified as an observation kind", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(classifiedTypes)) {
		if classifiedTypes[name].role == roleObservation && !d.observation[name] {
			t.Errorf("type %s is classified as an observation kind but has no observation marker method", name)
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
