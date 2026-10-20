# ResultClaimの受理（Control Plane側）

Workerから届いた`ResultClaim`を、Control PlaneのAttempt状態へ反映します。

```text
Claim
  -> Attemptを読む
  -> Leaseを読む（現在のfence）
  -> 記録済みのClaimを読む
  -> resultclaim.Decide（new / duplicate / stale / late / conflict）
  -> Claimを記録（一度だけ）
  -> Attemptを進める
       completed                     -> A-VERIFYING
       failed / reclaimed / deadline -> A-CLOSED（理由つき）
       cancelled / output_exceeded   -> A-CLOSED（理由つき）
       runner_stuck                  -> 変更しない
```

## 記録の順序

`completed`のClaimを、A-ASSIGNEDなどの早すぎる段階で受け取った場合は、Claimを記録する前に`ErrWrongPhase`で拒否します。適用できないClaimを保存して、後から「記録はあるが状態が進まない」状態を作らないためです。

`completed`は、A-DISPATCHEDからはA-CLAIMEDを経てA-VERIFYINGへ進みます。それ以外の終端のstatusは、A-CLOSEDへ一回で進みます。

## 再送とcrash

同じClaimの再送は、Claimを二度記録しません。前回の処理が「Claimを記録した後、Attemptを進める前」に落ちた場合も、`duplicate`のまま状態の遷移だけをやり直します。

## 原子性

Claimの記録とAttemptの遷移は、`ResultClaimStore.ApplyResultClaim`の一つのtransactionで行います。どちらかだけが残ることはありません。`completed`をA-DISPATCHEDからA-VERIFYINGへ進めるときだけ、A-CLAIMEDへの最初の一歩を別のCASで行います。その間に落ちても、AttemptはA-CLAIMEDで残り、再送でClaimの記録と検証への遷移をやり直せます。

## 何を確定しないか

- 結果の正しさ。`completed`はA-VERIFYINGへ進めるだけで、Accepted Resultにはしません。
- Leaseの復活。fenceが現在より古いClaimは記録だけ残します。
- 報告が届かないAttemptの扱い。Leaseの期限とheartbeatからの確定は別の境界です。
