# WOR-1のWorkOrder発行

このpackageは、Control PlaneがWorkerへ渡す署名済みWorkOrder（`eki.work-order/v1`）を作る境界です。WOR-1はWorkOrderに署名するAuthority roleです。

## 何が問題か

Workerは、署名済みWorkOrderを`workorderverify`で検証し、`workorderaccept`で自分のLease、Placement Approval、Executor Approvalと突き合わせ、`agent/startgate`でLocal Governorの上限と比べてから実行を始めます。ところが、これまでControl Plane側にWorkOrderへ署名する主体がありませんでした。

単純に「Leaseが発行済みならWorkOrderに署名して返す」とすると、次の問題が残ります。

- Leaseの発行後にPolicyが取り消されても、新しい実行指示が出てしまう。
- 署名の途中でprocessが落ちたとき、再試行で別のnonceのWorkOrderを作ると、一つのLeaseから二つの実行指示が生まれる。
- 署名関数が成功した時点を発行とみなすと、取消の直後でも古い前提のWorkOrderが外へ出る。

## どう扱うか

`Issuer.Issue`は、LSE-1の`leaseissue.Emitter`と同じ形で、署名とdurableな確定を分けます。

```text
WorkOrderのEMITTED / ABORTEDが既にある
  -> EMITTEDなら保存済みbytesをそのまま返す（再署名しない）
       ただしWorker、Executor Approval、limits、sandboxが違う要求にはbytesを返さない
  -> ABORTEDならErrWorkOrderAborted
LeaseがEMITTEDでない（RESERVED / ABORTED）
  -> ErrLeaseNotEmitted。何も記録しない
LeaseがEMITTED
  -> 要求のWorkerがLeaseのWorkerと違えばABORTED
  -> Task / Attempt / PolicyStateを読み直す
  -> canonical eki.work-order/v1 payloadを作る
       Decision IDとWorkOrder IDはLease IDから決まる
       Placement / Lease参照、Task、Attempt、Worker、policy revisionは保存済みのLeaseから導く
       期間はLeaseの期限とMaxLifetimeの内側へミリ秒単位で丸める
  -> 現在のprimary WOR-1 keyで署名
  -> 署名済みenvelopeをstrict decodeし、現在のTrust Bundleで検証し直す
  -> key、Trust Bundle、Task / Attempt / PolicyStateを最後にもう一度確認
  -> State StoreへEMITTEDをcommit（ここが線形化点）
```

EMITTEDのcommitでは、State Storeが同じ書き込みの直列化の中で次を照合します。

- LeaseがEMITTEDで、WorkOrderが参照するLease Decision ID / payload digestと一致する
- LeaseのFenceEpochが、そのTaskの最新のfenceである
- AttemptがそのLeaseを保持したまま`A-ASSIGNED`で、revisionが変わっていない
- Taskが取り消されておらず、`ready`または`active`で、revisionが変わっていない
- Leaseを予約したPolicy Epochが今もcurrentかつactiveである
- WorkOrderの期限がLeaseの期限を超えない

SQLite Adapterは、Adapterを経由しない書き込みに対しても同じ条件をtriggerで確かめます。

## 結果の分け方

| 状況 | 結果 |
| --- | --- |
| EMITTEDが既にある（Policy取消の後でも） | 保存済みbytesを返す。再署名しない |
| EMITTEDが既にあるが、要求の条件が違う | `ErrRequestConflict`。bytesを返さない |
| Policyの取消または新しいPolicy Epoch | ABORTED（`policy_revoked`） |
| WOR-1 keyの失効、侵害、primaryの変更 | ABORTED（`key_unauthorized`） |
| Leaseの期限切れ、WorkOrderの期間が残らない | ABORTED（`lease_expired`） |
| AttemptがLeaseを`A-ASSIGNED`で保持していない | ABORTED（`lease_superseded`） |
| Taskの取消 | ABORTED（`task_cancelled`） |
| Taskがready / active以外、TaskIRの不一致 | ABORTED（`task_not_schedulable`） |
| 要求のWorkerがLeaseのWorkerと違う | ABORTED（`worker_mismatch`） |
| LeaseがまだRESERVED、またはABORTED | `ErrLeaseNotEmitted`。何も記録しない |
| Policyを読めない、Trust Bundleが古い、signerが一時的に使えない | errorを返し、何も記録しない |
| commit前のcrash | 何も記録されない。再試行は新しく署名する |
| commit後に応答が失われた | 再試行は保存済みbytesを返す |

ABORTEDは`work_order_emissions`（memoryでは対応するmap）だけに記録します。Task、Attempt、Lease、FenceEpochは変えません。LSE-1のABORTEDと違い、Attemptを閉じません。Leaseは既にEMITTEDで、Leaseの期限まで同じTaskの新しいLeaseを排他するので、Attemptを閉じても再配置は早まらないためです。期限後の回収は既存のrecovery経路に任せます。

## 仮決め

#160で未決の論点のうち、次は最も保守的な側（fail closed）で仮に決めています。コード中にも「仮決め」と記しています。

- 一つのLeaseから作るWorkOrderは一つだけです。Lease更新（RenewalSequence）に伴う再発行は扱いません。
- WorkOrderを受け取れるのは、Leaseを保持したまま`A-ASSIGNED`のAttemptだけです。`A-DISPATCHED`以降やcloseされたAttemptは、Leaseが置き換えられたものとしてABORTEDにします。
- Artifact Grant（read / write）はWorkOrderに含めません。DAT-1の発行者と、Grantが今も有効かを確かめる経路がまだないためです。
- Executor Approvalは、信頼されたControl Planeの呼び出し側が渡す参照をそのまま結び付けます。EXE-1の発行者と検証器がまだないためです。Worker側は`workorderaccept`で自分の持つExecutor Approvalと照合します。
- 新しいPolicy Epochが有効になった時点で、古いEpochで予約したLeaseへのWorkOrderはABORTEDにします。旧revisionが明示的に取り消されていなくても同じです（#78のM0方針）。
- 要求のWorkerがLeaseのWorkerと違う場合も、そのLeaseのWorkOrderをABORTEDにします。

## 保証しないこと

- keyとTrust Bundleの状態はState Storeの外にあります。commit直前に確認しても、確認とcommitの間に起きたkey失効は検出できません。この隙間は、Worker側の`workorderverify`が現在のkey状態を確認することで閉じます。
- EMITTEDの後にPolicyを取り消しても、既に発行したWorkOrderは無効になりません。発行済みの実行の停止は、Leaseの期限、Lease revoke、Local Governorの役割です。
- nonceの使い捨てはWorker側（`workorderaccept`と`replayguard`）が担います。WOR-1はnonceを生成するだけです。
