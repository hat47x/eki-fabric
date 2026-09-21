# Learned Scheduling Shadow Observation Spike

## Purpose

This spike defines a research-only observation contract for EKI-ML-0A.

It does not select a production scheduler architecture and does not grant any
placement or authorization authority to a learned model.

```text
hard Security / Data / Resource eligibility
        ↓
deterministic placement-cost baseline
        ↓
research observation
        ↓
future residual / completion / preemption model
        ↓
shadow comparison only
```

## Contract

The observation retains:

- scenario / worker-population / time-regime grouping;
- task and worker class;
- policy eligibility separately from optimization features;
- deterministic compute + transfer baseline components;
- completed / preempted / failed / censored outcome identity;
- train / valid / test / unassigned split;
- explicit shadow-only research boundary.

The deterministic baseline is intentionally auditable arithmetic. A later
student should primarily learn residual/error or completion/preemption
probability rather than relearn the whole transfer + compute equation.

## Censoring

A censored row is not a negative completion label.

```text
censored
  = observation ended before terminal outcome
  != failed
  != preempted
  != known deadline miss
```

Accordingly, `CompletionBeforeDeadline` returns an unknown target for
censored observations. Terminal failed/preempted attempts may be represented
as known non-completions; completed attempts use the actual completion time.

## Split boundary

`ValidateNoSplitLeakage` prevents the same scenario family, worker
population, or time regime from crossing assigned train/valid/test splits.

This is deliberately strict for the first research slice. It is intended to
test transfer across regimes rather than near-duplicate interpolation.

## Stop lines

```text
prediction != Worker Assurance
score != authorization
score != forced placement
cache/locality claim != content integrity
historical success != trust
```

The production scheduler must remain policy-constrained even if later research
shows useful predictive signal.

## Verification

From this directory:

```bash
go test ./...
```

A passing unit test proves only the local observation-contract mechanics. It
does not establish prediction quality, product value, or permission to activate
a learned scheduler.
