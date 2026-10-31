package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	replaysqlite "github.com/hat47x/eki-fabric/internal/adapters/reference/replayguard/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/assignment"
	"github.com/hat47x/eki-fabric/internal/agent/startgate"
	"github.com/hat47x/eki-fabric/internal/agent/trustanchor"
	"github.com/hat47x/eki-fabric/internal/core/contract"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

// AssignmentConfig turns on assignment delivery for the process-level TEST
// PROFILE. It needs certificate authentication (state_dir): the route exists
// only on the mTLS listener. Without it the Agent does not fetch assignments.
//
// Every field is required. A missing trust anchor, replay database or approved
// Executor is a startup error, never a reason to accept less.
type AssignmentConfig struct {
	// TrustAnchorFile holds the Authority public keys and the Trust Bundle the
	// Worker verifies Leases and WorkOrders against (internal/agent/trustanchor).
	TrustAnchorFile string `json:"trust_anchor_file"`
	// ReplayDB is the Worker's durable WorkOrder nonce store.
	ReplayDB string `json:"replay_db"`
	// EventsFile receives one JSON line per assignment decision. It is the
	// observable result of this slice: the Agent does not execute Tasks.
	EventsFile string `json:"events_file"`
	// ApprovedExecutors are the Executor Approvals this Worker will run under.
	ApprovedExecutors []ExecutorApprovalConfig `json:"approved_executors"`
	// Machine is the capacity the start gate compares against. A static
	// stand-in for detection.
	Machine MachineConfig `json:"machine"`
	// CeilingPercent is the Local Governor's ceiling for every resource, 1..100.
	// A static stand-in for the real Local Governor.
	CeilingPercent int `json:"ceiling_percent"`
	// Execution turns on local execution of a ready assignment with one fixed
	// command. Nil keeps the Agent record-only.
	Execution *ExecutionConfig `json:"execution,omitempty"`
}

// ExecutionConfig is a fixed command the Agent runs for every ready
// assignment. The command and arguments are trusted Worker configuration, not
// Task input. This is not a sandbox.
type ExecutionConfig struct {
	Command          string   `json:"command"`
	Args             []string `json:"args,omitempty"`
	Env              []string `json:"env,omitempty"`
	AllowedArgs      []string `json:"allowed_args,omitempty"`
	ExecutableSHA256 string   `json:"executable_sha256,omitempty"`
	StopGraceSeconds int      `json:"stop_grace_seconds,omitempty"`
}

type ExecutorApprovalConfig struct {
	DecisionID     string `json:"decision_id"`
	PayloadDigest  string `json:"payload_digest"`  // 64 hex characters
	ExecutorDigest string `json:"executor_digest"` // 64 hex characters
}

type MachineConfig struct {
	CPUMillis   uint64 `json:"cpu_millis"`
	MemoryBytes uint64 `json:"memory_bytes"`
	GPUDevices  uint32 `json:"gpu_devices,omitempty"`
}

// prepareAssignments validates the configuration and loads the trust anchor and
// the executor list, before the Agent opens anything.
func prepareAssignments(c *AssignmentConfig) (*trustanchor.Anchor, []assignment.ExecutorApproval, error) {
	switch {
	case c.ReplayDB == "" || c.EventsFile == "":
		return nil, nil, fmt.Errorf("%w: assignments need replay_db and events_file", ErrInvalidConfig)
	case len(c.ApprovedExecutors) == 0:
		return nil, nil, fmt.Errorf("%w: assignments need at least one approved executor", ErrInvalidConfig)
	case c.Machine.CPUMillis == 0 || c.Machine.MemoryBytes == 0:
		return nil, nil, fmt.Errorf("%w: assignments need the machine's cpu_millis and memory_bytes", ErrInvalidConfig)
	case c.CeilingPercent < 1 || c.CeilingPercent > 100:
		return nil, nil, fmt.Errorf("%w: ceiling_percent must be 1..100", ErrInvalidConfig)
	case c.Execution != nil && c.Execution.Command == "":
		return nil, nil, fmt.Errorf("%w: execution.command is required when execution is enabled", ErrInvalidConfig)
	}
	anchor, err := trustanchor.Load(c.TrustAnchorFile)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	var execs []assignment.ExecutorApproval
	for i, e := range c.ApprovedExecutors {
		payload, err := contract.ParseSHA256(e.PayloadDigest)
		if err != nil || e.DecisionID == "" {
			return nil, nil, fmt.Errorf("%w: approved executor %d: decision_id and a 64-hex payload_digest are required", ErrInvalidConfig, i)
		}
		digest, err := contract.ParseSHA256(e.ExecutorDigest)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: approved executor %d: executor_digest must be 64 hex characters", ErrInvalidConfig, i)
		}
		execs = append(execs, assignment.ExecutorApproval{DecisionID: e.DecisionID, PayloadDigest: payload, ExecutorDigest: digest})
	}
	return anchor, execs, nil
}

type event struct {
	Time            time.Time `json:"time"`
	AttemptID       string    `json:"attempt_id"`
	LeaseID         string    `json:"lease_id"`
	Outcome         string    `json:"outcome"`
	Reason          string    `json:"reason,omitempty"`
	FenceEpoch      uint64    `json:"fence_epoch,omitempty"`
	CPUMillis       uint64    `json:"cpu_millis,omitempty"`
	MemBytes        uint64    `json:"memory_bytes,omitempty"`
	Deadline        time.Time `json:"deadline,omitzero"`
	ExecutionStatus string    `json:"execution_status,omitempty"`
	ExitCode        int       `json:"exit_code,omitempty"`
	OutputBytes     uint64    `json:"output_bytes,omitempty"`
	OutputDigest    string    `json:"output_digest,omitempty"`
	Failure         string    `json:"failure,omitempty"`
	ClaimDecision   string    `json:"claim_decision,omitempty"`
}

type eventLog struct {
	mu   sync.Mutex
	path string
}

func (l *eventLog) write(e event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(raw, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// runAssignments polls until ctx ends. It returns when the loop has stopped.
func runAssignments(ctx context.Context, c *AssignmentConfig, anchor *trustanchor.Anchor, execs []assignment.ExecutorApproval,
	source workerplane.AssignmentSource, claims workerplane.ClaimClient, principal workerplane.Principal, interval time.Duration, log *slog.Logger) error {
	guard, err := replaysqlite.Open(ctx, c.ReplayDB, systemClock{}, 4096)
	if err != nil {
		return fmt.Errorf("open replay guard: %w", err)
	}
	defer guard.Close()
	events := &eventLog{path: c.EventsFile}
	var handler assignment.Handler = assignment.HandlerFunc(func(context.Context, assignment.Ready) error { return nil })
	if c.Execution != nil {
		execHandler, err := newExecutionHandler(c.Execution, events, log, claims, principal)
		if err != nil {
			return err
		}
		handler = execHandler
	}
	svc, err := assignment.New(assignment.Config{
		Source: source, Principal: principal, Anchor: anchor, Replay: guard, Clock: systemClock{},
		Executors: execs, Governor: assignment.StaticGovernor(c.CeilingPercent),
		Machine: startgate.MachineProfile{CPUMillis: c.Machine.CPUMillis, MemoryBytes: c.Machine.MemoryBytes, GPUDevices: c.Machine.GPUDevices},
		Handler: handler,
		Logger:  log,
	})
	if err != nil {
		return err
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		results, err := svc.Poll(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			log.Error("assignment poll failed", "error", err.Error())
		}
		for _, r := range results {
			ev := event{Time: time.Now().UTC(), AttemptID: r.AttemptID, LeaseID: r.LeaseID, Outcome: string(r.Outcome), Reason: r.Reason, FenceEpoch: r.FenceEpoch}
			if r.Outcome == assignment.OutcomeReady {
				ev.CPUMillis, ev.MemBytes, ev.Deadline = r.Plan.Limits().CPUMillis, r.Plan.Limits().MemoryBytes, r.Plan.Deadline()
			}
			if err := events.write(ev); err != nil {
				log.Error("assignment event not recorded", "error", err.Error())
			}
			log.Info("assignment", "attempt", r.AttemptID, "outcome", string(r.Outcome), "reason", r.Reason)
		}
		timer.Reset(interval)
	}
}
