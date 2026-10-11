# 配備形態（Deployment Profiles）

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricは、10〜30台ほどの社内端末から、ブラウザを使った一時参加、複数組織の連携、Cloud Providerによる公益向けCompute Grantまでを、共通の原則で扱うことを目指します。

ただし、どの利用形態にも同じ数のService、同じPKIやDatabaseを求めると、小規模な導入には負担が大きすぎます。

この文書では、次の方針で、Deployment Profileを定義します。

- Logical Authorityは、共通に維持する。
- Process、Host、Networkの境界は、Deploymentの規模に応じて、比例的に決める。
- Security Principleを、簡略化という名目で、消さない。
- FederationやBrowser向けの機能を、小規模な構成に、無条件には要求しない。

関連する文書は、次のとおりです。

- AUTHORITY_BOUNDARIES.md
- CONTRACT_BOUNDARIES.md
- CONTROL_DATA_PLANE.md
- ../requirements/QUALITY_ATTRIBUTES.md QA-12
- ../requirements/ACCEPTANCE_SCENARIOS.md PAS-01 / PAS-08 / PAS-10 / PAS-11

---

# 2. ProfileはTrust Levelではない

Deployment Profileは、

> このProfileだから、Workerは信頼できる

という意味ではありません。

たとえば、Small Managed Organization Profileでも、侵害された社員のPCや、改変されたAgentを想定します。

Browser / Ad-hoc Profileでも、すべてのResultを無条件に捨てるわけではありません。Task側のResult Assuranceに応じて、検証します。

Profileが主に表すのは、次のものです。

- 物理的なComponentの構成
- Networkへの露出
- Identityの統合
- Worker class
- Data Classの上限
- Availabilityへの期待
- Operatorのmodel
- Administrationの境界

Assuranceは、別の軸です。

---

# 3. すべてのProfileで共通の不変条件

どのDeploymentでも、次のことを維持します。

1. Client Requestを、Workerに直接渡さない。
2. Client Plane、Worker Plane、Administrative Planeのcredentialを分ける。
3. Schedulerは、Security Authorityではない。
4. WorkOrderは、Authorityが確定したDecisionを束ねる。
5. Workerは、WorkOrderと前提のDecisionを再検証する。
6. Local Governorは、中央より厳しい上限を適用できる。
7. ArtifactGrantと、Task submissionのauthorityを分ける。
8. Content Digestと、Artifactのauthorizationを分ける。
9. ResultClaimとAcceptedResultを分ける。
10. Workerの自己申告だけで、高い保証のResultやContributionを確定しない。
11. Federation Identityと、ResourceやDataのAuthorizationを分ける。
12. Browserやアドホックな参加を、内部LANのmembershipに変換しない。
13. 任意のWorker間のP2Pを、Enterpriseの標準にしない。
14. Workerの損失、reclaim、retryを、通常の状態として扱う。
15. Security-criticalなunknownは、安全な側に評価する。
16. 未解決の研究を、Profileの暗黙の前提にしない。

---

# 4. 共通の論理Component

物理的な配置に関係なく、概念上は、次のものを識別します。

## Client-facing

- Task API
- Artifact Ingest API
- Result / Status API

## Worker-facing

- Worker EnrollmentとIdentity
- Worker Gateway
- LeaseとWorkOrderの配送
- Data Access Resolver
- TelemetryとResultClaimのingress

## Runtime Control

- Task Admission
- Task StateとRecovery
- Scheduler
- Placement Authorization
- Lease Authority
- WorkOrder Authority
- ArtifactとData Authority
- Result VerificationとAcceptance

## Administrative

- TrustとPolicyのAdministration
- Executor Admission
- Federation Administration
- Compute Grant Administration

## Data Plane

- Artifact Store Adapter
- IngestとSafe Processing
- Worker Cache
- 任意の、Site Cache
- ResultとCheckpointのStorage

一つのBinaryに、複数のComponentを載せることはできます。ただし、Logical AuthorityのIdentity、key、stateを書き込む境界は、残します。

---

# 5. Networkの原則

## 5.1 Workerは、outbound-firstである

標準のProfileでは、Workerに対する、Inboundのfirewallの開放を要求しません。

WorkerからControl PlaneやData Planeに、次のものを使って接続できる構造を、優先します。

- HTTPS
- mTLSを使えるoutboundの接続
- proxyに対応した接続
- long polling、streaming、WebSocketなどの候補

具体的なtransportは、後続の実装で決めます。

## 5.2 Worker間の直接の通信は、標準では使わない

Worker間の任意のP2P、LAN discovery、multicast、mesh overlayを、標準の要件にしません。

Managed Site Cacheなどが必要な場合は、明示的なData Planeのcomponentとして導入します。

## 5.3 Admin Planeは、別のSurfaceである

Federation root、Executor admission、Trust rootなどの、価値の高い操作を、通常のClientやWorkerのAPIと同じcredential scopeに載せません。

Small Profileで、同じProcessやportを使う場合も、route、audience、authorizationを分けます。

---

# 6. DP-0 — Development / Test Profile

## 6.1 目的

ローカルでの開発、UnitとIntegrationのTest、Protocolの試験に使います。

Productionレベルのsecurityを主張するProfileではありません。

## 6.2 最小構成の例

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

- local loopbackのtransport
- 開発用のkey
- 組み込みのdatabase
- filesystemのartifact store
- 単一のprocess
- 決定的なmockのidentity

## 6.4 簡略化してはいけないもの

Dev Profileでも、Contractの意味は、Productionと同じにします。

- TaskRequestとWorkOrderを、同じstructとして扱わない。
- Client credentialとWorker credentialを、同じroleにしない。
- Schedulerに、すべてのauthorityを持たせる近道を作らない。
- ResultClaimを、すぐにAcceptedResultにしない。
- ArtifactGrantを省略して、任意のfilesystem pathを渡さない。

Productionだけが別のSecurity Architectureになる状態を、避けます。

---

# 7. DP-1 — Small Managed Organization

## 7.1 想定

- 10〜50台ほどの、社員のPCやWorkstation
- Endpointが主な用途
- 一部にGPUを搭載
- 単一の組織
- 既存の社内Network
- 専任のPlatform Teamが大きくない
- PAS-01が中心

## 7.2 目標

専用の分散基盤を大量に導入せずに、

> 一般的な社内Systemとして説明できる、最小の構成

を成り立たせます。

## 7.3 推奨する物理構成

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

eki-controlの中に、次のものを同居させられます。

- Task Admission
- Task State
- Scheduler
- Placement Authorization
- Lease
- Data Access Resolver
- WorkOrderの組み立てと署名
- Result VerificationとAcceptance

## 7.4 同じProcessでも分けるもの

- Authorityごとのkey handle
- interface
- policyの判断
- 永続的な状態のrepository
- auditのevent
- credentialのaudience

Scheduler moduleが、WorkOrderのsigning keyを直接呼べる構造にはしません。

## 7.5 Metadata Store

専用の分散databaseを、必須にしません。

単一nodeのDBでも、開始できます。

ただし、Task、Attempt、Lease、Decisionの履歴を、永続的に保存します。Processが再起動しても、Authorityの状態があいまいにならない必要があります。

具体的なDBは、後続で比べます。

## 7.6 Artifact Store

Localのobject store、NAS上の専用のObject interface、S3互換のstoreなどを利用できます。

Workerに、NAS全体のcredentialを配りません。

## 7.7 Identity

既存のIdPや、企業のPKIに接続できることを、優先します。

EKI独自の大規模なPKIを、必須にしません。

小規模な組織では、管理されたenrollment credentialも候補です。ただし、Client、Worker、Adminのroleは分離します。

## 7.8 Network

次のことを、基本にします。

- Workerにinbound portが不要
- HTTPSと企業のproxyに対応
- LANのmulticastが不要
- 任意のendpoint同士の接続が不要
- Artifact Storeへの、範囲を限ったoutboundのaccess

## 7.9 障害への期待

eki-controlが単一nodeの場合、その障害の間は、新しいTaskの受け付けや、新しいLeaseが止まる可能性があります。

ただし、Endpointの利用者の安全は、Local Governorで維持します。

小規模なProfileの初期版では、Control PlaneのHAを必須にしません。

---

# 8. DP-2 — Enterprise Managed Fabric

## 8.1 想定

- 数百から数万のWorker
- 複数のSiteとRegion
- 管理されたEndpoint、Workstation、Serverの混在
- 企業のIdPとPKI
- SecurityとAuditの要件
- ProxyとFirewall
- Artifactの量が大きい
- Control PlaneのHAが必要

## 8.2 物理的な分離の方向

Logical Authorityを、Networkやprocessの境界で、さらに分離できます。

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

Workerの接続の終端を、価値の高いAuthorityから分離できます。

Gatewayが侵害されても、次のものまで取られないようにします。

- Executor Approval
- Data Grant Policy
- Federationの設定
- WorkOrderのsigning root

Gatewayが担うのは、主に、認証済みmessageのrelay、rate limit、接続の管理です。

## 8.4 価値の高いsigner

WorkOrder、Executor Approval、Federationなどのsigning keyは、KMSやHSMなどに置ける構造を取ります。

Scheduler、parser、公開のgateway processには、生のprivate keyを配りません。

## 8.5 Site Cache

WANの転送を減らすために、管理されたsite cacheを導入できます。

Site Cacheは、明示的なidentity、Data Class、quota、auditを持ちます。任意のWorkerのrelayとは、区別します。

## 8.6 Regionのpolicy

Dataのresidency、egress、latency、storeのlocationを、hard constraintとして、Placementの前に適用できます。

## 8.7 HA

Control PlaneのHAは、次のものと関係します。

- stateの所有
- leaderとlease
- signerの並行性
- 重複したattempt
- clockとexpiry
- queueのsemantics

そのため、Task State ModelのWork Unitで具体化します。

---

# 9. DP-3 — Browser / Ad-hoc Participation

## 9.1 想定

- Browserのtab
- 個人のPCのNative Worker
- イベントの参加者
- 強い事前審査がない
- public-interestとpublic data
- PAS-08が中心

## 9.2 最も重要な原則

参加者を、BeneficiaryやProviderの内部のNetworkに入れません。

~~~text
Internet
   │
   ▼
Public Worker Gateway
   │
   ▼
Low-assurance Worker Pool
~~~

内部のControl Planeに、直接接続させる必要はありません。

## 9.3 Public Worker Gateway

外部のWorker向けのGatewayは、次のものを担当できます。

- 短い有効期間のidentity
- enrollment
- rate limit
- sessionの管理
- workload profileの制限
- 公開用のerror surface
- abuseの検出

価値の高いAdmin APIと、同じSurfaceには置きません。

## 9.4 Taskの適格性

標準では、次のTaskを優先します。

- public
- 使い捨て
- 機微性が低い
- 短い
- stateless
- retryできる
- resultを検証できる

Confidentialなtaskを、

> BrowserだけれどTLSで安全

という理由で割り当てることはしません。

## 9.5 Executor

Browser Runtimeで実行できる、承認済みのExecutorに限定します。

任意のJavaScriptのURLを、Task Clientが指定する形にはしません。

## 9.6 Artifact

Publicや機微性の低いArtifactを、次のものから取得できます。

- CDN
- publicなobject store
- 短い有効期間で、範囲を限ったURL

内部のObject Storeのcredentialは、渡しません。

## 9.7 参加の可視性

BrowserのUIでは、次のことを、分かりやすく表示できる必要があります。

- Computeを提供している
- 現在のResourceの利用
- PauseとStop
- 対象のProjectとBeneficiary
- PrivacyとData Classの要約

hidden iframeや、通常の閲覧に偽装した参加を、標準にはしません。

## 9.8 Availability

Tabを閉じること、suspension、battery saverなどを、正常な状態として扱います。

厳しいdeadlineを持つTaskの、唯一のWorkerにはしません。

---

# 10. DP-4 — Federated Organizations

## 10.1 想定

- Company AとCompany B
- それぞれが独自のControl PlaneとIdentity Authorityを持つ
- 限定的なExecutor、Compute、Dataの協力
- PAS-10が中心

## 10.2 共有のControl Planeを前提にしない

標準のFederationでは、各Organizationが、自分のControl PlaneとTrust Domainを維持します。

~~~text
Organization A                       Organization B

A Control Plane                    B Control Plane
A Identity Authority               B Identity Authority
A Artifact Authority               B Artifact Authority
       │                                  │
       └──── explicit federation ─────────┘
~~~

一つのglobal rootに統合しません。

## 10.3 Federation Gateway

Organizationの間の通信を、論理的なFederation Gatewayに集約できます。

Gatewayが扱うものは、次のとおりです。

- 外部のidentityの検証
- trust domainのbinding
- federation policy
- grantとexportのcontract
- rate limit
- audit

Gatewayが、相手のOrganizationのすべての権限を獲得するわけではありません。

## 10.4 Taskのhandoff

Beneficiary AのTaskを、Provider BのWorkerで動かす場合、BはAのTaskRequestを、B内部のWorkOrderとして直接は信用しません。

概念上は、次のとおりです。

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

BのWorkerが、AのControl Planeの署名を、万能のAuthorityとして直接受け入れる構造にはしません。

## 10.5 Resultの返却

Result Artifactは、次のものを通して、Aに返せます。

- B側のResult Verification
- export policy
- Artifact Grant

Aは、必要に応じて、自分のResult Acceptance Policyで再評価します。

## 10.6 Data

Federation Identityが成立しただけでは、Dataをexportしません。

Dataには、別のExport Grantが必要です。

---

# 11. DP-5 — Public-interest Compute Grant

## 11.1 想定

- Cloud Provider
- 大手の企業
- 大学や公益団体
- 余剰のGPUとcompute
- 期間が限られ、preemptibleである
- PAS-11が中心

これは、Federationの一つの利用形態です。ただし、ProviderのreclaimとContributionが重要なので、独立したProfileとして整理します。

## 11.2 Compute Grant

Providerは、Grantごとに、次のものを制約できます。

- Beneficiary
- Resource Class
- 最大のcapacity
- time window
- priority
- preemptibility
- Data Class
- 許可するExecutor class
- Networkとegressのpolicy
- Contributionの測定のprofile

## 11.3 Beneficiaryは、Providerの内部LANに入らない

Beneficiary Clientは、Provider内部のWorkerに直接接続しません。

Provider側のFederationとGrantのGatewayに、Task intentを渡します。そして、Providerのローカルなcontrol planeが、Workerを選びます。

## 11.4 Reclaim

Providerの通常の需要が増えた場合、将来のcapacityを縮小または停止できます。

Reclaimは、Grantの契約で想定済みの、正常な事象です。

Taskのretryとmigrationは、mass reclaim recoveryのpolicyを通します。

## 11.5 Data

Compute GrantとData Grantは、別です。

Provider Workerは、次のものを使えます。

- Beneficiary Artifact Storeからの、範囲を限ったread
- 制御されたexportのcopy
- public dataset

Grantがあるからといって、Beneficiaryのすべてのdataを読めるわけではありません。

## 11.6 Contribution

Providerは、Resource Measurement Evidenceを、Contribution Authorityに渡せます。

外部のCSR、ESG、mutual creditなどに、どう利用するかは、ProviderとBeneficiary側のPolicyです。

EKI Coreは、価値を決めません。

---

# 12. DP-6 — Dedicated / Server Pool

## 12.1 想定

Endpointとは異なり、次のものをWorkerとして使う構成です。

- 24時間365日動くserver
- 専用のworkstation
- 管理されたGPU server

## 12.2 違い

Availabilityへの期待を、Endpointより高くできます。

Localの利用者が戻ることによるreclaimは、少ない可能性があります。

しかし、次のものについての原則は、変えません。

- malicious Worker
- supply chain
- Resultの破損
- Artifact authority
- WorkOrderの検証

## 12.3 Role

Dedicated Poolは、次のものに利用できます。

- 管理されたanchorによる、再計算
- 高い保証のvalidator
- 安定したcache
- deadlineが厳しいtask

ただし、dedicated=trueを、自動的にtrusted=trueに変換しません。

---

# 13. Profileの組み合わせ

実際のOrganizationは、複数のProfileを組み合わせられます。

たとえば、次のとおりです。

~~~text
Company
  DP-1 managed endpoints
  +
  DP-6 dedicated GPU pool
  +
  DP-3 public volunteer pool
~~~

Taskは、Data Class、Assurance、Resourceの要件によって、適切なPoolだけを候補にします。

Profileをまたぐときも、Contractは変えません。

---

# 14. Worker ClassとDeployment Profile

Worker Classと、Deployment Profileは、別のものです。

Worker Classの例は、次のとおりです。

- endpoint
- workstation
- dedicated
- server
- browser
- federated
- cloud

Deployment Profileは、System全体の構成です。

DP-2 Enterpriseの中に、endpoint、workstation、serverが共存できます。

一つのWorker Classだけで、Profileを決めることはしません。

---

# 15. Profileごとの、Data Classの上限

Profileごとに、標準的な上限を持てます。

たとえば、次のとおりです。

| Profile | 標準的なData Classの方向 |
| --- | --- |
| DP-0 Dev | syntheticとtestが中心 |
| DP-1 Small Managed | organization policyの範囲 |
| DP-2 Enterprise | policyとassurance次第 |
| DP-3 Browser / Ad-hoc | publicと機微性の低いものが中心 |
| DP-4 Federation | 明示的なexport grantの範囲 |
| DP-5 Public-interest Grant | grantの契約の範囲 |
| DP-6 Dedicated | 管理されたpolicyの範囲 |

これは、絶対的なLevelではありません。

たとえば、DP-4でも、Confidential Computingが成り立つ場合は、機密性の高いTaskを扱える可能性があります。

最終的には、Taskのassuranceと、Evidenceで判断します。

---

# 16. Identityのdeployment

## DP-1

次のいずれかを利用できます。

- 既存のIdP
- 企業のPKI
- 管理されたenrollment

## DP-2

次のものを強化できます。

- 企業のworkload identity
- 短い有効期間のservice credential
- mTLS
- KMSに支えられたauthority

## DP-3

- 短い有効期間のbrowserやnativeのidentity
- 匿名や、仮名での参加も可能
- identityの強さを、Resultが正しいことに読み替えない

## DP-4とDP-5

次のものを要求します。

- Trust Domainのnamespace
- 明示的なfederation
- 外部のbundleとissuerのbinding
- 非推移的なtrust

---

# 17. Componentの同居のmatrix

Baselineで認める、物理的な統合の方向を示します。

| Component | DP-1 Small | DP-2 Enterprise | DP-3 Ad-hoc | DP-4 Federation | DP-5 Grant |
| --- | --- | --- | --- | --- | --- |
| Client API | controlと同居可 | Gatewayを分離可 | publicの入口を分離することを推奨 | domainごと | beneficiaryとproviderで別 |
| Worker Gateway | controlと同居可 | 分離を推奨 | public gatewayを分離 | domainごと | provider側で分離可 |
| Scheduler | control内 | 独立してscale可 | internal | domainごと | provider local |
| Placement | control内で論理的に分離 | signerとpolicyを分離可 | internal | domain local | provider local |
| WorkOrder Authority | control内でkeyを分離 | KMS/HSMでの分離を推奨 | internal | domain local | provider local |
| Artifact Authority | resolverと同居可 | Data Planeでの分離を推奨 | public low-data用に分離 | domain local | data owner local |
| Artifact Store | localまたは既存 | regional | public store/CDN | domainごと | beneficiaryかproviderが選択 |
| Admin Plane | 制限したroute | 別の管理 | publicの入口と分離 | domainごと | providerとbeneficiaryで別 |
| Federation Gateway | 不要 | 任意 | 不要 | 必要 | 必要 |
| Contribution Authority | 任意 | 任意 | projectのpolicy | domainのpolicy | provider側が有力 |

この表は、Serviceの数の必須値ではありません。

---

# 18. Credentialの露出のmatrix

## Small Managed

同じProcessの中でも、次のものを、同じSecretにまとめない方向を、標準にします。

- Client credential
- Worker credential
- Admin credential
- WorkOrderのsigning key
- Artifact Grantのsigning key

## Enterprise

価値の高いkeyは、KMSやHSMに置き、GatewayやSchedulerには直接配りません。

## Ad-hoc

Public Worker Gatewayが持つCredentialは、Admin、Executor Admission、Federation rootには使えません。

## Federation

相手のDomainのcredentialを、ローカルのadmin roleに変換しません。

---

# 19. Queue / Brokerの露出

Internalなqueueやbrokerを、Workerに直接公開しません。

WorkerがNATS、Kafka、Redisなどの内部のcredentialを持ち、任意のsubjectやtopicにpublishできる構造は、標準にはしません。

Worker Planeは、安定したAPIとprotocolの境界を通します。

あとでQueueの実装を変えても、External Worker Contractを保てるようにします。

---

# 20. Control Planeのscaling

Control Planeで、水平にscaleしやすいComponentは、次のとおりです。

- API ingress
- Worker Gateway
- Schedulerのproposalの生成
- 読み取り専用のmetadataのquery

State consistencyが重要なComponentは、次のとおりです。

- Task Stateの遷移
- Lease Authority
- Quotaの予約
- WorkOrderの発行
- Result Acceptance

この2つを区別します。

具体的なconsensusやDB transactionの方式は、State Modelのwork unitで決めます。

---

# 21. Schedulerのscaling

Schedulerは、Security Authorityではありません。そのため、複数のinstanceにscaleしやすい構造を取れます。

複数のSchedulerが異なるProposalを出しても、PlacementとLease Authorityが、最終的な整合性を取ります。

Learned estimatorを導入する場合も、Scheduler instanceの一部として、shadowで評価できます。

---

# 22. Workerのsession model

Workerは、Control Planeに対して、outboundのsessionを作ります。

候補は、次のとおりです。

- long polling
- streaming RPC
- WebSocket
- message relay

どれを選ぶかは、後続で比べます。

重要なのは、次のことです。

- inbound portが不要
- Worker identityのbinding
- sessionのexpiry
- 再接続できる
- 重複した配送に耐える
- WorkOrderを冪等に受け入れる
- Local Governorが独立している

---

# 23. Offline / 切断されたときの挙動

WorkerがControl Planeから切断されても、次のものは動作します。

- Local Governor
- 現在のresource limit
- WorkOrderのローカルなexpiry
- Sandboxの強制

新しいWorkOrderのauthorityを、自分で生成することはしません。

Leaseのexpiryの後も、実行を続けることをTask Profileが許可するかは、後続のState Modelで定義します。

安全な側では、新しい処理の開始を止めます。

---

# 24. ProxyとEnterprise Network

Enterprise Profileでは、HTTP proxyなどを利用できる必要があります。

Workerの通信を、特殊なUDPや、inboundのtunnelだけに依存させません。

Artifactの転送も、proxyとegress policyと整合できるAdapterを選べます。

低いlatencyのP2Pを必須にしない理由の一つです。

---

# 25. Firewall

標準的に必要な通信の方向は、次のとおりです。

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

Workerへの任意のInboundを、標準では要求しません。

---

# 26. DNSとdiscovery

WorkerがLANのmulticastでControl Planeを探索することを、標準にはしません。

ControlとDataのendpointは、管理された設定、Enrollmentのresponse、well-knownなendpointなどで、取得できます。

Discoveryの情報自体を、Clientに、内部のTopologyとして露出しません。

---

# 27. Upgradeとversionのずれ

大規模なdeploymentでは、Control PlaneとWorkerのversionが、一時的にずれます。

Contract Boundaryのschema versionを使って、次のものを明示します。

- 最小の対応version
- 最大の対応version
- critical feature
- optional extension

古いWorkerが、未知のsecurity-criticalなsemanticsを、silentに無視して実行することはしません。

---

# 28. Profileの移行

Small ProfileからEnterprise Profileに移行するとき、Application Contractを変えないことを、めざします。

たとえば、次のとおりです。

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

この場合でも、次のものの意味は同じです。

- TaskRequest
- WorkOrder
- ArtifactGrant
- ResultClaim

Processを分割しても、Protocolのsemanticsが変わらないようにします。

---

# 29. Availability class

Deployment Profileは、Control Planeのavailabilityの目標にも影響します。

## DP-1

Control Planeの短時間の停止を許容します。Workerの安全性は、ローカルで維持します。

## DP-2

HA、rolling upgrade、regionをまたぐ回復力を、要求できます。

## DP-3

Public Gatewayを、DDoSとabuseを前提にscaleします。

## DP-4

相手のDomainが停止したとき、Federation Taskが、queueに入るか、安全に失敗します。

## DP-5

Providerのcapacityのreclaimと、Beneficiaryの接続の喪失を、通常の事象として扱います。

具体的なSLOは、実装の段階で測定します。

---

# 30. Observabilityのdeployment

Small Profileでは、一つのTelemetry collectorに集約できます。

Enterprise Profileでは、次のものを、別のpipelineに分離できます。

- security audit
- operational metrics
- tracing
- contribution measurement

Workerの生のtelemetryを、すべてのClientに公開しません。

---

# 31. Auditのdeployment

価値の高いEventは、Profileに関係なく、残します。

- Admission
- Placement
- Lease
- Artifact Grant
- WorkOrder
- VerificationとAcceptance
- Adminのpolicyの変更
- Federation
- Contribution

Small Profileだからといって、Auditを省略するのではなく、保存先を簡素にします。

---

# 32. Authorityの保護の段階的な強化

保護を、段階的に強化できます。

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

Authorityのidentityとinterfaceは同じまま、keyの保護を強化できる設計を、優先します。

---

# 33. 公開への露出

Internetに露出し得るSurfaceは、次のとおりです。

- 必要な場合の、Client API
- Public Worker Gateway
- Federation Gateway
- publicなArtifact Data Plane

Internetに露出しない方向を標準とするSurfaceは、次のとおりです。

- root policyのadministration
- Executor Admissionのadministration
- KMSとsignerの内部
- metadata database
- 内部のqueue
- 非公開のArtifact StoreのadminのAPI

---

# 34. Profileごとのabuseへの耐性

## DP-1

侵害された内部のClientを想定します。

## DP-2

内部と外部のClient、大規模なresourceのabuseを想定します。

## DP-3

匿名のabuse、botのWorker、Sybil、DDoSを、強く想定します。

## DP-4

誤って設定された、または侵害された、partnerのdomainを想定します。

## DP-5

Beneficiaryの不正利用、Providerのreclaim、組織をまたぐaccountingの紛争を想定します。

共通のAdmission、Budget、Result verificationの原則は、変えません。

---

# 35. Profileごとのdataのlocality

## DP-1

LANとローカルなcacheを、優先しやすい。

## DP-2

Site、Region、egressのcostを考慮します。

## DP-3

public CDNやpublic storeからの、小さなArtifactを優先します。

## DP-4

Domainをまたぐegressを、hard policyとcostとして扱います。

## DP-5

Beneficiary Data StoreとProvider Compute Regionの距離が大きい場合、Compute Grantの価値が、Network costで相殺され得ます。そのため、Placementの前に評価します。

---

# 36. Profileごとの機密性

Profileだけで、機密性を決めません。

ただし、標準のdefaultは異なります。

- DP-1: 管理されたendpointのpolicy
- DP-2: 管理されたもの + 任意のconfidential execution
- DP-3: publicで使い捨て
- DP-4: export policy + 遠隔でのassurance
- DP-5: grant + data ownerのpolicy
- DP-6: 管理されたserverのpolicy

高い保証のClaimには、Evidenceが必要です。

---

# 37. Profileごとの、Result Assurance

DP-3の匿名のWorkerでは、Replication、Validator、Proofなどが重要です。

DP-6の、管理されたanchorを、Verificationに利用できます。

DP-1やDP-2でも、Worker identityだけで、ResultをAcceptedにはしません。

TaskごとにResult Assuranceを指定します。

---

# 38. Profileごとの、Contribution

Contribution Accountingは、任意です。

## DP-1

内部のcostの配賦など。

## DP-3

Volunteerのcontribution record。

## DP-4

組織をまたぐ相互のaccounting。

## DP-5

公益のCompute Grantのevidence。

どのProfileでも、EKI Core Currencyに変換しません。

---

# 39. Profileの選択のguide

概略は、次のとおりです。

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

複数のProfileを、同時に利用できます。

---

# 40. 最小のComponentの原則

Profileを追加することによって、すべてのDeploymentに、不要なComponentを強制しません。

たとえば、次のとおりです。

- Federationを使わないDP-1に、Federation Gatewayを必須にしない。
- Browserを使わないEnterpriseに、Public Worker Gatewayを必須にしない。
- Contributionを使わないDeploymentに、Contribution Authorityを常駐させることを必須にしない。
- TEEを使わないDeploymentに、Attestation Verifierを必須にしない。
- zkVMを使わないDeploymentに、Proof Verifierを必須にしない。

Extensionは、必要なときに追加できる構造にします。

---

# 41. Small Profileで省略できない論理的な境界

物理的なComponentを減らしても、次のものを、意味の上で統合しません。

~~~text
Task Admission
  != Scheduler Proposal
  != Placement Approval
  != Artifact Grant
  != WorkOrder
  != Result Verification
  != Result Acceptance
~~~

一つのfunction chainで実装しても、Decisionのidentity、audit、policy checkは、区別します。

---

# 42. Federation Profileで共有しないもの

標準のFederationでは、次のものを、global shared stateにしません。

- root private key
- 完全なidentity database
- 完全なArtifact catalog
- 内部のWorker topology
- 内部のqueue
- すべてのTask state
- Administratorのrole

必要なContractだけを、Domainの間で交換します。

---

# 43. Public Workerの隔離

DP-3のpublic Worker poolは、Enterpriseの内部のWorker poolとは、論理的に分けます。

同じControl Planeが管理していても、次のものを分離できます。

- 受け入れるData Class
- Executorの集合
- Artifact Store
- Worker Gatewayのscope
- network policy
- verificationの要件

Public Workerが侵害されても、内部のprivate poolへ、lateral movementしにくい構造にします。

---

# 44. Compute Grantの隔離

DP-5では、ProviderがGrant用のcapacity poolを、次のいずれかで提供できます。

- 専用
- quotaを切り出したもの
- opportunisticに共有するもの

どの場合も、Beneficiary Taskが、Providerの通常のTaskのSecurity Policyを、迂回しません。

ProviderのローカルなPlacement、Local Governor、reclaim policyが、最終的な上限を持ちます。

---

# 45. 運用の所有

Deploymentごとに、誰が何を運用するかを、明確にします。

## DP-1

Organizationのoperator。

## DP-2

Platform、Security、Endpointのteamが分担できます。

## DP-3

Fabricのoperatorと、参加者自身のLocal Governor。

## DP-4

各Trust Domainのoperator。

## DP-5

Providerのoperatorと、Beneficiaryのoperator。

Responsibilityの境界を、Federationであいまいにしません。

---

# 46. Profile固有のevidence

Worker Profileは、Placementに使うEvidenceの種類を変えられます。

たとえば、次のものです。

- 管理されたenrollment
- deviceの管理の状況
- Attestation
- browserのruntimeの制約
- Providerの契約
- Trust Domainのmembership

ただし、Profileの名前そのものを、Evidenceにはしません。

---

# 47. 移行とrollback

Architectureの上で、Componentの分離を進めるときに、Profileを一度にすべて移行する必要はありません。

たとえば、次のとおりです。

~~~text
single eki-control
  ↓
Worker Gateway分離
  ↓
Data Authority分離
  ↓
Signer/KMS分離
~~~

各段階で、External Contractを維持し、rollbackできるようにします。

---

# 48. 非目標

Deployment Profileは、次のものを定義しません。

- 特定のCloud Provider
- Kubernetesを必須にすること
- Service Meshを必須にすること
- SPIFFEを必須にすること
- NATSやKafkaを必須にすること
- Postgresを必須にすること
- S3を必須にすること
- P2P network
- VMのorchestration
- Kubernetesの置き換え

必要なPropertyを先に定義し、実装の技術は、後続で比べます。

---

# 49. Architecture Invariants

1. 小規模なProfileでも、Logical Authorityを消さない。
2. 大規模なProfileでも、Processの数だけを、Securityの根拠にしない。
3. Workerは、outbound-firstを標準とする。
4. Workerへの、広いInboundのFirewallの開放を、必須にしない。
5. 任意のWorker間のP2Pを、Enterpriseの標準にしない。
6. Public Worker GatewayとAdmin Planeを、同じAuthorityにしない。
7. 内部のQueueを、Workerに直接公開しない。
8. Federationで、global rootやglobal identity namespaceに統合しない。
9. Compute GrantとData Grantを分ける。
10. Browserでの参加を、内部LANのmembershipにしない。
11. dedicatedなserverを、自動的にtrustedなWorkerとみなさない。
12. Profileによって、ResultClaimとAcceptedResultの境界を省略しない。
13. DP-1からDP-2に移行しても、Contractのsemanticsを変えない。
14. 任意のfeatureを、すべてのProfileに必須にしない。
15. Public / Ad-hocのpoolから、Privateな内部のpoolへの、lateral movementを抑える。
16. Federation partnerが侵害されても、ローカルのAdminの侵害に自動的に拡大しない。
17. Local Governorを、Profileに関係なく維持する。
18. Profileは、Assurance Levelの代わりにはならない。

---

# 50. 次のWork Unitへの入力

次は、Task、Attempt、Lease、RecoveryのState Modelを定義します。

Deployment Profileから引き継ぐ条件は、次のとおりです。

- DP-1の単一のControl Planeでも、再起動後に回復できる。
- DP-2の複数のinstanceでも、二重のAttemptやLeaseを制御できる。
- DP-3で、Workerが消えることを、大量に扱える。
- DP-4で、Federationの切断を表現できる。
- DP-5で、mass reclaimから、上限を設けて回復できる。
- side-effectのUNKNOWNを、failedに丸めない。
- Local Governorのreclaimを、Taskの完了より優先する。
- Profileによって、同じStateの意味を変えない。

State Modelの後に、Extension SPIに進みます。
