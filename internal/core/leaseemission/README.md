# Lease Authorityの発行状態

LSE-1では、署名処理が成功した時点をLease Authorityの発行時点にしません。

新しいLeaseを外部へ返せるのは、署名済みのexact bytesをState Storeへ耐久的に確定した後だけです。

```text
durable Lease reservation
  -> canonical Lease payload
  -> signing
       # まだ外部へ出さない
  -> durable emission commit
       # exact signed bytesを確定
  -> EMITTED
  -> delivery / replay
```

`EMITTED`のcommitを、M0におけるLease Authority発行の線形化点とします。

署名後にPolicy、Task、Attemptの前提が変わり、emission commitに失敗した場合、その署名bytesは外へ出さず破棄します。逆に、`EMITTED`が先に成立した後でPolicyやkeyの状態が変わっても、再送では新しい署名を作らず、耐久記録に残った同じbytesを返します。Worker側の受入可否は、受信時の検証境界で別に判断します。

## 状態

Lease reservationに対応する発行状態は、次の三つです。

- **RESERVED**: Leaseは予約済みだが、発行記録がない。
- **EMITTED**: exact signed envelopeを耐久的に確定した。
- **ABORTED**: Worker向けAuthorityを発行しないことを耐久的に確定した。

`RESERVED`は`lease_emissions`に行を持たない状態です。`EMITTED`と`ABORTED`だけを不変の記録として保存します。

## ABORTED

`ABORTED`は、署名前後でAuthorityの前提が失効した場合などに使います。

中止時には、対応するAttemptも同じ保存境界で`closed`へ進めます。これによって新しいPlacementへ進めるようにします。

ただし、消費済みのFenceEpochは戻しません。同じPlacement DecisionからLeaseを作り直すことも許しません。再配置では、新しいPlacement Decision、新しいAttempt、新しいLease、新しいFenceEpochを使います。

## Authorityの分離

`leaseemission.Record`は、署名機能そのものではありません。

`LeaseEmissionStore`へ新しい`EMITTED`をcommitする前に、上位のLSE-1境界で少なくとも次を満たす必要があります。

- canonical `eki.lease/v1` payloadを構築している。
- LSE-1として許可されたkeyで署名している。
- signed envelopeを再decodeし、payload、Decision ID、Authority principal、key、algorithmの対応を確認している。
- current Policy、Task、AttemptとLease reservationを再確認している。
- Trust Bundleとkeyのcurrentnessを、発行時点の要件として確認している。

`LeaseEmissionCommit`のmetadataを呼出側が埋めただけでは、このAuthority条件を満たしたことにはしません。今回のState Store境界は、上位で検証されたexact bytesを二重発行せず耐久化するための基盤です。

この上位の境界は`leaseissue.Emitter`です（`internal/core/leaseissue/README.md`）。

## 耐久性

Reference State Storeでは、次を共通conformanceとして確認します。

- 同じ発行の再送は同じsigned bytesへ収束する。
- Policy、Task、Attemptが変わった後は新しい`EMITTED`を作らない。
- `EMITTED`と`ABORTED`は相互に置き換えられない。
- `ABORTED`後は新しいPlacementへ進めるが、FenceEpochは増加する。
- SQLiteを再起動しても、発行状態とsigned bytesを復元できる。
- 発行記録は更新・削除できない。
