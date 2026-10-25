# Worker Planeの申告サービス（Control Plane側）

Worker Planeのtransport（Protobuf / Connect）は、Workerを認証し、このサービスを呼びます。この層は、認証済みprincipalとClaimを`claimplane`へ渡し、結果を`ClaimAck`へ写像します。

```text
Connect handler（認証・framing）
  -> Server.SubmitClaim(ctx, Principal, Claim)
       -> claimplane（本人確認）
       -> claimaccept（束縛・原子的な受理）
  -> ClaimAck{Decision, AttemptPhase, VerificationRequired}
```

`ClaimAck`は検証結果ではありません。`completed`のClaimでも、`VerificationRequired`は「A-VERIFYINGへ進んだ」という意味だけです。

## 責務の境界

- 認証とframing: Connect handler。
- 送信者のidentityとClaimの一致: `claimplane`。
- Attemptとの束縛、状態遷移、Claimの記録: `claimaccept`とState Store。
- 結果の正しさ: Result Verification。

## プロセス間の試験用HTTP入口（`httpserver.go`）

`NewHTTPHandler`は、別プロセスのAgentが`Server.SubmitClaim`を呼ぶための試験用HTTP入口です。**Worker Protocolではなく、Workerの認証・登録（enrollment）の決定でもありません**（[#198](https://github.com/hat47x/eki-fabric/issues/198)）。`worker.proto`との互換性は主張せず、ResultClaimとprotoの対応にも触れません。

- 認証は、設定表の`session_token_sha256`（トークンのSHA-256）に対する、全項目を走査する定数時間比較です。一致した行の`worker_identity`と`trust_domain`が、そのままprincipalになります。本文のClaimが名乗るWorkerは、principalに使いません。
- 認証は本文を読む前に行います。未知のトークンは401、別のWorkerを名乗るClaimは`claimplane`が拒否して403、本文の上限超過は413（既定64 KiB）です。応答に出すのは固定のコードだけで、Attempt IDやDBの経路などの内部の文字列は含めません。詳細はログにだけ残します。
- 本文は`internal/spi/workerplane/testprofile`のJSONです。AgentとControl Planeが同じ符号化・復号を使い、未知の項目と末尾のデータを拒否します。

`internal/control/service`の設定で、`worker`を指定したときだけ、Client APIとは別のlistenerを開きます。TLSが必須で、loopbackアドレスだけは平文でも開けます。設定が不正な場合は、Client APIも含めて起動せずに失敗します。

プロセス全体の確認は[`conformance/process`](../../../conformance/process/README.md)にあります。

## まだ無いもの

- Connect serviceの生成コードと、実際のhandler。上のHTTP入口は、その代わりではない。
- Workerの登録（enrollment）、トークンの更新と失効、mTLS。
- Leaseの配送、WorkOrderの配信。
- 流量制限と監査用のrequest ID。
