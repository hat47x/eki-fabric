package service

import (
	"errors"
	"testing"
)

// A class outside the vocabulary is refused at start-up rather than stored: a value
// nobody defined would look like a classification to everyone downstream
// (ADR-0034 §10.2).
func TestAnUnknownWorkerDataClassStopsTheProcess(t *testing.T) {
	cfg := Config{
		Listen:   "127.0.0.1:0",
		Database: "state.db",
		Worker:   &WorkerConfig{Auth: "mtls", DataClass: "secret"},
	}
	err := cfg.Validate()
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidConfig)
	}
}
