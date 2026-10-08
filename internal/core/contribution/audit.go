package contribution

import "time"

// AuditView is a projection of a record for audit and reporting. Identities
// and provenance text appear only in a full view. The view holds digests as
// strings so that it survives JSON encoding.
type AuditView struct {
	// Full reports whether the view was requested with identities included.
	Full bool

	SchemaVersion uint32
	RecordDigest  string
	TaskDigest    string
	ResultDigest  string
	ResourceClass string
	Metric        MetricName
	Quantity      Quantity
	Assurance     Assurance
	WindowStart   time.Time
	WindowEnd     time.Time
	ObserverCount int

	// The three fields below are set only in a full view.
	ProviderID string
	Observers  []Observer
	Provenance string
}

// NewAuditView projects r for audit. Passing full=false, which is the zero value,
// gives the restrictive view. That view leaves out the provider identity,
// observer identities, and provenance text, and keeps the count of observers.
func NewAuditView(r Record, full bool) (AuditView, error) {
	digest, err := r.Digest()
	if err != nil {
		return AuditView{}, err
	}
	view := AuditView{
		Full:          full,
		SchemaVersion: r.schemaVersion,
		RecordDigest:  digest.String(),
		TaskDigest:    r.taskDigest.String(),
		ResultDigest:  r.resultDigest.String(),
		ResourceClass: r.resourceClass,
		Metric:        r.metric,
		Quantity:      r.quantity,
		Assurance:     r.assurance,
		WindowStart:   r.windowStart,
		WindowEnd:     r.windowEnd,
		ObserverCount: len(r.observers),
	}
	if full {
		view.ProviderID = r.providerID
		view.Observers = r.Observers()
		view.Provenance = r.provenance
	}
	return view, nil
}
