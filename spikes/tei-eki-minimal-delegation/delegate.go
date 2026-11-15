package teideleg

import (
	"errors"
	"time"
)

// DelegateOptions controls one delegation. Uploads holds the bytes of every
// input, keyed by input name; the adapter refuses to submit a Task whose input
// it did not send.
type DelegateOptions struct {
	Uploads map[string][]byte
	// Deadline is TEI's own wait limit. It is deliberately separate from the
	// deadline in the request: the request's deadline is what TEI asks EKI to
	// record, while this one is how long TEI is willing to wait.
	Deadline time.Time
	// PollInterval is the pause between status reads. Zero means 200ms.
	PollInterval time.Duration
	// MaxPolls bounds the status reads even when the deadline is far away.
	// Zero means 30.
	MaxPolls int
	// Local is the same operation run on TEI's side, when available.
	Local *LocalRun
	// Now is the clock. nil means time.Now. Tests inject it.
	Now func() time.Time
	// Sleep is the wait. nil means time.Sleep. Tests inject it.
	Sleep func(time.Duration)
}

func (o DelegateOptions) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func (o DelegateOptions) sleep(d time.Duration) {
	if o.Sleep != nil {
		o.Sleep(d)
		return
	}
	time.Sleep(d)
}

// Delegation is the result of one Delegate call: the projection that was sent,
// what the input path returned, what was observed, and TEI's receipt.
type Delegation struct {
	Projection  Projection
	Uploads     []UploadRecord
	Observation Observation
	Receipt     Receipt
	Trace       Trace
}

func propertyOf(err error) Problem {
	var e *Error
	if errors.As(err, &e) {
		return e.Problem
	}
	return Problem{}
}

func problemOf(err error) Problem { return propertyOf(err) }

// ingestKey derives a bounded, deterministic key per input. The task key alone
// can be close to the 128-character limit, so appending a name is not safe.
func ingestKey(taskKey, inputName string) string {
	return "tei-ing:" + SHA256Hex([]byte(taskKey + "\x00" + inputName))[:40]
}

// isTerminal reports whether a public status is one EKI will not leave.
func isTerminal(status, terminalCode string) bool {
	if terminalCode != "" {
		return true
	}
	switch status {
	case "completed", "cancelled", "failed":
		return true
	}
	return false
}
