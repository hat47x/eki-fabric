# 申告が届かないAttemptの扱い（Claim reconciliation）

Leaseが期限切れになっても、ResultClaimが届かない場合があります。Workerが落ちた、networkが切れた、Agentのqueueに残っている、といった理由です。これを、ただちに「失敗」や「再実行してよい」へ丸めません。

```text
Attemptが開いている かつ Claimがない
  ├ Leaseがまだ期限内          -> AwaitClaim
  ├ 期限切れ + grace内          -> AwaitClaim
  ├ grace後 + 外部副作用の可能性 -> Reconcile
  └ grace後 + 副作用なし         -> CloseLeaseExpired
```

## なぜgraceを置くか

claimqueueはat-least-onceで送ります。Leaseの期限直後は、Claimがまだ届いていないだけかもしれません。すぐにAttemptを閉じると、届いたClaimが`late`になり、正しい結果を検証へ送れません。

## なぜ外部副作用で分けるか

TaskがEKIの外へ副作用を持つ場合、実行されたかどうかはControl Planeの状態だけでは分かりません。Receiptが得られるまでAttemptを閉じず、`Reconcile`として残します。閉じて再実行すると、二重の副作用になり得ます。

副作用がない場合も、`CloseLeaseExpired`はAttemptを閉じるだけです。再実行してよいかは、Taskの信頼性契約とattempt budgetで`recovery`が決めます。

## 何を保証しないか

- 実行が実際に止まったこと。Leaseの期限は、Workerが止まった証明ではありません。
- 外部副作用の有無の判定。Task契約から読みます。
- Claimのlate受信の扱い。`resultclaim.Decide`の`late`が担当します。
- graceの値。運用とTaskの性質から決める必要があります。
