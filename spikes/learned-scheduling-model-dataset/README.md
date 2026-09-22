# Learned Scheduling Model Dataset Freeze

## Purpose

This is **EKI-ML-0C-1** for Issue #50.

It prepares the first model-comparison dataset without fitting a learned model.

It reuses:

- `artifact-locality-cost` as the deterministic placement-cost baseline;
- `learned-scheduling-shadow-observation` as the observation/target/leakage authority;
- `learned-scheduling-residual-simulation` as the deterministic S1-S6 generator and M0 baseline evaluator.

It does not create a second scheduler or simulator.

## Output

The freeze bundle contains:

- exact source dataset identity/digest;
- a model-facing feature projection;
- targets stored separately from features;
- grouping/split provenance stored separately from features;
- valid/test M0 baseline results;
- content digests for the projected dataset and baseline bundle;
- a model-fit readiness diagnostic;
- `training_performed=false`;
- `placement_influenced=false`;
- `authorization_influenced=false`.

## Feature boundary

The v0 model-facing projection contains only fields that can be rederived from the current EKI-ML-0A observation:

- task class;
- worker class;
- total/local/missing input bytes;
- expected output/transfer bytes;
- deterministic transfer millis;
- deterministic compute millis;
- deterministic total estimate;
- local-input fraction;
- deadline slack relative to the deterministic estimate.

The current observation schema does **not** retain raw download/upload throughput or the original task transfer-budget value, so 0C-1 does not invent them.

The model-facing feature struct deliberately excludes:

- scenario family;
- worker-population identity;
- time-regime identity;
- split identity;
- outcome;
- actual duration;
- residual target;
- completion target;
- policy/authorization result.

Those remain outside the feature vector as evaluation/provenance or targets.

## Target boundary

Duration residual is emitted only when the existing shadow-observation helper says it is known.

Completion-before-deadline is emitted only when the existing helper says it is known.

Censored and policy-ineligible rows therefore do not acquire fabricated targets.

## Important readiness finding

Issue #50 preregisters three distinct held-out views:

- Test-A: unseen time regime while worker population is otherwise known;
- Test-B: unseen worker population while time regime is otherwise known;
- Test-C: compound shift.

The current 0B generator changes worker-population and time-regime identities together outside train. It does contain S6 compound rows, but it does **not** contain isolated Test-A or Test-B rows.

0C-1 preserves this fact instead of silently relabeling the current `valid` split.

Expected readiness on the current scenario set:

```text
test_a_unseen_time_only = 0
test_b_unseen_worker_only = 0
test_c_compound > 0
model_fit_ready = false
```

Therefore this work unit may freeze/validate data and baselines, but **0C-2 model fitting remains blocked** until a later single work unit extends the preregistered scenario set with isolated held-out A/B regimes or Issue #50 is explicitly revised.

## Verification

From this directory:

```bash
go test ./...
```

A passing test establishes dataset-freeze/projection/readiness mechanics only.

It does not establish:

- learned-model improvement;
- production prediction quality;
- scheduler deployment permission;
- Worker Assurance;
- authorization authority.
