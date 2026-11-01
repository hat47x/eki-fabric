package clientapi

// The P0 document is the Client's view of what the API can answer. This test
// walks the responses the handler returns that the document used to leave out
// (#239) and fails when a status is not declared for that operation, so the
// contract and the handler cannot drift apart unnoticed.

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// specOperation returns the operation object for one method and path template
// from openapi.json.
func specOperation(t *testing.T, method, template string) map[string]any {
	t.Helper()
	if specCache == nil {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "spec", "external", "openapi.json"))
		if err != nil {
			t.Fatalf("read openapi.json: %v", err)
		}
		if err := json.Unmarshal(raw, &specCache); err != nil {
			t.Fatalf("parse openapi.json: %v", err)
		}
	}
	paths := specCache["paths"].(map[string]any)
	item, ok := paths[template].(map[string]any)
	if !ok {
		t.Fatalf("path %q is not declared in openapi.json", template)
	}
	operation, ok := item[strings.ToLower(method)].(map[string]any)
	if !ok {
		t.Fatalf("%s %s is not declared in openapi.json", method, template)
	}
	return operation
}

func TestEveryResponseTheHandlerReturnsIsDeclaredInTheSpec(t *testing.T) {
	e := newEnvWith(t, newFakeResults())
	queued := submitFor(t, e, aliceToken, "key-declared-001")
	cancelled := submitFor(t, e, aliceToken, "key-declared-002")
	if res := e.cancel(aliceToken, cancelled, "key-declared-003"); res.status != http.StatusAccepted {
		t.Fatalf("cancel status = %d", res.status)
	}

	cases := []struct {
		name string
		// declared is the operation the status must be declared under. It is
		// the request method except for 405, where the request method is the
		// one the resource refuses.
		declared string
		template string
		res      response
		code     string
	}{
		{
			name:     "missing credentials",
			declared: http.MethodGet,
			template: "/v1/tasks/{task_id}",
			res:      e.get("", queued),
			code:     "AUTHENTICATION_REQUIRED",
		},
		{
			name:     "wrong method",
			declared: http.MethodGet,
			template: "/v1/tasks/{task_id}",
			res:      e.do(http.MethodPatch, "/v1/tasks/"+queued, aliceToken, nil, ""),
			code:     "METHOD_NOT_ALLOWED",
		},
		{
			name:     "artifact ingest is not implemented",
			declared: http.MethodPost,
			template: "/v1/artifacts/ingest-intents",
			res: e.do(http.MethodPost, "/v1/artifacts/ingest-intents", aliceToken,
				map[string]string{"Content-Type": jsonContentType}, "{}"),
			code: "NOT_IMPLEMENTED",
		},
		{
			name:     "artifact read is not implemented",
			declared: http.MethodGet,
			template: "/v1/artifacts/{artifact_id}",
			res:      e.do(http.MethodGet, "/v1/artifacts/art_00000000000000000000000000000001", aliceToken, nil, ""),
			code:     "NOT_IMPLEMENTED",
		},
		{
			name:     "result is not ready",
			declared: http.MethodGet,
			template: "/v1/tasks/{task_id}/result",
			res:      e.result(aliceToken, queued),
			code:     "RESULT_NOT_READY",
		},
		{
			name:     "result will never be available",
			declared: http.MethodGet,
			template: "/v1/tasks/{task_id}/result",
			res:      e.result(aliceToken, cancelled),
			code:     "RESULT_NOT_AVAILABLE",
		},
		{
			name:     "body is not json",
			declared: http.MethodPost,
			template: "/v1/tasks",
			res: e.do(http.MethodPost, "/v1/tasks", aliceToken,
				map[string]string{"Content-Type": "text/plain", "Idempotency-Key": "key-declared-009"}, "hello"),
			code: "UNSUPPORTED_MEDIA_TYPE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			operation := specOperation(t, tc.declared, tc.template)
			responses := operation["responses"].(map[string]any)
			status := strconv.Itoa(tc.res.status)
			if _, declared := responses[status]; !declared {
				declaredCodes := make([]string, 0, len(responses))
				for code := range responses {
					declaredCodes = append(declaredCodes, code)
				}
				t.Fatalf("%s %s answered %d, which is not declared (declared: %v)",
					tc.declared, tc.template, tc.res.status, declaredCodes)
			}
			p := decode(t, tc.res)
			if p["code"] != tc.code {
				t.Fatalf("code = %v, want %s", p["code"], tc.code)
			}
		})
	}
}

// A 409 from the result endpoint must explain the two cases in the contract:
// one clears with waiting, the other never does.
func TestResultConflictDescribesBothCases(t *testing.T) {
	operation := specOperation(t, http.MethodGet, "/v1/tasks/{task_id}/result")
	responses := operation["responses"].(map[string]any)
	conflict, ok := responses["409"].(map[string]any)
	if !ok {
		t.Fatal("the result endpoint declares no 409")
	}
	description, _ := conflict["description"].(string)
	for _, want := range []string{"RESULT_NOT_READY", "RESULT_NOT_AVAILABLE", "retryable"} {
		if !strings.Contains(description, want) {
			t.Errorf("the 409 description does not mention %q: %s", want, description)
		}
	}
}
