package federation

import (
	"errors"
	"strings"
	"testing"
)

func TestParseTrustDomainAcceptsCanonicalNames(t *testing.T) {
	longName := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." +
		strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) // exactly 253 bytes
	cases := map[string]string{
		"single label":          "localhost",
		"two labels":            "example.org",
		"hyphen inside a label": "company-a.example",
		"digits and hyphens":    "a1.b2-c3.d4",
		"digit-only labels":     "10.0.0",
		"253 bytes":             longName,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			domain, err := ParseTrustDomain(input)
			if err != nil {
				t.Fatalf("ParseTrustDomain(%q) error: %v", input, err)
			}
			if !domain.Valid() {
				t.Fatalf("ParseTrustDomain(%q) returned a domain that is not valid", input)
			}
			if domain.String() != input {
				t.Fatalf("String() = %q, want the input unchanged %q", domain.String(), input)
			}
		})
	}
}

func TestParseTrustDomainRejectsNonCanonicalNames(t *testing.T) {
	tooLong := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." +
		strings.Repeat("c", 63) + "." + strings.Repeat("d", 62) // 254 bytes
	cases := map[string]string{
		"empty":                      "",
		"254 bytes":                  tooLong,
		"uppercase letter":           "Example.org",
		"uppercase in TLD":           "example.ORG",
		"all uppercase":              "EXAMPLE.ORG",
		"leading dot":                ".example",
		"trailing dot":               "example.",
		"empty middle label":         "a..b",
		"only dots":                  "..",
		"single dot":                 ".",
		"leading hyphen":             "-a.example",
		"trailing hyphen":            "a.example-",
		"label with leading hyphen":  "a.-b",
		"label with trailing hyphen": "a-.b",
		"underscore":                 "a_b.example",
		"space":                      "a b.example",
		"slash":                      "a/b",
		"port suffix":                "example.org:443",
		"wildcard":                   "*.example",
		"at sign":                    "a@b.example",
		"newline":                    "example.org\n",
		"nul byte":                   "a\x00b",
		"non-ASCII letter":           "exa\xc3\xa9mple.org",
		"non-ASCII first label":      "\xc3\xa9.example",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			domain, err := ParseTrustDomain(input)
			if err == nil {
				t.Fatalf("ParseTrustDomain(%q) = %q, want an error", input, domain.String())
			}
			if !errors.Is(err, ErrInvalidTrustDomain) {
				t.Fatalf("error %v does not wrap ErrInvalidTrustDomain", err)
			}
			if domain.Valid() {
				t.Fatalf("zero value returned after an error must not be valid")
			}
		})
	}
}

func TestUppercaseIsRejectedNotNormalized(t *testing.T) {
	if _, err := ParseTrustDomain("Example.org"); err == nil {
		t.Fatal(`ParseTrustDomain("Example.org") succeeded; it must not alias "example.org"`)
	}
	lower, err := ParseTrustDomain("example.org")
	if err != nil {
		t.Fatalf("ParseTrustDomain(example.org): %v", err)
	}
	if lower.String() != "example.org" {
		t.Fatalf("canonical name changed to %q", lower.String())
	}
}

func TestZeroTrustDomainIsNotValid(t *testing.T) {
	var zero TrustDomain
	if zero.Valid() {
		t.Fatal("zero TrustDomain reports Valid() = true")
	}
	if zero.String() != "" {
		t.Fatalf("zero TrustDomain String() = %q, want empty", zero.String())
	}
}
