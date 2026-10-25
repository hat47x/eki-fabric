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

## プロセス間の試験用HTTP client（`http.go`）

`HTTPClient`は、`workerplane.ClaimClient`のHTTP(S)実装です。`cmd/eki-agent`が別プロセスのControl Planeへ申告を送るために使います。**Worker Protocolではなく、Workerの認証・登録（enrollment）の決定でもありません**（[#198](https://github.com/hat47x/eki-fabric/issues/198)）。`worker.proto`との互換性は主張しません。

- URLは`https`が必須です。`http`を許すのはloopbackアドレスだけで、パス・問い合わせ・userinfoを含むURLは拒否します。検証に使うCAは`LoadRootCAs`で与え、証明書の検証を省略する設定はありません。
- bearerトークンを`Authorization`ヘッダに付けます。リダイレクトには従いません（トークンが別の送信先へ渡るため）。
- 200以外の応答、読めない応答、上限を超える応答は、すべて「未配送」のエラーとしてqueueへ返し、申告を保持します。拒否（401、403など）の申告も、人が原因を直すまでqueueに残り、再送され続けます。
- 本文は`internal/spi/workerplane/testprofile`のJSONです。

## まだ無いもの

- Core/SPIの`ResultClaim`・`ClaimAck`とProtobufの対応の確定（[#198](https://github.com/hat47x/eki-fabric/issues/198)）。Connect clientの生成コード。
- 認証情報の更新と失効時の扱い、Workerの登録（enrollment）、mTLS。
- 恒久的に拒否された申告の扱い（上限回数、隔離、通知）。
- Control Plane側の流量制限。

`ClaimClient`と`ClaimAck`のProtobufとの対応は#198で確定する必要があります。`conformance/process`の試験が通っても、それは試験用の接続で二つのプロセスが申告を受け渡せたことを示すだけです。worker.protoとの互換性、認証方式の妥当性、Workerが実際にTaskを実行したことは示しません。

試験用JSONの受理確認では、`decision`・`attempt_phase`・`verification_required`の3項目を省略できず、`null`も拒否します。`verification_required: false`は有効ですが、項目の欠落を`false`として補いません。不完全な受理確認では申告を保持し、再起動後も再送時刻と失敗回数を引き継ぎます。これはJSONの項目の存在を確かめる検査であり、送った申告と応答の対応を証明するものではありません。
