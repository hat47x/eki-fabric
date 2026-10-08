package contribution

import (
	"fmt"
	"sort"
)

// Assurance states how well a measured quantity is established. The level is
// never supplied by the caller. The constructor derives it from the observers.
type Assurance string

const (
	AssuranceSelfReported     Assurance = "self-reported"
	AssuranceControlObserved  Assurance = "control-observed"
	AssuranceCrossValidated   Assurance = "cross-validated"
	AssuranceAttestedMetering Assurance = "attested-metering"
)

// AssuranceLevels returns the closed set of assurance levels, in the order the
// assurance model lists them.
func AssuranceLevels() []Assurance {
	return []Assurance{
		AssuranceSelfReported,
		AssuranceControlObserved,
		AssuranceCrossValidated,
		AssuranceAttestedMetering,
	}
}

// Valid reports whether a is one of the closed assurance levels.
func (a Assurance) Valid() bool {
	switch a {
	case AssuranceSelfReported, AssuranceControlObserved, AssuranceCrossValidated, AssuranceAttestedMetering:
		return true
	}
	return false
}

// ObserverKind classifies who made an observation. The package trusts this
// classification. It does not verify attestation evidence or control-plane
// identity; the caller's verification layer must establish them first.
type ObserverKind string

const (
	// ObserverWorker is the worker reporting on its own consumption.
	ObserverWorker ObserverKind = "worker"
	// ObserverControl is a component outside the worker boundary, such as a
	// control plane that watched the lease.
	ObserverControl ObserverKind = "control"
	// ObserverAttestedMeter is a metering component whose output the caller
	// has verified through attestation.
	ObserverAttestedMeter ObserverKind = "attested-meter"
)

func (k ObserverKind) valid() bool {
	switch k {
	case ObserverWorker, ObserverControl, ObserverAttestedMeter:
		return true
	}
	return false
}

// Observer is a principal that made an observation. Two observations with the
// same ID are the same principal.
type Observer struct {
	Kind ObserverKind
	ID   string
}

// assuranceFor derives the assurance level from a list of distinct principals.
//
// One principal yields the level its kind implies. Two or more principals yield
// cross-validated, but only when none of them is the worker: a worker cannot
// corroborate its own report.
func assuranceFor(principals []Observer) (Assurance, error) {
	if len(principals) == 0 {
		return "", fmt.Errorf("%w: observers", ErrMissingField)
	}
	for _, p := range principals {
		if !p.Kind.valid() {
			return "", ErrUnknownObserver
		}
	}
	if len(principals) == 1 {
		switch principals[0].Kind {
		case ObserverWorker:
			return AssuranceSelfReported, nil
		case ObserverControl:
			return AssuranceControlObserved, nil
		case ObserverAttestedMeter:
			return AssuranceAttestedMetering, nil
		}
		return "", ErrUnknownObserver
	}
	for _, p := range principals {
		if p.Kind == ObserverWorker {
			return "", ErrWorkerNotIndependent
		}
	}
	return AssuranceCrossValidated, nil
}

// observerLess orders observers by kind, then identity, giving records a
// canonical observer list that does not depend on input order.
func observerLess(a, b Observer) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.ID < b.ID
}

func sortObservers(obs []Observer) {
	sort.Slice(obs, func(i, j int) bool { return observerLess(obs[i], obs[j]) })
}

// checkObserver validates one observer's kind and identity.
func checkObserver(o Observer) error {
	if o.Kind == "" {
		return fmt.Errorf("%w: observer kind", ErrMissingField)
	}
	if !o.Kind.valid() {
		return ErrUnknownObserver
	}
	return checkText("observer identity", o.ID, MaxIdentifierLength)
}

// checkObserverSet validates a stored observer list. The list must be non-empty,
// bounded, canonically sorted, and free of repeated identities.
func checkObserverSet(obs []Observer) error {
	if len(obs) == 0 {
		return fmt.Errorf("%w: observers", ErrMissingField)
	}
	if len(obs) > MaxObservations {
		return fmt.Errorf("%w: observers", ErrFieldTooLong)
	}
	for i, o := range obs {
		if err := checkObserver(o); err != nil {
			return err
		}
		for j := 0; j < i; j++ {
			if obs[j].ID == o.ID {
				return fmt.Errorf("%w: duplicate observer", ErrInvalidField)
			}
		}
		if i > 0 && !observerLess(obs[i-1], o) {
			return fmt.Errorf("%w: observers are not canonically ordered", ErrInvalidField)
		}
	}
	return nil
}
