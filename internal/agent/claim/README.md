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

また、`completed`のOutcomeに非ゼロの終了コードが入っている場合、`resultclaim.NewClaim`の検証で拒否されます。このbuilderは、検証を通すために値を直しません。

## 何を保証しないか

- Claimの内容の正しさ。Workerの自己申告です。
- 送信と再送。`resultclaim.Decide`とtransportの責務です。
- bindingの出どころ。`Attempt`は、AdmissionとローカルLease記録から作ります。OutcomeやWorkOrderから作り直しません。
