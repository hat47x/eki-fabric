# 拡張用SPIの設計（Extension SPI Architecture）

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricは、SEI、SUI、TEIに加え、今後現れるソフトウェアや異なる基盤にも接続できる、用途を限定しない分散計算基盤を目指します。

そのため、次の機能を差し替えられる境界を設けます。

- Queue
- State Store
- Artifact Store
- Identity
- Executor Runtime
- Scheduler Policy
- Verification
- Attestation
- External Effect Reconciliation
- Contribution Measurement
- Telemetry

一方、拡張機能がCoreの意味や権限を変えられると、次のように権限境界が崩れます。

- Scheduler pluginが、Authorizationを作る。
- Queue adapterが、勝手にAttemptを増やす。
- Storage adapterが、ArtifactGrantを迂回する。
- Verification pluginが、Resultを直接Acceptedにする。
- Worker Runtimeが、Capabilityを自己拡張する。

この文書では、差し替え可能な機能と、Coreが守る不変条件を分けて定めます。

関連する文書は、次のとおりです。

- AUTHORITY_BOUNDARIES.md
- CONTRACT_BOUNDARIES.md
- CONTROL_DATA_PLANE.md
- DEPLOYMENT_PROFILES.md
- TASK_STATE_MODEL.md

---

# 2. ExtensionとApplication Integrationを分ける

最も重要な原則です。

未知のApplicationやOSSが、EKIを使うために、Control Plane pluginを作る必要はありません。

通常は、次の形で接続します。

~~~text
Application
   │
   ├ External Client Contract
   │
   └ approved Executor
          │
          ▼
       EKI Fabric
~~~

Extension SPIを使うのは、

> FabricのInfrastructureの能力そのものを、差し替える

場合です。

たとえば、次のものがあります。

- S3互換のArtifact Store
- 企業のOIDC
- WASM Runtime
- TEE Verifier
- 独自のResult Validator
- NATSやdatabaseを使った配送
- Cloud Meter adapter

Application semanticsを、SPIに持ち込まないことを、優先します。

---

# 3. Core KernelとAdapter

論理的な構造は、次のとおりです。

~~~text
EKI Core Kernel
  Authority semantics
  Task / Attempt / Lease state
  Contract validation
  Security invariants
  Budget / recovery rules
  Result acceptance rules
       │
       │ SPI
       ▼
Adapters
  Identity
  State Store
  Delivery
  Artifact Store
  Executor Runtime
  Verification
  ...
~~~

Adapterは、Core Kernelから呼ばれます。

AdapterがCore State Machineを所有することは、ありません。

---

# 4. SPIが変更できないもの

Extensionの実装が変わっても、次のものを変更してはいけません。

1. TaskとAttemptのIdentityの分離。
2. Retryが、新しいAttemptを作ること。
3. Leaseのfencing。
4. Client RequestとWorkOrderの分離。
5. Scheduler ProposalとPlacement Approvalの分離。
6. Task submissionのauthorityと、Data authorityの分離。
7. ResultClaimとAcceptedResultの分離。
8. External EffectのUNKNOWN。
9. ArtifactGrantのscope。
10. Content DigestとAuthorizationの分離。
11. Local Governorの拒否権。
12. Federation namespaceのbinding。
13. Security classificationを下げることの禁止。
14. Retry、fan-out、転送のbudget。
15. Workerの自己申告を、高い保証の事実に自動的に昇格しないこと。
16. Contribution Recordと、外部の価値の分離。

Adapterがこれらを変更する場合、それはSPIの実装ではなく、Architectureの変更です。

---

# 5. Extension Privilege Class

Extensionを、一つのplugin categoryとして扱いません。

保持するCredentialや、処理するDataによって、Privilegeが異なります。

## EP-0 — Observation / Optimization

たとえば、次のものです。

- Telemetry Exporter
- learned scheduling estimator
- locality estimator

原則として、Security Authorityを持ちません。

## EP-1 — Evidence Processing

たとえば、次のものです。

- Result Validator
- Attestation Verifier Adapter
- Artifact Provenanceのparser

悪意のあるevidenceを処理するため、parserとcomputeの隔離が重要です。

Evidenceを、正規化したResultに変換します。ただし、最終的なAuthorizationを単独では作りません。

## EP-2 — Resource Access / Enforcement

たとえば、次のものです。

- Artifact Store Adapter
- Executor Runtime
- Sandbox backend
- Secret Provider
- External Effect Adapter

DataやhostのResourceに触れるため、blast radiusが大きくなります。

範囲を限ったCredentialとCapabilityを要求します。

## EP-3 — Authority Support

たとえば、次のものです。

- KMSとSigner Adapter
- Policy Evaluator
- authoritativeな遷移のためのState Store
- Federation Trust Store

Authorityそのもの、またはAuthorityの根に近い機能です。

最も厳しい、configuration、provenance、auditを要求します。

Privilege Classは、Worker AssuranceのTrust Levelではありません。

Control Plane extensionの、危険度の分類です。

---

# 6. Extensionの登録は、Administrative Planeで行う

TaskRequestが、

> このpluginをdownloadして使ってください

と指定できる構造にはしません。

Extensionは、Administrative Planeで、次のものを登録します。

- Extension ID
- kind
- 実装のversion
- package、image、binaryのDigest
- provenance
- API version
- privilege class
- 許可するconfigurationのscope
- networkの要件
- credentialの要件

Clientは、登録済みのextensionの論理的なcapabilityを、利用することはできます。ただし、実装のpackageを変更することはできません。

---

# 7. Extension Descriptor

論理的なDescriptorの例は、次のとおりです。

~~~text
Extension ID
Extension Kind
SPI API Version
Implementation Version
Implementation Digest
Execution Mode
Privilege Class
Capabilities
Configuration Schema Digest
Provenance refs
Supported platform
Health interface version
Activation state
Policy Revision
~~~

実際のwire schemaは、後続の実装で決めます。

---

# 8. SPI API VersionとImplementation Version

この2つを分けます。

## SPI API Version

CoreとAdapterの間の、Contractのversionです。

たとえば、次のものです。

~~~text
eki.spi.artifact-store/v1
~~~

## Implementation Version

Adapter製品のversionです。

たとえば、次のものです。

~~~text
example-s3-adapter 2.4.1
~~~

Implementationをupgradeしても、同じSPI APIを保てます。

SPI APIの破壊的な変更には、明示的なversionの移行が必要です。

---

# 9. Capability Negotiation

Extensionは、対応する任意のcapabilityを宣言できます。

たとえば、次のとおりです。

Artifact Store:

- range-read
- resumable-upload
- trusted-checksum
- server-side-encryption
- locality-metadata

Executor Runtime:

- hard-memory-limit
- hard-process-limit
- gpu
- checkpoint
- network-isolation

Capabilityの宣言は、

> 現在、この実装が、その機能を提供できる

という情報です。

Security上、重要なcapabilityを利用する場合は、実測、管理されたpolicy、Evidenceなどの、Assuranceの根拠を、別に持てる構造にします。

自己申告だけで、HARDなenforcementに昇格しません。

---

# 10. Critical Capability

CoreがTaskのSecurity Requirementを満たすために必要なCapabilityが、Extensionにない場合、fallbackでRequirementを弱めません。

たとえば、次のとおりです。

~~~text
Task requires hard memory isolation
Runtime only supports best-effort
  -> placement / execution reject
~~~

未知のcapabilityを、自動的にsupportedとして扱いません。

---

# 11. Execution Mode

Extensionは、複数の実行の形態を認めます。

## In-process

利点は、次のとおりです。

- latencyが低い
- 小規模なdeploymentで簡単
- 型安全な参照実装を作りやすい

欠点は、次のとおりです。

- crashやmemoryの破損のblast radius
- languageの制約
- dependencyの衝突

## Local out-of-process

たとえば、次のものです。

- sidecar
- child process
- local IPC

利点は、次のとおりです。

- processの隔離
- 多言語に対応できる
- 再起動しやすい

特権の高いものや、悪意のある入力を扱うparserでは、有力です。

## Remote service

たとえば、次のものです。

- 企業のIdP
- cloudのobject store
- 管理されたKMS
- 外部のverifier

Networkの障害、credential、latencyを、明示的に扱います。

Extension Kindごとに、許容するExecution Modeを決められます。

---

# 12. In-processを、無条件に信頼しない

管理者がinstallしたExtensionでも、bugや侵害はあり得ます。

In-processのAdapterに、次のものを自動的に付与しません。

- すべてのsigning key
- すべてのtenantのdata
- 制限のないdatabase handle
- 制限のないnetwork

必要最小限のHost Interfaceを渡します。

---

# 13. Host Services

AdapterがCore内部に自由にaccessするのではなく、範囲を限ったHost Serviceを通します。

たとえば、次のものです。

- 構造化されたlogger
- telemetry emitter
- clock
- configuration reader
- secret referenceのresolver
- 範囲を限ったartifact access
- cancellation context

AdapterにTask State databaseのconnectionそのものを渡すことを、避けます。

---

# 14. Configuration

Extension configurationは、Administrative Planeで、versionを管理します。

Task Clientが、任意のconfigurationを直接注入することはありません。

Executor固有のTask Parameterは、別のContractです。

Infrastructure Adapterのconfigurationとは、分けます。

## 14.1 Secret

Configuration fileに、生のsecretを埋め込むよりも、次のものを利用できる構造を、優先します。

- secret reference
- KMS key reference
- workload identity

## 14.2 Configuration Revision

Security上、重要なAdapterは、どのConfig Revisionで、DecisionやEvidenceが生成されたかを、追跡できる余地を持ちます。

---

# 15. Activation

Extensionは、概念上、次の状態を持てます。

- registered
- validated
- active
- draining
- disabled
- revoked

Packageが存在するだけでは、activeにしません。

Activationは、Adminの操作です。

---

# 16. Upgrade

Extensionをupgradeしても、進行中のTaskのsemanticsを、突然変えません。

方法の例は、次のとおりです。

~~~text
v1 active
v2 registered
v2 health / conformance check
new work -> v2
old work -> v1 drain
v1 disable
~~~

Securityの緊急時は、drainせずにrevokeできます。

---

# 17. Rollback

Rollbackするときも、古い脆弱なversionを、無条件に再度activateしません。

許可されたversion、revocation、policy revisionを確認します。

Versionが古いことと、安全であることとは、別です。

---

# 18. 弱い実装への、silentなfallbackをしない

Extensionが失敗したときに、より弱い実装に、暗黙にfallbackしません。

たとえば、次のとおりです。

~~~text
TEE verifier unavailable
  != accept self-reported environment

KMS unavailable
  != use scheduler local dev key

private store unavailable
  != upload to public bucket
~~~

Fallbackを許す場合は、Policyで明示します。そして、同等以上のRequirementを満たす必要があります。

---

# 19. TimeoutとResource Budget

Adapterの呼び出し自体に、上限を設けます。

Kindに応じて、次のものを制限します。

- wall time
- memory
- requestのsize
- responseのsize
- concurrency
- retry

悪意のあるArtifactやEvidenceのparserが、Control Planeのthreadを、無期限に占有しないようにします。

---

# 20. Error Contract

Adapterのerrorは、型付きのcategoryに正規化します。

概念上の例は、次のとおりです。

- INVALID_INPUT
- UNSUPPORTED
- DENIED
- STALE
- CONFLICT
- INTEGRITY_FAILURE
- TRANSIENT
- UNAVAILABLE
- INTERNAL

Coreが、error categoryとTask semanticsから、retryなどを判断します。

Adapterが、retry policyを勝手に決めることはありません。

---

# 21. Health

Extensionは、healthとreadinessを報告できます。

Health=trueは、Security Assuranceではありません。

単に、

> serviceとして、requestを受けられる

という、Operational Observationです。

---

# 22. Circuit Breaker

Remote Adapterが大量の失敗を起こす場合、Core側で、上限を設けたretryやcircuit breakerを利用できます。

Circuit breakerがopenになったことを、

> Taskが失敗した

に直接変換しません。

別のAdapterや、あとからのretryが、Policyの上で可能かを評価します。

---

# 23. Queue / Delivery SPI

## 役割

永続的なstateで確定した、delivery intentを、Worker Gatewayなどに配送します。

候補は、次のとおりです。

- 組み込みのqueue
- databaseを使ったqueue
- NATSとJetStream
- Kafka
- Redis系
- cloudのqueue

## Queue SPIがしてはいけないこと

- Task IDの発行
- Attemptの作成
- Leaseの作成
- retry authorityの生成
- security classificationの変更
- Workerの選択

Queueのmessageは、Authorityではありません。

## 必要なsemantics

- at-least-onceの配送を扱える
- 安定したmessage ID
- ackとretry
- 上限のあるvisibilityやlease相当
- orderingを、必要以上に仮定しない
- 重複した配送を許容する
- dead-letterとpoisonの扱い
- backpressure

Exactly-onceのqueueを、Coreの正しさの前提にしません。

---

# 24. State Store SPI

## 役割

Task、Attempt、Lease、Budget、Decisionの、永続的なstateを保存します。

## 必要なproperty

少なくとも、次のものが必要です。

- 永続的な書き込み
- 一意なidentityの制約
- 条件付きの更新とcompare-and-swap
- transactionまたは同等の原子性
- 単調に増えるstate revision
- 現在のstateの読み取り
- 回復のためのscan
- 冪等なdecision record

## State Storeがしてはいけないこと

DBのTTLでLeaseのrowが消えたからといって、

> Taskが失敗した

と決めることは、できません。

Stateのsemanticsは、Coreが持ちます。

## 配送とtransaction

State updateとOutbox recordを、同じtransactionに含められるか、同等の永続的なpatternを提供できることが、望ましいです。

---

# 25. Artifact Store SPI

## 役割

Artifactのbyteの保存と転送を提供します。

## 必要なproperty

- 変更できないcontentへのaccess
- size
- digestの検証のevidence
- 範囲を限ったreadとwrite
- uploadの確定
- errorの分類
- localityのmetadata
- deletionとretentionのhook

## 任意

- range read
- multipart
- resumable
- 信頼できるserverのchecksum
- encryption
- replication
- siteのlocality

## 禁止

Artifact Store Adapter自体が、

> このWorkerは、このDataを読んでよい

というPolicyを、独自に決めてはいけません。

ArtifactGrantとData Authorityから、scopeを受け取ります。

---

# 26. Data Access Transport SPI

ArtifactGrantを、次のようなものに変換します。

- 署名付きURL
- 署名付きrequest
- mTLSのobject request
- local file descriptor
- proxy session

Transport固有のcredentialのscopeとTTLが、Grantを越えないことを要求します。

Storageのadmin credentialを、Workerに返しません。

---

# 27. Identity SPI

## 役割

Credentialを検証して、正規化したPrincipal Contextを作ります。

候補は、次のとおりです。

- OIDC
- X.509と企業のPKI
- SPIFFE
- cloudのworkload identity
- 管理されたenrollment

## Output

- Principal ID
- issuerとTrust Domain
- 認証の方法
- credentialの新しさ
- 関連する、認証済みの属性
- Evidenceの参照

## 禁止

Identity Adapterが、次のものを、自動的に確定してはいけません。

- Taskのauthorization
- Artifactのauthorization
- Resultが正しいこと
- Worker assurance

Authentication != Authorizationです。

---

# 28. Federation Trust SPI

複数のTrust Domainの、Bundleとissuerの検証を扱います。

必要なもの:

- Trust Domainのnamespaceのbinding
- 明示的なpeerの設定
- versionとrotation
- rollbackの防止
- 既定では推移的にしない

外部のidentityを、ローカルのadmin roleに、自動的にmapしません。

---

# 29. Policy Evaluator SPI

OPAやCedarなどのPolicy Engineを、将来接続できるようにします。

## 役割

Core Authorityが作ったcanonicalな入力に対して、Policyの評価結果を返します。

## 重要な点

Policy Evaluatorは、signing keyを持つ必要がありません。

~~~text
Policy Engine
  decides / explains
      ↓
Authority
  validates context
  records revision
  signs decision
~~~

このように分けられます。

## Output

- allowまたはdeny
- reason category
- 有効な制約
- policy revision
- 任意の、traceの参照

Clientが指定したpolicyを、そのまま評価して、Authorityにすることはしません。

---

# 30. Signer / KMS SPI

## 役割

Authority keyを安全に使って、Decisionに暗号学的なauthenticityを付与します。

候補は、次のとおりです。

- OS keystore
- local protected key
- cloud KMS
- HSM

## Confused Deputyへの対策

汎用の、

~~~text
sign(arbitrary bytes)
~~~

だけを、Control Planeのすべてのcomponentに公開しません。

可能な範囲で、次のものに範囲を限ります。

- Authority ID
- Decision Kind
- context namespace
- key handle
- canonical digest

SchedulerがWorkOrder keyを使って、任意のbytesに署名できる構造を、避けます。

---

# 31. Executor Runtime SPI

## 役割

承認済みのExecutor Artifactを、Sandboxやruntimeの上で実行します。

候補は、次のとおりです。

- 承認済みのnative
- WASM
- container
- browser
- 外部の管理されたserviceのadapter

## Input

- 正確なExecutor Digest
- WorkOrder
- Artifactのhandle
- Resource limit
- Capability set
- cancellationとleaseのcontext

## Output

- 実行の終了のobservation
- Result Artifactの参照
- resourceのtelemetry
- checkpointの参照
- error

## Runtimeがしてはいけないこと

- 任意のArtifactのfetch
- Capabilityの自己追加
- WorkOrderのsecurity fieldの変更
- Result Acceptance
- Contribution Recordの発行

## Enforcementのcapability

Runtimeは、次のようなenforcementのassuranceを表現できる余地を持ちます。

- HARD
- BEST_EFFORT
- OBSERVE_ONLY
- UNAVAILABLE

Taskの要件に合わないRuntimeには、Placementしません。

---

# 32. Sandbox Backend SPI

Executor Runtimeと、OS固有のenforcementを分離する場合に利用します。

候補は、次のとおりです。

- Linuxのcgroupとnamespace
- WindowsのJob Object
- WASMのsandbox
- containerのruntime

ResourceとCapabilityのlimitを適用します。

Sandbox Adapterが失敗しても、制限のないnative executionにはfallbackしません。

---

# 33. Scheduler Policy SPI

## 役割

すでに適格な候補の間で、Placement Proposalを生成します。

入力の例は、次のとおりです。

- Taskのresourceの必要量
- 候補のresourceの要約
- locality
- 転送のcost
- 予測される所要時間
- providerのavailability
- priorityとdeadline

## 出力

- 順位を付けた候補
- scoreとcost
- reason
- modelやalgorithmのversion

## Authorityの境界

Scheduler SPIは、次のことをしません。

- Data Classを変更しない
- Worker Assuranceを生成しない
- ArtifactGrantを発行しない
- Leaseを発行しない
- WorkOrderに署名しない

Learned modelも、このSPIの中のoptimizationです。

---

# 34. Learned Estimator SPI

Schedulerとは分けて、shadowで評価できるように、Prediction Adapterを置くこともできます。

predictionの候補は、次のとおりです。

- duration
- completion probability
- preemption risk
- transfer cost
- cache reuse

Outputは、Predictionです。

Permissionではありません。

Issue #19の研究は、この境界に接続できます。

---

# 35. Result Verification SPI

## 役割

ResultClaimについて、特定のmethodでEvidenceを評価し、正規化したVerificationResultを返します。

候補は、次のとおりです。

- 決定的な処理での、完全な一致
- application validator
- replicationの比較
- 管理下のanchor
- proof verifier

## 禁止

Verification Adapterが、AcceptedResultを直接生成してはいけません。

Result Acceptance Authorityが、Task requirementに照らして確定します。

---

# 36. Application Validator SPI

アプリケーション固有の、意味の検証を提供できます。

たとえば、次のものです。

- imageのoutput schemaの検証
- 科学的なresultの許容誤差
- business ruleのvalidator

Coreが、業務の意味を理解する必要はありません。

Validatorの、Identity、version、provenanceを、VerificationResultに残します。

---

# 37. Cryptographic Proof Verifier SPI

zkVMなどのProof Artifactを、nativeのverifierで検証し、正規化したVerificationResultに変換します。

Coreは、Proof Systemに固有のwire formatに依存しません。

Program、Input、Output、Attemptのbindingを、失わないことを要求します。

Issue #23のbenchmarkの結果で、利用できる範囲を、あとから評価します。

---

# 38. Attestation Verifier SPI

Vendor固有のEvidenceを評価して、正規化したAttestationResultに変換します。

Adapterが扱うものは、次のとおりです。

- Evidenceのparse
- 署名とendorsement
- nonceとfreshness
- reference value
- environmentのclaim

Relying Partyが、PlacementとData accessを、別に判断します。

Attestation Adapterが利用できないときに、自己申告にfallbackしません。

---

# 39. External Effect Reconciliation SPI

外部のSystemの、operation statusやreceiptを確認します。

Input:

- Operation ID
- Request Intent Digest
- effect adapter profile
- TaskとAttemptのcontext

Output:

- 適用済みと確認された
- 適用されていないと確認された
- まだ不明
- 権威のあるreceipt
- 一時的なerror

Adapterが、Task retryを直接起動することはありません。

Task State Authorityが、UNKNOWNとretryのsemanticsを評価します。

---

# 40. External Effect Execution Adapter

Workerに、任意のnetwork accessを与えずに、登録済みのexternal effectを実行するAdapterを利用できます。

たとえば、次のものです。

- messageの送信
- issueの作成
- 管理されたAPIの呼び出し

必要な条件は、次のとおりです。

- destinationを、adminの設定で固定する
- requestのschema
- credentialの隔離
- idempotencyのsemantics
- receiptへの対応
- rate limit
- audit

Task Clientが、Adapterのendpointを自由に差し替えることはできません。

---

# 41. Contribution Measurement SPI

Resource Measurement Evidenceを作ります。

候補は、次のとおりです。

- workerの自己申告
- Control Planeによるleaseの観測
- OSのcounter
- GPUのmeter
- cloud providerのmeter
- attestされたmeter

Output:

- Metric
- Unit
- Quantity
- Observer
- Assurance
- Work Context
- Evidence

Contribution RecordやCreditを、直接生成しません。

Contribution Authorityが、Policyを適用します。

---

# 42. Secret Provider SPI

Task、Attempt、Executorに範囲を限ったSecretを、materializeします。

候補は、次のとおりです。

- cloudのsecret manager
- 企業のvault
- localのOS store

必要なもの:

- 狭いscope
- 短い有効期間
- Schedulerに露出しない
- Workerに永続的なcacheを持たない
- 伏せ字にしたlog
- revokeとrotation

Secretを、Artifact localityのoptimizationの対象にはしません。

---

# 43. Telemetry Exporter SPI

Coreのsemanticなeventとmetricを、外部の観測基盤に送ります。

候補は、次のとおりです。

- OTLP
- local log
- 企業のSIEM
- metrics backend

Telemetryが失敗しても、Task retryを起こしません。

Telemetry Exporterに、生のsecretや不要な個人データを渡さない、redactionの境界を、Core側に置きます。

---

# 44. Audit Store SPI

Security上、重要なEventを、永続的に保存します。

必要なもの:

- 追記と永続的なrecord
- Task、Attempt、Decisionでのquery
- retention policy
- access control

任意のもの:

- 改ざんを検出できるchaining
- WORM
- 外部のSIEMへのexport

Audit Storeが失敗したときの、Task admissionのpolicyは、Deploymentとriskのprofileで決めます。

高い保証が必要な操作では、fail closedを選べます。

---

# 45. Safe Processing SPI

Artifactのparser、converter、inspectorを、上限を設けて実行します。

Inputは、悪意のあるArtifactです。

Output:

- formatのobservation
- 抽出して正規化したartifact
- safe-processingの結果
- resourceの使用量
- error

ParserがControl Planeのglobalなfilesystemやnetworkに、自由にaccessしないようにします。

---

# 46. Extensionの依存関係

Extension同士を、自由に再帰的に呼ばせると、Authorityのgraphが見えなくなります。

Baselineでは、Core orchestrationを中心にします。

~~~text
Core
  -> Policy
  -> Storage
  -> Runtime
  -> Verification
~~~

Adapter Aが、Adapter Bのadmin credentialを直接持つ構造を、避けます。

必要な組み合わせは、Core Host Serviceを通します。

---

# 47. ExtensionのNetwork access

Extensionのnetwork accessも、最小権限にします。

たとえば、次のとおりです。

- OIDC Adapter -> 設定されたIdP
- Artifact Store Adapter -> 設定されたstore
- KMS Adapter -> 設定されたKMS
- Effect Adapter -> 設定された外部API
- Scheduler -> 原則として、任意のInternetにはaccessしない

Taskのinputから、destinationを追加することはできません。

---

# 48. ExtensionのFilesystem access

In-processやlocalのExtensionに、globalなfilesystem accessが必要だとは、仮定しません。

Configuration、temporary directory、特定のsocketなどに範囲を限れます。

悪意のあるparserは、sandboxの中に隔離します。

---

# 49. ExtensionのCredential

Credentialは、Extension Kindやinstanceの単位で分けられる構造を、優先します。

同じcloud accountのkeyを、次のもので共有しない方向を、標準にします。

- Artifact Store
- KMS
- Federation
- Telemetry

---

# 50. Multi-tenant

Adapterがmulti-tenantのbackendに接続する場合も、Tenantのcontextを明示します。

Tenant AのArtifactGrantで、Tenant Bのprefixにaccessできないことを、Adapter conformanceでTestします。

Adapterのcacheでも、TenantとData Classの境界を維持します。

---

# 51. Adapter instance

同じKindのAdapterを、複数登録できます。

たとえば、次のとおりです。

~~~text
artifact-store:
  internal-east
  internal-west
  public-dataset

identity:
  employee-oidc
  partner-spiffe
~~~

Core Policyが、どのinstanceを使うかを決めます。

Clientが、任意のinstance名を使って、Policyを迂回することはできません。

---

# 52. Schemeの解決

ArtifactやIdentityなどに、scheme（`artifact://`のような接頭辞）に似た論理的なselectorを使う場合も、登録済みのmappingに限定します。

たとえば、次のものです。

~~~text
artifact://...
secret://...
~~~

これらを、OSのpathや任意のURLに、直結しません。

---

# 53. Extensionの選択

選択は、次のものから、Coreが行います。

- Task Policy
- Data Class
- Deployment Profile
- Extensionのhealth
- 必要なcapability
- 設定されたpriority

Extension自身が、

> 次は、自分ではなく、このuntrustedなendpointを使ってください

と、Authorityを委譲することはありません。

---

# 54. Fallbackのpolicy

複数のAdapterがある場合の、fallbackは、明示的です。

たとえば、次のとおりです。

~~~text
primary object store unavailable
  -> approved replica store

attestation verifier unavailable
  -> no equivalent verifier
  -> task held
~~~

Security Requirementを下げるfallbackは、禁止します。

---

# 55. Adapter呼び出しのretry

Coreは、operationのsemanticsにもとづいて、Adapterの呼び出しをretryします。

読み取り専用の操作や、冪等な操作は、retryしやすいです。

External effectなどは、Side-effect Contractを使います。

Adapterがtimeoutしただけで、冪等でないoperationを再送しません。

---

# 56. Idempotency key

Adapterのoperationにも、安定したidentityを渡せる余地を持ちます。

たとえば、次のものです。

- upload session
- state write
- external effect
- contribution recordの発行

重複した呼び出しで、二重のside effectを避けます。

---

# 57. Backpressure

Adapterは、過負荷を明示できます。

Coreは、UNAVAILABLEやBACKPRESSUREを、次のものに変換します。

- queueでの待ち
- 別のadapter
- 上限を設けたretry
- taskのhold

無制限のconcurrencyで押し込みません。

---

# 58. Cancellation

Coreから、Adapterに、cancellationとdeadlineのcontextを渡せます。

AdapterがCancellationを無視して、永遠に処理を続ける場合は、process isolationやtimeoutで、停止できる構造を選べます。

Cancellationは、external effectのrollbackを意味しません。

---

# 59. 決定性

Scheduler estimator、parser、validatorなどの決定性の要件を、Kindごとに定義できます。

Security Decisionで、非決定的なoutputを使う場合は、Auditできるように、input、version、seedなどを残します。

---

# 60. Supply-chain

Control Plane Extensionは、特権が高くなり得ます。

そのため、packageについて、次のものを持てる構造を要求します。

- 変更できないDigest
- publisherとbuildのprovenance
- 承認されたversion
- 脆弱性とrevocationのpolicy
- rollbackの制限

Task Executor Admissionの知見を利用します。ただし、Extensionのinstallationは、Administrative Planeです。

---

# 61. 自己更新の禁止

Extensionが、自分でInternetから新しいbinaryをdownloadして、Adminの承認なしに置き換えることを、標準にはしません。

Upgradeは、versionを持つAdministrativeな操作です。

---

# 62. Reference Adapter

EKIは、最低限のReference Adapterを提供できます。

目的は、次のとおりです。

- Core contractの実証
- conformance test
- 小規模なdeployment

Reference Adapterが、唯一の正解だとはしません。

---

# 63. Queue Conformance

最低限のTestの例は、次のとおりです。

- 重複した配送
- 遅れた配送
- 順序の入れ替わり
- consumerの再起動
- ackの喪失
- poison message
- backpressure

Queueが失敗しても、Attemptが勝手に増えないことを確認します。

---

# 64. State Store Conformance

次のことを確認します。

- 条件付きの更新の競合
- 古いrevisionのreject
- 一意なTaskとAttempt
- Leaseのfencing
- state commitの後のcrash
- 再起動後の回復
- 冪等な遷移
- outboxの永続性

---

# 65. Artifact Store Conformance

次のことを確認します。

- 誤ったdigest
- 途中で切れたobject
- rangeの挙動
- 範囲を限ったreadとwrite
- uploadのsizeの上限
- 中断されたmultipart
- tenantの境界
- 期限切れのaccess
- 変更できないcontent

---

# 66. Identity Conformance

次のことを確認します。

- issuerとTrust Domainの不一致
- 期限切れのcredential
- 誤ったaudience
- role credentialが推移しないこと
- namespaceの衝突
- rotationとrevocation

---

# 67. Policy Evaluator Conformance

次のことを確認します。

- policyがない
- 古いrevision
- 未知のfield
- denyの理由
- 決定的なcanonicalな入力
- security-criticalなunknownをfail closedにすること

---

# 68. Signer Conformance

次のことを確認します。

- 誤ったAuthority ID
- 誤ったDecision Kind
- revokeされたkey
- key rotation
- 署名のcontextの不一致
- 任意のbytesに署名しようとする試み

---

# 69. Executor Runtime Conformance

次のことを確認します。

- 誤ったExecutor Digest
- capabilityの拡大
- filesystemのdeny
- networkのdeny
- resource limit
- cancellation
- outputの上限
- WorkOrderのexpiry
- local governorのreclaim

---

# 70. Verification Conformance

次のことを確認します。

- 誤ったTaskやAttempt
- 改変されたoutput
- 偽のverifier identity
- 古いpolicy
- 未対応のmethod
- 矛盾するevidence
- 重複したresult

---

# 71. Attestation Conformance

次のことを確認します。

- nonceのreplay
- 古いevidence
- 誤ったWorker
- 誤ったTrust Domain
- 古いpolicy revision
- 期限切れのresult
- 未対応のenvironment claim

---

# 72. Effect Reconciliation Conformance

次のことを確認します。

- operation IDの不一致
- request digestの不一致
- outcomeが不明
- 重複したrequest
- 権威のあるreceipt
- timeout
- dispatchの後のcancellation

---

# 73. Contribution Measurement Conformance

次のことを確認します。

- 強いpolicyの下での、自己申告
- 偽のObserver
- assuranceの上限
- 重複した測定
- 矛盾する量
- TaskとAttemptの不一致
- unitとmetricのpolicy

---

# 74. Telemetry Conformance

次のことを確認します。

- secretの伏せ字
- 高いcardinalityの上限
- exporterの停止
- queueのbackpressure
- privacy fieldのfiltering

---

# 75. Adapterをまたぐ適合性テスト

個別のAdapterが正しくても、組み合わせると破綻する可能性があります。

たとえば、次の組み合わせです。

- Identity namespace + Artifact tenant scope
- Placement + Data Grant
- Lease + WorkOrderの配送
- Runtime + Artifact Digest
- Verification + Result Acceptance
- Effect Receipt + Task State
- Contribution + AcceptedResult

これらについて、Integration Testを持ちます。

---

# 76. Conformance Level

Adapterを、単純なpassかfailかだけでなく、対応するcapabilityを明示できるようにします。

たとえば、次のとおりです。

~~~text
artifact store
  core v1: PASS
  range-read: PASS
  trusted-checksum: UNAVAILABLE

runtime
  cpu hard limit: PASS
  gpu hard limit: BEST_EFFORT
~~~

Capabilityが足りないことを、隠しません。

---

# 77. Extensionの隔離の推奨

Privilegeに応じた推奨は、次のとおりです。

## EP-0

in-processでよい。

## EP-1

悪意のあるparserであれば、out-of-processを推奨する。

## EP-2

credentialとresourceのscopeを、強く分離する。Runtimeは、sandboxが必須である。

## EP-3

KMS、別のprocess、制限された管理の経路などを、優先する。

必須のProcess数としては、固定しません。

---

# 78. Browser Extension

Browser Workerに、Control PlaneのSPIをdownloadしません。

Browser側は、Worker Protocolと、承認済みのBrowser Executorだけを実装します。

Infrastructure Extensionは、Serverや管理されたruntime側です。

---

# 79. Federation Extension

Partner固有のbusiness logicを、Federation Trust SPIに入れすぎません。

Federation Adapterは、IdentityとTrust Materialの検証を担当します。Resource GrantとData Exportは、Core Policyに残します。

---

# 80. Cloud Provider Adapter

Cloud固有の、次のものを、Adapterで実装できます。

- workload identity
- object storage
- KMS
- resource meter

Cloud ProviderのIAM modelを、Core Contractそのものにはしません。

---

# 81. OCIやWASMなどの位置付け

OCI、WASM Component Modelなどは、有力なAdapterやpackagingの候補です。

しかし、次の関係を維持します。

~~~text
EKI Artifact != OCI Artifact
EKI Executor != WASM only
EKI Runtime != Container only
~~~

PropertyはCoreに置き、formatはAdapterに置きます。

---

# 82. Goによる参照実装

EKIをGoで参照実装しても、SPIの概念自体を、Goのinterfaceだけに閉じません。

2つの層を、認めます。

~~~text
Logical SPI Contract
   ├ in-process Go interface
   └ out-of-process language-neutral adapter protocol
~~~

Small Deploymentでは、Goのadapterを使いやすくします。Vendorの統合や、多言語での統合では、out-of-processのprotocolを使えます。

具体的なprotocolは、後続の設計です。

---

# 83. Out-of-process Adapter Protocol

最低限、次のものを持てる必要があります。

- adapter identity
- SPI API version
- capability negotiation
- request ID
- deadline
- cancellation
- 型付きのerror
- health
- telemetryのcorrelation

Control Planeの内部のstructを、そのままwireにserializeしません。

---

# 84. Extensionの発見

Filesystemをscanして見つかったbinaryを、自動的に実行することは、標準にしません。

Adminが登録し、承認したExtensionだけを、発見の対象にします。

動的な発見が必要な場合も、allowlistやregistryを通します。

---

# 85. Extension Registry

Control Planeの内部に、次のものを管理するRegistryを持てる構造にします。

- Extension ID
- 承認済みのDigest
- active version
- config revision
- privilege
- capabilities
- health
- revocation

Client APIには、Registry全体を公開しません。

---

# 86. Revocation

Extensionの脆弱性が見つかった場合は、次のことを行います。

- 新しい利用を止める
- 有効なTaskへの影響を評価する
- versionをrevokeする
- credentialをrotateする
- drainまたはkillする
- 別のadapterに移行する

Security-criticalなAdapterでは、すぐに止めることを選べます。

---

# 87. Adapterのstate

Adapter固有のStateと、Coreの権威あるstateを、分けます。

たとえば、次のものは、Adapter Stateです。

- upload multipartのsession
- model cacheのindex
- 外部providerのtoken cache

Task、Attempt、Leaseの正本を、Adapter独自のDBだけに置きません。

---

# 88. Adapterのcache

Adapterのcacheは、Optimizationです。

Cacheを失っても、Coreの正しさが失われない構造を、優先します。

永続的な状態が必要であれば、State Store Contractに昇格させます。

---

# 89. Schemaの所有

Core Contractのschemaは、EKI Coreが所有します。

Extension固有のschemaは、Extensionのnamespaceが所有できます。

Extensionのschemaで、CoreのSecurity fieldを上書きしません。

---

# 90. 名前空間付きのmetadata

Extensionのmetadataの例は、次のとおりです。

~~~text
extensions:
  com.example.foo/v1:
    ...
~~~

Coreが理解しない任意のmetadataを、Authority Decisionに利用しません。

criticalなextensionは、理解できなければ、rejectします。

---

# 91. Securityレビューの契機

次のExtensionの変更は、再度Reviewの対象です。

- Privilege Classの上昇
- 新しいnetwork access
- 新しいsecret access
- 新しいsigner key
- 新しいfilesystem scope
- security-criticalなcapabilityの追加
- schemaのmajor version
- tenantをまたぐ挙動の変更
- fallbackの挙動の変更

通常のbugfixより強いreviewを、要求できます。

---

# 92. 運用での説明可能性

Operatorは、次のものを、TaskやDecisionと関連付けて確認できる必要があります。

- どのAdapterが選ばれたか
- version
- capability
- config revision
- fallbackの有無
- 失敗の理由

内部のsecretは、表示しません。

---

# 93. Audit

Extension関連のEventは、Admin IdentityとPolicy Revisionに結び付けます。

- register
- activate
- config change
- upgrade
- drain
- disable
- revoke
- capability change
- fallback selection
- security failure

---

# 94. 性能の隔離

遅いExtensionが、Control Plane全体を詰まらせないように、Kindに応じて、次のものを利用できます。

- 上限のあるworker pool
- timeout
- circuit breaker
- queue
- 別のprocess

Scheduler estimatorが失敗しても、Admissionのthreadを、永久にblockしません。

---

# 95. Resourceのaccounting

Extension自身が、大量のCPU、Memory、Networkを使う場合も、Operational Budgetの対象です。

特に、次のものを、無制限のcontrol overheadにしません。

- Proofの検証
- Artifactのparse
- model inferenceを使うscheduler
- malware scan

---

# 96. Dataの最小化

Adapterに渡すInputを、最小化します。

基本は、次のとおりです。

Scheduler:

- 生のArtifactの本体を渡さない
- Clientのsecretを渡さない

Telemetry:

- 伏せ字にしたidentifier

Attestation:

- Raw Evidenceは、verifierにだけ渡す

Contribution:

- 必要な測定とwork contextだけ

Storage:

- Dataのcontentは渡すが、無関係なTaskのmetadataは渡さない

---

# 97. Untrustedな外部のservice

Remote SaaSのAdapterを使う場合、そのServiceを、EKI Control Planeの内部と同等には信頼しません。

Data Classとexport policyを適用して、必要最小限の情報だけを送ります。

Remote verification serviceに、confidentialな生のinputを送る場合は、明示的なData Grantが必要です。

---

# 98. Plugin marketplaceは、非目標

最初のEKI Coreは、次のものを提供しません。

- 公開のplugin marketplace
- 任意の第三者のpluginの自動インストール
- Clientが選んだplugin package
- 収益を分配するmarketplace

Extensionの配布は、Adminが管理します。

将来、ecosystemができても、Security modelは別に設計します。

---

# 99. Extensionの非目標

SPIは、次のものの抜け道ではありません。

- Requirementの回避
- Securityのdowngrade
- 任意のcodeの実行
- 動的なWorkerのP2P
- 制限のないnetwork
- 汎用のexactly-once
- 万能のTrust Score
- Core Currency
- Application business logicの集中

---

# 100. Architecture Invariants

1. 未知のApplicationは、原則としてClient APIとExecutorで統合し、Control Planeのpluginを要求しない。
2. Extensionは、Administrative Planeで登録して承認する。
3. Task Clientは、Extensionの実装を指定したりuploadしたりできない。
4. Adapterは、Core State Machineを所有しない。
5. Queue Adapterは、Attemptやretryのauthorityを作らない。
6. State Store Adapterは、stateのsemanticsを決めない。
7. Scheduler Adapterは、Authorizationを作らない。
8. Artifact Store Adapterは、Data Policyを独自に拡張しない。
9. Identity Adapterは、Authorizationを作らない。
10. Runtime Adapterは、Capabilityを自己拡張しない。
11. Verification Adapterは、AcceptedResultを直接作らない。
12. Attestation Adapterは、Placementを直接許可しない。
13. Effect Adapterは、retryを直接起動しない。
14. Measurement Adapterは、Contributionの価値を決めない。
15. Telemetry Adapterの失敗で、Task retryを起こさない。
16. Security-criticalなAdapterが失敗しても、弱い実装にsilentにfallbackしない。
17. Extensionのconfigは、Clientの入力から変更できない。
18. Extensionのupgradeとrollbackを、versionを持つAdminの操作として扱う。
19. Extensionのpackageを、変更できないDigestとprovenanceに結び付けられる。
20. 特権の高いAdapterに、制限のないCoreのDB、key、networkを渡さない。
21. Adapterの選択は、Core Policyが決める。
22. 未知のcriticalなSPIのcapabilityは、fail closedにする。
23. In-processであっても、Authorityの境界を消さない。
24. Goの参照実装を、言語に中立な論理的なSPIの正本にしない。
25. Adapterが変わった後も、Task、Attempt、Lease、ArtifactGrant、Resultのsemanticsを維持する。

---

# 101. 最初のReference Adapterの候補

ArchitectureからImplementationに進む際の、最小のReference Adapterの候補は、次のとおりです。

## MVPで必須

- State Store
- Delivery Queue
- Artifact Store
- Identity
- NativeまたはCommandのExecutor Runtime
- Scheduler baseline
- Result Verification baseline
- Telemetry

## 早い段階での任意

- WASM Runtime
- Browser Runtime
- OIDCと企業のidentity
- S3互換のstore
- Attestationのmockとadapter
- Contribution measurement

## 研究としての任意

- 実際のTEE verifier
- zkVM verifier
- learned schedulerのestimator
- attestされたGPU meter

MVPで、高度な任意のExtensionを、必須にしません。

---

# 102. 論理的なrepositoryの形への示唆

実装の構成は、後続の計画で確定します。論理的には、次の分離が自然です。

~~~text
core/
  contract
  authority
  state
  admission
  recovery

spi/
  state-store
  delivery
  artifact-store
  identity
  policy
  signer
  executor
  scheduler
  verification
  attestation
  effect
  contribution
  telemetry

adapters/
  reference implementations

protocol/
  external
  worker
  adapter
~~~

Directoryの名前自体は、まだ固定しません。

---

# 103. Architecture baselineの完成への入力

Authority、Contract、Control/Data Plane、Deployment Profile、State Model、Extension SPIがそろったため、次は、Architecture Baseline全体を、横断してレビューできます。

次の段階では、少なくとも、次のことを行います。

1. 文書の間の矛盾の確認
2. MVPのcomponentの切り出し
3. Protocolとschemaの最小の固定
4. Reference Adapterの選定
5. Security conformance suiteの計画
6. ADRにする必要がある、長期的な判断の抽出
7. Implementationのmilestoneとrepository layout

Issue #19と#23などの、残っている研究は、並行して継続します。ただし、未解決の部分を、MVPの必須に先取りしません。
