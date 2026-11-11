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

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/statestore/sqlite"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/workerca"
	"github.com/hat47x/eki-fabric/internal/control/claimaccept"
	"github.com/hat47x/eki-fabric/internal/control/claimbody"
	"github.com/hat47x/eki-fabric/internal/control/claimplane"
	"github.com/hat47x/eki-fabric/internal/control/clientapi"
	"github.com/hat47x/eki-fabric/internal/control/leaserecall"
	"github.com/hat47x/eki-fabric/internal/control/operatorapi"
	"github.com/hat47x/eki-fabric/internal/control/resultacceptance"
	"github.com/hat47x/eki-fabric/internal/control/workerplane"
	"github.com/hat47x/eki-fabric/internal/core/admission"
	"github.com/hat47x/eki-fabric/internal/core/ids"
	"github.com/hat47x/eki-fabric/internal/core/measurement"
	"github.com/hat47x/eki-fabric/internal/core/resultaccept"
	"github.com/hat47x/eki-fabric/internal/core/resultclaim"
	"github.com/hat47x/eki-fabric/internal/core/trustbundle"
	"github.com/hat47x/eki-fabric/internal/core/workerenrollment"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// Server is a running Control Plane process.
type Server struct {
	http     *http.Server
	store    *sqlite.Store
	listener net.Listener
	log      *slog.Logger

	// counters holds the ADR-0039 numbers for the life of the process.
	counters *measurement.Counters
	// recallDone closes when the lease reclaim loop has stopped (#555).
	recallDone chan struct{}
	// leaseRegistry is the file the recall loop reads. The placement loop writes
	// every Lease it reserves into it (#745). Nil when no lease_registry is set.
	leaseRegistry *leaserecall.File
	// countersDone closes when the periodic write-out has stopped (#392).
	countersDone chan struct{}
	// operator is the Operator listener of ADR-0038, nil when the process has no
	// Operator routes. approvals is the book those approvals are collected in,
	// shared with whatever admits a new trust bundle version.
	operator  *operatorapi.Server
	approvals *trustbundle.ApprovalBook

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

	// The retrieval route reads the same directory the Worker listener writes to.
	// Two Store values over one directory are fine: the store keeps no state in
	// memory, and a body is one file named after its digest (#494).
	var artifacts clientapi.ArtifactSource
	// sink is the same directory seen from the write side: a body a Client
	// uploads is fetched by the retrieval route (#245).
	var sink clientapi.ArtifactSink
	// inputs resolves an input reference for admission. Nil means this
	// deployment keeps no bodies, so a Task may not name any input: the
	// reference could never be checked and the Worker could never fetch it
	// (ADR-0029 §5.7).
	var inputs admission.ArtifactResolver
	if cfg.Worker != nil && cfg.Worker.ArtifactsDir != "" {
		bodies, aerr := artifactfile.New(cfg.Worker.ArtifactsDir)
		if aerr != nil {
			_ = store.Close()
			return nil, fmt.Errorf("open the artifact store: %w", aerr)
		}
		// The same state store answers whether a Task was withdrawn, so a body whose
		// Task was cancelled stops being served (ADR-0037 §9, #592).
		artifacts = artifactSource{store: bodies, tasks: taskWithdrawalFromState{tasks: store}}
		sink = artifactSink{store: bodies}
		inputs = artifactResolver{store: bodies}
	}

	svc, err := admission.New(admission.Config{
		Store:   store,
		Results: store,
		Policy:  policy,
		// A Task that names an input is admitted only when the body exists, so
		// the Worker is never told to fetch something no one kept (#763).
		Inputs: inputs,
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
	counters := measurement.New()
	// One book for the whole process: the Operator API writes into it and the
	// version admission path reads from it (#545).
	approvals := trustbundle.NewApprovalBook()
	handler, err := clientapi.NewHandler(clientapi.Config{
		Service:          svc,
		Authenticator:    auth,
		MaxBodyBytes:     cfg.MaxBodyBytes,
		Logger:           log,
		Ready:            readyState.Load,
		Artifacts:        artifacts,
		Sink:             sink,
		ArtifactAuditErr: artifactAuditErr(cfg, log, ctx),
	})
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	// The counters of ADR-0039 are counted by the handler and written out when the
	// process stops; nothing here is stored.
	handler.SetCounters(counters)

	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("listen on %s: %w", cfg.Listen, err)
	}

	httpServer := clientapi.NewServer(cfg.Listen, handler, clientapi.Timeouts{
		Read:  cfg.ReadTimeout,
		Write: cfg.WriteTimeout,
		Idle:  cfg.IdleTimeout,
	})
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

	s := &Server{http: httpServer, store: store, listener: listener, log: log, done: make(chan struct{}), counters: counters}
	s.readyFlag = &readyState
	s.refreshReady()
	if cfg.LeaseRegistry == "" {
		// Reclaim loses its memory without a registry: leases that expire while the
		// process is down are not swept on the next start (#555). Say so at start-up
		// rather than let a deployment assume it is reclaiming.
		log.Warn("lease reclaim is disabled: no lease_registry is configured, so expired leases are only found by whoever looks")
	}
	if cfg.LeaseRegistry != "" {
		registry, rerr := leaserecall.OpenFile(cfg.LeaseRegistry)
		if rerr != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, rerr
		}
		s.leaseRegistry = registry
		// The reclaimer closes the Attempt of an expired Lease before removing
		// it from the registry, so an Attempt without a ResultClaim does not
		// stay open forever (ADR-0037, #745).
		closer := recallCloser{
			attempts: store, claims: store, admissions: store, reclaimer: registry,
			grace: RecallGrace, now: time.Now,
		}
		if cerr := closer.validate(); cerr != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, cerr
		}
		recall, rerr := leaserecall.New(leaserecall.Config{
			Lister: registry, Reclaimer: closer, OnReclaim: recallCounter(counters),
		})
		if rerr != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, rerr
		}
		s.recallDone = make(chan struct{})
		go func() {
			defer close(s.recallDone)
			leaserecall.Run(ctx, recall, time.Minute, log)
		}()
	}

	// The ADR-0039 numbers are written periodically, not only on the way out
	// (#392). A deployment that runs for weeks would otherwise never see them: the
	// shutdown line is a convenience, not the way the numbers reach anyone.
	s.countersDone = make(chan struct{})
	go func() {
		defer close(s.countersDone)
		ticker := time.NewTicker(counterWriteEvery)
		defer ticker.Stop()
		for {
			select {
			case <-s.done:
				// Shutdown closes done before it waits here, so the last write on
				// the way out still happens after this returns.
				return
			case <-ticker.C:
				counters.Log(log)
			}
		}
	}()

	if cfg.Operator != nil {
		// The Operator listener is a second, separate door (ADR-0038): its own
		// port, its own CA, and a client certificate required before any request
		// reaches the handler.
		oc, err := cfg.Operator.ServerConfig()
		if err != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, err
		}
		s.approvals = approvals
		oc.Approver = approvals
		// The default record is the process log. ADR-0040 §2 asks for append-only
		// storage that the process cannot delete from, and a log file is neither, so
		// say at start-up that the promise is not met yet (the operator can plug a
		// sink in).
		if cfg.AuditSinkURL == "" {
			log.Warn("operator audit goes to the process log: ADR-0040 §2 asks for append-only storage the process cannot rewrite")
		}
		oc.Audit = func(a operatorapi.Access) {
			log.Info("operator access", "name", a.Name, "role", a.Role,
				"operation", a.Operation, "target", a.Target, "outcome", a.Outcome)
		}
		oc.AuditErr = operatorAuditErr(cfg, log, ctx)
		oc.Book = approvals
		oc.Bundles = store
		oc.Admitter = startAdmitter(ctx, store, approvals, append([]string{cfg.TrustBundleKey}, cfg.TrustBundleKeys...), log)
		oc.Logger = log
		handler, tlsConfig, err := operatorapi.Build(oc)
		if err != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, err
		}
		s.operator, err = operatorapi.Serve(cfg.Operator.Listen, handler, tlsConfig, log)
		if err != nil {
			_ = listener.Close()
			_ = store.Close()
			return nil, err
		}
		log.Info("operator listener started", "address", s.operator.Addr())
	}

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

// defaultResultPolicy is the fallback policy when a deployment does not supply
// one. It requires two independent verifications that differ in both principal
// and operator (ADR-0032 §2.x). A deployment that needs a different policy
// should make ResultPolicySource configurable.
var defaultResultPolicy = resultacceptance.ResultPolicy{
	Policy: resultaccept.Policy{
		Level:              resultaccept.LevelRA2,
		Threshold:          2,
		DistinctDimensions: []resultaccept.Dimension{resultaccept.DimensionPrincipal, resultaccept.DimensionOperator},
	},
	Output: resultaccept.OutputMechanical,
}

// defaultProducers is the fallback producer source. It returns the Worker
// identity as the principal and the claim's executor digest, which was already
// verified against the WorkOrder when the claim was accepted. The same identity
// is used for the operator dimension as a fallback; a deployment that needs
// independent operator tracking should configure a custom ProducerSource.
type defaultProducers struct{}

func (defaultProducers) ProducerDimensions(_ context.Context, workerIdentity string, claim resultclaim.Claim) (map[resultaccept.Dimension]string, error) {
	return map[resultaccept.Dimension]string{
		resultaccept.DimensionPrincipal:      workerIdentity,
		resultaccept.DimensionOperator:       workerIdentity,
		resultaccept.DimensionExecutorDigest: claim.ExecutorDigest().Hex(),
	}, nil
}

func (s *Server) resultAcceptance(store *sqlite.Store) (*resultacceptance.Service, error) {
	svc, err := resultacceptance.New(resultacceptance.Config{
		Attempts:   store,
		Tasks:      store,
		Claims:     store,
		Results:    store,
		Rejections: store,
		Policies:   fixedPolicy{policy: defaultResultPolicy},
		Producers:  defaultProducers{},
		Clock:      systemClock{},
	})
	if err != nil {
		return nil, err
	}
	return svc, nil
}

type fixedPolicy struct {
	policy resultacceptance.ResultPolicy
}

func (f fixedPolicy) ResultPolicy(context.Context, ids.TaskID) (resultacceptance.ResultPolicy, error) {
	return f.policy, nil
}

// startWorker opens the optional Worker listener (process-level test profile)
// over the same state store as the Client API. Any failure is returned before
// the process serves anything: a half-configured Worker listener is not a
// reason to run without it.
func (s *Server) startWorker(ctx context.Context, w *WorkerConfig, store *sqlite.Store) error {
	resultAcceptance, err := s.resultAcceptance(store)
	if err != nil {
		return err
	}
	accept, err := claimaccept.New(claimaccept.Config{Attempts: store, Leases: store, Claims: store, Clock: systemClock{}, Tasks: store, ResultAcceptance: resultAcceptance})
	if err != nil {
		return err
	}
	plane, err := claimplane.New(accept)
	if err != nil {
		return err
	}
	// ADR-0039 §2.1: count the refusals this boundary makes. The reason is a closed
	// set ("identity", "invalid", "owner", "rejected"), so the number is readable
	// without parsing prose.
	plane.SetRefusalHook(func(reason string) { s.counters.Add(measurement.ClaimRefused, reason) })
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
	srv := clientapi.NewServer(w.Listen, handler, clientapi.Timeouts{})
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
	assignments, err := workerplane.NewAssignmentService(store, store)
	if err != nil {
		return nil, err
	}
	if w.ArtifactsDir == "" {
		return nil, fmt.Errorf("%w: artifacts_dir is required with auth %q: a Control Plane that cannot keep bodies must not accept them (ADR-0034 2.3)", ErrInvalidConfig, w.Auth)
	}
	bodies, err := artifactfile.New(w.ArtifactsDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	receiver, err := claimbody.New(store, store, leaseWorker{store}, bodySink{bodies})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	// Keeping a body without recording who may read it would fill the disk with
	// bytes nobody can fetch, so the metadata is written as part of receiving
	// (#514). The retention matches the Worker's own spool default, so a body a
	// Worker still holds is not already expired here.
	receiver, err = receiver.WithMeta(attemptOwner{attempts: store, admissions: store}, metaSink{store: bodies, dataClass: w.DataClass}, artifactRetention)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	handler, err := workerplane.NewMTLSHandler(workerplane.MTLSConfig{
		Claims: api, Assignments: assignments, Heartbeats: store, BodyReceiver: bodyReceiver{receiver}, Registry: registry, WorkerCA: ca.Certificate(), TrustDomain: w.MTLS.TrustDomain,
		MaxBodyBytes: w.MaxBodyBytes, Logger: s.log,
		// The version the Control Plane holds is what Workers are given at
		// enrollment and what their roots are checked against (ADR-0036 §2.1).
		// A store that does not keep trust bundles leaves the check off, so a
		// deployment can adopt the feature by storing its first version.
		TrustBundle: currentTrustBundle(store),
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
		if s.operator != nil {
			if err := s.operator.Close(ctx); err != nil {
				result = errors.Join(result, fmt.Errorf("shutdown operator: %w", err))
			}
		}
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
		// Write the ADR-0039 numbers once, on the way out. They are not stored
		// anywhere by EKI, so this line is what a deployment keeps.
		if s.recallDone != nil {
			<-s.recallDone
		}
		if s.countersDone != nil {
			<-s.countersDone
		}
		if s.counters != nil {
			s.counters.Log(s.log)
		}
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

// leaseWorker reads the Worker identity a Lease names, from the lease record
// the State Store already keeps. The receiver's LeaseStore is the identity
// read, not the whole record, so the receiver cannot grow a dependency on
// lease internals.
type leaseWorker struct {
	store *sqlite.Store
}

func (l leaseWorker) LoadLeaseWorker(ctx context.Context, id ids.LeaseID) (string, error) {
	lease, err := l.store.LoadLease(ctx, id)
	if err != nil {
		return "", err
	}
	return lease.WorkerIdentity, nil
}

// currentTrustBundle reports the newest stored version, or false when the store
// keeps none. It reads on each call so a version admitted at runtime is used by
// the next request without a restart; the read is one row plus its issuers.
func currentTrustBundle(store any) func() (trustbundle.Signed, bool, error) {
	bundles, ok := store.(statestore.TrustBundleStore)
	if !ok {
		return nil
	}
	return func() (trustbundle.Signed, bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		record, err := bundles.LatestTrustBundle(ctx)
		if errors.Is(err, statestore.ErrNotFound) {
			// No version stored is the compatibility mode the operator chose, not a
			// fault: the certificate check alone applies (ADR-0036 §2.3).
			return trustbundle.Signed{}, false, nil
		}
		if err != nil {
			// A version may exist and be unreadable. Saying "none" here would skip the
			// version check while the operator believes it is on (#467).
			return trustbundle.Signed{}, false, fmt.Errorf("trust bundle: read latest: %w", err)
		}
		signed, err := trustbundle.FromRecord(record)
		if err != nil {
			return trustbundle.Signed{}, false, fmt.Errorf("trust bundle: decode version %d: %w", record.Version, err)
		}
		return signed, true, nil
	}
}

// counterWriteEvery is how often the ADR-0039 numbers are written while the
// process is running (#392). Five minutes keeps a long-running deployment's numbers
// visible without turning the log into a stream of counters.
const counterWriteEvery = 5 * time.Minute
