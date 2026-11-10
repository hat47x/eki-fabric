package clientapi

import (
	"net/http"
	"time"
)

// NewServer returns an http.Server with timeouts and size limits set. The
// defaults of net/http are unbounded, which is wrong for a hostile network.
//
// A zero duration in the overrides means "keep the default".
func NewServer(addr string, h http.Handler, overrides Timeouts) *http.Server {
	read := overrides.Read
	if read == 0 {
		read = 15 * time.Second
	}
	write := overrides.Write
	if write == 0 {
		write = 30 * time.Second
	}
	idle := overrides.Idle
	if idle == 0 {
		idle = 60 * time.Minute
	}
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       read,
		WriteTimeout:      write,
		IdleTimeout:       idle,
		MaxHeaderBytes:    16 * 1024,
	}
}

// Timeouts overrides the default server timeouts. Zero means "use the default".
type Timeouts struct {
	Read  time.Duration
	Write time.Duration
	Idle  time.Duration
}
