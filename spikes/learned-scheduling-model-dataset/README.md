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
- policy eligibility stored separately from target availability;
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

The projected row uses `policy_eligible`, not a generic `optimization_eligible` flag. A policy-eligible censored observation may legitimately have no optimization target, so downstream fitting must use target presence/knownness rather than treating policy eligibility as target eligibility.

The readiness gate requires **both** policy eligibility and a known target. Even if a malformed/directly-constructed comparison row carries a non-null target pointer, `policy_eligible=false` cannot contribute to Test-A/B/C target coverage or unlock model fitting.

## Current readiness and resolved blocker

Issue #50 preregisters three held-out views:

- Test-A: worker-population family is represented in train, while the time-regime family is unseen;
- Test-B: time-regime family is represented in train, while the worker-population family is unseen;
- Test-C: both abstract families are unseen as a compound shift.

The first 0C-1 freeze correctly found that the original 0B dataset could not express isolated Test-A / Test-B without violating the concrete split-leakage contract. That was a real blocker, not a labeling problem.

Issue #53 resolved this by separating two concepts:

```text
concrete split-group identity
    !=
generalization/evaluation family
```

PR #54 added versioned evaluation-only generalization axes while keeping concrete source/cohort identities leakage-clean. PR #55 then tightened the contract so one concrete worker/time group cannot map to conflicting abstract families.

The current v1 projection therefore evaluates held-out novelty from `evaluation_axes` and keeps `worker_population` / `time_regime` as leakage identities. Evaluation-axis metadata remains outside `features`.

The deterministic scenario set is now mechanically capable of representing target-bearing Test-A / Test-B / Test-C slices, and the readiness calculation may report `model_fit_ready=true`. This only means the preregistered dataset shape no longer blocks the next research work unit.

It does **not** mean:

- a learned model has been fitted;
- predictive value has been established;
- a learned rank may influence placement;
- authorization or Worker Assurance semantics changed.

PR #54 and #55 were merged without an exact-head Go PASS being recorded in their PR bodies. Before 0C-2 fitting begins, run the current-main tests for both the residual-simulation and model-dataset modules and retain the exact main SHA, Go version, test result, and exit status as the execution evidence for this transition.

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

## Generalization-axis readiness

Issue #53 updates the comparison projection so held-out readiness is based on the v1 simulation dataset's `evaluation_axes`, while the concrete observation split groups remain governed by the original no-leakage validator.

The projected row stores evaluation axes outside `features`. They are evaluator metadata only and must not be passed to M1/M2/M3 unless a later preregistration explicitly promotes a derived field into the model feature contract.

The current deterministic scenario set is intended to make Test-A/B/C mechanically representable without reusing a concrete worker cohort or time window across splits. A successful readiness result means only that the next model-fitting work unit has the preregistered held-out slices and known targets.

It does **not** mean:

- a learned model has been trained;
- prediction quality is established;
- a learned rank may influence placement;
- authorization/assurance semantics changed.
