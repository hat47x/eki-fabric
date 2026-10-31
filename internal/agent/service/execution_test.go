package service

import (
	"context"
	"log/slog"
	"testing"

	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type fakeClaims struct{}

func (fakeClaims) Enqueue(context.Context, resultclaim.Claim) (bool, error) { return false, nil }

var testPrincipal = workerplane.Principal{WorkerIdentity: "worker-test", TrustDomain: "td-test"}

func TestExecutionHandlerRequiresCommand(t *testing.T) {
	if _, err := newExecutionHandler(nil, &eventLog{}, slog.Default(), fakeClaims{}, testPrincipal); err == nil {
		t.Fatal("nil execution config was accepted")
	}
	if _, err := newExecutionHandler(&ExecutionConfig{}, &eventLog{}, slog.Default(), fakeClaims{}, testPrincipal); err == nil {
		t.Fatal("empty command was accepted")
	}
}

func TestExecutionHandlerRequiresClaimQueue(t *testing.T) {
	if _, err := newExecutionHandler(&ExecutionConfig{Command: "echo"}, &eventLog{}, slog.Default(), nil, testPrincipal); err == nil {
		t.Fatal("nil claim queue was accepted")
	}
}

func TestExecutionHandlerBuildsRunner(t *testing.T) {
	handler, err := newExecutionHandler(&ExecutionConfig{Command: "echo", Args: []string{"hello"}}, &eventLog{}, slog.Default(), fakeClaims{}, testPrincipal)
	if err != nil {
		t.Fatal(err)
	}
	if handler.runner == nil || handler.supervisor == nil || handler.claims == nil {
		t.Fatal("runner, supervisor, or claim queue is nil")
	}
}
