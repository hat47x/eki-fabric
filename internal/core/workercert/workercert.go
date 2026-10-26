// Package workercert defines how a Worker's identity appears in its client
// certificate, and when the Worker renews it (ADR-0027 sections 2.3 and 2.4).
//
// It holds no key and signs nothing, so both the Control Plane and the Agent
// can import it. The CA that signs lives in the workerca adapter.
package workercert

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hat47x/eki-fabric/internal/core/federation"
	"github.com/hat47x/eki-fabric/internal/core/ids"
)

const (
	// Scheme is the URI scheme of a Worker's certificate name.
	Scheme = "eki"
	// workerSegment separates the Worker namespace from Client and Admin
	// names, which never share it (ADR-0027 section 2.3 item 10).
	workerSegment = "worker"

	// DefaultValidity is the lifetime of a client certificate when the
	// configuration names none. The value is a provisional default (ADR-0027
	// section 2.4 item 14).
	DefaultValidity = 72 * time.Hour
	// MinValidity and MaxValidity bound the configurable lifetime. The lower
	// bound exists so tests can exercise expiry and renewal quickly.
	MinValidity = 10 * time.Second
	MaxValidity = 30 * 24 * time.Hour
)

var ErrInvalidName = errors.New("invalid worker certificate name")

// URI returns eki://<trust-domain>/worker/<worker-identity>.
func URI(trustDomain string, identity ids.WorkerIdentity) (*url.URL, error) {
	if _, err := federation.ParseTrustDomain(trustDomain); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidName, err)
	}
	if _, err := ids.ParseWorkerIdentity(string(identity)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidName, err)
	}
	return &url.URL{Scheme: Scheme, Host: trustDomain, Path: "/" + workerSegment + "/" + string(identity)}, nil
}

// ParseURI is the strict inverse of URI. Anything that URI would not produce is
// refused: another scheme, a port, user info, a query, a fragment, extra path
// segments, or an identity the registry could not have issued.
func ParseURI(u *url.URL) (trustDomain string, identity ids.WorkerIdentity, err error) {
	if u == nil || u.Scheme != Scheme || u.Opaque != "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || u.RawFragment != "" || u.ForceQuery || u.Host == "" || u.Port() != "" {
		return "", "", fmt.Errorf("%w: not an %s://<trust-domain>/%s/<identity> name", ErrInvalidName, Scheme, workerSegment)
	}
	if _, err := federation.ParseTrustDomain(u.Host); err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidName, err)
	}
	rest, ok := strings.CutPrefix(u.EscapedPath(), "/"+workerSegment+"/")
	if !ok || strings.ContainsAny(rest, "/%") {
		return "", "", fmt.Errorf("%w: not a worker path", ErrInvalidName)
	}
	id, err := ids.ParseWorkerIdentity(rest)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrInvalidName, err)
	}
	return u.Host, id, nil
}

// RenewAt is the time from which a Worker renews: when one third of the
// lifetime remains (ADR-0027 section 2.4 item 14).
func RenewAt(notBefore, notAfter time.Time) time.Time {
	return notAfter.Add(-notAfter.Sub(notBefore) / 3)
}
