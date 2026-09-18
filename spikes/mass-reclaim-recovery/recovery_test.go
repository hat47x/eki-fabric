package recovery

import (
	"fmt"
	"testing"
	"time"
)

func defaultPolicy() RecoveryPolicy {
	return RecoveryPolicy{
		BaseBackoff:          time.Second,
		MaxBackoff:           30 * time.Second,
		MaxReleasesPerWindow: 50,
		MaxTransferPerWindow: 2 * 1024 * 1024 * 1024,
		Window:               time.Second,
	}
}

func TestMassReclaimDoesNotImmediatelyReleaseEverything(t *testing.T) {
	now := time.Unix(0, 0)
	attempts := make([]InterruptedAttempt, 1000)
	for i := range attempts {
		attempts[i] = InterruptedAttempt{
			TaskID:             fmt.Sprintf("task-%04d", i),
			Priority:           PriorityNormal,
			Retryable:          true,
			AttemptsUsed:       1,
			MaxAttempts:        4,
			EstimatedTransfer:  64 * 1024 * 1024,
			EstimatedComputeMs: 30_000,
		}
	}

	naiveRetries, naiveTransfer := NaiveImmediateRetry(attempts)
	if naiveRetries != 1000 {
		t.Fatalf("naive retries=%d", naiveRetries)
	}
	if naiveTransfer != 64*1024*1024*1000 {
		t.Fatalf("unexpected naive transfer=%d", naiveTransfer)
	}

	intake, err := IntakeReclaim(now, attempts, defaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(intake.Pending) != 1000 || len(intake.Held) != 0 {
		t.Fatalf("intake=%+v", intake)
	}

	result, err := ReleaseWindow(now.Add(31*time.Second), intake.Pending, defaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Released) != 32 {
		t.Fatalf("released=%d, expected transfer budget to bind at 32", len(result.Released))
	}
	if result.TransferBytes > defaultPolicy().MaxTransferPerWindow {
		t.Fatalf("transfer budget exceeded: %d", result.TransferBytes)
	}
	if len(result.Deferred) != 968 {
		t.Fatalf("deferred=%d", len(result.Deferred))
	}
}

func TestCountBudgetCanBindBeforeNetworkBudget(t *testing.T) {
	now := time.Unix(0, 0)
	p := defaultPolicy()
	p.MaxReleasesPerWindow = 10
	p.MaxTransferPerWindow = 10 * 1024 * 1024 * 1024

	attempts := make([]InterruptedAttempt, 100)
	for i := range attempts {
		attempts[i] = InterruptedAttempt{
			TaskID:            fmt.Sprintf("task-%d", i),
			Retryable:         true,
			MaxAttempts:       2,
			EstimatedTransfer: 1,
		}
	}
	intake, _ := IntakeReclaim(now, attempts, p)
	result, _ := ReleaseWindow(now.Add(31*time.Second), intake.Pending, p)
	if len(result.Released) != 10 {
		t.Fatalf("released=%d", len(result.Released))
	}
}

func TestUnsafeSideEffectTaskIsHeldNotRetried(t *testing.T) {
	now := time.Unix(0, 0)
	intake, err := IntakeReclaim(now, []InterruptedAttempt{{
		TaskID:       "notify-once",
		Retryable:    false,
		AttemptsUsed: 1,
		MaxAttempts:  3,
	}}, defaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(intake.Pending) != 0 || len(intake.Held) != 1 {
		t.Fatalf("intake=%+v", intake)
	}
}

func TestRetryBudgetExhaustionIsHeld(t *testing.T) {
	now := time.Unix(0, 0)
	intake, _ := IntakeReclaim(now, []InterruptedAttempt{{
		TaskID:       "done-retrying",
		Retryable:    true,
		AttemptsUsed: 3,
		MaxAttempts:  3,
	}}, defaultPolicy())
	if len(intake.Pending) != 0 || len(intake.Held) != 1 {
		t.Fatalf("intake=%+v", intake)
	}
}

func TestUrgentTaskWinsOnceEligible(t *testing.T) {
	now := time.Unix(0, 0)
	p := defaultPolicy()
	p.MaxReleasesPerWindow = 1
	p.MaxTransferPerWindow = 1024

	pending := []Pending{
		{TaskID: "best-effort", Priority: PriorityBestEffort, NotBefore: now, EstimatedTransfer: 1},
		{TaskID: "urgent", Priority: PriorityUrgent, NotBefore: now, EstimatedTransfer: 1},
	}
	result, err := ReleaseWindow(now, pending, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Released) != 1 || result.Released[0].TaskID != "urgent" {
		t.Fatalf("released=%+v", result.Released)
	}
}

func TestDeadlineBreaksTieWithinPriority(t *testing.T) {
	now := time.Unix(0, 0)
	p := defaultPolicy()
	p.MaxReleasesPerWindow = 1
	p.MaxTransferPerWindow = 1024

	pending := []Pending{
		{TaskID: "later", Priority: PriorityNormal, Deadline: now.Add(10 * time.Minute), NotBefore: now, EstimatedTransfer: 1},
		{TaskID: "sooner", Priority: PriorityNormal, Deadline: now.Add(time.Minute), NotBefore: now, EstimatedTransfer: 1},
	}
	result, _ := ReleaseWindow(now, pending, p)
	if result.Released[0].TaskID != "sooner" {
		t.Fatalf("released=%+v", result.Released)
	}
}

func TestTaskLargerThanWindowBudgetStaysDeferred(t *testing.T) {
	now := time.Unix(0, 0)
	p := defaultPolicy()
	pending := []Pending{{
		TaskID:            "huge-artifact",
		Priority:          PriorityUrgent,
		NotBefore:         now,
		EstimatedTransfer: p.MaxTransferPerWindow + 1,
	}}
	result, err := ReleaseWindow(now, pending, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Released) != 0 || len(result.Deferred) != 1 {
		t.Fatalf("result=%+v", result)
	}
}

func TestBackoffProducesSpreadNotSingleTimestamp(t *testing.T) {
	now := time.Unix(0, 0)
	attempts := make([]InterruptedAttempt, 100)
	for i := range attempts {
		attempts[i] = InterruptedAttempt{TaskID: fmt.Sprintf("task-%d", i), Retryable: true, AttemptsUsed: 1, MaxAttempts: 4}
	}
	intake, err := IntakeReclaim(now, attempts, defaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[time.Time]struct{}{}
	for _, p := range intake.Pending {
		seen[p.NotBefore] = struct{}{}
	}
	if len(seen) < 10 {
		t.Fatalf("jitter produced too little spread: %d unique timestamps", len(seen))
	}
}

func TestDuplicateInterruptedTaskIsRejected(t *testing.T) {
	now := time.Unix(0, 0)
	_, err := IntakeReclaim(now, []InterruptedAttempt{
		{TaskID: "same", Retryable: true, MaxAttempts: 2},
		{TaskID: "same", Retryable: true, MaxAttempts: 2},
	}, defaultPolicy())
	if err == nil {
		t.Fatal("expected duplicate TaskID rejection")
	}
}
