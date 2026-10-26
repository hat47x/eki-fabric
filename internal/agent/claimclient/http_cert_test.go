package claimclient

import (
	"crypto/tls"
	"errors"
	"testing"
)

func TestHTTPClientCertificateConfig(t *testing.T) {
	cert := func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &tls.Certificate{}, nil }
	for name, cfg := range map[string]HTTPConfig{
		"certificate and token": {ControlURL: "https://127.0.0.1:1", Token: httpToken, ClientCertificate: cert},
		"certificate over http": {ControlURL: "http://127.0.0.1:1", ClientCertificate: cert},
		"neither":               {ControlURL: "https://127.0.0.1:1"},
	} {
		if _, err := NewHTTP(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: %v, want ErrInvalidConfig", name, err)
		}
	}
	if _, err := NewHTTP(HTTPConfig{ControlURL: "https://127.0.0.1:1", ClientCertificate: cert}); err != nil {
		t.Fatalf("certificate authentication: %v", err)
	}
}
