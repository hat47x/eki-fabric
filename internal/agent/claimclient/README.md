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

通信エラーがなくても、空の受理確認や未知の判定・Attempt状態を成功扱いしません。受理確認の`VerificationRequired`が`verifying`状態と一致しない場合も、`ErrInvalidAcknowledgement`をqueueへ返して申告を保持します。既知の`new`・`duplicate`・`stale`・`late`は、正しい形の受理確認があれば送信済みとして扱います。`stale`や`late`の記録も結果の採用やLeaseの復活を意味しません。

この検査は受理確認の形と項目間の整合性だけを確かめます。応答元の認証と、送信した申告と応答の対応確認はtransportの責務です。ネットワークの実装や実行の一回性を保証するものではありません。

## 送信の期限

`New`は1回の送信に既定30秒の期限を付けます。`NewWithTimeout`で正の期間を指定すれば変更できます。呼出し側が既に短い期限を設定していれば、それを延長しません。取消済みのcontextではtransportを呼びません。子contextは呼出しの終了時に解放し、呼出し側のcontextは取り消しません。

transportが期限切れのエラーを返すと、queueは申告を保持して再送を予約します。待ち時間は送信失敗後に取得した時刻を起点に計算します。期限切れは、Control Planeが未受理であることの証明にはなりません。再送の二重記録は既存の`duplicate`判定で防ぎます。

transportはcontextの取消に応じて返る必要があります。このclientはcontextを無視する実装を強制終了せず、放置したgoroutineを作って待機を打ち切ることもしません。実ネットワークでの取消や期限の扱いは、transportの接続時に別に確かめます。

## まだ無いもの

- `ClaimClient`の実装。Connect clientの生成コードと接続設定。
- 認証情報の更新と失効時の扱い。
- Control Plane側の流量制限。
