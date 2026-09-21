package ids

import (
	"errors"
	"strings"
	"testing"
)

func TestTaskIDRoundTrip(t *testing.T) {
	id, err := NewTaskID()
	if err != nil {
		t.Fatalf("NewTaskID() error = %v", err)
	}
	if !strings.HasPrefix(id.String(), "tsk_") {
		t.Fatalf("TaskID prefix = %q", id)
	}
	parsed, err := ParseTaskID(id.String())
	if err != nil {
		t.Fatalf("ParseTaskID() error = %v", err)
	}
	if parsed != id {
		t.Fatalf("parsed = %q, want %q", parsed, id)
	}
}

func TestIDsRejectWrongPrefixAndMalformedSuffix(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) error
		in   string
	}{
		{"task wrong prefix", func(v string) error { _, err := ParseTaskID(v); return err }, "atm_0123456789abcdef0123456789abcdef"},
		{"attempt malformed", func(v string) error { _, err := ParseAttemptID(v); return err }, "atm_not-hex"},
		{"lease too short", func(v string) error { _, err := ParseLeaseID(v); return err }, "les_00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn(tt.in)
			if !errors.Is(err, ErrInvalidID) {
				t.Fatalf("error = %v, want ErrInvalidID", err)
			}
		})
	}
}
