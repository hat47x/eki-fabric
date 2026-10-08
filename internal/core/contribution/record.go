package contribution

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hat47x/eki-fabric/internal/core/contract"
)

// SchemaVersion is the only schema version this package writes or accepts.
const SchemaVersion uint32 = 1

// Limits on input size. Input beyond a limit is rejected, never truncated. Text
// limits are in bytes.
const (
	// MaxIdentifierLength bounds provider identities, observer identities, and
	// resource classes.
	MaxIdentifierLength = 128
	// MaxProvenanceLength bounds the provenance description.
	MaxProvenanceLength = 512
	// MaxObservations bounds the number of observations in one record. It also
	// bounds the number of distinct observers.
	MaxObservations = 16
	// MaxWindowDuration bounds the measurement window. Work measured over a
	// longer period yields several records.
	MaxWindowDuration = 24 * time.Hour
)

// Quantity is an amount expressed in one unit. The unit must be the one the
// record's metric declares.
type Quantity struct {
	Unit  Unit
	Value int64
}

// Observation is one principal's reading of a quantity.
type Observation struct {
	Observer Observer
	Quantity Quantity
}

// ResultStatus is the verification outcome the caller reports for a result.
type ResultStatus string

const (
	ResultStatusAccepted   ResultStatus = "accepted"
	ResultStatusRejected   ResultStatus = "rejected"
	ResultStatusUnverified ResultStatus = "unverified"
)

// AcceptedResultRef binds a record to one accepted result of one task. The only
// way to build a non-zero value is NewAcceptedResultRef, which refuses every
// status other than accepted.
type AcceptedResultRef struct {
	taskDigest   contract.Digest
	resultDigest contract.Digest
}

// NewAcceptedResultRef builds a work reference. A contract.Digest can only come
// from contract.SHA256 or contract.ParseSHA256, so both digests are already
// well-formed; this function rejects absent ones and any status other than
// accepted.
func NewAcceptedResultRef(taskDigest, resultDigest contract.Digest, status ResultStatus) (AcceptedResultRef, error) {
	switch status {
	case ResultStatusAccepted:
	case ResultStatusRejected, ResultStatusUnverified:
		return AcceptedResultRef{}, ErrResultNotAccepted
	default:
		return AcceptedResultRef{}, ErrUnknownResultStatus
	}
	if taskDigest.IsZero() {
		return AcceptedResultRef{}, fmt.Errorf("%w: task digest", ErrMissingField)
	}
	if resultDigest.IsZero() {
		return AcceptedResultRef{}, fmt.Errorf("%w: result digest", ErrMissingField)
	}
	return AcceptedResultRef{taskDigest: taskDigest, resultDigest: resultDigest}, nil
}

// ParseAcceptedResultRef is NewAcceptedResultRef for digests received as text in
// the form "sha256:" followed by 64 hexadecimal digits. Malformed text is
// rejected with ErrInvalidField before any status check.
func ParseAcceptedResultRef(taskDigest, resultDigest, status string) (AcceptedResultRef, error) {
	task, err := parseDigestText("task digest", taskDigest)
	if err != nil {
		return AcceptedResultRef{}, err
	}
	result, err := parseDigestText("result digest", resultDigest)
	if err != nil {
		return AcceptedResultRef{}, err
	}
	return NewAcceptedResultRef(task, result, ResultStatus(status))
}

func parseDigestText(field, text string) (contract.Digest, error) {
	if text == "" {
		return contract.Digest{}, fmt.Errorf("%w: %s", ErrMissingField, field)
	}
	hexPart, ok := strings.CutPrefix(text, "sha256:")
	if !ok {
		return contract.Digest{}, fmt.Errorf("%w: %s", ErrInvalidField, field)
	}
	digest, err := contract.ParseSHA256(hexPart)
	if err != nil {
		return contract.Digest{}, fmt.Errorf("%w: %s", ErrInvalidField, field)
	}
	return digest, nil
}

// RecordInput is everything NewRecord needs. The assurance level is not an
// input; it follows from Observations.
type RecordInput struct {
	ProviderID    string
	ResourceClass string
	Metric        MetricName
	Observations  []Observation
	Work          AcceptedResultRef
	WindowStart   time.Time
	WindowEnd     time.Time
	Provenance    string
}

// Record states that a provider supplied a measured quantity of a resource
// class to one accepted result. It records what was observed and by whom. It
// carries no currency, price, exchange rate, or score. Its fields are
// unexported, so a valid Record comes only from NewRecord. The zero value is
// not valid: Digest and NewAuditView reject it.
type Record struct {
	schemaVersion uint32
	providerID    string
	resourceClass string
	metric        MetricName
	quantity      Quantity
	observers     []Observer
	assurance     Assurance
	taskDigest    contract.Digest
	resultDigest  contract.Digest
	provenance    string
	windowStart   time.Time
	windowEnd     time.Time
}

// NewRecord validates in and builds a Record. On error it returns the zero
// Record, so no partially built or truncated record can escape.
func NewRecord(in RecordInput) (Record, error) {
	if err := checkText("provider identity", in.ProviderID, MaxIdentifierLength); err != nil {
		return Record{}, err
	}
	if err := checkText("resource class", in.ResourceClass, MaxIdentifierLength); err != nil {
		return Record{}, err
	}
	if err := checkText("provenance", in.Provenance, MaxProvenanceLength); err != nil {
		return Record{}, err
	}
	def, err := LookupMetric(in.Metric)
	if err != nil {
		return Record{}, err
	}
	principals, values, err := collectObservations(def, in.Observations)
	if err != nil {
		return Record{}, err
	}
	assurance, err := assuranceFor(principals)
	if err != nil {
		return Record{}, err
	}
	quantity := values[0]
	for _, v := range values[1:] {
		if v != quantity {
			return Record{}, ErrObservationMismatch
		}
	}
	sortObservers(principals)
	rec := Record{
		schemaVersion: SchemaVersion,
		providerID:    in.ProviderID,
		resourceClass: in.ResourceClass,
		metric:        def.Name,
		quantity:      quantity,
		observers:     principals,
		assurance:     assurance,
		taskDigest:    in.Work.taskDigest,
		resultDigest:  in.Work.resultDigest,
		provenance:    in.Provenance,
		windowStart:   in.WindowStart.UTC(),
		windowEnd:     in.WindowEnd.UTC(),
	}
	if err := rec.validate(); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// collectObservations checks each observation on its own. Repeated observations
// by one principal are merged into one entry, so they count as one observer.
// The merged entries are returned in input order, with one quantity per entry.
func collectObservations(def MetricDefinition, obs []Observation) ([]Observer, []Quantity, error) {
	if len(obs) == 0 {
		return nil, nil, fmt.Errorf("%w: observations", ErrMissingField)
	}
	if len(obs) > MaxObservations {
		return nil, nil, fmt.Errorf("%w: observations", ErrFieldTooLong)
	}
	var principals []Observer
	var values []Quantity
	for _, o := range obs {
		if err := checkObserver(o.Observer); err != nil {
			return nil, nil, err
		}
		if err := checkQuantity(def, o.Quantity); err != nil {
			return nil, nil, err
		}
		i := slices.IndexFunc(principals, func(p Observer) bool { return p.ID == o.Observer.ID })
		if i < 0 {
			principals = append(principals, o.Observer)
			values = append(values, o.Quantity)
			continue
		}
		if principals[i].Kind != o.Observer.Kind {
			return nil, nil, ErrObserverConflict
		}
		if values[i] != o.Quantity {
			return nil, nil, ErrObservationMismatch
		}
	}
	return principals, values, nil
}

// validate checks every field of a record. NewRecord, Digest, and AuditView all
// call it, so a record that was tampered with after construction is still
// rejected.
func (r Record) validate() error {
	if r.schemaVersion != SchemaVersion {
		return ErrUnsupportedSchema
	}
	if err := checkText("provider identity", r.providerID, MaxIdentifierLength); err != nil {
		return err
	}
	if err := checkText("resource class", r.resourceClass, MaxIdentifierLength); err != nil {
		return err
	}
	if err := checkText("provenance", r.provenance, MaxProvenanceLength); err != nil {
		return err
	}
	def, err := LookupMetric(r.metric)
	if err != nil {
		return err
	}
	if err := checkQuantity(def, r.quantity); err != nil {
		return err
	}
	if err := checkObserverSet(r.observers); err != nil {
		return err
	}
	if !r.assurance.Valid() {
		return ErrUnknownAssurance
	}
	want, err := assuranceFor(r.observers)
	if err != nil {
		return err
	}
	if want != r.assurance {
		return fmt.Errorf("%w: assurance does not follow from observers", ErrInvalidField)
	}
	if r.taskDigest.IsZero() {
		return fmt.Errorf("%w: task digest", ErrMissingField)
	}
	if r.resultDigest.IsZero() {
		return fmt.Errorf("%w: result digest", ErrMissingField)
	}
	return checkWindow(r.windowStart, r.windowEnd)
}

// Digest returns the content digest of the record. The digest covers the
// canonical encoding of every field, so any change to a field changes it.
func (r Record) Digest() (contract.Digest, error) {
	if err := r.validate(); err != nil {
		return contract.Digest{}, err
	}
	return contract.SHA256(r.canonical()), nil
}

// SchemaVersion returns the schema version the record was written under.
func (r Record) SchemaVersion() uint32 { return r.schemaVersion }

// ProviderID returns the provider identity the record is attributed to.
func (r Record) ProviderID() string { return r.providerID }

// ResourceClass returns the resource class that was provided.
func (r Record) ResourceClass() string { return r.resourceClass }

// Metric returns the metric name the quantity was measured under.
func (r Record) Metric() MetricName { return r.metric }

// Quantity returns the measured quantity and its unit.
func (r Record) Quantity() Quantity { return r.quantity }

// Observers returns a copy of the observer list, sorted by kind and identity.
func (r Record) Observers() []Observer { return slices.Clone(r.observers) }

// Assurance returns the measurement assurance level derived from the observers.
func (r Record) Assurance() Assurance { return r.assurance }

// TaskDigest returns the digest identifying the task the record is for.
func (r Record) TaskDigest() contract.Digest { return r.taskDigest }

// ResultDigest returns the digest of the accepted result the record is for.
func (r Record) ResultDigest() contract.Digest { return r.resultDigest }

// Provenance returns the description of how the quantity was measured.
func (r Record) Provenance() string { return r.provenance }

// WindowStart returns the start of the measurement window in UTC.
func (r Record) WindowStart() time.Time { return r.windowStart }

// WindowEnd returns the end of the measurement window in UTC.
func (r Record) WindowEnd() time.Time { return r.windowEnd }

// checkText requires a single-line, valid UTF-8 string of at most max bytes
// with no leading or trailing space. Each rule has its own sentinel.
func checkText(field, value string, max int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrMissingField, field)
	}
	if len(value) > max {
		return fmt.Errorf("%w: %s", ErrFieldTooLong, field)
	}
	if value != strings.TrimSpace(value) || !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s", ErrInvalidField, field)
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %s", ErrInvalidField, field)
		}
	}
	return nil
}

// checkWindow requires start and end to be set, end to be strictly after start,
// and the duration to be at most MaxWindowDuration. The package has no clock,
// so it cannot reject windows that end in the future; callers must check that.
func checkWindow(start, end time.Time) error {
	if start.IsZero() {
		return fmt.Errorf("%w: window start", ErrMissingField)
	}
	if end.IsZero() {
		return fmt.Errorf("%w: window end", ErrMissingField)
	}
	if !end.After(start) {
		return ErrInvalidWindow
	}
	if end.Sub(start) > MaxWindowDuration {
		return ErrWindowTooLong
	}
	return nil
}
