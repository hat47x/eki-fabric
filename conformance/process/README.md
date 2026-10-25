# プロセス単位の申告配送の試験

`eki-control`と`eki-agent`を別のOSプロセスとして起動し、AgentのSQLite queueにあるResultClaimを、TLS越しにControl Planeへ届けます。`TestMain`が`go build`で両方のバイナリを作り、一時ディレクトリ、空きポート、試験内で作ったCAと証明書だけを使います。

## 試験用の構成（#198ではない）

この試験は**試験用のprofile**です。Workerの認証・登録（enrollment）と通信契約は未決で（[#198](https://github.com/hat47x/eki-fabric/issues/198)）、ここで使うものは決定ではありません。

- Workerの認証は、設定表の`session_token_sha256 → {worker_identity, trust_domain}`です。
- 本文は、プロセス内の経路が運ぶClaimをそのままJSONにしたものです（`internal/spi/workerplane/testprofile`）。`worker.proto`とは互換ではありません。
- 申告queueの意味（`claimqueue`）と、ResultClaimとprotoの対応は変えていません。

## 確かめること

| 試験 | 内容 |
|---|---|
| `TestAgentDeliversClaimToControlAcrossControlRestart` | dispatched AttemptのClaimを届けると、Control PlaneのAttemptがverifyingに進みClaimが記録され、Agentのqueueが空になる。Control Planeを強制終了（SIGKILL）して次のClaimを積むと、Agentは保持して再送を重ね、Control Plane側は何も変わらない。同じ状態ファイルで再起動すると届く。Agentはinterruptで正常終了する |
| `TestControlRejectsUnknownTokenAndWrongPrincipalWithoutChange` | 未知のトークンは401、別のWorkerのセッションで他のWorkerのClaimを送ると403。どちらもAgentのqueueに残り、Control PlaneのAttemptとClaimは変わらず、ログにトークンが出ない |
| `TestControlRefusesPlaintextWorkerListenerOffLoopback` | loopback以外で平文のWorker listenerを設定すると、`eki-control`は起動せずに失敗する |

同期は、期限付きのpollingです。固定時間のsleepで待ちません。

## 確かめないこと

実ネットワークの障害、複数のAgent、署名つきのPlacement・Lease・WorkOrderの連鎖、Verificationの結果、Executorの実行は対象外です。Control Planeを強制終了しても、再起動後に同じClaimが届くことまでを確かめます。電源断は対象外です。seedのTask・Attempt・Leaseは、State Storeの公開APIで作った合成の入力で、署名の検証を経たものではありません。
