# Durable Policy Epoch

LSE-1が新しいLease Authorityへ進む直前に使う、永続的なpolicy snapshotです。

単に`CurrentPolicyRevision() string`を読むだけでは、read直後のrevokeとLease reservation commitのraceを閉じられません。

```text
read PolicyState(epoch=N, revision=P1, active)
  -> verify Placement(P1)
  -> concurrent revoke / policy update
  -> LeaseStore compares expected epoch=N inside reservation transaction
  -> stale writer fails closed
```

M0ではcurrent policyを一つに限定し、epochは1から単調増加します。revoked revisionを同じidentityのままactiveへ戻しません。

PolicyStoreはAdmission Storeと別Authority surfaceです。物理DBを共有しても、Admission Serviceへpolicy mutation APIを渡しません。
