# 申告の再送queue（Claim queue）

Attemptが終わったとき、Control Planeへ届く前にWorkerが止まる場合があります。回収、ネットワーク断、Agentの再起動です。終わり方の申告は、何が起きたかの証拠なので、一度の送信失敗で捨てません。

```text
Enqueue(claim)
  -> 同じAttemptの同じClaim -> 既にqueued
  -> 同じAttemptの違うClaim -> ErrConflict

DeliverDue(now, limit)
  -> Send成功 -> 削除
  -> Send失敗 -> Attempts++、NextAt = now + backoff、保持
```

## at-least-once

queueは少なくとも一度は送ります。二度届くことがあります。Control Planeは`resultclaim.Decide`で、同じClaimの再送を`duplicate`として畳みます。この二つを組にして、失わないことと、二重に記録しないことを両立します。

## backoff

失敗すると指数関数的に間隔を延ばします。上限は`MaxDelay`です。jitterはclaimのdigestとattempt回数から決まるため、Agentを再起動しても同じ待ち時間の列に戻ります。均等jitterで、遅延の半分以上は必ず空けます。多数のWorkerが一斉に再送しないようにするためです。

## 何を保証しないか

- Control Planeが受理したこと。送信の成功は、受理の確認ではありません。
- queueの耐久性はStoreに依存します。memory Storeはプロセス内だけで、再起動すると消えます。記録を残すには、後述のSQLite Storeなど、耐久的な保存先が必要です。
- Claimの順序。Attemptごとに独立して扱います。
- 送信結果が不明な場合の判別。`Sender.Send`がエラーを返したとき、Control Planeが受理したかは分かりません。再送し、Control Planeの重複判定に任せます。

## 保存則

`Sender.Send`が成功しても、Storeの削除に失敗する場合があります。その場合、次のpassで同じClaimを再送します。Control Plane側は`duplicate`として扱うため、記録は増えません。

再起動後も未送信の申告と再送予定を保持する場合は、[SQLite Store](../../adapters/reference/claimqueue/sqlite/README.md)を注入できます。メモリ実装やAgentの起動設定を自動で切り替えるものではありません。登録前のcrashや、Control Planeによる受理の確認は、引き続き別の境界です。
