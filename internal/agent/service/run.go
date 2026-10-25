// Package service is the Agent process's claim delivery loop for the
// process-level TEST PROFILE (not the Worker Protocol #198, and not an
// enrollment decision).
//
// It opens the Agent's durable claim queue and repeatedly delivers the claims
// that are due to a Control Plane over HTTP(S). It never discards a claim: a
// failed delivery stays queued and is retried with backoff, across restarts of
// either process.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	queuesqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/claimclient"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

var ErrInvalidConfig = errors.New("invalid agent configuration")

const (
	DefaultInterval   = 2 * time.Second
	DefaultRetryBase  = time.Second
	DefaultRetryMax   = time.Minute
	DefaultBatchLimit = 16
)

// Config is the Agent's delivery configuration.
type Config struct {
	// ControlURL is the Control Plane's Worker listener, https://host:port
	// (http only on a loopback address).
	ControlURL string `json:"control_url"`
	// QueueDB is the Agent's dedicated local SQLite claim queue.
	QueueDB string `json:"queue_db"`
	// CAFile is a PEM file of the CA that vouches for the Control Plane. Empty
	// means the system roots.
	CAFile string `json:"ca_file,omitempty"`
	// Token is the bearer token of the test profile's session table. Prefer
	// TokenFile or the environment over the command line.
	Token     string `json:"token,omitempty"`
	TokenFile string `json:"token_file,omitempty"`
	// WorkerIdentity and TrustDomain are the principal this Agent expects the
	// token to authenticate as. Claims naming another Worker are never sent.
	WorkerIdentity string `json:"worker_identity"`
	TrustDomain    string `json:"trust_domain,omitempty"`

	// Interval is how often the queue is checked. Zero means the default.
	Interval time.Duration `json:"-"`
	// RetryBase and RetryMax bound the backoff after a failed delivery. Zero
	// means the defaults.
	RetryBase time.Duration `json:"-"`
	RetryMax  time.Duration `json:"-"`
	// BatchLimit is the most claims one pass sends. Zero means the default.
	BatchLimit int `json:"batch_limit,omitempty"`
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Run delivers queued claims until ctx is cancelled, then closes the queue and
// returns nil. A configuration error is returned before anything is opened.
func Run(ctx context.Context, cfg Config, token string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	interval, base, max, limit := cfg.Interval, cfg.RetryBase, cfg.RetryMax, cfg.BatchLimit
	if interval == 0 {
		interval = DefaultInterval
	}
	if base == 0 {
		base = DefaultRetryBase
	}
	if max == 0 {
		max = DefaultRetryMax
	}
	if limit == 0 {
		limit = DefaultBatchLimit
	}
	switch {
	case interval < 0 || base < 0 || max < 0 || limit < 0:
		return fmt.Errorf("%w: negative interval, retry delay or batch limit", ErrInvalidConfig)
	case cfg.QueueDB == "":
		return fmt.Errorf("%w: queue database is required", ErrInvalidConfig)
	case cfg.WorkerIdentity == "":
		return fmt.Errorf("%w: worker identity is required", ErrInvalidConfig)
	}
	var cfgHTTP claimclient.HTTPConfig
	cfgHTTP.ControlURL, cfgHTTP.Token = cfg.ControlURL, token
	if cfg.CAFile != "" {
		pool, err := claimclient.LoadRootCAs(cfg.CAFile)
		if err != nil {
			return err
		}
		cfgHTTP.RootCAs = pool
	}
	api, err := claimclient.NewHTTP(cfgHTTP)
	if err != nil {
		return err
	}
	defer api.CloseIdleConnections()
	client, err := claimclient.New(api, workerplane.Principal{WorkerIdentity: cfg.WorkerIdentity, TrustDomain: cfg.TrustDomain})
	if err != nil {
		return err
	}

	store, err := queuesqlite.Open(ctx, cfg.QueueDB)
	if err != nil {
		return fmt.Errorf("open claim queue: %w", err)
	}
	defer store.Close()
	queue, err := claimqueue.New(claimqueue.Config{
		Store: store, Sender: client, Clock: systemClock{}, BaseDelay: base, MaxDelay: max,
	})
	if err != nil {
		return err
	}

	log.Info("agent delivering claims", "control", cfg.ControlURL, "worker", cfg.WorkerIdentity)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		report, err := queue.DeliverDue(ctx, limit)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			log.Error("claim delivery pass failed", "error", err.Error())
		case report.Delivered+report.Retried > 0:
			log.Info("claim delivery pass", "delivered", report.Delivered, "retried", report.Retried)
		}
		timer.Reset(interval)
	}
}
