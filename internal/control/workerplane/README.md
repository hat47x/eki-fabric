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

## まだ無いもの

- Connect serviceの生成コードと、実際のhandler。
- Workerの登録、Leaseの配送、WorkOrderの配信。
- 流量制限と監査用のrequest ID。
