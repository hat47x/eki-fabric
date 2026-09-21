package protocol

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/authority"
)

func decisionCDDL(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "decisions", "v1", "decisions.cddl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read decision CDDL: %v", err)
	}
	return string(raw)
}

func TestDecisionKindsAreExplicitAndComplete(t *testing.T) {
	schema := decisionCDDL(t)
	want := []string{
		string(authority.DecisionArtifactReadGrant),
		string(authority.DecisionArtifactWriteGrant),
		string(authority.DecisionExecutorApproval),
		string(authority.DecisionLease),
		string(authority.DecisionPlacementApproval),
		string(authority.DecisionWorkOrder),
	}
	re := regexp.MustCompile(`"eki\.[a-z0-9.-]+/v1"`)
	foundMap := map[string]bool{}
	for _, raw := range re.FindAllString(schema, -1) {
		foundMap[strings.Trim(raw, `"`)] = true
	}
	found := make([]string, 0, len(foundMap))
	for v := range foundMap {
		found = append(found, v)
	}
	sort.Strings(found)
	if strings.Join(found, "\n") != strings.Join(want, "\n") {
		t.Fatalf("decision kinds:\n got: %v\nwant: %v", found, want)
	}
}

func TestAuthorityRolesUseArchitectureIdentifiers(t *testing.T) {
	schema := decisionCDDL(t)
	for _, role := range []string{
		string(authority.RoleExecutorAdmission),
		string(authority.RolePlacementAuthorization),
		string(authority.RoleLease),
		string(authority.RoleDataCapability),
		string(authority.RoleWorkOrder),
	} {
		if !strings.Contains(schema, `1: "`+role+`"`) {
			t.Fatalf("missing authority role %s", role)
		}
	}
	for _, forbidden := range []string{
		"executor-authority = {\n  1: 1,",
		"placement-authority = {\n  1: 4,",
		"lease-authority = {\n  1: 5,",
		"data-authority = {\n  1: 6,",
		"work-order-authority = {\n  1: 7,",
	} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("numeric authority alias reintroduced: %q", forbidden)
		}
	}
}

func TestWorkOrderUsesTypedPrerequisiteReferences(t *testing.T) {
	schema := decisionCDDL(t)
	body := cddlMapRule(t, schema, "work-order-body-v1")
	required := []string{
		"6: placement-approval-ref",
		"7: lease-ref",
		"8: executor-approval-ref",
		"10: [*64 artifact-read-grant-ref]",
		"11: [*16 artifact-write-grant-ref]",
	}
	for _, want := range required {
		if !strings.Contains(body, want) {
			t.Fatalf("work-order-body-v1 missing %q", want)
		}
	}
}

func TestReadGrantRequiresVerifiedContentDigest(t *testing.T) {
	schema := decisionCDDL(t)
	body := cddlMapRule(t, schema, "artifact-read-grant-body-v1")
	if !strings.Contains(body, "2: digest") {
		t.Fatal("artifact read grant must bind a content digest")
	}
}

func TestWriteGrantBindsMaximumBytes(t *testing.T) {
	schema := decisionCDDL(t)
	body := cddlMapRule(t, schema, "artifact-write-grant-body-v1")
	if !strings.Contains(body, "8: uint") {
		t.Fatal("artifact write grant must bind maximum bytes")
	}
}

func TestMVPWorkOrderHasNoExternalEffectCapability(t *testing.T) {
	schema := decisionCDDL(t)
	body := cddlMapRule(t, schema, "work-order-body-v1")
	if !strings.Contains(body, "14: 0") {
		t.Fatal("MVP work order effect mode must be fixed to none")
	}
}

func TestDecisionSchemaAvoidsFloatingPointAndRawLocations(t *testing.T) {
	schema := decisionCDDL(t)
	if regexp.MustCompile(`\\bfloat(?:16|32|64)?\\b`).MatchString(schema) {
		t.Fatal("decision schema must not use floating-point CDDL types")
	}
	for _, forbidden := range []string{"http://", "https://", "file://", "\\\\"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("decision schema contains raw location token %q", forbidden)
		}
	}
}

func TestSHA256DigestHasExactLength(t *testing.T) {
	schema := decisionCDDL(t)
	body := cddlMapRule(t, schema, "digest")
	if !strings.Contains(body, "1: 1") || !strings.Contains(body, "2: bstr .size 32") {
		t.Fatal("digest must be SHA-256 with exactly 32 raw bytes")
	}
}

func TestDecisionMapsDoNotReuseNumericKeys(t *testing.T) {
	schema := decisionCDDL(t)
	start := regexp.MustCompile(`(?m)^([a-z][a-z0-9-]*)\s*=\s*\{`)
	for _, loc := range start.FindAllStringSubmatchIndex(schema, -1) {
		name := schema[loc[2]:loc[3]]
		open := strings.Index(schema[loc[0]:loc[1]], "{") + loc[0]
		body := cddlBlockFromOpenBrace(t, schema, open)

		keyRe := regexp.MustCompile(`(?m)^\s*\??\s*([0-9]+)\s*:`)
		seen := map[string]bool{}
		for _, match := range keyRe.FindAllStringSubmatch(body, -1) {
			key := match[1]
			if seen[key] {
				t.Fatalf("CDDL map %s reuses numeric key %s", name, key)
			}
			seen[key] = true
		}
	}
}

func TestDecisionSchemaUsesBoundedCollections(t *testing.T) {
	schema := decisionCDDL(t)
	for _, want := range []string{
		"[*16 evidence-ref]",
		"[*64 artifact-read-grant-ref]",
		"[*16 artifact-write-grant-ref]",
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("missing bounded collection %q", want)
		}
	}
}

func cddlMapRule(t *testing.T, schema, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*=\s*\{`)
	loc := re.FindStringIndex(schema)
	if loc == nil {
		t.Fatalf("CDDL map rule %s not found", name)
	}
	open := strings.Index(schema[loc[0]:loc[1]], "{") + loc[0]
	return cddlBlockFromOpenBrace(t, schema, open)
}

func cddlBlockFromOpenBrace(t *testing.T, schema string, open int) string {
	t.Helper()
	depth := 0
	for i := open; i < len(schema); i++ {
		switch schema[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return schema[open+1 : i]
			}
		}
	}
	t.Fatal("unterminated CDDL map block")
	return ""
}
