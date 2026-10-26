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
func RenewLoop(ctx context.Context, holder *Holder, controlURL, stateDir string, retry time.Duration, log *slog.Logger, onRenewed func()) {
	if retry <= 0 {
		retry = DefaultRenewRetry
	}
	for {
		id := holder.Get()
		wait := time.Until(id.RenewAt())
		if wait > maxCheck {
			wait = maxCheck
		}
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			continue
		}
		if id.Expired(time.Now()) {
			log.Error("worker certificate expired and cannot renew; enroll again with a new enrollment credential", "worker", id.WorkerIdentity)
			return
		}
		next, err := Renew(ctx, controlURL, stateDir, id)
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
		log.Warn("worker certificate renewal failed; will retry", "worker", id.WorkerIdentity, "error", err.Error(), "expires", id.Leaf.NotAfter.Format(time.RFC3339))
		t := time.NewTimer(retry)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}
