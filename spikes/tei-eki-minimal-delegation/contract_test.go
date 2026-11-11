package teideleg

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// These tests pin the findings this spike depends on to EKI's published
// contract. If EKI gains a field for the executable digest, an Attempt
// identifier, or a result body, they fail on purpose: the receipt's claims and
// the mapping table must be read again rather than silently kept.

type schemaNode struct {
	Description string                `json:"description"`
	Enum        []string              `json:"enum"`
	Properties  map[string]schemaNode `json:"properties"`
	Required    []string              `json:"required"`
}

type openAPIDoc struct {
	Paths      map[string]json.RawMessage `json:"paths"`
	Components struct {
		Schemas map[string]schemaNode `json:"schemas"`
	} `json:"components"`
}

func loadOpenAPI(t *testing.T) openAPIDoc {
	t.Helper()
	raw, err := os.ReadFile("../../spec/external/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc openAPIDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestContractHasNoExecutorDigestField(t *testing.T) {
	doc := loadOpenAPI(t)
	executor, ok := doc.Components.Schemas["ExecutorRequirement"]
	if !ok {
		t.Fatal("ExecutorRequirement is missing from the contract")
	}
	keys := make([]string, 0, len(executor.Properties))
	for k := range executor.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "id,version_constraint" {
		t.Fatalf("ExecutorRequirement properties = %v; if a digest field was added, revisit the receipt's claims", keys)
	}
}

func TestContractDoesNotPassParametersToWorkers(t *testing.T) {
	doc := loadOpenAPI(t)
	task := doc.Components.Schemas["TaskRequest"]
	params := task.Properties["parameters"]
	if !strings.Contains(params.Description, "not passed to workers") {
		t.Fatalf("TaskRequest.parameters no longer says parameters are not passed to workers; the recorded-only binding may have changed: %q", params.Description)
	}
}

func TestContractOnlyAdmitsEffectNone(t *testing.T) {
	doc := loadOpenAPI(t)
	reliability := doc.Components.Schemas["ReliabilityRequest"]
	got := reliability.Properties["effect_mode"].Enum
	if len(got) != 1 || got[0] != "none" {
		t.Fatalf("effect_mode enum = %v; this spike assumes a side-effect-free operation", got)
	}
}

func TestContractPublicProjectionHidesLeaseAndAttempt(t *testing.T) {
	doc := loadOpenAPI(t)
	for _, name := range []string{"TaskPublicStatus", "TaskResult"} {
		node, ok := doc.Components.Schemas[name]
		if !ok {
			t.Fatalf("%s is missing from the contract", name)
		}
		keys := make([]string, 0, len(node.Properties))
		for k := range node.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lower := strings.ToLower(k)
			if strings.Contains(lower, "lease") || strings.Contains(lower, "attempt_id") ||
				strings.Contains(lower, "worker") || strings.Contains(lower, "fence") {
				t.Errorf("%s exposes %q; the receipt may no longer need to call Attempt identity unavailable", name, k)
			}
		}
	}
	status := doc.Components.Schemas["TaskPublicStatus"]
	if _, ok := status.Properties["attempt_count"]; !ok {
		t.Error("TaskPublicStatus no longer exposes attempt_count; the attempt binding must be revisited")
	}
	result := doc.Components.Schemas["TaskResult"]
	if _, ok := result.Properties["output_digest"]; !ok {
		t.Error("TaskResult no longer exposes output_digest; the digest comparison would be impossible")
	}
}

func TestContractDeclaresTheRoutesTheSpikeUses(t *testing.T) {
	doc := loadOpenAPI(t)
	for _, path := range []string{
		"/v1/tasks",
		"/v1/tasks/{task_id}",
		"/v1/tasks/{task_id}/result",
		"/v1/artifacts/ingest-intents",
		"/v1/artifact-uploads/{upload_id}",
		"/v1/artifacts/{artifact_id}",
	} {
		if _, ok := doc.Paths[path]; !ok {
			t.Errorf("contract does not declare %s", path)
		}
	}
}
