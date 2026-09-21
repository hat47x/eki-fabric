# Extension SPI Architecture

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricは、現在存在するSEI / SUI / TEIだけでなく、将来の未知のソフトウェアや異なるInfrastructureへ接続できる用途非依存のdistributed compute fabricを目指します。

そのため、

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

等を差し替え可能にします。

一方で、ExtensionがCore semanticsを変更できるようにすると、

- Scheduler pluginがAuthorizationを作る
- Queue adapterが勝手にAttemptを増やす
- Storage adapterがArtifactGrantを迂回する
- Verification pluginがResultを直接Acceptedにする
- Worker RuntimeがCapabilityを自己拡張する

といったAuthority崩壊が起きます。

この文書では、何を差し替えてよく、何をCoreの不変条件として固定するかを定義します。

関連:

- AUTHORITY_BOUNDARIES.md
- CONTRACT_BOUNDARIES.md
- CONTROL_DATA_PLANE.md
- DEPLOYMENT_PROFILES.md
- TASK_STATE_MODEL.md

---

# 2. ExtensionとApplication Integrationを分ける

最も重要な原則です。

未知のApplicationやOSSがEKIを使うために、Control Plane pluginを作る必要はありません。

通常は、

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

で接続します。

Extension SPIは、

> FabricのInfrastructure能力そのものを差し替える

場合に使います。

例:

- S3-compatible Artifact Store
- enterprise OIDC
- WASM Runtime
- TEE Verifier
- custom Result Validator
- NATS / database-backed delivery
- Cloud Meter adapter

Application semanticsをSPIへ持ち込まないことを優先します。

---

# 3. Core KernelとAdapter

Logical構造:

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

AdapterはCore Kernelから呼ばれます。

AdapterがCore State Machineを所有しません。

---

# 4. SPIが変更できないもの

Extension実装が変わっても、次を変更してはいけません。

1. TaskとAttemptのIdentity分離。
2. Retryが新Attemptを作ること。
3. Lease fencing。
4. Client RequestとWorkOrderの分離。
5. Scheduler ProposalとPlacement Approvalの分離。
6. Task submission authorityとData authorityの分離。
7. ResultClaimとAcceptedResultの分離。
8. External Effect UNKNOWN。
9. ArtifactGrant scope。
10. Content DigestとAuthorizationの分離。
11. Local Governor veto。
12. Federation namespace binding。
13. Security classification downgrade禁止。
14. Retry / fan-out / transfer budget。
15. Worker self-reportを高保証事実へ自動昇格しないこと。
16. Contribution Recordと外部価値の分離。

Adapterがこれらを変更する場合、それはSPI実装ではなくArchitecture変更です。

---

# 5. Extension Privilege Class

Extensionを一つのplugin categoryとして扱いません。

保持するCredentialや処理するDataによってPrivilegeが異なります。

## EP-0 — Observation / Optimization

例:

- Telemetry Exporter
- learned scheduling estimator
- locality estimator

原則としてSecurity Authorityを持ちません。

## EP-1 — Evidence Processing

例:

- Result Validator
- Attestation Verifier Adapter
- Artifact Provenance parser

hostile evidenceを処理するため、parser / compute isolationが重要です。

Evidenceをnormalized Resultへ変換しますが、最終Authorizationを単独では作りません。

## EP-2 — Resource Access / Enforcement

例:

- Artifact Store Adapter
- Executor Runtime
- Sandbox backend
- Secret Provider
- External Effect Adapter

Data / host resourceへ触れるためblast radiusが大きくなります。

scopeされたCredentialとCapabilityを要求します。

## EP-3 — Authority Support

例:

- KMS / Signer Adapter
- Policy Evaluator
- State Store for authoritative transition
- Federation Trust Store

Authorityそのもの、またはAuthorityの根に近い機能です。

最も厳しいconfiguration / provenance / auditを要求します。

Privilege ClassはWorker AssuranceのTrust Levelではありません。

Control Plane extensionの危険度分類です。

---

# 6. Extension RegistrationはAdministrative Plane

TaskRequestが、

> このpluginをdownloadして使ってください

と指定できる構造にしません。

ExtensionはAdministrative Planeで、

- Extension ID
- kind
- implementation version
- package / image / binary Digest
- provenance
- API version
- privilege class
- allowed configuration scope
- network requirement
- credential requirement

を登録します。

Clientはregistered extensionのlogical capabilityを利用することはできますが、実装packageを変更できません。

---

# 7. Extension Descriptor

Logical Descriptor例:

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

実際のwire schemaは後続実装で決めます。

---

# 8. SPI API VersionとImplementation Version

二つを分けます。

## SPI API Version

CoreとAdapter間のContract versionです。

例:

~~~text
eki.spi.artifact-store/v1
~~~

## Implementation Version

Adapter製品のversionです。

例:

~~~text
example-s3-adapter 2.4.1
~~~

Implementationをupgradeしても同じSPI APIを保てます。

SPI API breaking changeは明示的version migrationを必要とします。

---

# 9. Capability Negotiation

Extensionはsupportするoptional capabilityを宣言できます。

例:

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

Capability declarationは、

> 現在この実装が機能を提供できる

という情報です。

Security-sensitive capabilityを利用する場合、実測 / managed policy / Evidence等のAssurance basisを別に持てる構造にします。

自己申告だけでHARD enforcementへ昇格しません。

---

# 10. Critical Capability

CoreがTaskのSecurity Requirementを満たすため必要なCapabilityがExtensionにない場合、fallbackでRequirementを弱めません。

例:

~~~text
Task requires hard memory isolation
Runtime only supports best-effort
  -> placement / execution reject
~~~

unknown capabilityを自動的にsupportedとして扱いません。

---

# 11. Execution Mode

Extensionは複数の実行形態を許容します。

## In-process

利点:

- low latency
- small deploymentで簡単
- type-safe reference implementationを作りやすい

欠点:

- crash / memory corruption blast radius
- language制約
- dependency conflict

## Local out-of-process

例:

- sidecar
- child process
- local IPC

利点:

- process isolation
- polyglot
- restartしやすい

高Privilege / hostile input parserでは有力です。

## Remote service

例:

- enterprise IdP
- cloud object store
- managed KMS
- external verifier

Network failure / credential / latencyを明示的に扱います。

Extension Kindごとに許容Execution Modeを決められます。

---

# 12. In-processを無条件に信頼しない

AdminがinstallしたExtensionでもbug / compromiseはあり得ます。

In-process Adapterへ、

- all signing keys
- all tenant data
- unrestricted database handle
- unrestricted network

を自動付与しません。

必要最小限のHost Interfaceを渡します。

---

# 13. Host Services

AdapterがCore内部へ自由にaccessするのではなく、scopeされたHost Serviceを通します。

例:

- structured logger
- telemetry emitter
- clock
- configuration reader
- secret reference resolver
- scoped artifact access
- cancellation context

AdapterへTask State database connectionそのものを渡すことを避けます。

---

# 14. Configuration

Extension configurationはAdministrative Planeでversion管理します。

Task Clientがarbitrary configurationを直接注入しません。

Executor-specific Task Parameterは別Contractです。

Infrastructure Adapter configurationとは分けます。

## 14.1 Secret

Configuration fileへraw secretを埋め込むより、

- secret reference
- KMS key reference
- workload identity

を利用できる構造を優先します。

## 14.2 Configuration Revision

Security-sensitive Adapterは、どのConfig RevisionでDecision / Evidenceが生成されたか追跡できる余地を持ちます。

---

# 15. Activation

Extensionは概念上:

- registered
- validated
- active
- draining
- disabled
- revoked

を持てます。

Packageが存在するだけでactiveにしません。

ActivationはAdmin operationです。

---

# 16. Upgrade

Extension upgradeで、進行中Taskのsemanticsを突然変えません。

方法例:

~~~text
v1 active
v2 registered
v2 health / conformance check
new work -> v2
old work -> v1 drain
v1 disable
~~~

Security emergencyではdrainせずrevokeできます。

---

# 17. Rollback

Rollback時もold vulnerable versionを無条件に再activateしません。

Allowed version / revocation / policy revisionを確認します。

Versionが古いことと安全であることは別です。

---

# 18. No silent weaker fallback

Extension failure時に、より弱い実装へ暗黙fallbackしません。

例:

~~~text
TEE verifier unavailable
  != accept self-reported environment

KMS unavailable
  != use scheduler local dev key

private store unavailable
  != upload to public bucket
~~~

Fallbackを許す場合はPolicyで明示し、同等以上のRequirementを満たす必要があります。

---

# 19. Timeout / Resource Budget

Adapter呼出し自体をboundedにします。

- wall time
- memory
- request size
- response size
- concurrency
- retry

をKindに応じて制限します。

hostile Artifact / Evidence parserがControl Plane threadを無期限占有しないようにします。

---

# 20. Error Contract

Adapter errorはtyped categoryへnormalizationします。

概念例:

- INVALID_INPUT
- UNSUPPORTED
- DENIED
- STALE
- CONFLICT
- INTEGRITY_FAILURE
- TRANSIENT
- UNAVAILABLE
- INTERNAL

Coreはerror categoryとTask semanticsからretry等を判断します。

Adapterがretry policyを勝手に決めません。

---

# 21. Health

Extensionはhealth / readinessを報告できます。

Health=trueはSecurity Assuranceではありません。

単に、

> serviceとしてrequestを受けられる

というOperational Observationです。

---

# 22. Circuit Breaker

Remote Adapterが大量failureを起こす場合、Core側でbounded retry / circuit breakerを利用できます。

Circuit breaker openを、

> Taskが失敗した

へ直接変換しません。

別Adapterやlater retryがPolicy上可能か評価します。

---

# 23. Queue / Delivery SPI

## 役割

durable stateで確定したdelivery intentをWorker Gateway等へ配送します。

候補:

- embedded queue
- database-backed queue
- NATS / JetStream
- Kafka
- Redis系
- cloud queue

## Queue SPIがしてはいけないこと

- Task ID発行
- Attempt作成
- Lease作成
- retry authority生成
- security classification変更
- Worker選択

Queue messageはAuthorityではありません。

## Required semantics

- at-least-once deliveryを扱える
- stable message ID
- ack / retry
- bounded visibility / lease相当
- orderingを必要以上に仮定しない
- duplicate deliveryを許容
- dead-letter / poison handling
- backpressure

Exactly-once queueをCore correctness前提にしません。

---

# 24. State Store SPI

## 役割

Task / Attempt / Lease / Budget / Decisionのdurable stateを保存します。

## Required properties

少なくとも:

- durable write
- unique identity constraint
- conditional update / compare-and-swap
- transactionまたは同等のatomicity
- monotonic state revision
- read current state
- recovery scan
- idempotent decision record

が必要です。

## State Storeがしてはいけないこと

DB TTLでLease rowが消えたから、

> Task failed

と決めることはできません。

State semanticsはCoreが持ちます。

## Transactional delivery

State updateとOutbox recordを同一transactionへ含められるか、同等のdurable patternを提供できることが望ましいです。

---

# 25. Artifact Store SPI

## 役割

Artifact byte storage / transferを提供します。

## Required properties

- immutable content access
- size
- digest verification evidence
- scoped read / write
- upload finalization
- error classification
- locality metadata
- deletion / retention hook

## Optional

- range read
- multipart
- resumable
- trusted server checksum
- encryption
- replication
- site locality

## 禁止

Artifact Store Adapter自体が、

> このWorkerはこのDataを読んでよい

というPolicyを独自に決めません。

ArtifactGrant / Data Authorityからscopeを受けます。

---

# 26. Data Access Transport SPI

ArtifactGrantを、

- signed URL
- signed request
- mTLS object request
- local file descriptor
- proxy session

等へ変換します。

Transport-specific credentialのscope / TTLがGrantを越えないことを要求します。

Storage admin credentialをWorkerへ返しません。

---

# 27. Identity SPI

## 役割

Credentialを検証し、normalized Principal Contextを作ります。

候補:

- OIDC
- X.509 / enterprise PKI
- SPIFFE
- cloud workload identity
- managed enrollment

## Output

- Principal ID
- issuer / Trust Domain
- authentication method
- credential freshness
- relevant authenticated attributes
- Evidence refs

## 禁止

Identity Adapterが、

- Task authorization
- Artifact authorization
- Result correctness
- Worker assurance

を自動確定しません。

Authentication != Authorizationです。

---

# 28. Federation Trust SPI

複数Trust DomainのBundle / issuer validationを扱います。

Required:

- Trust Domain namespace binding
- explicit peer configuration
- version / rotation
- rollback prevention
- non-transitive default

foreign identityをlocal admin roleへ自動mapしません。

---

# 29. Policy Evaluator SPI

OPA / Cedar等のPolicy Engineを将来接続できるようにします。

## Role

Core Authorityが作ったcanonical inputに対してPolicy評価結果を返します。

## Important

Policy Evaluatorがsigning keyを持つ必要はありません。

~~~text
Policy Engine
  decides / explains
      ↓
Authority
  validates context
  records revision
  signs decision
~~~

と分けられます。

## Output

- allow / deny
- reason category
- effective constraints
- policy revision
- optional trace reference

Client supplied policyをそのまま評価してAuthorityにしません。

---

# 30. Signer / KMS SPI

## 役割

Authority keyを安全に使ってDecisionへcryptographic authenticityを付与します。

候補:

- OS keystore
- local protected key
- cloud KMS
- HSM

## Confused Deputy対策

generic:

~~~text
sign(arbitrary bytes)
~~~

だけをControl Plane全Componentへ公開しません。

可能な範囲で、

- Authority ID
- Decision Kind
- context namespace
- key handle
- canonical digest

へscopeします。

SchedulerがWorkOrder keyを使って任意bytesへ署名できる構造を避けます。

---

# 31. Executor Runtime SPI

## 役割

approved Executor ArtifactをSandbox / Runtime上で実行します。

候補:

- approved native
- WASM
- container
- browser
- external managed service adapter

## Input

- exact Executor Digest
- WorkOrder
- Artifact handles
- Resource limits
- Capability set
- cancellation / lease context

## Output

- execution exit observation
- Result Artifact refs
- resource telemetry
- checkpoint refs
- error

## Runtimeがしてはいけないこと

- arbitrary Artifact fetch
- Capability自己追加
- WorkOrder security field変更
- Result Acceptance
- Contribution Record発行

## Enforcement capability

Runtimeは、

- HARD
- BEST_EFFORT
- OBSERVE_ONLY
- UNAVAILABLE

等のenforcement assuranceを表現できる余地を持ちます。

Task requirementと合わないRuntimeへPlacementしません。

---

# 32. Sandbox Backend SPI

Executor RuntimeとOS-specific enforcementを分離する場合に利用します。

候補:

- Linux cgroup / namespace
- Windows Job Object
- WASM sandbox
- container runtime

Resource / Capability limitを適用します。

Sandbox Adapter failureでunbounded native executionへfallbackしません。

---

# 33. Scheduler Policy SPI

## 役割

already eligible candidate間でPlacement Proposalを生成します。

入力例:

- Task resource need
- candidate resource summary
- locality
- transfer cost
- predicted duration
- provider availability
- priority / deadline

## 出力

- ranked candidate
- score / cost
- reason
- model / algorithm version

## Authority boundary

Scheduler SPIは、

- Data Classを変更しない
- Worker Assuranceを生成しない
- ArtifactGrantを発行しない
- Leaseを発行しない
- WorkOrderへ署名しない

Learned modelもこのSPI内のoptimizationです。

---

# 34. Learned Estimator SPI

Schedulerと分けてshadow evaluationできるよう、Prediction Adapterを置くこともできます。

候補prediction:

- duration
- completion probability
- preemption risk
- transfer cost
- cache reuse

OutputはPredictionです。

Permissionではありません。

Issue #19の研究はこの境界へ接続できます。

---

# 35. Result Verification SPI

## 役割

ResultClaimについて特定methodでEvidenceを評価し、normalized VerificationResultを返します。

候補:

- deterministic equality
- application validator
- replication comparison
- managed anchor
- proof verifier

## 禁止

Verification AdapterがAcceptedResultを直接生成しません。

Result Acceptance AuthorityがTask requirementへ照合します。

---

# 36. Application Validator SPI

Application-specific semantic validationを提供できます。

例:

- image output schema validation
- scientific result tolerance
- business rule validator

Coreが業務意味を理解する必要はありません。

ValidatorのIdentity / version / provenanceをVerificationResultへ残します。

---

# 37. Cryptographic Proof Verifier SPI

zkVM等のProof Artifactをnative verifierで検証し、normalized VerificationResultへ変換します。

CoreはProof System固有wire formatへ依存しません。

Program / Input / Output / Attempt bindingを失わないことを要求します。

Issue #23のbenchmark結果で利用範囲を後から評価します。

---

# 38. Attestation Verifier SPI

Vendor固有Evidenceを評価し、normalized AttestationResultへ変換します。

Adapterが扱うもの:

- Evidence parse
- signature / endorsement
- nonce / freshness
- reference values
- environment claim

Relying PartyがPlacement / Data accessを別に判断します。

Attestation Adapter unavailable時にself-reportへfallbackしません。

---

# 39. External Effect Reconciliation SPI

外部Systemのoperation status / receiptを確認します。

Input:

- Operation ID
- Request Intent Digest
- effect adapter profile
- Task / Attempt context

Output:

- confirmed applied
- confirmed not applied
- still unknown
- authoritative receipt
- transient error

AdapterがTask retryを直接起動しません。

Task State AuthorityがUNKNOWN / retry semanticsを評価します。

---

# 40. External Effect Execution Adapter

Workerへarbitrary network accessを与えず、registered external effectを実行するAdapterを利用できます。

例:

- message send
- issue creation
- managed API call

必要:

- destination fixed by admin config
- request schema
- credential isolation
- idempotency semantics
- receipt support
- rate limit
- audit

Task ClientがAdapter endpointを自由に差し替えません。

---

# 41. Contribution Measurement SPI

Resource Measurement Evidenceを作ります。

候補:

- worker self-report
- Control Plane lease observation
- OS counter
- GPU meter
- cloud provider meter
- attested meter

Output:

- Metric
- Unit
- Quantity
- Observer
- Assurance
- Work Context
- Evidence

Contribution RecordやCreditを直接生成しません。

Contribution AuthorityがPolicyを適用します。

---

# 42. Secret Provider SPI

Task / Attempt / ExecutorへscopeされたSecretをmaterializeします。

候補:

- cloud secret manager
- enterprise vault
- local OS store

Required:

- narrow scope
- short lifetime
- no Scheduler exposure
- no worker persistent cache
- redacted logs
- revoke / rotation

SecretはArtifact locality optimization対象にしません。

---

# 43. Telemetry Exporter SPI

Core semantic event / metricを外部観測基盤へ送ります。

候補:

- OTLP
- local log
- enterprise SIEM
- metrics backend

Telemetry failureでTask retryを起こしません。

Telemetry Exporterへraw secretやunnecessary personal dataを渡さないredaction boundaryをCore側に置きます。

---

# 44. Audit Store SPI

Security-sensitive Eventをdurable保存します。

Required:

- append / durable record
- query by Task / Attempt / Decision
- retention policy
- access control

Optional:

- tamper-evident chaining
- WORM
- external SIEM export

Audit Store failure時のTask admission policyはDeployment / risk profileで決めます。

High-assurance operationではfail closedを選べます。

---

# 45. Safe Processing SPI

Artifact parser / converter / inspectorをboundedに実行します。

Inputはhostile Artifactです。

Output:

- format observation
- extracted normalized artifact
- safe-processing result
- resource usage
- error

ParserがControl Plane global filesystem / networkへ自由にaccessしないようにします。

---

# 46. Extension Dependency

Extension同士を自由にrecursive callさせるとAuthority graphが見えなくなります。

BaselineではCore orchestrationを中心にします。

~~~text
Core
  -> Policy
  -> Storage
  -> Runtime
  -> Verification
~~~

Adapter AがAdapter Bのadmin credentialを直接持つ構造を避けます。

必要なcompositionはCore Host Serviceを通します。

---

# 47. Extension Network Access

ExtensionのNetwork accessもleast privilegeにします。

例:

- OIDC Adapter -> configured IdP
- Artifact Store Adapter -> configured store
- KMS Adapter -> configured KMS
- Effect Adapter -> configured external API
- Scheduler -> 原則no arbitrary Internet

Task inputからdestinationを追加できません。

---

# 48. Extension Filesystem Access

In-process / local Extensionへglobal filesystem accessが必要とは仮定しません。

Configuration、temporary directory、specific socket等へscopeできます。

hostile parserはsandbox内へ隔離します。

---

# 49. Extension Credential

CredentialはExtension Kind / instance単位で分けられる構造を優先します。

同じcloud account keyを、

- Artifact Store
- KMS
- Federation
- Telemetry

で共有しない方向を標準にします。

---

# 50. Multi-tenant

Adapterがmulti-tenant backendへ接続する場合も、Tenant contextを明示します。

Tenant AのArtifactGrantでTenant B prefixへaccessできないことをAdapter conformanceでTestします。

Adapter cacheでもTenant / Data Class boundaryを維持します。

---

# 51. Adapter instance

同じKindのAdapterを複数登録できます。

例:

~~~text
artifact-store:
  internal-east
  internal-west
  public-dataset

identity:
  employee-oidc
  partner-spiffe
~~~

Core Policyがどのinstanceを使うか決めます。

Clientがarbitrary instance nameでPolicyを迂回しません。

---

# 52. Scheme resolution

Artifact / Identity等にscheme-like logical selectorを使う場合も、registered mappingへ限定します。

例:

~~~text
artifact://...
secret://...
~~~

をOS path / arbitrary URLへ直結しません。

---

# 53. Extension selection

Selectionは、

- Task Policy
- Data Class
- Deployment Profile
- Extension health
- required capability
- configured priority

からCoreが行います。

Extension自身が、

> 次は自分ではなくこのuntrusted endpointを使ってください

とAuthorityを委譲しません。

---

# 54. Fallback policy

Multiple Adapterがある場合のfallbackはexplicitです。

例:

~~~text
primary object store unavailable
  -> approved replica store

attestation verifier unavailable
  -> no equivalent verifier
  -> task held
~~~

Security Requirementを下げるfallbackは禁止します。

---

# 55. Retry of Adapter calls

Coreはoperation semanticsに基づいてAdapter callをretryします。

Read-only / idempotent operationはretryしやすい。

External effect等はSide-effect Contractを使います。

Adapter timeoutだけでnon-idempotent operationを再送しません。

---

# 56. Idempotency key

Adapter operationにもstable identityを渡せる余地を持ちます。

例:

- upload session
- state write
- external effect
- contribution record issue

重複Callで二重side effectを避けます。

---

# 57. Backpressure

Adapterはoverloadを明示できます。

CoreはUNAVAILABLE / BACKPRESSUREを、

- queue wait
- alternate adapter
- bounded retry
- task hold

へ変換します。

無限concurrencyで押し込みません。

---

# 58. Cancellation

CoreからAdapterへcancellation / deadline contextを渡せます。

AdapterがCancellationを無視して永遠に処理を続ける場合、process isolation / timeoutで停止できる構造を選べます。

Cancellationはexternal effect rollbackを意味しません。

---

# 59. Determinism

Scheduler estimator、parser、validator等のdeterminism requirementをKindごとに定義できます。

Security Decisionでnon-deterministic outputを使う場合、Audit可能なinput / version / seed等を残します。

---

# 60. Supply-chain

Control Plane Extensionは高Privilegeになり得ます。

そのためpackageについて:

- immutable Digest
- publisher / build provenance
- approved version
- vulnerability / revocation policy
- rollback restriction

を持てる構造を要求します。

Task Executor Admissionの知見を利用しますが、Extension installationはAdministrative Planeです。

---

# 61. Self-update禁止

Extensionが自分でInternetから新binaryをdownloadし、Admin Approvalなしにreplaceすることを標準にしません。

Upgradeはversioned Administrative operationです。

---

# 62. Reference Adapter

EKIは最低限のReference Adapterを提供できます。

目的は、

- Core contractの実証
- conformance test
- small deployment

です。

Reference Adapterが唯一の正解とはしません。

---

# 63. Queue Conformance

最低限のTest例:

- duplicate delivery
- delayed delivery
- out-of-order
- consumer restart
- ack loss
- poison message
- backpressure

Queue failureでAttemptが勝手に増えないことを確認します。

---

# 64. State Store Conformance

- conditional update conflict
- stale revision reject
- unique Task / Attempt
- Lease fencing
- crash after state commit
- restart recovery
- idempotent transition
- outbox durability

を確認します。

---

# 65. Artifact Store Conformance

- wrong digest
- truncated object
- range behavior
- scoped read / write
- upload size limit
- interrupted multipart
- tenant boundary
- expired access
- immutable content

を確認します。

---

# 66. Identity Conformance

- issuer / Trust Domain mismatch
- expired credential
- wrong audience
- role credential non-transitive
- namespace collision
- rotation / revocation

を確認します。

---

# 67. Policy Evaluator Conformance

- missing policy
- stale revision
- unknown field
- deny reason
- deterministic canonical input
- security-critical unknown fail closed

を確認します。

---

# 68. Signer Conformance

- wrong Authority ID
- wrong Decision Kind
- revoked key
- key rotation
- signature context mismatch
- arbitrary bytes signing attempt

を確認します。

---

# 69. Executor Runtime Conformance

- wrong Executor Digest
- capability widening
- filesystem deny
- network deny
- resource limit
- cancellation
- output limit
- WorkOrder expiry
- local governor reclaim

を確認します。

---

# 70. Verification Conformance

- wrong Task / Attempt
- modified output
- fake verifier identity
- stale policy
- unsupported method
- conflicting evidence
- duplicate result

を確認します。

---

# 71. Attestation Conformance

- nonce replay
- stale evidence
- wrong Worker
- wrong Trust Domain
- old policy revision
- expired result
- unsupported environment claim

を確認します。

---

# 72. Effect Reconciliation Conformance

- operation ID mismatch
- request digest mismatch
- unknown outcome
- duplicate request
- authoritative receipt
- timeout
- cancellation after dispatch

を確認します。

---

# 73. Contribution Measurement Conformance

- self-report under strong policy
- fake Observer
- assurance ceiling
- duplicate measurement
- conflicting quantity
- Task / Attempt mismatch
- unit / metric policy

を確認します。

---

# 74. Telemetry Conformance

- secret redaction
- high-cardinality bound
- exporter outage
- queue backpressure
- privacy field filtering

を確認します。

---

# 75. Cross-Adapter Conformance

個別Adapterが正しくても組み合わせで破綻する可能性があります。

例:

- Identity namespace + Artifact tenant scope
- Placement + Data Grant
- Lease + WorkOrder delivery
- Runtime + Artifact Digest
- Verification + Result Acceptance
- Effect Receipt + Task State
- Contribution + AcceptedResult

についてIntegration Testを持ちます。

---

# 76. Conformance Level

Adapterを単純なpass/failだけでなく、support capabilityを明示できます。

例:

~~~text
artifact store
  core v1: PASS
  range-read: PASS
  trusted-checksum: UNAVAILABLE

runtime
  cpu hard limit: PASS
  gpu hard limit: BEST_EFFORT
~~~

Capability不足を隠しません。

---

# 77. Extension Isolation Recommendation

Privilegeに応じた推奨:

## EP-0

in-process可。

## EP-1

hostile parserならout-of-process推奨。

## EP-2

credential / resource scopeを強く分離。Runtimeはsandbox必須。

## EP-3

KMS / separate process / restricted management path等を優先。

必須Process数として固定はしません。

---

# 78. Browser Extension

Browser WorkerにControl Plane SPIをdownloadしません。

Browser側はWorker Protocolとapproved Browser Executorだけを実装します。

Infrastructure ExtensionはServer / managed runtime側です。

---

# 79. Federation Extension

Partner-specific business logicをFederation Trust SPIへ入れすぎません。

Federation AdapterはIdentity / Trust Material validationを担当し、Resource Grant / Data ExportはCore Policyへ残します。

---

# 80. Cloud Provider Adapter

Cloud-specific:

- workload identity
- object storage
- KMS
- resource meter

をAdapterで実装できます。

Cloud ProviderのIAM modelをCore Contractそのものにしません。

---

# 81. OCI / WASM等の位置付け

OCI、WASM Component Model等は有力Adapter / packaging candidateです。

しかし:

~~~text
EKI Artifact != OCI Artifact
EKI Executor != WASM only
EKI Runtime != Container only
~~~

を維持します。

PropertyをCoreへ置き、formatはAdapterへ置きます。

---

# 82. Go reference implementation

EKIがGoでReference実装されても、SPI概念自体をGo interfaceだけに閉じません。

二層を許容します。

~~~text
Logical SPI Contract
   ├ in-process Go interface
   └ out-of-process language-neutral adapter protocol
~~~

Small DeploymentではGo Adapterを使いやすくし、Vendor / polyglot integrationではout-of-process protocolを使えます。

具体protocolは後続設計です。

---

# 83. Out-of-process Adapter Protocol

最低限:

- adapter identity
- SPI API version
- capability negotiation
- request ID
- deadline
- cancellation
- typed error
- health
- telemetry correlation

を持てる必要があります。

Control Plane private structsをwireへそのままserializeしません。

---

# 84. Extension Discovery

Filesystem scanningで見つかったbinaryを自動実行することを標準にしません。

Adminが登録・承認したExtensionだけをDiscovery対象にします。

Dynamic discoveryが必要でもallowlist / registryを通します。

---

# 85. Extension Registry

Control Plane内部に、

- Extension ID
- approved Digest
- active version
- config revision
- privilege
- capabilities
- health
- revocation

を管理するRegistryを持てる構造にします。

Client APIへfull Registryを公開しません。

---

# 86. Revocation

Extension vulnerabilityが見つかった場合:

- new use停止
- active Task impact評価
- revoke version
- rotate credential
- drain / kill
- alternate adapterへmigration

を行います。

Security-critical Adapterではimmediate stopを選べます。

---

# 87. Adapter State

Adapter固有StateとCore authoritative stateを分けます。

例:

- upload multipart session
- model cache index
- external provider token cache

はAdapter Stateです。

Task / Attempt / Lease正本をAdapter独自DBだけに置きません。

---

# 88. Adapter Cache

Adapter cacheはOptimizationです。

Cache lossでCore correctnessが失われない構造を優先します。

必要なdurable stateならState Store Contractへ昇格させます。

---

# 89. Schema ownership

Core Contract schemaはEKI Coreが所有します。

Extension-specific schemaはExtension namespaceが所有できます。

Extension schemaでCore Security fieldを上書きしません。

---

# 90. Namespaced metadata

Extension metadata例:

~~~text
extensions:
  com.example.foo/v1:
    ...
~~~

Coreが理解しないoptional metadataはAuthority Decisionに利用しません。

critical extensionは理解できなければrejectします。

---

# 91. Security review trigger

次のExtension変更は再Review対象です。

- Privilege Class上昇
- new network access
- new secret access
- new signer key
- new filesystem scope
- security-critical capability追加
- schema major version
- cross-tenant behavior変更
- fallback behavior変更

通常bugfixより強いreviewを要求できます。

---

# 92. Operational explainability

Operatorは、

- どのAdapterが選ばれたか
- version
- capability
- config revision
- fallback有無
- failure reason

をTask / Decisionと関連付けて確認できる必要があります。

内部secretは表示しません。

---

# 93. Audit

Extension関連Event:

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

をAdmin Identity / Policy Revisionへbindingします。

---

# 94. Performance isolation

Slow ExtensionがControl Plane全体を詰まらせないよう、

- bounded worker pool
- timeout
- circuit breaker
- queue
- separate process

をKindに応じて利用できます。

Scheduler estimator failureでAdmission threadを永久blockしません。

---

# 95. Resource accounting

Extension自身が大量CPU / Memory / Networkを使う場合もOperational Budget対象です。

特に:

- Proof verification
- Artifact parsing
- model inference scheduler
- malware scan

等をunbounded control overheadにしません。

---

# 96. Data minimization

Adapterへ渡すInputを最小化します。

Scheduler:

- no raw Artifact body
- no Client secret

Telemetry:

- redacted identifiers

Attestation:

- Raw Evidence only to verifier

Contribution:

- necessary measurement / work context only

Storage:

- Data content but not unrelated Task metadata

を基本にします。

---

# 97. Untrusted external service

Remote SaaS Adapterを使う場合、そのServiceをEKI Control Plane内部と同等には信頼しません。

Data Class / export policyを適用し、必要最小情報だけ送ります。

Remote verification serviceへconfidential raw inputを送る場合は明示的Data Grantが必要です。

---

# 98. Plugin marketplaceは非目標

初期EKI Coreは、

- public plugin marketplace
- arbitrary third-party auto-install
- client-selected plugin package
- revenue sharing marketplace

を提供しません。

Extension distributionはAdmin-managedです。

将来ecosystemができてもSecurity modelを別途設計します。

---

# 99. Extension non-goals

SPIは次の抜け道ではありません。

- Requirement回避
- Security downgrade
- arbitrary code execution
- dynamic Worker P2P
- unrestricted network
- generic exactly-once
- universal Trust Score
- Core Currency
- Application business logicの集中

---

# 100. Architecture Invariants

1. Unknown Applicationは原則Client API / Executorで統合し、Control Plane pluginを要求しない。
2. ExtensionはAdministrative Planeで登録・承認する。
3. Task ClientがExtension implementationを指定・uploadできない。
4. AdapterはCore State Machineを所有しない。
5. Queue AdapterはAttempt / retry authorityを作らない。
6. State Store Adapterはstate semanticsを決めない。
7. Scheduler AdapterはAuthorizationを作らない。
8. Artifact Store AdapterはData Policyを独自拡張しない。
9. Identity AdapterはAuthorizationを作らない。
10. Runtime AdapterはCapabilityを自己拡張しない。
11. Verification AdapterはAcceptedResultを直接作らない。
12. Attestation AdapterはPlacementを直接許可しない。
13. Effect Adapterはretryを直接起動しない。
14. Measurement AdapterはContribution価値を決めない。
15. Telemetry Adapter failureはTask retryを起こさない。
16. Security-critical Adapter failureで弱い実装へsilent fallbackしない。
17. Extension configはClient inputから変更できない。
18. Extension upgrade / rollbackをversioned Admin operationとして扱う。
19. Extension packageをimmutable Digest / provenanceへbindingできる。
20. High-privilege Adapterへunrestricted Core DB / key / networkを渡さない。
21. Adapter selectionはCore Policyが決める。
22. Unknown critical SPI capabilityはfail closedする。
23. In-processであってもAuthority boundaryを消さない。
24. Go reference implementationをlanguage-neutral logical SPIの正本にしない。
25. Adapter変更後もTask / Attempt / Lease / ArtifactGrant / Result semanticsを維持する。

---

# 101. 初期Reference Adapter候補

ArchitectureからImplementationへ進む際、最小Reference Adapter候補は次です。

## Required MVP

- State Store
- Delivery Queue
- Artifact Store
- Identity
- Native or Command Executor Runtime
- Scheduler baseline
- Result Verification baseline
- Telemetry

## Early optional

- WASM Runtime
- Browser Runtime
- OIDC / enterprise identity
- S3-compatible store
- Attestation mock / adapter
- Contribution measurement

## Research optional

- real TEE verifier
- zkVM verifier
- learned scheduler estimator
- attested GPU meter

MVPで高度Optional Extensionを必須にしません。

---

# 102. Logical repository shapeへの示唆

実装構成は後続計画で確定しますが、論理的には次の分離が自然です。

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

Directory名自体はまだ固定しません。

---

# 103. Architecture baseline completionへの入力

Authority、Contract、Control/Data Plane、Deployment Profile、State Model、Extension SPIが揃ったため、次はArchitecture Baseline全体を横断レビューできます。

次段階では少なくとも:

1. 文書間の矛盾確認
2. MVP component cut
3. Protocol / schemaの最小固定
4. Reference Adapter選定
5. Security conformance suite計画
6. ADR化が必要な長期判断の抽出
7. Implementation milestone / repository layout

を行います。

Issue #19 / #23等のResidual Researchは並行継続しますが、未解決部分をMVP必須へ先取りしません。
