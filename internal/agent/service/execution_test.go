package service

import (
	"log/slog"
	"testing"
)

func TestExecutionHandlerRequiresCommand(t *testing.T) {
	if _, err := newExecutionHandler(nil, &eventLog{}, slog.Default()); err == nil {
		t.Fatal("nil execution config was accepted")
	}
	if _, err := newExecutionHandler(&ExecutionConfig{}, &eventLog{}, slog.Default()); err == nil {
		t.Fatal("empty command was accepted")
	}
}

func TestExecutionHandlerBuildsRunner(t *testing.T) {
	handler, err := newExecutionHandler(&ExecutionConfig{Command: "echo", Args: []string{"hello"}}, &eventLog{}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if handler.runner == nil || handler.supervisor == nil {
		t.Fatal("runner or supervisor is nil")
	}
}
