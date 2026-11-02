package claimbody

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type stubSource struct {
	body []byte
	size int64
	err  error
}

func (s stubSource) Open(string) (io.ReadCloser, int64, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	return io.NopCloser(bytes.NewReader(s.body)), s.size, nil
}

func TestTheBodyIsStreamedWhenTheSizeMatches(t *testing.T) {
	var out bytes.Buffer
	written, err := Stream(OutcomeDeliver, stubSource{body: []byte("hello"), size: 5}, "abc", 5, &out)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if written != 5 || out.String() != "hello" {
		t.Fatalf("written = %d, body = %q", written, out.String())
	}
}

func TestAShortBodyIsRefusedRatherThanServed(t *testing.T) {
	var out bytes.Buffer
	_, err := Stream(OutcomeDeliver, stubSource{body: []byte("hel"), size: 5}, "abc", 5, &out)
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrSizeMismatch)
	}
}

func TestALongerBodyIsRefused(t *testing.T) {
	var out bytes.Buffer
	_, err := Stream(OutcomeDeliver, stubSource{body: []byte("hello world"), size: 11}, "abc", 5, &out)
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrSizeMismatch)
	}
}

func TestAnOutcomeThatIsNotDeliverWritesNothing(t *testing.T) {
	for _, outcome := range []Outcome{OutcomeNotFound, OutcomeExpired} {
		var out bytes.Buffer
		if _, err := Stream(outcome, stubSource{body: []byte("hello"), size: 5}, "abc", 5, &out); err == nil {
			t.Fatalf("Stream(%v) accepted a body", outcome)
		}
		if out.Len() != 0 {
			t.Fatalf("Stream(%v) wrote %d bytes", outcome, out.Len())
		}
	}
}

func TestAnOpenFailureIsReported(t *testing.T) {
	want := errors.New("gone")
	var out bytes.Buffer
	_, err := Stream(OutcomeDeliver, stubSource{err: want}, "abc", 5, &out)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if out.Len() != 0 {
		t.Fatal("Stream wrote bytes after a failed open")
	}
}

func TestAnEmptyBodyWithZeroSizeIsFine(t *testing.T) {
	var out bytes.Buffer
	written, err := Stream(OutcomeDeliver, stubSource{body: nil, size: 0}, "abc", 0, &out)
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if written != 0 || !strings.Contains(out.String(), "") {
		t.Fatalf("written = %d", written)
	}
}
