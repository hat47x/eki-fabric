package admission

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// Structural limits applied to hostile JSON before it is decoded into any Go
// value. They are deliberately far above what a valid TaskRequest needs and far
// below what could hurt the process.
const (
	maxJSONDepth        = 12
	maxJSONArrayItems   = 256
	maxJSONObjectFields = 256
	maxJSONStringBytes  = 8 * 1024
	maxJSONKeyBytes     = 256
)

var errMalformedJSON = errors.New("malformed JSON")

// scanJSON checks the shape of raw without building a value tree. It rejects
// what encoding/json would silently accept or normalize:
//
//   - invalid UTF-8 (encoding/json replaces it with U+FFFD)
//   - duplicate object keys (encoding/json keeps the last one)
//   - trailing data after the top-level value
//   - excessive nesting, array length, object width, and string length
//
// The top-level value must be an object.
func scanJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("%w: invalid UTF-8", errMalformedJSON)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%w: %v", errMalformedJSON, err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("%w: top-level value must be an object", errMalformedJSON)
	}
	if err := scanObject(dec, 1); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing data after the top-level value", errMalformedJSON)
	}
	return nil
}

// scanObject reads members up to the closing brace. The opening brace has
// already been consumed.
func scanObject(dec *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("%w: nesting exceeds %d levels", errMalformedJSON, maxJSONDepth)
	}
	seen := make(map[string]struct{})
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%w: %v", errMalformedJSON, err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("%w: object key is not a string", errMalformedJSON)
		}
		if len(key) > maxJSONKeyBytes {
			return fmt.Errorf("%w: object key exceeds %d bytes", errMalformedJSON, maxJSONKeyBytes)
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("%w: duplicate object key %q", errMalformedJSON, truncate(key))
		}
		if len(seen) >= maxJSONObjectFields {
			return fmt.Errorf("%w: object has more than %d fields", errMalformedJSON, maxJSONObjectFields)
		}
		seen[key] = struct{}{}

		if err := scanValue(dec, depth); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return fmt.Errorf("%w: %v", errMalformedJSON, err)
	}
	return nil
}

func scanArray(dec *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("%w: nesting exceeds %d levels", errMalformedJSON, maxJSONDepth)
	}
	items := 0
	for dec.More() {
		items++
		if items > maxJSONArrayItems {
			return fmt.Errorf("%w: array has more than %d items", errMalformedJSON, maxJSONArrayItems)
		}
		if err := scanValue(dec, depth); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil { // closing bracket
		return fmt.Errorf("%w: %v", errMalformedJSON, err)
	}
	return nil
}

func scanValue(dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("%w: %v", errMalformedJSON, err)
	}
	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			return scanObject(dec, depth+1)
		}
		return scanArray(dec, depth+1)
	case string:
		if len(v) > maxJSONStringBytes {
			return fmt.Errorf("%w: string exceeds %d bytes", errMalformedJSON, maxJSONStringBytes)
		}
	}
	return nil
}

func truncate(s string) string {
	const max = 64
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
