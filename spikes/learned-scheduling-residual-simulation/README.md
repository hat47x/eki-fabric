# Learned Scheduling Residual Simulation Spike

## Purpose

This is EKI-ML-0B mechanics for Issue #48. It reuses the existing deterministic artifact-locality estimate and EKI-ML-0A shadow observation contract.

It adds only:

- deterministic scenario generation from a scenario-set version + generator version + seed;
- whole-group train/valid/test fixtures for compute, bandwidth, cache/locality, churn, deadline and compound shifts;
- exact residual/completion target derivation through the 0A helpers;
- deterministic/global/grouped **non-ML** baselines;
- duration and completion baseline metrics;
- explicit shadow-only/no-winner evaluation metadata.

No learned model is trained and no placement or authorization path is changed.

## Module reuse

The local module imports sibling spikes through `go.mod` replace directives:

```text
artifact-locality-cost
        ↓ deterministic estimate
learned-scheduling-shadow-observation
        ↓ validation / targets / leakage guard
learned-scheduling-residual-simulation
        ↓ generator + baseline evaluator only
```

This is intentionally not a second scheduler implementation.

## Censoring and eligibility

- censored observations never become known deadline negatives;
- policy-ineligible observations never become residual/completion optimization targets;
- every generated observation remains validator-clean under the 0A schema.

## Split boundary

Scenario family, worker population and time regime groups contain multiple observations where applicable, but each group identity belongs to exactly one assigned split. `ValidateNoSplitLeakage` remains authoritative for the first research slice.

## Baselines

The evaluator reports, separately:

- deterministic estimate MAE / median AE / p90 AE;
- global mean residual correction;
- global median residual correction;
- worker-class + task-class grouped mean correction with global fallback;
- unconditional completion-before-deadline probability;
- grouped empirical probability;
- Brier score and known-target/censoring coverage.

It does not select a winner or emit a composite score.

## Verification

From this directory:

```bash
go test ./...
```

A passing test proves only generator/evaluator mechanics for synthetic scenarios. It does not establish production prediction quality or permission to activate learned scheduling.

## EKI-ML-0C generalization-axis extension

Issue #53 separates **concrete split-group identity** from **abstract evaluation families**.

The v1 residual dataset keeps the existing observation/split fields authoritative for leakage checks:

- scenario_family
- worker_population
- time_regime

and adds research-only, observation-id keyed `evaluation_axes`:

- worker_population_family
- time_regime_family
- shift_axes

The concrete identities still must not cross train/valid/test. Abstract families may recur across splits because they describe the class of regime being tested, not the same worker cohort or acquisition window.

The generated scenario set now includes:

- isolated Test-A: new concrete worker/time groups, known worker family, unseen time family;
- isolated Test-B: new concrete worker/time groups, unseen worker family, known time family;
- Test-C: compound worker/time-family shift.

This extension remains simulation/shadow-only. Evaluation-axis metadata is not a scheduler input and does not alter policy eligibility, placement, authorization, or Worker Assurance.
