# LSE-1のLease発行サービス

このpackageは、LSE-1がLeaseを発行する二つの段階を持ちます。

1. `Service.Reserve`: 署名済みPlacement Approvalを検証し、durable Lease reservation（RESERVED）とFenceEpochを確定する。
2. `Emitter.Emit`: RESERVEDのLeaseに`eki.lease/v1`の署名を付け、EMITTEDとして耐久的に確定する。前提が失われていればABORTEDを確定する。

Workerへ渡してよいのは、2.でEMITTEDとして保存されたbytesだけです。

低層の`statestore.LeaseReservation`は保存契約として公開されていますが、production callerがAuthority fieldを自由に組み立てるためのAPIではありません。

```text
signed Placement Approval bytes
  -> current Trust Bundle
  -> strict COSE / P2 / key / role / principal / signature verification
  -> current Task / Attempt / policy read
  -> current-state revalidation
  -> Authority fieldsをVerifiedから一方向に導出
  -> durable Lease reservation / FenceEpoch
  -> Emitter: canonical Lease payload / LSE-1 signing / EMITTED commit
```

callerがRequestで指定できるのは次だけです。

- signed Placement Approval bytes
- 希望Lease期間

現在時刻はcallerから受け取りません。LSE-1が構成時に受け取るtrusted `Clock`から取得します。過去時刻を申告して期限切れPlacementを復活させる経路を作らないためです。

Task、TaskIR、Attempt、Worker、Trust Domain、PLC-1 principal、Decision ID、payload digest、policy revision、Placement expiryはcallerから受け取りません。

Lease IDもservice内部で生成します。

## expiry

Lease expiryは次の最小値です。

- callerが希望した期間
- LSE-1 local policyのMaxLifetime
- signed Placement Approvalのexpiry

したがってPlacementより長いLeaseは生成しません。

## replay

同じsigned Placementのexact replayはStoreで既存Leaseへ収束します。新しいFenceEpochをmintしません。

ただし既存Leaseがすでに期限切れなら、serviceは`ErrPlacementConsumed`を返します。同じPlacementを再送して新しいLease Authorityを復活させることはできません。再配置には新しいPlacement Decisionが必要です。

## 署名と発行（`Emitter.Emit`）

### 何が問題か

reservationを確定してから実際に署名するまでの間にも、Policyの取消、署名keyの失効、Taskの取消は起こり得ます。署名関数が成功した時点でLeaseを発行済みとみなすと、取消の直後でも古い前提で署名したLeaseが外へ出てしまいます。逆に、署名の途中でprocessが落ちたときに「署名したかもしれないので別のDecisionとして署名し直す」と、一つのreservationから二つのLease Decisionが生まれます。

### どう扱うか

`Emitter.Emit`は、Lease IDだけを受け取ります。署名対象の値はすべてdurableなreservationから導きます。

```text
EMITTED / ABORTEDが既にある
  -> EMITTEDなら保存済みbytesをそのまま返す（再署名しない）
  -> ABORTEDならErrLeaseAborted
RESERVED
  -> Task / Attempt / PolicyStateを読み直す
  -> canonical eki.lease/v1 payloadを作る
       Decision IDはLease IDから決まる（再試行しても同じDecision）
       期間はreservationの内側へミリ秒単位で丸める
  -> 現在のprimary LSE-1 keyで署名
  -> 署名済みenvelopeをstrict decodeし、現在のTrust Bundleで検証し直す
  -> keyとTask / Attempt / PolicyStateを最後にもう一度確認
  -> State StoreへEMITTEDをcommit（ここが線形化点）
```

EMITTEDのcommitでは、State Storeが同じtransactionの中でPolicy Epoch、Task revision、Attempt revisionを照合します。最後の確認とcommitの間にPolicyが取り消された場合も、State Storeがcommitを拒否します。その場合、Emitterは署名済みbytesを捨て、ABORTEDを記録します。

### 結果の分け方

| 状況 | 結果 |
| --- | --- |
| EMITTEDが既にある（Policy取消の後でも） | 保存済みbytesを返す。再署名しない |
| Policyの取消または差し替え | ABORTED（`policy_revoked`） |
| LSE-1 keyの失効、侵害、primaryの変更 | ABORTED（`policy_revoked`） |
| Taskの取消 | ABORTED（`cancelled`） |
| Taskがready / active以外、TaskIRの不一致 | ABORTED（`stale_attempt`） |
| Leaseの期限切れ | ABORTED（`lease_expired`） |
| Policyを読めない、Trust Bundleが古い、signerが一時的に使えない | errorを返し、RESERVEDのまま残す |
| commit前のcrash | RESERVEDのまま残る。再試行は同じpayloadに署名する |
| commit後に応答が失われた | 再試行は保存済みbytesを返す |

ABORTEDは、Attemptのcloseと同じ保存境界で記録します。FenceEpochは戻しません。再配置には新しいPlacement Decision、Attempt、Lease、FenceEpochを使います。ABORTEDになったreservationと同じPlacementから、Leaseを作り直すことはできません。

### 保証しないこと

- keyとTrust Bundleの状態はState Storeの外にあります。commit直前に確認しても、確認とcommitの間に起きたkey失効は検出できません。この隙間は、Worker側の検証が現在のkey状態を確認することで閉じます。
- `PolicyEpoch`は`eki.lease/v1`のwireに載せません。wire上のPolicyの識別子は`policy_revision`で、PolicyStoreは一度currentになったrevisionを再利用しません（#78）。PolicyEpochはEMITTED記録に残ります。
- EMITTEDの後にPolicyを取り消しても、既に発行したLeaseは無効になりません。発行済みLeaseの停止は、Lease revoke、renewal停止、Worker側の検証やLocal Governorの役割です。
- AttemptがEmitterの外で既にcloseされた場合、Emitterは何も記録せずerrorを返します。State StoreはそのreservationからEMITTEDを作りません。

### 未決事項

Worker側の検証で、Policyのcurrentnessをどこまで確認させるかは未決です（#78）。Workerは現在、Lease bytesから`policy_revision`を読めますが、そのrevisionが今も有効かを確かめる経路を持ちません。
