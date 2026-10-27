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

`internal/control/service`の設定で、`worker`を指定したときだけ、Client APIとは別のlistenerを開きます。これは静的Bearerの表を使う試験用profileなので、既定ではloopbackアドレス（`127.0.0.1`、`::1`、`localhost`）だけを受け付け、`0.0.0.0`、`::`、空のホスト、`localhost`以外のホスト名は、名前を引かずにloopback以外として拒否します。loopback以外は、`allow_test_profile_off_loopback`（仮の名前）を明示した場合に限り許し、起動時に警告を出します。その場合もTLSは必須で、loopbackアドレスだけが平文でも開けます（[ADR-0027](../../../plan/adr/0027-worker-authentication-and-enrollment.md) 2.5、[#224](https://github.com/hat47x/eki-fabric/issues/224)）。設定が不正な場合は、Client APIも含めて起動せずに失敗します。

プロセス全体の確認は[`conformance/process`](../../../conformance/process/README.md)にあります。

## WorkerごとのmTLS（`mtls.go`、ADR-0027）

`NewMTLSHandler`は、Worker listenerを`auth: "mtls"`で動かすときのhandlerです。Workerの認証の既定の方式で、IDV-1のAdapterとして、検証した証明書を`Principal{WorkerIdentity, TrustDomain}`へ正規化します。

| 経路 | 認証 | 内容 |
|---|---|---|
| `POST /v1/worker/enroll` | クライアント証明書は不要。`Authorization: Bearer <enrollment資格>` | CSRを受け、資格を消費し、登録簿が発行した識別子の証明書とCAの証明書を返す |
| `POST /v1/worker/renew` | 現在有効なクライアント証明書（mTLS） | 新しい鍵のCSRで、新しい証明書を返す。同じ鍵のCSRは400。期限切れ・無効化・失効のWorkerは401 |
| `POST /test-profile/v1/claims`（試験用profileと同じ経路） | クライアント証明書（mTLS） | ResultClaimを受ける。Claimの本文は試験用profileのJSONのまま |

### 認証

- TLSの設定は証明書を**要求するが検証しない**（`tls.RequestClientCert`）。検証はhandlerが**要求ごとに**行います。enrollmentの経路は証明書なしで通す必要があること、接続が証明書の期限や登録簿の状態より長く続いても止められることが理由です。
- 検証は、Worker CAだけを信頼の起点にし、拡張鍵用途をクライアント認証に限り、現在時刻で行います。SANは`eki://<信頼領域>/worker/<識別子>`の一つだけで、他の種類のSANを持つ証明書は拒否します。GoはURIの名前制約でサブドメインも通すので、信頼領域は設定値と完全一致で比べます。
- 識別子は証明書の名前だけから取ります。ResultClaimなどの本文にWorkerの項目があっても、Principalを上書きしません。`claimplane`の所有検査（Claimが名指すWorkerと認証されたWorkerの一致）は変えていません。
- そのうえで、**毎回**`workerenrollment.Authorize`を呼び、登録簿の状態が`registered`であることを確かめます。無効化または失効したWorkerは、既存の接続の次の要求から401になり、応答に`Connection: close`を付けて接続を閉じます。登録簿を読めないときは、認証せずに503を返します。
- 失敗の応答は、証明書の期限切れ、未知のCA、別の信頼領域、未登録の識別子のどれでも同じ401です。

### enrollment

- 資格は`Authorization`ヘッダだけで受けます。URLにクエリ文字列が付いていれば、資格の中身を見る前に400で拒否します。
- 資格が使えない理由（形式の誤り、未知、期限切れ、使用済み、失効済み、別の信頼領域）にかかわらず、同じ401と同じ本文を返します。理由は監査ログ（`reason=`）にだけ残し、資格は出しません。
- CSRは、資格を消費する前に検証します（署名、P-256、8 KiB以下）。形式の違う要求で資格を消費しません。CSRの主体や名前は使いません。

### listenerの分離

Worker listenerは、Client APIとは別のlistenerと別のauthenticatorです。Worker証明書はClient APIでは資格になりません（Bearerだけを受ける）。Client APIのトークンは、Worker listenerのどの経路でも資格になりません。

## まだ無いもの

- Connect serviceの生成コードと、実際のhandler。上のHTTP入口は、その代わりではない。
- sessionの識別子を証明書へ束縛すること、再接続のたびの新しいsession（ADR-0027の2.3 項13）。今は接続の単位でしか分けていない。
- Leaseの配送、WorkOrderの配信。
- 流量制限と監査用のrequest ID。enrollmentの総当たりへの対策も、まだ無い。
