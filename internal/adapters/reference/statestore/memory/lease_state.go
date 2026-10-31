package memory

import (
	"time"

	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// leaseDeliveryState names how current a Lease is at listing time
// (ADR-0030 §2.2). The emission record carries no expiry of its own, so the
// currency comes from the Lease row beside it; an ABORTED emission is revoked
// even though the Abort itself carries no time: the state flip is the fact the
// Worker must learn.
func leaseDeliveryState(record leaseemission.Record, leaseExpiresAt time.Time, now time.Time) statestore.LeaseDeliveryState {
	if record.Status() == leaseemission.StatusAborted {
		return statestore.LeaseDeliveryRevoked
	}
	if !now.Before(leaseExpiresAt) {
		return statestore.LeaseDeliveryExpired
	}
	return statestore.LeaseDeliveryCurrent
}

// Today the public Store API refuses to abort an EMITTED emission (the only
// abort path is the emitter's own pre-emission rollback), so this branch
// becomes reachable when the Control Plane revoke operation lands; the agent
// side already refuses a revoked listing.
