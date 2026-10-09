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

M0ではcurrent policyを一つに限定し、epochは1から単調増加します。revision identityも一度currentになったら再利用しません。明示的にrevokeされたrevisionだけでなく、別revisionへ置き換えられた旧revisionも後からactiveへ戻せません。epochだけを増やして古いpolicy内容へrollbackする経路を閉じるためです。

`policystate.ValidateTransition`は現在値と次値だけを見るpure ruleです。過去revisionの再利用禁止は履歴を持つ`PolicyStore`が担当し、memory / SQLite / SQLite triggerで同じ規則を実装します。

P2 `eki.lease/v1`はwire上に`policy_revision`を持ちますが、内部の`PolicyEpoch`は持ちません。このためrevision identityの単一使用は、同じ署名上のrevision文字列が後から別epoch・別policy snapshotへ再解釈されることも防ぎます。内部Epochはlinearizationと監査に使い、P2 v1の意味同一性は一意なrevision identityで維持します。

PolicyStoreはAdmission Storeと別Authority surfaceです。物理DBを共有しても、Admission Serviceへpolicy mutation APIを渡しません。
