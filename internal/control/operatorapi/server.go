package operatorapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server is the Operator listener once it is accepting connections.
type Server struct {
	http   *http.Server
	ln     net.Listener
	logger *slog.Logger
}

// Serve starts the Operator listener on addr and returns it. The caller stops it
// with Close.
//
// The TLS configuration comes from Build, which is the only place that decides
// whether a client certificate is required; this function does not relax it.
func Serve(addr string, handler *Handler, tlsConfig *tls.Config, log *slog.Logger) (*Server, error) {
	if handler == nil {
		return nil, fmt.Errorf("%w: no handler", ErrOperatorCA)
	}
	if tlsConfig == nil || len(tlsConfig.Certificates) == 0 {
		return nil, fmt.Errorf("%w: the operator listener needs its own certificate", ErrOperatorCA)
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		return nil, fmt.Errorf("%w: the operator listener must require a client certificate", ErrOperatorCA)
	}
	if log == nil {
		log = slog.Default()
	}
	raw, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("operatorapi: listen: %w", err)
	}
	srv := &Server{
		http: &http.Server{
			Handler:           handler,
			TLSConfig:         tlsConfig,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    16 * 1024,
		},
		ln:     tls.NewListener(raw, tlsConfig),
		logger: log,
	}
	go func() {
		err := srv.http.Serve(srv.ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			srv.logger.Error("operator listener stopped", "error", err.Error())
		}
	}()
	return srv, nil
}

// Addr is the address the listener bound, which is what a test or a log line
// needs when the configuration asked for port 0.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops accepting and waits for in-flight requests to finish.
func (s *Server) Close(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
