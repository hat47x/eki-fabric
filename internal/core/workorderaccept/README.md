# WorkOrderの受理（WorkOrder acceptance）

Workerが署名済みWorkOrderを受け取っても、まだ実行を始めません。このパッケージは、WorkOrderを、Worker自身のLease、Placement Approval、Executor Approval、nonceの記録と突き合わせ、実行を始めてよいという**呼出しごとの証拠**（`Admission`）を作ります。

```text
signed WorkOrder bytes
  -> workorderverify.Verify       # 署名・宛先・期限・Trust Bundle
  -> local Lease evidence         # Task / TaskIR / Attempt / Worker / Lease ref / expiry
  -> Placement Approval evidence  # decision ID / digest / subject
  -> Executor Approval evidence   # decision ID / digest / executor digest
  -> nonce claim                  # 期限内で一度だけ
  -> Admission
```

## 順序

nonceは、他のすべての確認が通った後にだけ獲得します。署名が不正なWorkOrder、Leaseと食い違うWorkOrder、期限切れのLeaseに対するWorkOrderは、nonceを消費しません。一度拒否された指示を、別の理由で後から再送しても、nonceの予算を無駄に使わないためです。

## Admissionが表すもの

`Admission`は、次を記録します。

- 対象のTask、TaskIR、Attempt、Worker。
- 根拠にしたWorkOrder DecisionとLease Decision。
- Leaseのfencing epoch。
- Executorのdigest。
- resource limitsとsandbox policy。
- 受理した時刻と、有効期限（WorkOrderとLeaseの早い方）。
- 署名に使われたkeyとTrust Bundleのrevision。

```text
Admission != Lease
Admission != 実行の許可（無条件）
Admission != Local Governorの承認
```

`Admission`は、`AdmittedAt`の時点で指示がcurrentで、nonceが未使用だったことを示すだけです。その後の実行では、Local Governorがいつでも上限を下げたり停止したりできます。Admissionは、その判断を上書きしません。

## 確かめないこと

- Local Governorの現在の上限との突き合わせ。AdmissionはWorkOrderのlimitsを持つだけで、Local Governorがより低い上限を返した場合の調整は呼出し側の境界です。
- Leaseの更新。Admissionの期限は固定で、`RenewalSequence`が進んだ場合は新しい受理が必要です。
- 参照先Decisionのpayloadそのもの。Workerが保持するLease / Placement / Executorの記録と、WorkOrderの参照（decision IDとdigest）を照合します。
- nonce記録の耐久性。`replayguard`のAdapterに依存します。メモリ実装は再起動で消えます。

## 失敗の扱い

`ErrLeaseMismatch`、`ErrLeaseExpired`、`ErrPlacementMismatch`、`ErrExecutorMismatch`、`ErrReplay`、`ErrReplayUnknown`を返します。`ErrReplayUnknown`は、nonceの獲得結果が分からない状態であり、実行の許可として扱いません。署名検証の失敗は、`decisiontrust.VerificationFailure`としてそのまま返します。
