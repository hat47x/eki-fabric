package clientapi

import (
	"net/http"
	"time"
)

// NewServer returns an http.Server with timeouts and size limits set. The
// defaults of net/http are unbounded, which is wrong for a hostile network.
//
// The ReadTimeout (15s) and WriteTimeout (30s) are shared by both the Control
// Plane and the Worker listener. For the Control Plane, 15s means a 64 MiB
// upload needs at least ~4.3 MB/s; for the Worker listener, 30s means an 8 MiB
// output needs at least ~2.7 MB/s. Deployments on slower networks should raise
// these values (#816).
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Minute,
		MaxHeaderBytes:    16 * 1024,
	}
}
