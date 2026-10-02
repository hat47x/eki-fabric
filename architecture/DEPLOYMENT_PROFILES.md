# Deployment Profiles

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricは、10〜30台程度の社内Endpointから、Browserを含むアドホック参加、複数組織のFederation、Cloud Provider等による公益Compute Grantまで同じCore Principleで扱うことを目指します。

一方で、すべての利用形態へ同じ数のService、同じPKI、同じDatabase、同じNetwork構成を強制すると、小規模利用での導入負荷が過剰になります。

この文書では、

- Logical Authorityは共通に維持する
- Process / Host / Network境界はDeployment規模に応じて比例化する
- Security Principleを簡略化の名目で消さない
- FederationやBrowser向けの機能を小規模構成へ無条件に要求しない

という方針でDeployment Profileを定義します。

関連は次のとおりです。

- AUTHORITY_BOUNDARIES.md
- CONTRACT_BOUNDARIES.md
- CONTROL_DATA_PLANE.md
- ../requirements/QUALITY_ATTRIBUTES.md QA-12
- ../requirements/ACCEPTANCE_SCENARIOS.md PAS-01 / PAS-08 / PAS-10 / PAS-11

---

# 2. ProfileはTrust Levelではない

Deployment Profileは、

> このProfileだからWorkerは信頼できる

という意味ではありません。

たとえばSmall Managed Organization Profileでも、侵害された社員PCや改変Agentを想定します。

Browser / Ad-hoc Profileでも、すべてのResultを無条件に捨てるわけではなく、Task側のResult Assuranceに応じて検証します。

Profileは主に、

- 物理Component構成
- Network exposure
- Identity integration
- Worker class
- Data Class上限
- Availability expectation
- Operator model
- Administration boundary

を表します。

Assuranceは別軸です。

---

# 3. 全Profileで共通の不変条件

どのDeploymentでも次を維持します。

1. Client RequestをWorkerへ直接渡さない。
2. Client Plane / Worker Plane / Administrative Planeのcredentialを分ける。
3. SchedulerはSecurity Authorityではない。
4. WorkOrderはAuthorityが確定したDecisionを束ねる。
5. WorkerはWorkOrderと前提Decisionを再検証する。
6. Local Governorは中央より厳しい上限を適用できる。
7. ArtifactGrantとTask submission authorityを分ける。
8. Content DigestとArtifact authorizationを分ける。
9. ResultClaimとAcceptedResultを分ける。
10. Worker self-reportだけで高保証Result / Contributionを確定しない。
11. Federation IdentityとResource / Data Authorizationを分ける。
12. Browser / ad-hoc参加を内部LAN membershipへ変換しない。
13. arbitrary Worker-to-Worker P2PをEnterprise標準にしない。
14. Worker loss / reclaim / retryを通常状態として扱う。
15. Security-critical unknownは安全側へ評価する。
16. 未解決研究をProfileの暗黙前提にしない。

---

# 4. 共通Logical Component

物理配置に関係なく、概念上は次を識別します。

## Client-facing

- Task API
- Artifact Ingest API
- Result / Status API

## Worker-facing

- Worker Enrollment / Identity
- Worker Gateway
- Lease / WorkOrder delivery
- Data Access Resolver
- Telemetry / ResultClaim ingress

## Runtime Control

- Task Admission
- Task State / Recovery
- Scheduler
- Placement Authorization
- Lease Authority
- WorkOrder Authority
- Artifact / Data Authority
- Result Verification / Acceptance

## Administrative

- Trust / Policy Administration
- Executor Admission
- Federation Administration
- Compute Grant Administration

## Data Plane

- Artifact Store Adapter
- Ingest / Safe Processing
- Worker Cache
- optional Site Cache
- Result / Checkpoint Storage

一つのBinaryに複数Componentを載せることはできますが、Logical AuthorityのIdentity / key / state write boundaryは残します。

---

# 5. Network原則

## 5.1 Workerはoutbound-first

標準ProfileではWorkerへのInbound Firewall開放を要求しません。

WorkerからControl / Data Planeへ、

- HTTPS
- mTLS可能なoutbound connection
- proxy compatible connection
- long polling / streaming / WebSocket等の候補

で接続できる構造を優先します。

具体transportは後続実装で決めます。

## 5.2 Worker間直接通信は標準では使わない

Worker間の任意P2P、LAN discovery、multicast、mesh overlayを標準要件にしません。

Managed Site Cache等が必要なら、明示的なData Plane Componentとして導入します。

## 5.3 Admin Planeは別Surface

Federation root、Executor admission、Trust root等の高価値操作は、通常Client / Worker APIと同じcredential scopeへ載せません。

Small Profileで同じProcess / portを使う場合も、route / audience / authorizationを分けます。

---

# 6. DP-0 — Development / Test Profile

## 6.1 目的

Local development、Unit / Integration Test、Protocol試験に使います。

Production Securityを主張するProfileではありません。

## 6.2 最小構成例

~~~text
developer machine

eki-control
  client API
  worker API
  logical authorities
  scheduler
  verifier

embedded / local metadata store

local artifact store

local worker agent
~~~

## 6.3 許容する簡略化

- local loopback transport
- development key
- embedded database
- filesystem artifact store
- single process
- deterministic mock identity

## 6.4 簡略化してはいけないもの

Dev ProfileでもContract意味はProductionと同じにします。

- TaskRequestとWorkOrderを同一struct扱いしない
- Client credentialとWorker credentialを同一roleにしない
- Schedulerへall-authority shortcutを作らない
- ResultClaimを即AcceptedResultにしない
- ArtifactGrantを省略してarbitrary filesystem pathを渡さない

Productionだけ別Security Architectureになる状態を避けます。

---

# 7. DP-1 — Small Managed Organization

## 7.1 想定

- 10〜50台程度の社員PC / Workstation
- Endpointが主用途
- 一部GPU搭載
- 単一組織
- 既存社内Network
- 専任Platform Teamが大きくない
- PAS-01中心

## 7.2 目標

専用分散基盤を大量導入せず、

> 一般的な社内Systemとして説明できる最小構成

を成立させます。

## 7.3 推奨物理構成

~~~text
             existing IdP / PKI
                    │
                    ▼
             +-------------+
Client ─────►| eki-control |
             |             |
Worker ─────►|             |
             +-------------+
               │       │
               │       └──── Artifact Store
               │
               └──────────── Metadata Store

Endpoint Agents
  outbound only
  local governor
  verified cache
~~~

eki-controlの中に、

- Task Admission
- Task State
- Scheduler
- Placement Authorization
- Lease
- Data Access Resolver
- WorkOrder assembly / signing
- Result Verification / Acceptance

を同居させることができます。

## 7.4 同一Processでも分けるもの

- Authority-specific key handle
- interface
- policy decision
- persistent state repository
- audit event
- credential audience

Scheduler moduleがWorkOrder signing keyを直接呼べる構造にはしません。

## 7.5 Metadata Store

専用distributed databaseを必須にしません。

単一node DBでも開始できます。

ただしTask / Attempt / Lease / Decision historyをdurableに保存し、Process restartでAuthority stateが曖昧にならない必要があります。

具体DBは後続比較です。

## 7.6 Artifact Store

Local object store、NAS上の専用Object interface、S3-compatible store等を利用できます。

WorkerへNAS全体のcredentialを配りません。

## 7.7 Identity

既存IdP / Enterprise PKIへ接続できることを優先します。

EKI独自の大規模PKIを必須にしません。

小規模組織ではmanaged enrollment credentialも候補ですが、Client / Worker / Admin roleは分離します。

## 7.8 Network

- Worker inbound port不要
- HTTPS / enterprise proxy対応
- LAN multicast不要
- arbitrary endpoint-to-endpoint connection不要
- Artifact Storeへのscopeされたoutbound access

を基本にします。

## 7.9 Failure expectation

eki-control単一node障害中は新規Task受付や新規Leaseが止まる可能性があります。

ただしEndpoint user safetyはLocal Governorで維持します。

小規模Profileの初期版でControl Plane HAを必須にしません。

---

# 8. DP-2 — Enterprise Managed Fabric

## 8.1 想定

- 数百〜数万Worker
- 複数Site / Region
- managed Endpoint、Workstation、Server混在
- Enterprise IdP / PKI
- Security / Audit要件
- Proxy / Firewall
- Artifact量が大きい
- Control Plane HAが必要

## 8.2 物理分離の方向

Logical AuthorityをNetwork / Process境界へさらに分離できます。

~~~text
Client API Gateway
      │
      ▼
Admission / Task State
      │
      ├──────── Scheduler
      │             │ proposal
      │             ▼
      ├──── Placement Authorization
      │
      ├──── Lease / WorkOrder
      │
      └──── Verification / Acceptance

Admin Plane
  Trust / Policy
  Executor Admission
  Federation

Data Plane
  Artifact Authority / Resolver
  Regional Stores
  Site Caches

Worker Gateways
  horizontally scalable
  outbound worker sessions
~~~

## 8.3 Worker Gateway

Worker connection terminationを、high-value Authorityと分離できます。

Gateway compromiseで、

- Executor Approval
- Data Grant Policy
- Federation configuration
- WorkOrder signing root

まで取られないようにします。

Gatewayはauthenticated message relay / rate limit / connection managementを中心にします。

## 8.4 High-value signer

WorkOrder / Executor Approval / Federation等のsigning keyはKMS / HSM等へ置ける構造を取ります。

Scheduler / parser / public gateway processへraw private keyを配りません。

## 8.5 Site Cache

WAN transfer削減のためmanaged site cacheを導入できます。

Site Cacheは明示的identity / Data Class / quota / auditを持ち、任意Worker relayとは区別します。

## 8.6 Regional policy

Data residency、egress、latency、store locationをhard constraintとしてPlacement前に適用できます。

## 8.7 HA

Control Plane HAは、

- state ownership
- leader / lease
- signer concurrency
- duplicate attempt
- clock / expiry
- queue semantics

と関係するため、Task State ModelのWork Unitで具体化します。

---

# 9. DP-3 — Browser / Ad-hoc Participation

## 9.1 想定

- Browser tab
- 個人PC Native Worker
- イベント参加者
- 強い事前審査なし
- public-interest / public data
- PAS-08中心

## 9.2 最も重要な原則

参加者をBeneficiary / Providerの内部Networkへ入れません。

~~~text
Internet
   │
   ▼
Public Worker Gateway
   │
   ▼
Low-assurance Worker Pool
~~~

内部Control Planeへ直接接続させる必要はありません。

## 9.3 Public Worker Gateway

外部Worker向けGatewayは、

- short-lived identity
- enrollment
- rate limit
- session management
- workload profile restriction
- public error surface
- abuse detection

を担当できます。

High-value Admin APIと同じSurfaceへ置きません。

## 9.4 Task eligibility

標準では、

- public
- disposable
- low-sensitivity
- short-lived
- stateless
- retryable
- result verifiable

なTaskを優先します。

Confidential Taskを、

> BrowserだからTLSで安全

という理由で割り当てません。

## 9.5 Executor

Browser Runtimeで実行可能なapproved Executorへ限定します。

任意JavaScript URLをTask Clientが指定する形にはしません。

## 9.6 Artifact

Public / low-sensitivity Artifactを、

- CDN
- public object store
- short-lived scoped URL

等から取得できます。

内部Object Store credentialは渡しません。

## 9.7 Participation visibility

Browser UIでは、

- Compute提供中
- 現在のResource利用
- Pause / Stop
- 対象Project / Beneficiary
- Privacy / Data Class summary

を分かりやすく表示できる必要があります。

hidden iframeや通常閲覧に偽装した参加を標準にしません。

## 9.8 Availability

Tab close、suspension、battery saver等を正常系として扱います。

厳しいdeadlineの唯一Workerにしません。

---

# 10. DP-4 — Federated Organizations

## 10.1 想定

- Company A / Company B
- それぞれ独自Control Plane / Identity Authority
- 限定的Executor / Compute / Data協力
- PAS-10中心

## 10.2 Shared Control Planeを前提にしない

標準Federationでは、各Organizationが自分のControl PlaneとTrust Domainを維持します。

~~~text
Organization A                       Organization B

A Control Plane                    B Control Plane
A Identity Authority               B Identity Authority
A Artifact Authority               B Artifact Authority
       │                                  │
       └──── explicit federation ─────────┘
~~~

一つのglobal rootへ統合しません。

## 10.3 Federation Gateway

Organization間通信をlogical Federation Gatewayへ集約できます。

Gatewayは、

- foreign identity validation
- trust-domain binding
- federation policy
- grant / export contract
- rate limit
- audit

を扱います。

Gatewayが相手Organizationの全権限を獲得するわけではありません。

## 10.4 Task handoff

Beneficiary AのTaskをProvider BのWorkerで動かす場合、BはAのTaskRequestをB内部WorkOrderとして直接信用しません。

概念上は次のとおりです。

~~~text
A admitted intent
   │ federation contract
   ▼
B federation ingress
   │ validate A identity / grant / task profile
   ▼
B local admission / provider policy
   ▼
B scheduler / placement / lease
   ▼
B WorkOrder
~~~

BのWorkerがAのControl Plane署名を直接万能Authorityとして受け入れる構造にはしません。

## 10.5 Result return

Result Artifactは、

- B側Result Verification
- export policy
- Artifact Grant

を通してAへ返せます。

Aは必要に応じて自分のResult Acceptance Policyで再評価します。

## 10.6 Data

Federation Identity成立だけではData exportしません。

Dataは別Export Grantを必要とします。

---

# 11. DP-5 — Public-interest Compute Grant

## 11.1 想定

- Cloud Provider
- 大手企業
- 大学 / 公益団体
- 余剰GPU / compute
- 期間限定・preemptible
- PAS-11中心

これはFederationの一利用形態ですが、Provider reclaimとContributionが重要なので独立Profileとして整理します。

## 11.2 Compute Grant

ProviderはGrantごとに、

- Beneficiary
- Resource Class
- maximum capacity
- time window
- priority
- preemptibility
- Data Class
- allowed Executor class
- Network / egress policy
- Contribution measurement profile

を制約できます。

## 11.3 BeneficiaryはProvider内部LANへ入らない

Beneficiary ClientはProvider内部Workerへ直接接続しません。

Provider側Federation / Grant GatewayへTask intentを渡し、Provider local Control PlaneがWorkerを選びます。

## 11.4 Reclaim

Providerの通常需要が増えた場合、future capacityを縮小・停止できます。

ReclaimはGrant契約で想定済みの正常事象です。

Task retry / migrationはmass reclaim recovery policyを通します。

## 11.5 Data

Compute GrantとData Grantは別です。

Provider Workerは、

- Beneficiary Artifact Storeからscopeされたread
- controlled export copy
- public dataset

等を使えます。

GrantがあるからBeneficiary全Dataを読めるわけではありません。

## 11.6 Contribution

ProviderはResource Measurement EvidenceをContribution Authorityへ渡せます。

外部のCSR / ESG / mutual credit等へどう利用するかはProvider / Beneficiary側Policyです。

EKI Coreは価値を決めません。

---

# 12. DP-6 — Dedicated / Server Pool

## 12.1 想定

Endpointと異なり、

- 24/7 server
- dedicated workstation
- managed GPU server

をWorkerとして使う構成です。

## 12.2 違い

Availability expectationをEndpointより高くできます。

Local user returnによるreclaimは少ない可能性があります。

しかし、

- malicious Worker
- supply chain
- Result corruption
- Artifact authority
- WorkOrder validation

の原則は変えません。

## 12.3 Role

Dedicated Poolは、

- managed anchor recomputation
- high-assurance validator
- stable cache
- deadline-sensitive task

へ利用できます。

ただしdedicated=trueを自動的にtrusted=trueへ変換しません。

---

# 13. Profile Composition

実際のOrganizationは複数Profileを組み合わせられます。

例は次のとおりです。

~~~text
Company
  DP-1 managed endpoints
  +
  DP-6 dedicated GPU pool
  +
  DP-3 public volunteer pool
~~~

TaskはData Class / Assurance / Resource requirementにより適切なPoolだけを候補にします。

Profileを跨ぐときもContractは変えません。

---

# 14. Worker ClassとDeployment Profile

Worker ClassとDeployment Profileは別です。

Worker Class例:

- endpoint
- workstation
- dedicated
- server
- browser
- federated
- cloud

Deployment ProfileはSystem全体の構成です。

DP-2 Enterprise内にendpoint / workstation / serverが共存できます。

一つのWorker ClassだけでProfileを決めません。

---

# 15. Data ClassのProfile上限

Profileごとに標準的な上限を持てます。

例は次のとおりです。

| Profile | 標準的なData Class方向 |
| --- | --- |
| DP-0 Dev | synthetic / test中心 |
| DP-1 Small Managed | organization policy範囲 |
| DP-2 Enterprise | policy / assurance次第 |
| DP-3 Browser / Ad-hoc | public / low-sensitivity中心 |
| DP-4 Federation | explicit export grant範囲 |
| DP-5 Public-interest Grant | grant contract範囲 |
| DP-6 Dedicated | managed policy範囲 |

これは絶対Levelではありません。

たとえばDP-4でもConfidential Computingが成立する場合は高機密Taskを扱える可能性があります。

Task AssuranceとEvidenceで最終判断します。

---

# 16. Identity deployment

## DP-1

- existing IdP
- enterprise PKI
- managed enrollment

のいずれかを利用可能。

## DP-2

- enterprise workload identity
- short-lived service credential
- mTLS
- KMS-backed authority

等を強化可能。

## DP-3

- short-lived browser / native identity
- anonymous / pseudonymous participationも可能
- identity strengthをResult correctnessへ読み替えない

## DP-4 / DP-5

- Trust Domain namespace
- explicit federation
- foreign bundle / issuer binding
- non-transitive trust

を要求します。

---

# 17. Component co-location matrix

Baselineで許容する物理統合の方向を示します。

| Component | DP-1 Small | DP-2 Enterprise | DP-3 Ad-hoc | DP-4 Federation | DP-5 Grant |
| --- | --- | --- | --- | --- | --- |
| Client API | controlと同居可 | Gateway分離可 | public入口分離推奨 | domainごと | beneficiary/provider別 |
| Worker Gateway | controlと同居可 | 分離推奨 | public gateway分離 | domainごと | provider側分離可 |
| Scheduler | control内 | 独立scale可 | internal | domainごと | provider local |
| Placement | control内logical分離 | signer/policy分離可 | internal | domain local | provider local |
| WorkOrder Authority | control内key分離 | KMS/HSM分離推奨 | internal | domain local | provider local |
| Artifact Authority | resolverと同居可 | Data Plane分離推奨 | public low-data用分離 | domain local | data owner local |
| Artifact Store | local / existing | regional | public store/CDN | domainごと | beneficiary/provider選択 |
| Admin Plane | restricted route | separate management | public入口と分離 | domainごと | provider / beneficiary別 |
| Federation Gateway | 不要 | optional | 不要 | 必要 | 必要 |
| Contribution Authority | optional | optional | project policy | domain policy | provider側有力 |

この表はService数の必須値ではありません。

---

# 18. Credential exposure matrix

## Small Managed

同じProcess内でも:

- Client credential
- Worker credential
- Admin credential
- WorkOrder signing key
- Artifact Grant signing key

を同一Secretへまとめない方向を標準にします。

## Enterprise

High-value keyはKMS/HSMへ置き、Gateway / Schedulerへ直接配りません。

## Ad-hoc

Public Worker Gatewayが持つCredentialは、Admin / Executor Admission / Federation rootへ使えません。

## Federation

相手Domain credentialをlocal admin roleへ変換しません。

---

# 19. Queue / Broker exposure

Internal Queue / BrokerをWorkerへ直接公開しません。

WorkerがNATS / Kafka / Redis等のinternal credentialを持ち、任意subject / topicへpublishする構造を標準にはしません。

Worker Planeはstable API / protocol boundaryを通します。

Queue implementationを後で変えてもExternal Worker Contractを保てるようにします。

---

# 20. Control Plane scaling

Control Planeで水平scaleしやすいComponent:

- API ingress
- Worker Gateway
- Scheduler proposal generation
- read-only metadata query

State consistencyが重要なComponent:

- Task State transition
- Lease Authority
- Quota reservation
- WorkOrder issue
- Result Acceptance

を区別します。

具体consensus / DB transaction方式はState Model work unitで決めます。

---

# 21. Scheduler scaling

SchedulerはSecurity Authorityではないため、複数instanceへscaleしやすい構造を取れます。

複数Schedulerが異なるProposalを出しても、Placement / Lease Authorityが最終整合性を取ります。

Learned estimatorを導入する場合もScheduler instanceの一部としてshadow evaluationできます。

---

# 22. Worker session model

WorkerはControl Planeへoutbound sessionを作ります。

候補は次のとおりです。

- long polling
- streaming RPC
- WebSocket
- message relay

は後続比較です。

重要なのは、

- inbound port不要
- Worker identity binding
- session expiry
- reconnect可能
- duplicate delivery耐性
- WorkOrder idempotent acceptance
- Local Governor独立

です。

---

# 23. Offline / disconnected behavior

WorkerがControl Planeから切断されても、

- Local Governor
- current resource limit
- WorkOrder local expiry
- Sandbox enforcement

は動作します。

新しいWorkOrder authorityを自分で生成しません。

Lease expiry後の継続実行をTask Profileが許可するかは後続State Modelで定義します。

安全側では新規処理開始を止めます。

---

# 24. Proxy / Enterprise Network

Enterprise ProfileではHTTP proxy等を利用できる必要があります。

Worker通信を特殊なUDP / inbound tunnelだけに依存させません。

Artifact transferもproxy / egress policyと整合できるAdapterを選べます。

Low-latency P2Pを必須にしない理由の一つです。

---

# 25. Firewall

標準的に必要な通信方向は次のとおりです。

~~~text
Client
  -> Client API

Worker
  -> Worker Gateway
  -> Data Plane

Admin
  -> Admin API

Control components
  -> Metadata Store / Queue / KMS / Data Authority
~~~

Workerへの任意Inboundを標準要求にしません。

---

# 26. DNS / discovery

WorkerがLAN multicastでControl Planeを探索することを標準にしません。

Managed configuration、Enrollment response、well-known endpoint等でControl / Data endpointを取得できます。

Discovery情報自体をClientへ内部Topologyとして露出しません。

---

# 27. Upgrade / version skew

Large deploymentではControl PlaneとWorkerのversionが一時的にずれます。

Contract Boundaryのschema versionを使い、

- minimum supported version
- maximum supported version
- critical feature
- optional extension

を明示します。

Unknown security-critical semanticsを古いWorkerがsilent ignoreして実行しません。

---

# 28. Profile transition

Small ProfileからEnterprise Profileへ移行するとき、Application Contractを変えないことを目指します。

例は次のとおりです。

~~~text
DP-1
  single eki-control
      ↓ growth
DP-2
  API Gateway
  Worker Gateway
  Scheduler
  Authority services
~~~

でも、

- TaskRequest
- WorkOrder
- ArtifactGrant
- ResultClaim

の意味は同じです。

Process分割がProtocol semanticsを変えないようにします。

---

# 29. Availability class

Deployment ProfileはControl Plane availability目標にも影響します。

## DP-1

短時間のControl Plane停止を許容し、Worker安全性をlocalで維持。

## DP-2

HA / rolling upgrade / regional resilienceを要求可能。

## DP-3

Public GatewayをDDoS / abuse前提でscale。

## DP-4

相手Domain停止時にFederation Taskがqueue / fail safely。

## DP-5

Provider capacity reclaimとBeneficiary connectivity lossを通常事象として扱う。

具体SLOは実装段階で測定します。

---

# 30. Observability deployment

Small Profileでは一つのTelemetry collectorへ集約できます。

Enterprise Profileでは、

- security audit
- operational metrics
- tracing
- contribution measurement

を別pipelineへ分離できます。

Raw Worker telemetryを全Clientへ公開しません。

---

# 31. Audit deployment

High-value EventはProfileに関係なく残します。

- Admission
- Placement
- Lease
- Artifact Grant
- WorkOrder
- Verification / Acceptance
- Admin policy change
- Federation
- Contribution

Small ProfileだからAuditを省略するのではなく、保存先を簡素化します。

---

# 32. Upgrade of authority protection

段階的にProtectionを強化できます。

~~~text
Development key
    ↓
local protected key
    ↓
OS keystore
    ↓
KMS
    ↓
HSM / multi-party approval
~~~

Authority identity / interfaceは同じまま、key protectionを強化できる設計を優先します。

---

# 33. Public exposure

Internetへ露出し得るSurface:

- Client API if required
- Public Worker Gateway
- Federation Gateway
- public Artifact Data Plane

Internetへ露出しない方向を標準とするSurface:

- root policy administration
- Executor Admission administration
- KMS / signer internals
- metadata database
- internal queue
- private Artifact Store admin API

---

# 34. Abuse resistance by profile

## DP-1

Compromised internal Clientを想定。

## DP-2

Internal + external Client、large-scale resource abuseを想定。

## DP-3

anonymous abuse、bot Worker、Sybil、DDoSを強く想定。

## DP-4

misconfigured / compromised partner domainを想定。

## DP-5

Beneficiary misuse、Provider reclaim、cross-org accounting disputeを想定。

共通Admission / Budget / Result verification principleは変えません。

---

# 35. Data locality by profile

## DP-1

LAN / local cacheを優先しやすい。

## DP-2

Site / Region / egress costを考慮。

## DP-3

public CDN / public storeからのsmall Artifactを優先。

## DP-4

cross-domain egressをhard policy / costとして扱う。

## DP-5

Beneficiary Data StoreとProvider Compute Regionの距離が大きい場合、Compute Grantの価値がNetwork costで相殺され得るため、Placement前に評価します。

---

# 36. Confidentiality by profile

Profileだけで機密性を決めません。

ただし標準defaultは異なります。

- DP-1: managed endpoint policy
- DP-2: managed + optional confidential execution
- DP-3: public / disposable
- DP-4: export policy + remote assurance
- DP-5: grant + data owner policy
- DP-6: managed server policy

高保証ClaimにはEvidenceが必要です。

---

# 37. Result Assurance by profile

DP-3のanonymous WorkerではReplication / Validator / Proof等が重要です。

DP-6のmanaged anchorをVerificationに利用できます。

DP-1 / DP-2でもWorker identityだけでResultをAcceptedにしません。

TaskごとにResult Assuranceを指定します。

---

# 38. Contribution by profile

Contribution Accountingはoptionalです。

## DP-1

内部cost allocation等。

## DP-3

Volunteer contribution record。

## DP-4

cross-organization mutual accounting。

## DP-5

公益Compute Grant evidence。

どのProfileでもEKI Core Currencyへ変換しません。

---

# 39. Profile selection guide

概略は次のとおりです。

~~~text
単一組織・数十台
  -> DP-1

大規模・複数Site・HA
  -> DP-2

一般参加者 / Browser
  -> DP-3 overlay

複数Organization間
  -> DP-4

Providerが公益Resourceを提供
  -> DP-5

安定Server / GPU pool
  -> DP-6 overlay
~~~

複数Profileを同時利用できます。

---

# 40. Minimum Component Principle

Profile追加によって、すべてのDeploymentに不要Componentを強制しません。

例は次のとおりです。

- Federationを使わないDP-1にFederation Gatewayを必須にしない。
- Browserを使わないEnterpriseにPublic Worker Gatewayを必須にしない。
- Contributionを使わないDeploymentにContribution Authority常駐を必須にしない。
- TEEを使わないDeploymentにAttestation Verifierを必須にしない。
- zkVMを使わないDeploymentにProof Verifierを必須にしない。

Extensionは必要時に追加できる構造にします。

---

# 41. Small Profileで省略できないLogical Boundary

物理Componentを減らしても、次を意味上統合しません。

~~~text
Task Admission
  != Scheduler Proposal
  != Placement Approval
  != Artifact Grant
  != WorkOrder
  != Result Verification
  != Result Acceptance
~~~

一つのfunction chainで実装しても、Decision identity / audit / policy checkを区別します。

---

# 42. Federation Profileで共有しないもの

標準Federationでは次をglobal shared stateにしません。

- root private key
- full identity database
- full Artifact catalog
- internal Worker topology
- internal queue
- all Task state
- Administrator role

必要なContractだけをDomain間で交換します。

---

# 43. Public Worker isolation

DP-3 public Worker poolはEnterprise internal Worker poolとlogicalに分けます。

同じControl Planeが管理しても、

- accepted Data Class
- Executor set
- Artifact Store
- Worker Gateway scope
- network policy
- verification requirement

を分離できます。

Public Worker compromiseからinternal private poolへlateral movementしにくい構造にします。

---

# 44. Compute Grant isolation

DP-5ではProviderがGrant用capacity poolを、

- dedicated
- quota carved
- opportunistic shared

のいずれかで提供できます。

どの場合もBeneficiary TaskがProvider通常TaskのSecurity Policyを迂回しません。

Provider local Placement / Local Governor / reclaim policyが最終上限を持ちます。

---

# 45. Operational ownership

Deploymentごとに誰が何を運用するかを明確にします。

## DP-1

Organization operator。

## DP-2

Platform / Security / Endpoint teamsが分担可能。

## DP-3

Fabric operator + Participant自身のLocal Governor。

## DP-4

各Trust Domainのoperator。

## DP-5

Provider operator + Beneficiary operator。

Responsibility境界をFederationで曖昧にしません。

---

# 46. Profile-specific evidence

Worker ProfileはPlacementへ使うEvidenceの種類を変えられます。

例は次のとおりです。

- managed enrollment
- device management status
- Attestation
- browser runtime constraints
- Provider contract
- Trust Domain membership

しかしProfile nameそのものをEvidenceにしません。

---

# 47. Migration and rollback

Architecture Component分離を進める際は、Profileを一度に全面移行する必要はありません。

例は次のとおりです。

~~~text
single eki-control
  ↓
Worker Gateway分離
  ↓
Data Authority分離
  ↓
Signer/KMS分離
~~~

各段階でExternal Contractを維持し、rollback可能にします。

---

# 48. Non-goals

Deployment Profileは次を定義しません。

- 特定Cloud Provider
- Kubernetes必須化
- Service Mesh必須化
- SPIFFE必須化
- NATS / Kafka必須化
- Postgres必須化
- S3必須化
- P2P network
- VM orchestration
- Kubernetes replacement

必要なPropertyを先に定義し、実装技術は後続比較します。

---

# 49. Architecture Invariants

1. 小規模ProfileでもLogical Authorityを消さない。
2. 大規模ProfileでもProcess数だけをSecurity根拠にしない。
3. Workerはoutbound-firstを標準とする。
4. Workerへの広いInbound Firewall開放を必須にしない。
5. arbitrary Worker P2PをEnterprise標準にしない。
6. Public Worker GatewayとAdmin Planeを同じAuthorityにしない。
7. Internal QueueをWorkerへ直接公開しない。
8. Federationでglobal root / global identity namespaceへ統合しない。
9. Compute GrantとData Grantを分ける。
10. Browser参加を内部LAN membershipにしない。
11. dedicated serverを自動的にtrusted Workerとみなさない。
12. ProfileによってResultClaim / AcceptedResult境界を省略しない。
13. DP-1からDP-2へ移行してもContract semanticsを変えない。
14. optional featureを全Profileへ必須化しない。
15. Public / Ad-hoc poolからPrivate internal poolへのlateral movementを抑える。
16. Federation partner compromiseをlocal Admin compromiseへ自動拡張しない。
17. Local GovernorをProfileに関係なく維持する。
18. ProfileはAssurance Levelの代替ではない。

---

# 50. 次のWork Unitへの入力

次はTask / Attempt / Lease / Recovery State Modelを定義します。

Deployment Profileから引き継ぐ条件:

- DP-1単一Control Planeでもrestart recoveryできる。
- DP-2複数instanceでも二重Attempt / Leaseを制御できる。
- DP-3 Worker disappearanceを大量に扱える。
- DP-4 Federation disconnectを表現できる。
- DP-5 mass reclaimをbounded recoveryできる。
- side-effect UNKNOWNをfailedへ丸めない。
- Local Governor reclaimをTask完遂より優先する。
- Profileにより同じStateの意味を変えない。

State Modelの後にExtension SPIへ進みます。
