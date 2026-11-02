package measurement

import "log/slog"

// Log writes every count as one line. It is the default way a deployment gets the
// numbers out: ADR-0039 §2.3 says EKI does not store the totals, so writing them
// is the caller's job, and a log line is the shape that needs no new storage.
//
// One line per (kind, reason) rather than one line with everything keeps the
// numbers queryable: a log reader can count `kind=admission.rejected` without
// parsing a blob.
func (c *Counters) Log(log *slog.Logger) {
	if c == nil || log == nil {
		return
	}
	for _, entry := range c.Snapshot() {
		log.Info("measurement",
			"kind", string(entry.Kind),
			"reason", entry.Reason,
			"count", entry.Count,
		)
	}
}
