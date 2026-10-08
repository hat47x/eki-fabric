package resultaccept

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

const (
	maxVerifications = 1024
	maxThreshold     = 1024
	maxIDBytes       = 256
)

func rejectf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRejectedInput, fmt.Sprintf(format, args...))
}

// checkPolicy validates the policy and returns a canonical copy with its digest.
// The copy is sorted, so the digest does not depend on the order the caller wrote
// the lists in.
func checkPolicy(p *Policy) (Policy, contract.Digest, error) {
	if p == nil {
		return Policy{}, contract.Digest{}, rejectf("policy is nil")
	}
	if p.Level != LevelRA1 && p.Level != LevelRA2 {
		return Policy{}, contract.Digest{}, rejectf("policy level %q is not RA-1 or RA-2", p.Level)
	}
	if p.Threshold < 1 || p.Threshold > maxThreshold {
		return Policy{}, contract.Digest{}, rejectf("policy threshold %d is outside 1..%d", p.Threshold, maxThreshold)
	}
	if len(p.DistinctDimensions) == 0 {
		return Policy{}, contract.Digest{}, rejectf("policy names no distinct dimension")
	}
	distinct := slices.Clone(p.DistinctDimensions)
	slices.Sort(distinct)
	for i, d := range distinct {
		if !d.valid() {
			return Policy{}, contract.Digest{}, rejectf("policy names unknown dimension %d", uint8(d))
		}
		if i > 0 && distinct[i-1] == d {
			return Policy{}, contract.Digest{}, rejectf("policy repeats dimension %s", d)
		}
	}
	authorities := slices.Clone(p.RejectAuthorities)
	slices.Sort(authorities)
	for i, id := range authorities {
		if !validID(id) {
			return Policy{}, contract.Digest{}, rejectf("policy has an invalid reject authority %q", id)
		}
		if i > 0 && authorities[i-1] == id {
			return Policy{}, contract.Digest{}, rejectf("policy repeats reject authority %q", id)
		}
	}
	canonical := Policy{
		Level:              p.Level,
		Threshold:          p.Threshold,
		DistinctDimensions: distinct,
		RejectAuthorities:  authorities,
	}
	return canonical, contract.SHA256(encodePolicy(canonical)), nil
}

func checkOutput(output OutputClass, level AssuranceLevel) error {
	switch output {
	case OutputMechanical:
		return nil
	case OutputSemanticallyOpen:
		if level == LevelRA2 {
			return rejectf("%s cannot be reported for a semantically open output", LevelRA2)
		}
		return nil
	}
	return rejectf("unknown output class %d", uint8(output))
}

// requiredDimensions lists the dimensions every claim and verification must carry.
// Principal and executor digest are always needed for the producer exclusion.
func requiredDimensions(pol Policy) []Dimension {
	required := []Dimension{DimensionPrincipal, DimensionExecutorDigest}
	for _, d := range pol.DistinctDimensions {
		if !slices.Contains(required, d) {
			required = append(required, d)
		}
	}
	return required
}

func checkClaim(c ResultClaim, required []Dimension) error {
	if !validID(c.TaskID) {
		return rejectf("claim task id is invalid")
	}
	if !validID(c.AttemptID) {
		return rejectf("claim attempt id is invalid")
	}
	if !validID(c.WorkerID) {
		return rejectf("claim worker id is invalid")
	}
	if !canonicalDigest(c.OutputDigest) {
		return rejectf("claim output digest is not canonical sha256 hex")
	}
	return checkDimensions("claim", c.Dimensions, required)
}

func checkVerification(c ResultClaim, required []Dimension, v Verification) error {
	if !validID(v.VerifierID) {
		return rejectf("verifier identity is invalid")
	}
	who := fmt.Sprintf("verification of %q", v.VerifierID)
	if v.TaskID != c.TaskID || v.AttemptID != c.AttemptID {
		return rejectf("%s is not bound to this task attempt", who)
	}
	if !v.Check.valid() {
		return rejectf("%s has unknown check %d", who, uint8(v.Check))
	}
	if !canonicalDigest(v.ObservedDigest) {
		return rejectf("%s has a non-canonical observed digest", who)
	}
	same := v.ObservedDigest == c.OutputDigest
	if v.Check == CheckDiffers && same {
		return rejectf("%s reports a difference but observed the claimed digest", who)
	}
	if v.Check != CheckDiffers && !same {
		return rejectf("%s observed a digest other than the claimed one", who)
	}
	return checkDimensions(who, v.Dimensions, required)
}

func checkDimensions(who string, dims map[Dimension]string, required []Dimension) error {
	for _, d := range sortedDimensions(dims) {
		if !d.valid() {
			return rejectf("%s names unknown dimension %d", who, uint8(d))
		}
		if err := checkValue(d, dims[d]); err != nil {
			return rejectf("%s has an invalid %s value: %v", who, d, err)
		}
	}
	for _, d := range required {
		if _, ok := dims[d]; !ok {
			return rejectf("%s is missing %s", who, d)
		}
	}
	return nil
}

// checkValue rejects values that could hide a missing or unknown identity. Those
// are empty values, placeholders such as "unknown", and text that could be read
// as two different strings.
func checkValue(d Dimension, value string) error {
	if d == DimensionExecutorDigest {
		if !canonicalDigest(value) {
			return errors.New("executor digest must be canonical sha256 hex")
		}
		return nil
	}
	if !validID(value) {
		return errors.New("must be 1..256 bytes of valid text without surrounding space or control characters")
	}
	switch strings.ToLower(value) {
	case "unknown", "unspecified", "any", "*":
		return errors.New("is a reserved placeholder")
	}
	return nil
}

func validID(s string) bool {
	if s == "" || len(s) > maxIDBytes || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// canonicalDigest reports whether s is a sha256 digest in the exact lowercase form
// that contract.Digest.Hex produces. Other spellings of the same digest are refused,
// so two spellings can never count as two different values.
func canonicalDigest(s string) bool {
	d, err := contract.ParseSHA256(s)
	return err == nil && d.Hex() == s
}

func sortedDimensions(dims map[Dimension]string) []Dimension {
	keys := make([]Dimension, 0, len(dims))
	for d := range dims {
		keys = append(keys, d)
	}
	slices.Sort(keys)
	return keys
}

// verificationKey is the canonical content of a verification, used to tell
// byte-identical repeats from conflicting records. The task and attempt are
// omitted because checkVerification already tied them to the claim.
func verificationKey(v Verification) string {
	var e encoder
	e.text(v.VerifierID)
	e.number(uint64(v.Check))
	e.text(v.ObservedDigest)
	dims := sortedDimensions(v.Dimensions)
	e.number(uint64(len(dims)))
	for _, d := range dims {
		e.number(uint64(d))
		e.text(v.Dimensions[d])
	}
	return string(e.buf)
}

func encodePolicy(p Policy) []byte {
	var e encoder
	e.text("eki.resultaccept.policy/v1")
	e.text(string(p.Level))
	e.number(uint64(p.Threshold))
	e.number(uint64(len(p.DistinctDimensions)))
	for _, d := range p.DistinctDimensions {
		e.number(uint64(d))
	}
	e.number(uint64(len(p.RejectAuthorities)))
	for _, id := range p.RejectAuthorities {
		e.text(id)
	}
	return e.buf
}

// encoder writes length-prefixed fields, so no two different field sequences
// produce the same bytes.
type encoder struct {
	buf []byte
}

func (e *encoder) number(v uint64) {
	e.buf = binary.AppendUvarint(e.buf, v)
}

func (e *encoder) text(s string) {
	e.number(uint64(len(s)))
	e.buf = append(e.buf, s...)
}
