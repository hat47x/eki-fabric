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
	"sync/atomic"
	"time"

	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
)

// Server is a running Control Plane process.
type Server struct {
	http     *http.Server
	store    *sqlite.Store
	listener net.Listener
	log      *slog.Logger

	done     chan struct{}
	serveErr error

	workerHTTP     *http.Server
	workerListener net.Listener
	workerDone     chan struct{}
	workerErr      error

	// workerConfigured records that a Worker listener was requested. The
	// readiness probe requires it to be up before it answers ok (#289).
	workerConfigured bool
	workerUp         bool

	// readyFlag is the shared readiness switch the /readyz probe reads.
	readyFlag *atomic.Bool
	closing   sync.Once

	// dispatchDone is closed when the placement loop has stopped.
	dispatchDone chan struct{}

	// ca is the Worker CA when the Worker listener uses mTLS. caExpiry stops
	// the expiry monitor.
	ca       *workerca.CA
	caExpiry chan struct{}
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
		Store:   store,
		Results: store,
		Policy:  policy,
		// A quota rejection is retryable only when this deployment can finish a
		// Task without the Client: that is what the placement loop starts. A
		// process without it must tell the Client to cancel instead (#282).
		QuotaDrainsAutomatically: cfg.Dispatch != nil,
		OnInternalError: func(operation string, err error) {
			log.Error("internal error", "operation", operation, "error", err.Error())
		},
	})
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	// The /readyz probe reads the readiness state through this variable,
	// because the Server itself does not exist until the listener is bound
	// (#289). Until then the probe refuses: the process is not serving for
	// real yet.
	var readyState atomic.Bool
	handler, err := clientapi.NewHandler(clientapi.Config{
		Service:       svc,
		Authenticator: auth,
		MaxBodyBytes:  cfg.MaxBodyBytes,
		Logger:        log,
		Ready:         readyState.Load,
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
	s.readyFlag = &readyState
	s.refreshReady()

	if cfg.Worker != nil {
		s.workerConfigured = true
		if err := s.startWorker(ctx, cfg.Worker, store); err != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, err
		}
	}

	if cfg.Dispatch != nil {
		if err := s.startDispatch(ctx, cfg.Dispatch, store); err != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, err
		}
	}
	s.startCAExpiryMonitor()

	go func() {
		defer close(s.done)
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.serveErr = err
		}
	}()
	log.Info("control plane listening", "addr", listener.Addr().String(), "tls", cfg.TLS != nil)
	return s, nil
}

// refreshReady recomputes the /readyz answer from the listener state: the
// process is ready when every configured listener is serving (#289). The
// state store is open and the migrations ran before Start binds anything, so
// they are not part of the answer at run time.
func (s *Server) refreshReady() {
	if s.readyFlag == nil {
		return
	}
	s.readyFlag.Store(s.workerConfigured == false || s.workerUp)
}

// startWorker opens the optional Worker listener (process-level test profile)
// over the same state store as the Client API. Any failure is returned before
// the process serves anything: a half-configured Worker listener is not a
// reason to run without it.
func (s *Server) startWorker(ctx context.Context, w *WorkerConfig, store *sqlite.Store) error {
	accept, err := claimaccept.New(claimaccept.Config{Attempts: store, Leases: store, Claims: store, Clock: systemClock{}})
	if err != nil {
		return err
	}
	plane, err := claimplane.New(accept)
	if err != nil {
		return err
	}
	api, err := workerplane.New(workerplane.Config{Claims: plane})
	if err != nil {
		return err
	}
	var tlsConfig *tls.Config
	if w.TLS != nil {
		cert, err := tls.LoadX509KeyPair(w.TLS.CertFile, w.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("load worker tls certificate: %w", err)
		}
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}
	var handler http.Handler
	if w.mtls() {
		handler, err = s.mtlsHandler(w, store, plane)
		if err != nil {
			return err
		}
		// The client certificate is requested here and verified per request,
		// so the enrollment route works without one and a revoked Worker is
		// refused on its next request, not at its next handshake.
		tlsConfig.MinVersion = tls.VersionTLS13
		tlsConfig.ClientAuth = tls.RequestClientCert
	} else {
		handler, err = w.handler(api, s.log)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
	}
	if host, _, err := net.SplitHostPort(w.Listen); err == nil && !w.mtls() && !isLoopback(host) {
		s.log.Warn("worker listener is the TEST PROFILE (static Bearer token table) on a non-loopback address; allowed only by allow_test_profile_off_loopback, not for production (ADR-0027 2.5)", "addr", w.Listen)
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", w.Listen)
	if err != nil {
		return fmt.Errorf("listen for workers on %s: %w", w.Listen, err)
	}
	if tlsConfig != nil {
		listener = tls.NewListener(listener, tlsConfig)
	}
	srv := clientapi.NewServer(w.Listen, handler)
	srv.ErrorLog = slog.NewLogLogger(s.log.Handler(), slog.LevelWarn)
	s.workerHTTP, s.workerListener, s.workerDone = srv, listener, make(chan struct{})
	s.workerUp = true
	s.refreshReady()
	go func() {
		defer close(s.workerDone)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.workerErr = err
			// Do not keep serving the Client API with the Worker side dead.
			_ = s.http.Close()
		}
	}()
	s.log.Info("control plane worker listener", "addr", listener.Addr().String(), "tls", w.TLS != nil, "auth", w.authMode())
	return nil
}

// mtlsHandler builds the Worker listener of ADR-0027: the Worker CA from its
// files, the registry service over the State Store, and the mTLS handler.
func (s *Server) mtlsHandler(w *WorkerConfig, store *sqlite.Store, plane *claimplane.Service) (http.Handler, error) {
	api, err := workerplane.New(workerplane.Config{Claims: plane})
	if err != nil {
		return nil, err
	}
	ca, err := workerca.LoadFiles(w.MTLS.CACertFile, w.MTLS.CAKeyFile, w.MTLS.TrustDomain, w.MTLS.validity())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	// An expired CA stops every enrollment and renewal at once, and the error a
	// Worker sees (401) names none of that. Refuse to start instead (#304).
	now := time.Now().UTC()
	if err := ca.ValidateCurrent(now); err != nil {
		return nil, fmt.Errorf("%w: worker CA is not usable: %v", ErrInvalidConfig, err)
	}
	if remaining, soon := ca.ExpiresWithin(now, workerca.ExpiryWarningWindow); soon {
		s.log.Warn("worker CA certificate expires soon",
			"not_after", ca.NotAfter().Format(time.RFC3339),
			"remaining", remaining.Truncate(time.Minute).String())
	}
	s.ca = ca
	registry, err := workerenrollment.New(workerenrollment.Config{Store: store, Clock: systemClock{}, Issuer: ca})
	if err != nil {
		return nil, err
	}
	assignments, err := workerplane.NewAssignmentService(store)
	if err != nil {
		return nil, err
	}
	handler, err := workerplane.NewMTLSHandler(workerplane.MTLSConfig{
		Claims: api, Assignments: assignments, Heartbeats: store, Registry: registry, WorkerCA: ca.Certificate(), TrustDomain: w.MTLS.TrustDomain,
		MaxBodyBytes: w.MaxBodyBytes, Logger: s.log,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	return handler, nil
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// WorkerAddr is the address of the Worker listener, or "" when none is
// configured.
func (s *Server) WorkerAddr() string {
	if s.workerListener == nil {
		return ""
	}
	return s.workerListener.Addr().String()
}

// Addr is the address the Client API is bound to.
func (s *Server) Addr() string { return s.listener.Addr().String() }

// Done is closed when the server has stopped serving.
func (s *Server) Done() <-chan struct{} { return s.done }

// Err returns the reason serving stopped, or nil for a clean shutdown. It is
// only meaningful after Done is closed.
func (s *Server) Err() error { return errors.Join(s.serveErr, s.workerErr) }

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
		if s.workerHTTP != nil {
			if err := s.workerHTTP.Shutdown(ctx); err != nil {
				result = errors.Join(result, fmt.Errorf("shutdown worker http: %w", err))
				_ = s.workerHTTP.Close()
			}
			<-s.workerDone
		}
		if s.dispatchDone != nil {
			<-s.dispatchDone
		}
		if s.caExpiry != nil {
			close(s.caExpiry)
		}
		<-s.done
		if err := s.store.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close state store: %w", err))
		}
	})
	return result
}

// caExpiryCheckInterval is how often a running Control Plane re-checks the
// Worker CA certificate. The value is a compromise: often enough to warn well
// before a 30-day window closes, rarely enough to keep the log quiet.
const caExpiryCheckInterval = 6 * time.Hour

// startCAExpiryMonitor warns periodically while the Worker CA certificate is
// close to expiry (#304). A missing CA (no mTLS Worker listener) starts nothing.
func (s *Server) startCAExpiryMonitor() {
	if s.ca == nil {
		return
	}
	stop := make(chan struct{})
	s.caExpiry = stop
	go func() {
		ticker := time.NewTicker(caExpiryCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case now := <-ticker.C:
				if remaining, soon := s.ca.ExpiresWithin(now.UTC(), workerca.ExpiryWarningWindow); soon {
					s.log.Warn("worker CA certificate expires soon",
						"not_after", s.ca.NotAfter().Format(time.RFC3339),
						"remaining", remaining.Truncate(time.Minute).String())
				}
			}
		}
	}()
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
