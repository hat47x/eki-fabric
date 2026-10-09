# LSE-1 verified Placement service

このpackageは、署名済みPlacement Approvalからdurable Lease reservationへ進むproduction向け境界です。

低層の`statestore.LeaseReservation`は保存契約として公開されていますが、production callerがAuthority fieldを自由に組み立てるためのAPIではありません。

```text
signed Placement Approval bytes
  -> current Trust Bundle
  -> strict COSE / P2 / key / role / principal / signature verification
  -> current Task / Attempt / policy read
  -> current-state revalidation
  -> Authority fieldsをVerifiedから一方向に導出
  -> durable Lease reservation / FenceEpoch
  -> future canonical Lease payload / LSE-1 signing
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

## Authority境界

このserviceの成功結果も、まだWorkerへ提示できる署名済みP2 Leaseではありません。

次段で必要です。

- canonical `eki.lease/v1` payload
- LSE-1 signing key / role
- COSE envelope
- Worker側のstrict decode / key / role / subject / FenceEpoch / expiry verification

durable reservation commit前に署名済みLeaseを外へ出しません。

また、durable reservationが成功したことだけを後続signing authorityにしません。reservation後にkey state、Trust Bundle、policy、Task / Attempt stateが変わる可能性があるため、将来のLSE-1 signing境界は**署名直前のcurrent key / trust / policy / subject stateを再確認**し、失効・取消・supersedeをfail closedにする必要があります。

このserviceは「署名可能なLeaseを確定した」のではなく、「現在検証したPlacementを、Authorityを増幅させずdurable fencing stateへ予約した」ところまでを保証します。

## reservation後のdrift

verificationとdurable reservationの後、実際にLeaseをsignする前にkey、Trust Bundle、policy、Task / Attempt stateが変わるraceは残ります。

Security上はsigningをfail closedにすればAuthorityを外へ出さずに済みます。ただし、signingを拒否したreservation / fenceをそのままexpiryまで残すと、新しいPlacementを一時的に妨げる可用性問題になります。

そのため後続のsigning work unitでは、少なくとも次を同時に設計します。

- signing直前のcurrent authority再検証
- signing失敗・policy revoke・key compromise時のreservation revoke / close
- stale reservationを新Authorityへ再利用しない
- cleanupがFenceEpochを巻き戻さない
- Worker / Local Governor currentnessをAuthority sourceへ接続するまでは、Worker向けsigned Leaseを完成扱いしない

現在のserviceはこのraceを「安全に解決済み」とは主張しません。
