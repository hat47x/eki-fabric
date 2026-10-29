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
| `TestControlRefusesTestProfileOffLoopbackByDefault` | TLSを付けても、loopback以外のWorker listenerは既定で起動を拒否する。理由に、試験用であることとADR-0027を含む |
| `TestControlAllowsTestProfileOffLoopbackOnlyWhenExplicitAndWarns` | `allow_test_profile_off_loopback`とTLSがあるときだけloopback以外で起動し、試験用profileであることを警告に出す |
| `TestControlRefusesPlaintextWorkerListenerOffLoopback` | `allow_test_profile_off_loopback`を指定しても、TLSがなければ`eki-control`は起動せずに失敗する |

同期は、期限付きのpollingです。固定時間のsleepで待ちません。

## WorkerごとのmTLS（ADR-0027 段階2）

`mtls_test.go`は、Worker listenerを`auth: "mtls"`で起動して、運用者のsubcommand、Agent、Control Planeを通して確かめます。CAは`eki-control gen-worker-ca`、資格は`eki-control enroll-token`、登録は`eki-agent enroll`、失効は`eki-control worker-state`で作ります。この試験は、認証の方式を確かめるもので、通信契約（#198）の決定ではありません。

| 試験 | 内容 |
|---|---|
| `TestMTLSEnrollWithCLICredentialThenDeliverClaim` | 運用者が発行した資格でAgentが登録し、登録簿の識別子の証明書でmTLS接続して、Claimを届けるとAttemptがverifyingへ進む。同じ資格の2回目は拒否され、存在しない資格と同じ401と本文になる。URLのクエリに資格を置くと400。資格がどのプロセスのログにも出ない |
| `TestMTLSWrongFingerprintAbortsEnrollmentBeforeTheSecretIsSent` | 指紋が違うと、Agentは資格を送らずに止まる。Control Planeのログに登録要求が現れず、資格は未使用のまま、正しい指紋で登録できる |
| `TestMTLSRevokedWorkerIsRefusedOnItsNextRequest` | Agentが接続を保持している間に失効すると、次のClaimは401で拒否され、AgentのqueueにClaimが残り、Attemptは変わらない。失効は最終で、更新もできない |
| `TestMTLSAgentRenewsItsCertificateAndExpiredCertificatesAreRefused` | 有効期間30秒の証明書を、Agentが残り3分の1で自動更新する（識別子は同じ、登録簿の通し番号が変わる）。元の期限を過ぎても、更新後の証明書でClaimが届く。更新していない別のWorkerの期限切れの証明書は、Claimも更新も401 |
| `TestMTLSRefusesCertificatesOfOtherCAsAndTrustDomains` | 未知のCA、別の信頼領域のCA、サブドメイン、別の名前空間（`/admin/`）、未登録の識別子の証明書は401。同じClaimは正しい証明書では通る |
| `TestMTLSForgedCSRSubjectStillGetsTheRegistryIdentity` | 別のWorkerの識別子を名乗るCSRでも、登録簿の識別子の証明書になる。その証明書で他のWorkerのClaimを送ると、変更していない所有検査が403で拒否する |
| `TestMTLSWorkerAndClientCredentialsDoNotCross` | ClientのトークンはWorker listenerの全経路で401。Worker証明書はClient APIで401（Client APIはTLSで起動して確かめる） |
| `TestMTLSConfigurationFailsClosed` | 静的な表との併用、CA鍵がない、別の信頼領域のCA、TLSなし、のどれでも`eki-control`は起動せず失敗する |

## 割当の配送（MVP #57、Control Plane側）

`assignment_test.go`は、`conformance/signedchain`が実際のPLC-1、LSE-1、WOR-1の署名をState Storeへ書き、`eki-control`のmTLS Worker listenerが`/test-profile/v1/assignments`で配る経路を確かめます。#198の通信契約ではありません。

| 試験 | 内容 |
|---|---|
| `TestAssignmentsReachOnlyTheWorkerThatHoldsTheAttempt` | 登録した2つのWorkerに別々のAttemptを署名つきで配置する。Aは、自分のAttemptの保存されたLeaseとWorkOrderのbytesをそのまま受け取り、Bの識別子を応答に含まない。2回目も同じbytes。Bは自分のbytes。Attemptを持たないWorkerは空の一覧。別のWorkerを名指す本文は400。証明書なしは401。失効したWorkerは次の要求で401になり何も受け取らない。Bは影響を受けない |

更新の試験は、証明書の有効期間が壁時計で判定されることを使います。待ちは壁時計で行います。WSLのように壁時計と単調時計がずれる環境で、固定のsleepが期限より短くならないようにするためです。

## 確かめないこと

TLSを終端するプロキシを挟んだ構成、CA鍵のKMS・HSM保管、実時間の長い運用（72時間の更新）、実ネットワークの障害、複数のAgent、Worker側でのLease・WorkOrderの検証、Verificationの結果、Executorの実行は対象外です。Control Planeを強制終了しても、再起動後に同じClaimが届くことまでを確かめます。電源断は対象外です。seedのTask・Attempt・Leaseは、State Storeの公開APIで作った合成の入力で、署名の検証を経たものではありません。
