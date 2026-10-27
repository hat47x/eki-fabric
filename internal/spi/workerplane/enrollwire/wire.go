// Package enrollwire is the HTTP shape of Worker enrollment and certificate
// renewal (ADR-0027 section 2.2 and 2.4). The Control Plane's Worker listener
// and the Agent share it, so neither re-declares the paths.
//
// Like testprofile, this is the process-level profile: it is not the Worker
// Protocol (#198).
package enrollwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const (
	// EnrollPath takes a POST whose Authorization header carries the
	// enrollment secret and whose body is a certificate signing request. The
	// request needs no client certificate. The secret is never in the URL.
	EnrollPath = "/v1/worker/enroll"
	// RenewPath takes a POST authenticated by the Worker's current client
	// certificate (mTLS) with a new certificate signing request as the body.
	RenewPath = "/v1/worker/renew"

	// CSRContentType marks a body that is one PKCS#10 request, as PEM or DER.
	CSRContentType = "application/pkcs10"
	// JSONContentType is the content type of a response.
	JSONContentType = "application/json"

	// MaxCSRBytes bounds a request body.
	MaxCSRBytes = 8 * 1024
	// MaxResponseBytes bounds what a client reads back.
	MaxResponseBytes = 64 * 1024

	// RefusedCode is the one error code of an unusable enrollment secret.
	RefusedCode = "enrollment_refused"
)

// Response is the body of a successful enroll or renew.
type Response struct {
	WorkerIdentity string `json:"worker_identity"`
	TrustDomain    string `json:"trust_domain"`
	// CertificatePEM is the Worker's new client certificate.
	CertificatePEM string `json:"certificate_pem"`
	// CAChainPEM is the Worker CA certificate that signed it.
	CAChainPEM string    `json:"ca_chain_pem"`
	NotBefore  time.Time `json:"not_before"`
	NotAfter   time.Time `json:"not_after"`
}

var ErrInvalidResponse = errors.New("invalid enrollment response")

// DecodeResponse parses a response strictly: unknown fields and trailing data
// are refused.
func DecodeResponse(raw []byte) (Response, error) {
	var r Response
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Response{}, ErrInvalidResponse
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Response{}, ErrInvalidResponse
	}
	if r.WorkerIdentity == "" || r.TrustDomain == "" || r.CertificatePEM == "" || r.NotAfter.IsZero() {
		return Response{}, ErrInvalidResponse
	}
	return r, nil
}
