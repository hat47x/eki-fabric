package leaserecall

import (
	"context"
	"log/slog"
	"time"
)

// Run sweeps until ctx ends (#555). The first sweep happens immediately: a process
// that has just started is the one most likely to find leases whose window passed
// while it was down.
//
// A sweep failure is logged and the loop continues. Stopping the loop would mean
// that one bad read turns into "nothing is ever reclaimed again", which is the
// failure the loop exists to avoid.
func Run(ctx context.Context, svc *Service, every time.Duration, log *slog.Logger) {
	if svc == nil || every <= 0 || ctx == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	sweep := func() {
		taken, err := svc.Sweep(ctx)
		if err != nil {
			log.Warn("lease reclaim sweep failed", "error", err.Error())
			return
		}
		if taken > 0 {
			log.Info("leases reclaimed", "count", taken)
		}
	}
	sweep()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		}
	}
}
