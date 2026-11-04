package service

import (
	"errors"
	"testing"
)

func TestAnAbsentOperatorSectionMeansNoRoute(t *testing.T) {
	var cfg *OperatorConfig
	if _, err := cfg.ServerConfig(); !errors.Is(err, ErrInvalidOperatorConfig) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidOperatorConfig)
	}
}

func TestACompleteSectionIsAccepted(t *testing.T) {
	cfg := &OperatorConfig{
		Listen: "127.0.0.1:8444", CAFile: "ca.pem", CertFile: "server.pem", KeyFile: "server.key",
		TrustDomain: "example.org",
	}
	got, err := cfg.ServerConfig()
	if err != nil {
		t.Fatalf("ServerConfig() error = %v", err)
	}
	if got.CAPath != "ca.pem" || got.TrustDomain != "example.org" {
		t.Fatalf("config = %+v", got)
	}
}

func TestASectionWithoutAListenAddressIsRefused(t *testing.T) {
	cfg := &OperatorConfig{CAFile: "ca.pem", CertFile: "server.pem", KeyFile: "server.key"}
	if _, err := cfg.ServerConfig(); !errors.Is(err, ErrInvalidOperatorConfig) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidOperatorConfig)
	}
}

func TestASectionWithoutACAItsRefused(t *testing.T) {
	cfg := &OperatorConfig{Listen: "127.0.0.1:8444", CertFile: "server.pem", KeyFile: "server.key"}
	if _, err := cfg.ServerConfig(); !errors.Is(err, ErrInvalidOperatorConfig) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidOperatorConfig)
	}
}

func TestASectionWithoutItsOwnCertificateIsRefused(t *testing.T) {
	cfg := &OperatorConfig{Listen: "127.0.0.1:8444", CAFile: "ca.pem"}
	if _, err := cfg.ServerConfig(); !errors.Is(err, ErrInvalidOperatorConfig) {
		t.Fatalf("error = %v, want %v", err, ErrInvalidOperatorConfig)
	}
}
