package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/core/admission"
)

// Server is a running Control Plane process.
type Server struct {
	http     *http.Server
	store    *sqlite.Store
	listener net.Listener
	log      *slog.Logger

	done     chan struct{}
	serveErr error
	closing  sync.Once
}

// Start opens the state store, builds the Client API, and begins serving. It
// returns once the listener is bound, so Addr is usable immediately. The
// caller stops the process with Shutdown.
func Start(ctx context.Context, cfg Config, log *slog.Logger) (*Server, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	auth, err := cfg.authenticator()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	policy, err := cfg.admissionPolicy()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}

	store, err := sqlite.Open(ctx, cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("open state store: %w", err)
	}

	svc, err := admission.New(admission.Config{
		Store:  store,
		Policy: policy,
		OnInternalError: func(operation string, err error) {
			log.Error("internal error", "operation", operation, "error", err.Error())
		},
	})
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	handler, err := clientapi.NewHandler(clientapi.Config{
		Service:       svc,
		Authenticator: auth,
		MaxBodyBytes:  cfg.MaxBodyBytes,
		Logger:        log,
	})
	if err != nil {
		_ = store.Close()
		return nil, err
	}

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	httpServer := clientapi.NewServer(cfg.Listen, handler)
	httpServer.ErrorLog = slog.NewLogLogger(log.Handler(), slog.LevelWarn)
	if cfg.TLS != nil {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, fmt.Errorf("load tls certificate: %w", err)
		}
		httpServer.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{cert},
		}
		listener = tls.NewListener(listener, httpServer.TLSConfig)
	}

	s := &Server{http: httpServer, store: store, listener: listener, log: log, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.serveErr = err
		}
	}()
	log.Info("control plane listening", "addr", listener.Addr().String(), "tls", cfg.TLS != nil)
	return s, nil
}

// Addr is the address the Client API is bound to.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// Done is closed when the server has stopped serving.
func (s *Server) Done() <-chan struct{} { return s.done }

// Err returns the reason serving stopped, or nil for a clean shutdown. It is
// only meaningful after Done is closed.
func (s *Server) Err() error { return s.serveErr }

// Shutdown stops accepting connections, waits for in-flight requests until ctx
// ends, then closes the state store. Committed state is already durable, so a
// forced stop loses nothing that was acknowledged.
func (s *Server) Shutdown(ctx context.Context) error {
	var result error
	s.closing.Do(func() {
		if err := s.http.Shutdown(ctx); err != nil {
			result = errors.Join(result, fmt.Errorf("shutdown http: %w", err))
			_ = s.http.Close()
		}
		<-s.done
		if err := s.store.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close state store: %w", err))
		}
	})
	return result
}

// ShutdownGrace is how long Run waits for in-flight requests.
const ShutdownGrace = 10 * time.Second

// Run starts the server and blocks until ctx is cancelled or serving fails.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	s, err := Start(ctx, cfg, log)
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
	case <-s.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), ShutdownGrace)
	defer cancel()
	err = s.Shutdown(shutdownCtx)
	return errors.Join(s.Err(), err)
}
