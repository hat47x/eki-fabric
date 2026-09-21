package protocol

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func workerProto(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "worker", "v1", "worker.proto")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read worker proto: %v", err)
	}
	return string(raw)
}

func TestWorkerSchemaKeepsIdentityInAuthenticationContext(t *testing.T) {
	schema := workerProto(t)
	if regexp.MustCompile(`(?m)^\s*string\s+worker_id\s*=`).MatchString(schema) {
		t.Fatal("worker.proto must not let a message field grant Worker identity")
	}
}

func TestHeartbeatDoesNotContainLeaseAuthority(t *testing.T) {
	schema := workerProto(t)
	body := protoBlock(t, schema, "message", "Heartbeat")
	for _, forbidden := range []string{"expires_at", "renewal", "signed_lease"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("Heartbeat contains authority-bearing field %q", forbidden)
		}
	}
}

func TestSignedDecisionCarrierCarriesCOSEBytes(t *testing.T) {
	schema := workerProto(t)
	body := protoBlock(t, schema, "message", "SignedDecisionCarrier")
	if !regexp.MustCompile(`(?m)^\s*bytes\s+cose_sign1\s*=\s*2\s*;`).MatchString(body) {
		t.Fatal("SignedDecisionCarrier must carry COSE_Sign1 bytes")
	}
}

func TestWorkerServiceProvidesStreamingAndPollingCommandDelivery(t *testing.T) {
	schema := workerProto(t)
	service := protoBlock(t, schema, "service", "WorkerPlaneService")
	required := []string{
		"rpc OpenSession(",
		"rpc PostEvents(",
		"rpc PollCommands(",
		"rpc WatchCommands(",
		"returns (stream ControlCommand)",
	}
	for _, want := range required {
		if !strings.Contains(service, want) {
			t.Fatalf("WorkerPlaneService missing %q", want)
		}
	}
}

func TestEveryEnumHasUnspecifiedZero(t *testing.T) {
	schema := workerProto(t)
	enumStart := regexp.MustCompile(`(?m)^enum\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	for _, match := range enumStart.FindAllStringSubmatchIndex(schema, -1) {
		name := schema[match[2]:match[3]]
		body := blockFromOpenBrace(t, schema, strings.Index(schema[match[0]:], "{")+match[0])
		zero := regexp.MustCompile(`(?m)^\s*([A-Z][A-Z0-9_]*)\s*=\s*0\s*;`).FindAllStringSubmatch(body, -1)
		if len(zero) != 1 {
			t.Fatalf("enum %s has %d zero values, want 1", name, len(zero))
		}
		if !strings.HasSuffix(zero[0][1], "_UNSPECIFIED") {
			t.Fatalf("enum %s zero value %s must end in _UNSPECIFIED", name, zero[0][1])
		}
	}
}

func TestMessageFieldNumbersAreUnique(t *testing.T) {
	schema := workerProto(t)
	msgStart := regexp.MustCompile(`(?m)^message\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	fieldNumber := regexp.MustCompile(`=\s*([0-9]+)\s*;`)
	for _, match := range msgStart.FindAllStringSubmatchIndex(schema, -1) {
		name := schema[match[2]:match[3]]
		body := blockFromOpenBrace(t, schema, strings.Index(schema[match[0]:], "{")+match[0])
		seen := map[int]bool{}
		for _, f := range fieldNumber.FindAllStringSubmatch(body, -1) {
			n, err := strconv.Atoi(f[1])
			if err != nil {
				t.Fatalf("message %s field number %q: %v", name, f[1], err)
			}
			if seen[n] {
				t.Fatalf("message %s reuses field number %d", name, n)
			}
			seen[n] = true
		}
	}
}

func TestPostEventsBindsBatchToOneSession(t *testing.T) {
	schema := workerProto(t)
	request := protoBlock(t, schema, "message", "PostEventsRequest")
	if !regexp.MustCompile(`(?m)^\s*string\s+session_id\s*=\s*1\s*;`).MatchString(request) {
		t.Fatal("PostEventsRequest must bind the batch to one session")
	}
	event := protoBlock(t, schema, "message", "WorkerEvent")
	if regexp.MustCompile(`(?m)^\s*string\s+session_id\s*=`).MatchString(event) {
		t.Fatal("WorkerEvent must not override the batch session")
	}
}

func protoBlock(t *testing.T, schema, kind, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(kind) + `\s+` + regexp.QuoteMeta(name) + `\s*\{`)
	loc := re.FindStringIndex(schema)
	if loc == nil {
		t.Fatalf("%s %s not found", kind, name)
	}
	open := strings.Index(schema[loc[0]:loc[1]], "{") + loc[0]
	return blockFromOpenBrace(t, schema, open)
}

func blockFromOpenBrace(t *testing.T, schema string, open int) string {
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
	t.Fatal("unterminated protobuf block")
	return ""
}
