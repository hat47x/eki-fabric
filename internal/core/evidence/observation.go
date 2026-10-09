package evidence

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxIDLength bounds every identifier in an observation, in bytes. Oversized
// input is rejected rather than truncated, so two different IDs never collapse
// into one.
const MaxIDLength = 128

var (
	// ErrInvalidObservation reports a missing or malformed field in an observation.
	ErrInvalidObservation = errors.New("evidence: invalid observation")
	// ErrDuplicateObservation reports that one observation ID appears twice in a log.
	ErrDuplicateObservation = errors.New("evidence: duplicate observation ID")
	// ErrDuplicateResult reports that one result ID has more than one decision in a log.
	ErrDuplicateResult = errors.New("evidence: result decided more than once")
	// ErrUnknownObservation reports an observation type this package does not define.
	ErrUnknownObservation = errors.New("evidence: unknown observation type")
	// ErrInvalidPercentile reports a percentile outside the closed interval [0, 100].
	ErrInvalidPercentile = errors.New("evidence: percentile outside [0, 100]")
)

// Observation is one typed fact in the append-only evidence log. The set of
// implementations is closed: the unexported marker method keeps other packages
// from adding kinds that the derivation would not understand.
type Observation interface {
	// ObservationID returns the identifier that metrics cite as their source.
	ObservationID() string
	observation()
}

// GovernorReclaim records that a local governor on a worker returned a resource
// to the human user. ReclaimCompleted is the time the resource was actually
// released. A zero ReclaimCompleted means the reclaim has started but has not
// completed, and it is reported as pending.
type GovernorReclaim struct {
	ID               string
	WorkerID         string
	ReclaimStart     time.Time
	ReclaimCompleted time.Time
}

// ResultDecision records the acceptance decision for one result and whether a
// verifier established the result as verified. The caller's verification layer
// sets Verified. The package cannot check it, so it trusts that value.
//
// A verified result must also name the verification record that established it in
// VerificationRecordID. Without that ID, a reader of the verified share cannot find the
// record behind a count. A verified result without the ID is rejected. For an
// unverified result the ID is optional, and when it is present it is traced like any
// other examined evidence.
type ResultDecision struct {
	ID                   string
	ResultID             string
	Accepted             bool
	Verified             bool
	VerificationRecordID string
}

// SuitabilityDecision records one outcome of the suitability gate for one task.
type SuitabilityDecision struct {
	ID      string
	Outcome SuitabilityOutcome
}

// RetryEvent records a retry after a reclaim or a worker departure. Cause names the
// event that started the retry, and it is required. AuthorityGrew is set by the
// caller's authority check. It is true when the retry ran with more authority than the
// original attempt had. DuplicateExecution is set by the caller when the retry made the
// same work run a second time.
type RetryEvent struct {
	ID                 string
	Cause              RetryCause
	AuthorityGrew      bool
	DuplicateExecution bool
}

// RetryCause says why a retry happened. The set is closed, so every retry belongs to
// exactly one cause, and the counts by cause add up to the total number of retries.
type RetryCause string

const (
	// RetryCauseReclaim means the retry followed a governor reclaim of the resource.
	RetryCauseReclaim RetryCause = "RECLAIM"
	// RetryCauseWorkerDeparture means the retry followed a worker leaving the fabric.
	RetryCauseWorkerDeparture RetryCause = "WORKER_DEPARTURE"
	// RetryCauseOther means the retry followed some other event. Use it only when neither
	// of the two causes above applies.
	RetryCauseOther RetryCause = "OTHER"
)

// RetryCauses returns the closed set of retry causes in a fixed order.
func RetryCauses() []RetryCause {
	return []RetryCause{
		RetryCauseReclaim,
		RetryCauseWorkerDeparture,
		RetryCauseOther,
	}
}

// Valid reports whether c is one of the closed retry causes. The empty value is not
// valid, so a retry without a cause is rejected rather than counted as "other".
func (c RetryCause) Valid() bool {
	switch c {
	case RetryCauseReclaim, RetryCauseWorkerDeparture, RetryCauseOther:
		return true
	}
	return false
}

// ContributionRecord records a resource-provision fact. Verified is set only when
// the caller's verification layer has established the fact. An unverified record
// is a claim and never counts toward a verified metric.
//
// SupportingFactIDs names the facts that back the record. A verified record must name
// at least one, because a verified count with no fact behind it cannot be traced. An
// unverified record may name facts too, and they are traced like any other examined
// evidence. The package does not interpret the facts.
type ContributionRecord struct {
	ID                string
	WorkerID          string
	Verified          bool
	SupportingFactIDs []string
}

// SuitabilityOutcome is one of the three values the suitability gate returns. The
// strings match the Decision values of internal/core/suitability, so a caller can
// convert between them. This package does not import that package.
type SuitabilityOutcome string

const (
	// SuitabilityFabricJustified means the fabric path was judged worth using.
	SuitabilityFabricJustified SuitabilityOutcome = "FABRIC_JUSTIFIED"
	// SuitabilityFabricNotJustified means the task was judged better kept local or
	// on existing infrastructure. This is a normal, reportable outcome.
	SuitabilityFabricNotJustified SuitabilityOutcome = "FABRIC_NOT_JUSTIFIED"
	// SuitabilityNeedsMeasurement means the gate could not decide without more data.
	SuitabilityNeedsMeasurement SuitabilityOutcome = "NEEDS_MEASUREMENT"
)

// SuitabilityOutcomes returns the closed set of outcomes in a fixed order.
func SuitabilityOutcomes() []SuitabilityOutcome {
	return []SuitabilityOutcome{
		SuitabilityFabricJustified,
		SuitabilityFabricNotJustified,
		SuitabilityNeedsMeasurement,
	}
}

// Valid reports whether o is one of the closed suitability outcomes.
func (o SuitabilityOutcome) Valid() bool {
	switch o {
	case SuitabilityFabricJustified, SuitabilityFabricNotJustified, SuitabilityNeedsMeasurement:
		return true
	}
	return false
}

// ObservationID returns the record's ID.
func (o GovernorReclaim) ObservationID() string { return o.ID }

// ObservationID returns the record's ID.
func (o ResultDecision) ObservationID() string { return o.ID }

// ObservationID returns the record's ID.
func (o SuitabilityDecision) ObservationID() string { return o.ID }

// ObservationID returns the record's ID.
func (o RetryEvent) ObservationID() string { return o.ID }

// ObservationID returns the record's ID.
func (o ContributionRecord) ObservationID() string { return o.ID }

func (GovernorReclaim) observation()     {}
func (ResultDecision) observation()      {}
func (SuitabilityDecision) observation() {}
func (RetryEvent) observation()          {}
func (ContributionRecord) observation()  {}

// buckets groups a validated log by observation kind. Each slice keeps the input
// order, and derivations sort what they output.
type buckets struct {
	reclaims []GovernorReclaim
	results  []ResultDecision
	outcomes []SuitabilityDecision
	retries  []RetryEvent
	contribs []ContributionRecord
}

// split validates the whole log and then groups it by kind. Validation covers the
// fields of every observation, and it also checks that no observation ID or result
// ID repeats. A nil or unknown observation is rejected before any field is read,
// so a nil pointer never reaches a method call.
func split(log []Observation) (buckets, error) {
	var b buckets
	seenIDs := make(map[string]struct{}, len(log))
	seenResults := make(map[string]struct{})
	for i, o := range log {
		var id string
		switch v := o.(type) {
		case GovernorReclaim:
			if err := checkReclaim(v); err != nil {
				return buckets{}, err
			}
			id = v.ID
			b.reclaims = append(b.reclaims, v)
		case ResultDecision:
			if err := checkID("result ID", v.ResultID); err != nil {
				return buckets{}, err
			}
			if _, dup := seenResults[v.ResultID]; dup {
				return buckets{}, fmt.Errorf("%w: %s", ErrDuplicateResult, v.ResultID)
			}
			seenResults[v.ResultID] = struct{}{}
			if v.Verified && strings.TrimSpace(v.VerificationRecordID) == "" {
				return buckets{}, fmt.Errorf("%w: verified result %s has no verification record", ErrInvalidObservation, v.ID)
			}
			if v.VerificationRecordID != "" {
				if err := checkID("verification record ID", v.VerificationRecordID); err != nil {
					return buckets{}, err
				}
			}
			id = v.ID
			b.results = append(b.results, v)
		case SuitabilityDecision:
			if !v.Outcome.Valid() {
				return buckets{}, fmt.Errorf("%w: suitability outcome %q", ErrInvalidObservation, string(v.Outcome))
			}
			id = v.ID
			b.outcomes = append(b.outcomes, v)
		case RetryEvent:
			if !v.Cause.Valid() {
				return buckets{}, fmt.Errorf("%w: retry cause %q is not one of the closed set", ErrInvalidObservation, string(v.Cause))
			}
			id = v.ID
			b.retries = append(b.retries, v)
		case ContributionRecord:
			if err := checkID("worker ID", v.WorkerID); err != nil {
				return buckets{}, err
			}
			if v.Verified && len(v.SupportingFactIDs) == 0 {
				return buckets{}, fmt.Errorf("%w: verified contribution %s has no supporting fact", ErrInvalidObservation, v.ID)
			}
			for _, f := range v.SupportingFactIDs {
				if err := checkID("supporting fact ID", f); err != nil {
					return buckets{}, err
				}
			}
			id = v.ID
			b.contribs = append(b.contribs, v)
		case nil:
			return buckets{}, fmt.Errorf("%w: observation %d is nil", ErrInvalidObservation, i)
		default:
			return buckets{}, fmt.Errorf("%w: observation %d has type %T", ErrUnknownObservation, i, o)
		}
		if err := checkID("observation ID", id); err != nil {
			return buckets{}, err
		}
		if _, dup := seenIDs[id]; dup {
			return buckets{}, fmt.Errorf("%w: %s", ErrDuplicateObservation, id)
		}
		seenIDs[id] = struct{}{}
	}
	if err := checkEvidenceIDs(b, seenIDs); err != nil {
		return buckets{}, err
	}
	return b, nil
}

// checkEvidenceIDs requires that no evidence ID reuses an observation ID. Evidence IDs,
// such as verification record IDs, appear in the SourceIDs of the metrics next to
// observation IDs. If one value named both, a reader could not tell which record a
// trace points at. The check runs after the whole log is read, so the result does not
// depend on the order of the log.
func checkEvidenceIDs(b buckets, observationIDs map[string]struct{}) error {
	reuse := func(kind, id string) error {
		if _, clash := observationIDs[id]; clash {
			return fmt.Errorf("%w: %s %s reuses an observation ID", ErrInvalidObservation, kind, id)
		}
		return nil
	}
	// records collects the verification record IDs of all results before any fact is
	// checked, so the fact check does not depend on the order of the two kinds in the log.
	records := map[string]struct{}{}
	for _, r := range b.results {
		if r.VerificationRecordID == "" {
			continue
		}
		if err := reuse("verification record", r.VerificationRecordID); err != nil {
			return err
		}
		records[r.VerificationRecordID] = struct{}{}
	}
	// A verification record and a supporting fact must not share a name either. Both
	// are evidence, but they are different kinds, and SourceIDs keeps one entry per name.
	for _, c := range b.contribs {
		for _, f := range c.SupportingFactIDs {
			if err := reuse("supporting fact", f); err != nil {
				return err
			}
			if _, clash := records[f]; clash {
				return fmt.Errorf("%w: supporting fact %s reuses a verification record ID", ErrInvalidObservation, f)
			}
		}
	}
	return nil
}

// checkReclaim requires a worker ID and a start time. A completion time, when
// present, must not precede the start. A zero completion time is allowed and means
// the reclaim is still pending.
func checkReclaim(r GovernorReclaim) error {
	if err := checkID("worker ID", r.WorkerID); err != nil {
		return err
	}
	if r.ReclaimStart.IsZero() {
		return fmt.Errorf("%w: reclaim start is missing for %s", ErrInvalidObservation, r.ID)
	}
	if !r.ReclaimCompleted.IsZero() && r.ReclaimCompleted.Before(r.ReclaimStart) {
		return fmt.Errorf("%w: reclaim of %s completed before it started", ErrInvalidObservation, r.ID)
	}
	return nil
}

// checkID requires a non-blank identifier of at most MaxIDLength bytes. It must
// have no surrounding space, valid UTF-8, no control characters, and no format
// characters (Unicode category Cf). A format character, such as a zero-width space
// or a bidi override, is invisible or reorders the text around it, so two different
// IDs could look the same to a reader.
func checkID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is missing", ErrInvalidObservation, field)
	}
	if len(value) > MaxIDLength {
		return fmt.Errorf("%w: %s is longer than %d bytes", ErrInvalidObservation, field, MaxIDLength)
	}
	if value != strings.TrimSpace(value) || !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s has surrounding space or invalid UTF-8", ErrInvalidObservation, field)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return fmt.Errorf("%w: %s contains a control or format character", ErrInvalidObservation, field)
		}
	}
	return nil
}
