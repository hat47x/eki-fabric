# Claimの送信（Agent側）

`claimqueue`の`Sender`として、Worker Planeの`ClaimClient`へClaimを送ります。Agentの認証済みprincipalを固定して持ち、Claimがそのprincipalと一致しない場合は送信しません。

```text
claimqueue.DeliverDue
  -> claimclient.Send
       claim.WorkerIdentity == Agentのprincipal ?
       ├ no  -> エラー（queueは保持）
       └ yes -> ClaimClient.SubmitClaim
```

送信エラーはqueueへ返し、再送されます。at-least-onceと、Control Plane側の`duplicate`判定で、失わないことと二重記録しないことを両立します。

## まだ無いもの

- `ClaimClient`の実装。Connect clientの生成コードと接続設定。
- 認証情報の更新と失効時の扱い。
- 送信のタイムアウトと、Control Plane側の流量制限。
