package teiprojection

import (
	"os"
	"strings"
	"testing"
)

const valid = `{
  "schema": "tai.reference.tei-eki-task-request-projection/v0",
  "contract": {"id": "eki.task-request.projection", "version": "1"},
  "source": {"teiCommit": "abc", "contract": {"contractId": "eki.task-request.record", "contractVersion": "1", "contractDigest": "sha256:x", "sourceDigest": "sha256:y", "cases": 4}},
  "taskRequest": {"schema": "eki.task-request/v1", "executorId": "dev.example.transform", "versionConstraint": "^1", "priority": "normal"}
}`

func TestParseAcceptsValidArtifact(t *testing.T) {
	artifact, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if artifact.TaskRequest.ExecutorID != "dev.example.transform" {
		t.Fatalf("executorId = %q", artifact.TaskRequest.ExecutorID)
	}
}

func TestParseRejectsWrongSchema(t *testing.T) {
	_, err := Parse([]byte(strings.Replace(valid, ArtifactSchema, "other", 1)))
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseRejectsMissingExecutor(t *testing.T) {
	broken := strings.Replace(valid, `"executorId": "dev.example.transform"`, `"executorId": ""`, 1)
	if _, err := Parse([]byte(broken)); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseRealArtifactWhenProvided(t *testing.T) {
	path := os.Getenv("TEI_EKI_PROJECTION_ARTIFACT")
	if path == "" {
		t.Skip("TEI_EKI_PROJECTION_ARTIFACT is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	artifact, err := Parse(data)
	if err != nil {
		t.Fatalf("parse artifact: %v", err)
	}
	if artifact.TaskRequest.ExecutorID == "" {
		t.Fatal("executorId is empty")
	}
}
