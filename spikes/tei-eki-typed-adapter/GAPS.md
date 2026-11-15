# EKI Gaps for TEI Integration

Status: **Measured — 2026-11-11**
Baseline: EKI main@`72f4a18`, OpenAPI `spec/external/openapi.json`

## Summary

This file records measurable gaps between TEI integration requirements
and current EKI P0 contracts. Gaps are observed facts, not assumed
limitations. Each gap includes a test or measurement that can be rerun
if the contract changes.

## Artifact Delivery

- **Gap A1**: TEI workspace inputs require Artifact ingestion before Task
  submission. Current EKI P0 returns 422 for Artifact ingestion (#112).
- **Measurement**: `spikes/tei-eki-minimal-delegation/contract_test.go`
  asserts expected_digest handling and ArtifactDescriptor.digest type.
- **Freeze condition**: T1 EKI Native path is frozen until Artifact
  ingestion works for at least one pure computation.

## Result Retrieval

- **Gap R1**: Result body retrieval returns 501 in current EKI P0 (#112).
- **Measurement**: `spikes/tei-eki-minimal-delegation/tei_result_acceptance_test.go`
  checks `GET /v1/tasks/{id}/result` returns 200 with output_digest.
- **Freeze condition**: T1 EKI Native path is frozen until Result body
  retrieval works for at least one pure computation.

## Executor Identity

- **Gap E1**: ExecutorRequirement has no field for executable digest.
  `parameters` are not delivered to Worker.
- **Measurement**: `spikes/tei-eki-minimal-delegation/README.md` binding
  table records "保証しない" for executable_sha256 and toolchain.
- **Impact**: TEI cannot enforce that the Worker runs the exact intended
  binary. This is recorded in Evidence as `not_checked`.

## Effect Mode

- **Gap EF1**: effect_mode only accepts "none" in P0.
- **Measurement**: `spikes/tei-eki-minimal-delegation/contract_test.go`
  asserts effect_mode is "none" for pure computation.
- **Impact**: Effectful TEI operations cannot be delegated to EKI until
  effect_mode supports other values and RQ-054 is implemented.

## Runtime Class

- **Gap RC1**: TaskResult provenance does not include execution environment
  classification (OS, arch, toolchain) (#112).
- **Measurement**: `spikes/tei-eki-minimal-delegation/README.md` binding
  table records "保証しない" for toolchain.
- **Impact**: TEI cannot verify that the Worker ran in the expected
  runtime class. This is recorded in Evidence as `not_checked`.

## Observation Export

- **Gap O1**: eki-control has no metrics or healthcheck export (#112).
- **Measurement**: `spikes/tei-eki-minimal-delegation/README.md` lists
  "観測情報の出口" as gap.
- **Impact**: TEI cannot read resource utilization or cost from EKI
  observation plane until OTLP or equivalent is implemented.

## Deadline Enforcement

- **Gap D1**: EKI does not enforce terminal transition after deadline
  (#111, #113 PAS-CAND-23).
- **Measurement**: `spikes/tei-eki-minimal-delegation/tei_lease_loss_test.go`
  checks Task state after lease loss, but deadline terminal transition
  is not observed.
- **Impact**: TEI cannot rely on EKI to cancel or terminate Tasks after
  deadline. Deadline recovery logic must be handled by TEI or Policy.
