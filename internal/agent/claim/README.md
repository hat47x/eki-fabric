# 実行結果からResultClaimを作る（Agent側）

実行の終わり（`executor.Outcome`）を、Control Planeへ送る`resultclaim.Claim`へ変換します。

```text
Outcome（status / exit code / 出力byte / 開始・終了時刻）
  + Attemptのbinding（Task / TaskIR / Attempt / Worker / Lease / fence / WorkOrder / executor / nonce）
  -> Build
  -> Claim（resultclaim.NewClaimで検証）
```

## なぜ写像を明示するか

Outcomeのstatusを、そのまま文字列として送らないためです。新しいstatusが増えたときに、黙って`failed`や`completed`へ丸めず、`ErrUnknownStatus`で止めます。

また、`completed`のOutcomeに非ゼロの終了コードが入っている場合、`resultclaim.NewClaim`の検証で拒否されます。このbuilderは検証を通すために値を直しません。

終了時刻が開始時刻より前の場合も、時刻を補正せず`resultclaim.ErrInvalidClaim`で拒否します。同時刻の観測は有効です。時計が戻った場合、その観測をどう扱うかは呼出し側で判断する必要があります。

## 何を保証しないか

- Claimの内容の正しさ。Workerの自己申告です。
- 送信と再送。`resultclaim.Decide`とtransportの責務です。
- bindingの出どころ。`Attempt`は、AdmissionとローカルLease記録から作ります。OutcomeやWorkOrderから作り直しません。
