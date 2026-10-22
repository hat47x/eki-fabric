# ADR-0025 Leaseのpolicyの鮮度はControl Planeが守り、Workerには確かめさせない

- 状態: **採用（仮承認）**
- 日付: 2026-10-22
- 関連: Issue [#78](https://github.com/hat47x/eki-fabric/issues/78)、[#57](https://github.com/hat47x/eki-fabric/issues/57)、PR [#121](https://github.com/hat47x/eki-fabric/pull/121)、[ADR-0017](0017-start-and-stop-authority.md)

## 1. 背景

PR #121で、LSE-1の署名の境界（`leaseissue.Emitter`）を実装しました。RESERVEDのLeaseに`eki.lease/v1`の署名を付ける直前に、PolicyState、鍵、Trust Bundle、Task、Attemptを確かめ直し、前提が古ければABORTEDを記録して、署名済みのbytesを外へ出しません。

このとき、次の二点が決まっていませんでした（#78の2026-10-20のコメント）。

1. Leaseを受け取ったWorkerに、そのLeaseのpolicyが今も有効かを確かめさせるか。Workerは`policy_revision`をLeaseのbytesから読めますが、そのrevisionが今も有効かを問い合わせる経路を持ちません。
2. 署名している間にLSE-1の鍵のprimaryが入れ替わったとき、ABORTEDにするか、新しいprimaryで同じpayloadに署名し直すか。

#78には推奨案が示され、利用者が2026-10-22に仮承認しました。このADRはその推奨案を記録します。

## 2. 決定

### Workerに求める検証

1. Workerは、Leaseの署名、Trust Bundle、有効期限、Leaseのbytesにある`policy_revision`と自分が受け取った値の一致を確かめる。policyが今も有効かどうかは確かめない。
2. policyの取り消しへの防御は、Control Planeが担う。FenceEpochの前進と、reservationを閉じる処理で、取り消し後に新しいLeaseが出ないようにする。
3. したがって、policyを取り消してから、すでに配送したLeaseによる実行が止まるまでの上限は、Leaseの有効期間になる。Leaseの有効期間は短く保つ。
4. 有効期限を待たずに止める必要がある場合（emergency revoke）は、Control PlaneからWorkerへAttemptの取り消しを伝える経路で扱う。この経路は、このADRでは定めない。

### 署名中の鍵の入れ替わり

5. 署名している間にLSE-1の鍵のprimaryが入れ替わった場合は、ABORTEDを記録する。同じpayloadを新しいprimaryで署名し直さない。
6. ABORTEDの後、呼び出し側は新しいreservationから始める。FenceEpochは戻さない。

### 確定から署名の公開までの間の失効

7. 最後の確認から確定までの短い間に鍵が失効した場合、State Storeの側では検出できない。この場合も、1.の検証と、Leaseの有効期間の上限で扱う。

## 3. 理由

- Workerは、policyの読み取り元へ到達できるとは限りません。到達を必須にすると、Workerが参加しにくくなり、読み取り元が止まったときにWorker全体が止まります。
- reservationは、予約した時点の鍵とTrust Bundleを前提にしています。署名し直すと、呼び出し側の知らないところで前提が変わります。ABORTEDにすれば、呼び出し側は新しい前提で始め直せます。
- 鍵の入れ替えはまれなので、始め直す費用は小さいと見込みます。

## 4. 影響

- 保証しないこと: policyを取り消した直後に、すでに配送したLeaseによる実行が止まること。止まるまでの上限は、Leaseの有効期間です。
- Leaseの有効期間の既定値は、この上限を意識して決める必要があります。
- #78の残りの論点は、このADRで閉じます。

## 5. 未決事項

- Leaseの有効期間の既定値と上限の具体的な値。
- emergency revokeで、Control PlaneからWorkerへAttemptの取り消しを伝える経路の設計。
- 仮承認の見直し: 利用者が本承認するまで、状態は「採用（仮承認）」とする。見直しで方針が変わった場合は、このADRを置き換えるADRを書く。
