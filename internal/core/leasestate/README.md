# Durable Lease state

`leasestate.LeaseRecord`はLSE-1が保持する永続的なfencing stateです。

これはWorkerが実行権限として利用するP2 `eki.lease/v1`そのものではありません。

```text
verified Placement Approval
  -> current-state revalidation
  -> durable Lease reservation / FenceEpoch
  -> canonical P2 Lease payload
  -> LSE-1 signing
  -> Worker-side verification
```

この順序により、署名してから保存に失敗して「Authorityだけ外へ出た」状態を避けます。

同じPlacement Decisionの再送は同じLeaseへ収束させます。Decision identityは、検証時のTrust Domain、PLC-1 principal、Decision ID、payload digestとsubject bindingを保持します。同じDecision IDを別payloadや別subjectへ結び直すことは拒否します。別Trust Domainまたは別PLC-1 principalの同名Decision IDは別Decisionとして扱います。新しいLeaseは同じTask内でFenceEpochを単調増加させ、古いLeaseが後から復活できない前提を作ります。
