# Workerからの申告の入口（Claim plane）

Worker Planeのtransportは、Workerを認証し、認証済みのprincipalをこの境界へ渡します。このpackageは、**申告が名乗るWorkerと、認証されたWorkerが同じか**を確かめてから、`claimaccept`へ渡します。

```text
transport（認証）
  -> Principal{WorkerIdentity, TrustDomain}
  -> Submit
       claim.WorkerIdentity == principal.WorkerIdentity ?
       ├ no  -> ErrNotClaimOwner（acceptorを呼ばない）
       └ yes -> claimaccept.Accept
```

## なぜ必要か

`claimaccept`は、ClaimのWorkerIdentityが、保存されたAttemptのWorkerIdentityと一致することを確認します。しかし、それだけでは、認証されたWorkerが**別のWorkerの名前を書いたClaim**を送ることを止められません。Attemptの名前と一致してしまえば通ってしまうためです。

この境界は、transportが確かめたprincipalとClaimの名前を比べます。Claimの内容の正しさは保証しません。それは`claimaccept`とResult Verificationの仕事です。

## 何を保証しないか

- 認証そのもの。transportの責務です。
- Claimの内容の正しさ。
- Trust Domainの一致。ClaimはTrust Domainを持たないため、ここではprincipalの形式だけを確認します。
- 同じWorkerが複数のClaimを送ること。Attemptごとの重複と競合は`resultclaim.Decide`が扱います。
