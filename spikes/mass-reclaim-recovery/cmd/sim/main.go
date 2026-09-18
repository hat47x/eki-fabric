package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	recovery "eki.local/mass-reclaim-spike"
)

type report struct {
	InterruptedTasks        int    `json:"interrupted_tasks"`
	NaiveImmediateRetries   int    `json:"naive_immediate_retries"`
	NaiveTransferBytes      uint64 `json:"naive_transfer_bytes"`
	ControlledFirstWindow   int    `json:"controlled_first_window_releases"`
	ControlledTransferBytes uint64 `json:"controlled_first_window_transfer_bytes"`
	DeferredAfterFirst      int    `json:"deferred_after_first_window"`
}

func main() {
	const MiB = uint64(1024 * 1024)
	const GiB = uint64(1024 * 1024 * 1024)
	now := time.Unix(0, 0)
	policy := recovery.RecoveryPolicy{
		BaseBackoff:          time.Second,
		MaxBackoff:           30 * time.Second,
		MaxReleasesPerWindow: 50,
		MaxTransferPerWindow: 2 * GiB,
		Window:               time.Second,
	}

	attempts := make([]recovery.InterruptedAttempt, 1000)
	for i := range attempts {
		attempts[i] = recovery.InterruptedAttempt{
			TaskID:            fmt.Sprintf("task-%04d", i),
			Priority:          recovery.PriorityNormal,
			Retryable:         true,
			AttemptsUsed:      1,
			MaxAttempts:       4,
			EstimatedTransfer: 64 * MiB,
		}
	}

	naiveRetries, naiveTransfer := recovery.NaiveImmediateRetry(attempts)
	intake, err := recovery.IntakeReclaim(now, attempts, policy)
	if err != nil {
		panic(err)
	}
	first, err := recovery.ReleaseWindow(now.Add(31*time.Second), intake.Pending, policy)
	if err != nil {
		panic(err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(report{
		InterruptedTasks:        len(attempts),
		NaiveImmediateRetries:   naiveRetries,
		NaiveTransferBytes:      naiveTransfer,
		ControlledFirstWindow:   len(first.Released),
		ControlledTransferBytes: first.TransferBytes,
		DeferredAfterFirst:      len(first.Deferred),
	})
}
