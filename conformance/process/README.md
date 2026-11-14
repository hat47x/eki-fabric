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

## 期限切れLeaseの回収（#745）

`lease_reclaim_test.go`は、`eki-control`を`lease_registry`つきで別プロセスとして起動し、Workerを動かさない状態で、回収ループが配線されていることを確かめます。

| 試験 | 内容 |
|---|---|
| `TestControlClosesAttemptOfExpiredLeaseWithoutClaim` | 状態ファイルに、dispatchedのAttemptを二つ置き、そのLeaseを台帳に載せる。2時間前に失効したLeaseのAttemptは`closed`（`lease_expired`）になり、台帳から消え、ResultClaimは付かない。2秒前に失効したLease（猶予の30秒以内）のAttemptは`dispatched`のままで、台帳にも残る |

Attemptを閉じたあとのTaskの扱いと、外部作用がありうるTaskの調停は確かめません。Taskのモデルに外部作用の印がないため、期限切れのAttemptは全て再試行可能として閉じます（#745の未決）。

## 割当の検証（MVP #57、Agent側）

`assignment_agent_test.go`は、`eki-agent`がmTLSで割当を受け取り、ファイルから読んだ信頼の起点で検証し、nonceを耐久DBに登録し、開始の関門を通すことを、実際の署名で確かめます。Taskは実行しません。

| 試験 | 内容 |
|---|---|
| `TestAgentVerifiesAndGateStartsDeliveredAssignments` | 本物の署名のAttemptは`ready`（上限は付与とローカルの天井の小さい方）。10分前に署名されたLeaseは配信一覧から外れ、Agentには届かない（Agent側の`lease_expired`拒否は`internal/agent/assignment`の試験で固定）。別のWorkerのAttemptはこのAgentに届かず、そのWorkerのAgentには届く。Agentを再起動して同じbytesを受けても、`replayed`で、二度は開始しない |
| `TestAgentWithAnotherTrustAnchorRefusesGenuineAssignments` | 別の鍵の信頼の起点を持つAgentは、本物の割当をLeaseの検証で拒否する |
| `TestRevokedWorkersAgentReceivesNothing` | 失効したWorkerのAgentは、要求が拒否され、何も決定しない |
| `TestAgentRefusesToStartWithoutATrustAnchor` | 信頼の起点のファイルがないと、Agentは起動せず失敗する |

改ざんしたbytes、別のWorkerのWorkOrderを混ぜたLease、Governorによる保留、Policy revisionの不一致などの否定の場合は、同じ署名を使うプロセス内の試験（`internal/agent/assignment`）で確かめます。
更新の試験は、証明書の有効期間が壁時計で判定されることを使います。待ちは壁時計で行います。WSLのように壁時計と単調時計がずれる環境で、固定のsleepが期限より短くならないようにするためです。

## 確かめないこと

この節までに説明した**基本的な申告配送試験**では、TLSを終端するプロキシ、CA鍵のKMS・HSM保管、実時間の長い運用（72時間の更新）、実ネットワークの障害、複数のAgent、Executorの実行とResult Verificationを対象外としています。後続の別テストで進んだ内容は次節に分けて記録します。Control Planeを強制終了しても、再起動後に同じClaimが届くことまでを確かめます。電源断は対象外です。seedのTask・Attempt・Leaseは、State Storeの公開APIで作った合成の入力で、署名の検証を経たものではありません。

## 2026-11-09 実装済み部品と一貫した実行経路を分けた確認

本節は現在のmainの**ソースと試験コードを照合した範囲**である。今回、Go 1.27を要求する正本リポジトリで試験を再実行したという意味ではない。

| 実装・試験 | 確認できる段階 | まだ証明しない段階 |
| --- | --- | --- |
| `internal/control/service/run.go`／`dispatch.go` | `dispatch`を設定した場合、Control Planeの起動時に配置ループを組み立て、Placement→Lease発行→WorkOrder発行へ進む経路がある。必要な設定や署名鍵が欠ければ起動を拒否する | 既定配備で有効になっていること、認可済みWorkerへの任意の連続Data配送 |
| [`dispatch_loop_test.go`](dispatch_loop_test.go) | **合成的にREADYへ進めたTask**から、起動済み`eki-control`が署名付きLease・WorkOrderを発行し、登録したWorkerがmTLSの試験用割当経路で受け取るケースを記述 | 外部Clientの申請から同じTaskを連続して実行する完全なE2E、正式なConnect Worker Protocol |
| [`assignment_agent_test.go`](assignment_agent_test.go) | `eki-agent`が試験用割当経路から署名付きLeaseを受けて確認し、開始の関門を通すケースを記述 | 任意のData/Delivery契約を使った媒体ストリーム |
| [`execution_agent_test.go`](execution_agent_test.go) | テストが別途用意した署名付き割当をAgentが受け、信頼済みの固定コマンドを実行し、ResultClaimを送る。テスト内で再実行のdigestを確認し、`resultacceptance.New`で作ったServiceに**合成Verificationを渡して明示的に`Accept`を呼ぶ** | 起動済みControl Planeが自動的にResult Verification／Acceptanceまで接続していること、実Providerの副作用 |

このため、以前の説明にある「起動経路に配置・Lease発行が全く存在しない」という状態は、現行mainには当てはまらない。さらに、後続の`http_submit_agent_test.go`は**Client APIで受理した同一Taskの実行・Claim・テスト内の明示的なResult Acceptance**を一連で検証する。ただし、TaskのREADYへの移行をテストが補助し、Verificationもテストが生成するため、**本番の自動検証・結果受理のE2Eは別の未達**である。[#242](https://github.com/hat47x/eki-fabric/issues/242)ではこの最後の境界と失敗時の復旧を追う。

別件の[#753](https://github.com/hat47x/eki-fabric/issues/753)では、OSのUnixソケットで固定イベントを送受信したが、上記Worker制御／割当経路を新しいメディアData Adapterとして解釈しない。現行Worker listenerとData Adapterの権限・時刻・出力の証拠を分け、既存のプロセス試験の足場を再利用する。新しいHostや独自の権限管理を増やさず、利用側の意味受理や外部効果をEKIのClaim受理から推定しない。

## 2026-11-09 同一Taskの実行証拠と未知Effectの停止条件

[同一TaskのEvidence計画](EXECUTION_EVIDENCE_PLAN.md)に、Client API→Dispatch→Lease/WorkOrder→Agent→Claim→Verification/Acceptanceの一貫したID照合、独立Expectedを持つES-01〜ES-07、および期限切れ時の未知External Effectを扱うStop Gateをまとめた。既存の各テストの成功を継ぎ合わせても同一TaskのE2E実証にはならない。実装変更は#242／#745の所有Issueで判断し、本節はテスト条件を共有するのみ。

## 2026-11-12 #745完了後の同一Task E2E検証

[#745](https://github.com/hat47x/eki-fabric/issues/745)の現行実装では、`recallCloser`が保存済みAdmissionのEffectNone宣言をTask／Attempt／Lease／Workerと照合し、証明できない期限切れAttemptを安全側に保留する。純粋な`ProvenM0EffectNone`のPASSは、現在のPolicy/Grant、実外部Effect、回収後の再投入の許可を意味しない。現行`recall_test.go`には正例・Admission欠損の保持負例がある。

[同一TaskのEvidence計画](EXECUTION_EVIDENCE_PLAN.md)には、Client API→Dispatch→Agent実行→Claim→Result Verification/Acceptanceを**同じTaskとAttemptで追う**#242の残りと、既存の限定Go 1.27 D0-A結果と現行revisionの未実行を区別したステータスを記録する。`http_submit_agent_test.go`の同一Task縦断テストの存在を踏まえ、テストが補助したREADY遷移・合成Verification・明示的Acceptanceと、実起動構成の自動処理を区別する。別々の合成Taskの成功を足して、本番の自動E2EがPASSしたとはしない。

## 2026-11-13 Harness連携を想定したEvidenceの受入条件

[Harnessへの受け渡し証拠・負例](HANDOFF_EVIDENCE_GATES.md)に、固定対象の再解決・開示許可・EKIの実行資格・送受信・Consumer Load・外部Tool実行・Provider効果を分離したHE-01〜10を整理した。追加のruntime状態機械ではなく、既存の同一Task Conformanceの検証条件である。#745のM0回収の実装完了と#754のM0/MVP限定の方式判断は、#242の実行E2Eや#753の実Data Adapter比較の完了を意味しない。

## 2026-11-13 Client APIの結果公開Gateを縦断テストで確認

[`TestHTTPTaskSubmissionIsExecutedByTheAgent`](http_submit_agent_test.go)は、Client APIから受理したTaskを使い、起動したControl／Agent間で署名付き割当から固定コマンド実行・ResultClaim保存まで進める。既存テストでも検証と受理はテスト内のServiceに明示的に証拠を渡して実行しており、独立した本番Verifierではない。

今回、(1) Agentのイベントを発行した**同一Attempt**に限定、(2) 記録されたClaimとTask／Attempt／Worker／LeaseおよびFenceEpochの一致を正本State Storeで照合、(3) Claim記録後・明示的Verification前の`GET /v1/tasks/{id}/result`は**409 RESULT_NOT_READY**、(4) テスト内で明示的に受理した後には同じTaskのAPIが**200**を返し、出力digestがAcceptedResultと一致する、という検証を加えた。

この追加テストは**現行mainのGo 1.27環境で未実行**。元のテストが存在することと、今回の追加アサーションがPASSしたことを混同しない。Claimを処理する本番Worker入口には`ResultAcceptance` Serviceが配線されているが、独立Verificationが不足していれば結果は保留される。次はテスト内の合成Verificationを本番Verifierの出典付き観測へ置き換え、結果受理後のPublic API確認まで同一Taskで証明する。外部ProviderのCommit、Consumer実受領、一般用途Data Adapterの配送は別の検証範囲。
