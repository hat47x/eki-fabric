package operatorapi

import (
	"crypto/tls"
	"testing"
)

func TestServingNeedsAHandler(t *testing.T) {
	if _, err := Serve("127.0.0.1:0", nil, &tls.Config{}, nil); err == nil {
		t.Fatal("Serve() accepted no handler")
	}
}

func TestServingNeedsItsOwnCertificate(t *testing.T) {
	handler := New(Config{})
	if _, err := Serve("127.0.0.1:0", handler, &tls.Config{}, nil); err == nil {
		t.Fatal("Serve() accepted a listener without a certificate")
	}
}

func TestServingRefusesAListenerThatDoesNotVerifyTheClient(t *testing.T) {
	handler := New(Config{})
	cfg := &tls.Config{
		Certificates: []tls.Certificate{{}},
		ClientAuth:   tls.NoClientCert,
	}
	if _, err := Serve("127.0.0.1:0", handler, cfg, nil); err == nil {
		t.Fatal("Serve() accepted a listener that does not verify the client")
	}
}
