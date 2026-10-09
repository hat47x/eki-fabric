# 実行端末との通信仕様（P1）

状態: **設計基準0.1（2026年9月21日）**

正本は次のとおりです。

- `v1/worker.proto` — 実行端末との通信を定めるProtobufのインターフェース定義
- 通信方式の選択: `../../plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`
- 契約の意味: `../../architecture/CONTRACT_BOUNDARIES.md`
- 処理依頼と期限付き実行権の意味: `../../architecture/TASK_STATE_MODEL.md`

## 目的

P1では、制御層の内部にある待ち行列を実行端末（Worker）へ直接公開しません。ネイティブ・ブラウザ・連携先の各種実行端末で共通して使える、特定の実装言語に依存しない通信契約を定義します。

実行端末から届いたメッセージは、送信元が認証済みであっても、自己申告や観測情報として扱います。端末が送った値だけを根拠に、処理の配置、期限付き実行権、結果の受理、資源提供への評価などの権限を与えません。

## 通信方式

参照する通信方式はProtobufとConnectの組み合わせです。

最小実用版では、同じメッセージの意味を保って次の二方式を利用できます。

1. `WatchCommands`による制御層から端末への連続配信と、`PostEvents`によるイベント送信
2. `PollCommands`と`PostEvents`による、要求と応答を一回ずつ行う方式への切り替え

企業の中継サーバーなどのために連続配信を使いにくい環境でも、処理依頼（Task）、実行試行（Attempt）、期限付き実行権（Lease）の意味を変えず、定期的な問い合わせ方式に切り替えられます。

P1では、実行端末から制御層へのイベント送信を`PostEvents`に統一します。双方向の連続配信を最小実用版の必須条件にはしません。

## 認証と実行端末の識別情報

`OpenSessionRequest`に`worker_id`はありません。

実行端末の識別情報は、mTLSやワークロード識別情報などの認証時の情報を使い、制御層が確定します。`agent_instance_id`は再起動や診断に使う一時的な識別子であり、実行権限を与えるものではありません。

依頼元の資格情報で実行端末向けAPIを呼べたり、実行端末の資格情報で処理依頼の投入権限を得られたりしないよう、資格情報の対象と権限範囲を分けます。

## セッション

`OpenSession`でControl Planeが`session_id`を発行します。

`PostEventsRequest`では、まとめて送るイベント全体を一つの`session_id`に結び付けます。同じ送信単位に、異なるセッションのイベントを混在させません。

実行端末の再起動やブラウザの再読み込みでは、新しいセッションを作ります。以前のセッションのメッセージを使って、期限付き実行権や作業指示書（WorkOrder）の権限を復活させません。

`WorkerEvent.sequence`はセッション内で観測の順序を補う値です。安全上の権限を与えるものではありません。

## イベントの配送

実行端末は再送時にも同じ`event_id`を使います。制御層は、同じイベントが一回以上届くことを前提に、重複による効果を生じさせずに処理します。

`EventReceipt`は`ACCEPTED / DUPLICATE / REJECTED`を返せます。

待ち行列からの再送だけを理由に、新しい実行試行を作りません。

## 指示の配送

`command_id`も同じ指示が繰り返し届くことを前提に扱います。

`WorkOrderDelivery.delivery_id`と`WorkOrderAck`を使い、同じ作業指示書が繰り返し届いても、仕様に従う実行エージェントは二重実行しないようにします。

悪意のある実行端末が二重実行する可能性は、結果、副作用、資源提供への評価に関する別の境界で扱います。

## 署名付き権限判断の配送

作業指示書や期限付き実行権など、安全性に関わる判断は、Protobufで符号化した値そのものには署名しません。

`SignedDecisionCarrier.cose_sign1`は、P2で定義したCOSE_Sign1形式のバイト列を配送するためのものです。

`kind`は配送先を選ぶための手掛かりであり、権限そのものではありません。WorkerはCOSEを検証し、signed payload内のkind / subject / Task / Attempt / Worker / expiry等との一致を確認します。

## 計算資源に関する申告

`ResourceAdvertisement`は実行端末による自己申告です。

- CPU / memory / GPU
- Runtime availability
- Local Governor上限
- verified cache hint

を処理の配置を決める部分へ観測情報として送れます。

`claimed_worker_class`やGPU性能に関する申告を、安全性についての保証とみなしません。Confidential execution等のClaimには別のAttestation / managed evidenceが必要です。

## 定期的な稼働通知

定期的な稼働通知（Heartbeat）は、端末の稼働や処理状況に関する観測情報です。

稼働通知を受信しただけで期限付き実行権を延長しません。

Lease renewalは`LeaseRenewalRequest`を受けてLease AuthorityがDecisionを作り、`LeaseRenewalDecision.signed_lease`として返します。

## 端末側の利用制限と資源の返還

`LocalAvailability`と`ReclaimNotice`は、Endpoint利用者・温度・battery・Provider reclaim等によるlocal-side constraintを表します。

Local Governorは中央より厳しい上限を適用でき、Taskが重要であることを理由にControl Planeが強制拡大しません。

`ReclaimNotice`は、送信できる範囲での通知です。突然Workerが消えてNoticeを送れなくても、Lease expiryとRecoveryで扱います。

## 結果の申告

`ResultClaim`は実行端末による結果の申告です。

`EXECUTION_CLAIM_OUTCOME_COMPLETED`と申告されても、処理依頼が完了したことにはなりません。

Output Artifactのfinalization / Digest、Result Verification、Result Acceptanceを通した後にのみAcceptedResultが成立します。

`exit_code`、failure code、実行時刻もself-reported Observationであり、高保証事実へ自動昇格しません。

## 成果物のデータ本体

Artifact input/outputのbulk bytesはWorker Protocol messageへ埋め込みません。

Data PlaneのArtifactGrant / upload sessionを使い、P1ではResultClaimがfinalized Output Artifactを参照します。

## 入力内容の検証

Protobufの復号に成功しただけでは、入力を受理しません。

実装では、少なくとも次の項目をアプリケーション側で検証します。

- protocol revision
- session binding
- ID format / maximum length
- enumの`UNSPECIFIED` / unknown value
- repeated field件数
- string / bytes長
- timestamp range
- Digest algorithm / length
- event batch size
- signed decision carrier size
- monotonic sequence where applicable

Security-critical unknown enumを既知の安全な値として扱いません。

## 内部構成の秘匿

このProtocolはWorker↔Control内部Surfaceです。

ResourceAdvertisementやcache hintをExternal Client APIへそのまま投影しません。ClientへWorker hostname / IP / cache内容 / concrete identityを公開しないArchitecture原則を維持します。

## P1で未固定のもの

- concrete authentication mechanism
- Connect code generation version
- exact HTTP endpoint
- message compression default
- Browser Worker特有のAPI
- Attestation Evidence carrier
- Contribution metering
- P2 CDDL / COSE payload schema
- Artifact Data Plane transport

## 適合性の検証候補

- session identity mismatch
- stale session event
- duplicate event
- duplicate command
- unknown enum
- oversized batch / message
- heartbeat does not renew Lease
- old Lease revision renewal reject
- WorkOrder wrong Worker / expired / invalid prerequisite
- late ResultClaim does not revive Lease
- ReclaimNotice absence followed by Lease expiry
- polling / streaming modeでState semanticsが同じ

## P2との関係

P2では、作業指示書、期限付き実行権、配置の承認、成果物の利用許可など、署名対象となる正規のデータ形式を決定論的なCBORとして定義済みです。正本は`../decisions/v1/decisions.cddl`と`../decisions/v1/cose-profile.cddl`です。P1の`SignedDecisionCarrier`は、その署名付きデータを運びます。
