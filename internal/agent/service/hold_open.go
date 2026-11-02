package service

import (
	"log/slog"
	"path/filepath"
	"time"

	"github.com/hat47x/eki-fabric/internal/agent/spool"
)

// DefaultOutputRetention is how long a body the Agent kept on disk may live.
// It matches the Control Plane's artifact retention (service.artifactRetention):
// a body the Agent still holds must not be expired on the other side (#508).
const DefaultOutputRetention = 24 * time.Hour

// openHold returns the hold the execution handler and the sender share. With a
// state directory it mirrors every body to disk, so a crash between the claim and
// the send no longer loses the output; without one it keeps the memory-only hold
// this Agent has always used.
//
// A spool that cannot be opened is reported and then ignored: losing durability is
// worse than nothing, but refusing to run at all would turn a disk problem into an
// outage.
func openHold(stateDir string, outputLimit int64, log *slog.Logger) *bodyHold {
	if stateDir == "" {
		return newBodyHold()
	}
	if outputLimit <= 0 {
		log.Warn("output spool disabled: no output limit", "state_dir", stateDir)
		return newBodyHold()
	}
	store, err := spool.New(filepath.Join(stateDir, "spool"), DefaultOutputRetention, nil)
	if err != nil {
		log.Warn("output spool disabled", "error", err.Error())
		return newBodyHold()
	}
	return newDurableBodyHold(store, outputLimit)
}

// DefaultOutputLimit bounds what the Agent mirrors to disk when it has no
// per-Task limit at hand. The WorkOrder's output_bytes_max is the authority at
// the Control Plane; this is the Worker-side guard that keeps a runaway output
// from filling the disk (#508, ADR-0037 §5).
const DefaultOutputLimit = 8 << 20
