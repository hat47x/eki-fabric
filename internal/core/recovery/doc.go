// Package recovery decides whether an interrupted Task Attempt may re-enter
// the Fabric as a new Attempt, and bounds how much work and network transfer
// may re-enter per window.
//
// The package is a pure core: it reads no clock, opens no file, and starts no
// goroutine. The caller passes the current time, the policy, and the durable
// state it has read. Everything the package decides is a function of those
// inputs, so the same inputs produce the same Decision and the same Permit
// digest after a Control Plane restart.
//
// # Why this is separate from the Scheduler
//
// A Worker leaving is normal (RQ-052). It must not be treated as a Task
// failure, and it must not be answered with an immediate retry storm. The
// recovery boundary separates three questions that are easy to conflate:
//
//  1. May this Task run again at all? The Task's reliability contract decides
//     it (RQ-011), and the attempt budget decides how often.
//  2. When may the next Attempt start? Backoff and jitter decide it.
//  3. How much may re-enter per window? Release count, transfer bytes, and
//     compute millis decide it, separately from 1 and 2.
//
// Priority only orders the releases inside a window. It never raises a budget;
// that keeps a flood of urgent Tasks from turning into a flood of transfers.
//
// # What a Permit is
//
// A Permit is evidence that the recovery boundary admitted a *new* Attempt for
// a specific Task, previous Attempt, policy revision, and time window. It is
// not a Lease, not a Placement Approval, and not authority to run. The fields
// are unexported so that other packages cannot construct one; a production
// caller must obtain it from this package and persist it with the new Attempt.
//
// The permit is intentionally narrow:
//
//	recovery.Permit != Lease
//	recovery.Permit != Placement Approval
//	recovery.Permit != permission to widen the attempt budget
package recovery
