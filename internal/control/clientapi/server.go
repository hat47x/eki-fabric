package clientapi

import (
	"net/http"
	"time"
)

// NewServer returns an http.Server with timeouts and size limits set. The
// defaults of net/http are unbounded, which is wrong for a hostile network.
func NewServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}
}
