package enroll

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	// maxCheck bounds how long the loop sleeps at a time. Timers count
	// monotonic time, and certificates expire by the wall clock; after a
	// suspend or a clock step the loop looks again within this time.
	maxCheck = 5 * time.Second
	// DefaultRenewRetry is the wait after a failed renewal.
	DefaultRenewRetry = 15 * time.Second
)

// RenewLoop renews the certificate in holder when a third of its lifetime
// remains (ADR-0027 section 2.4 item 14), and keeps trying after a failure
// until the certificate expires and then for as long as ctx lives. An expired
// certificate cannot renew; the loop reports that and stops, because only a
// new enrollment credential helps.
//
// onRenewed runs after each success, for example to close idle connections so
// the next one uses the new certificate.
//
// The loop reads the time from the clock given by WithClock, the system clock
// by default. A certificate expires by the wall clock, so the default compares
// it with the wall clock and re-reads it at least every maxCheck.
func RenewLoop(ctx context.Context, holder *Holder, controlURL, stateDir string, retry time.Duration, log *slog.Logger, onRenewed func(), opts ...Option) {
	clock := applyOptions(opts).clock
	if retry <= 0 {
		retry = DefaultRenewRetry
	}
	for {
		id := holder.Get()
		wait := id.RenewAt().Sub(clock.Now())
		if wait > maxCheck {
			wait = maxCheck
		}
		if wait > 0 {
			t := clock.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C():
			}
			continue
		}
		if id.Expired(clock.Now()) {
			log.Error("worker certificate expired and cannot renew; enroll again with a new enrollment credential", "worker", id.WorkerIdentity)
			return
		}
		next, err := Renew(ctx, controlURL, stateDir, id, opts...)
		switch {
		case err == nil:
			holder.Set(next)
			log.Info("worker certificate renewed", "worker", next.WorkerIdentity, "not_after", next.Leaf.NotAfter.Format(time.RFC3339))
			if onRenewed != nil {
				onRenewed()
			}
			continue
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrExpired):
			log.Error("worker certificate expired and cannot renew; enroll again with a new enrollment credential", "worker", id.WorkerIdentity)
			return
		}
		// A server-side failure can be an expired Worker CA (#304): every
		// renewal fails the same way, and enrolling again fails too. The Agent
		// cannot tell that apart from a network failure, so the message names
		// the possibility instead of promising that enrolling again helps.
		log.Warn("worker certificate renewal failed; will retry (if it persists, the Control Plane's Worker CA may be expired)",
			"worker", id.WorkerIdentity, "error", err.Error(), "expires", id.Leaf.NotAfter.Format(time.RFC3339))
		t := clock.NewTimer(retry)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}
	}
}
