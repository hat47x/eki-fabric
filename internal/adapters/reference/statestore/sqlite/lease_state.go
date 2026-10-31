package sqlite

import (
	"github.com/hat47x/eki-fabric/internal/core/leaseemission"
	"github.com/hat47x/eki-fabric/internal/spi/statestore"
)

// leaseDeliveryState names how current a Lease emission is at listing time
// (ADR-0030 §2.2). The listing query already drops Leases whose validity
// window has closed, so an emitted record is always current here; an ABORTED
// one is revoked and still listed, so a Worker that already started the
// Attempt learns the revocation on its next poll instead of hearing silence.
func leaseDeliveryState(record leaseemission.Record, _ leaseemission.Status) statestore.LeaseDeliveryState {
	if record.Status() == leaseemission.StatusAborted {
		return statestore.LeaseDeliveryRevoked
	}
	return statestore.LeaseDeliveryCurrent
}

// Today the public Store API refuses to abort an EMITTED emission (the only
// abort path is the emitter's own pre-emission rollback), so the revoked
// branch becomes reachable when the Control Plane revoke operation lands; the
// agent side already refuses a revoked listing.
