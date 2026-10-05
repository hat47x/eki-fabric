# Core authority-chain conformance

This package fixes the first product-value boundary from RQ-014 Suitability
through SCH-1 planning and PLC-1 Placement Authorization.

It is intentionally narrower than a full E2E Worker test.

The required negative chain is:

```text
FABRIC_NOT_JUSTIFIED / NEEDS_MEASUREMENT
  -> no FabricEntry
  -> no Scheduler Proposal
  -> no PLC-1 ApprovalDraft
```

For a justified Task, Scheduler may create a non-authoritative Proposal, but a
current Placement hard check can still stop the chain:

```text
FABRIC_JUSTIFIED
  -> FabricEntry
  -> Scheduler Proposal
  -> Data / assurance / provider / resource / transfer re-check
      -> reject: no PLC-1 draft
      -> pass: PLC-1 ApprovalDraft only
```

The current conformance endpoint is an **unsigned PLC-1 ApprovalDraft**.
Suitability, Scheduler, and Placement Authorization do not mint a Lease,
ArtifactGrant, or WorkOrder. Those require later Authority-specific work units.

The tests also freeze policy-drift behavior: a Proposal produced under an older
Suitability policy revision cannot silently become a current Placement
Approval draft.
