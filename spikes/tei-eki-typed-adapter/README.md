# TEI -> EKI Typed Adapter Spike

Role: evaluate how a TEI `Operation` can be projected onto an EKI
`TaskRequest` without changing EKI core. This spike records the
binding table, schema stability, and measurable gaps. It does not
change EKI admission or state machine.

Run:

```powershell
go test ./spikes/tei-eki-typed-adapter/...
```

Limits:

- No network I/O.
- No real EKI control plane interaction.
- Gaps are recorded as observed facts, not assumed limitations.
