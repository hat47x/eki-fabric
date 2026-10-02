# Control Plane / Data Plane Architecture

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricでは、Taskの受付、Policy判断、Placement、Lease、WorkOrder発行などの制御情報と、Model、Dataset、Document、Result、Checkpointなどの大きなbyte列を同じ経路へ集約しません。

この文書では、

- Control Planeが何を扱うか
- Data Planeが何を扱うか
- ArtifactのIdentityとAccess Authorityをどう分けるか
- WorkerがDataへ到達するまでに何を検証するか
- Cache / localityをSecurity Authorityにしない方法
- 大量転送やretryがNetworkを圧迫しないための境界
- small deploymentからFederationまで同じ論理構造をどう保つか

をArchitecture Baselineとして定義します。

関連は次のとおりです。

- AUTHORITY_BOUNDARIES.md
- CONTRACT_BOUNDARIES.md
- ../requirements/PRODUCT_REQUIREMENTS.md
- ../requirements/QUALITY_ATTRIBUTES.md
- ../spikes/artifact-locality-cost/README.md
- ../spikes/artifact-ingest/README.md
- ../spikes/control-plane-authority-separation/README.md

## 2. 最も重要な分離

EKIでは次を同一視しません。

~~~text
Artifact Content Identity
  そのbyte列は何か

Artifact Record
  誰の、どの分類・保持条件のArtifactか

Storage Location
  どこにbyte列があるか

Artifact Grant
  誰が、どのTask / Attemptで、何の操作をしてよいか

Transfer Credential
  特定transportで実際にbyte列へ到達するための短命Credential

Cache Locality
  近くにcopyがある可能性
~~~

Digestが一致していることは、Access Authorityを意味しません。

Storage URIを知っていることも、Access Authorityを意味しません。

Cacheに同じDigestがあることも、Security requirementを満たす根拠ではありません。

---

# 3. Planeの定義

## 3.1 Control Plane

Control Planeは小さなmetadata、Decision、Stateを扱います。

主な対象は次のとおりです。

- TaskRequest / AdmittedTask / TaskIR
- Attempt
- ResourceAdvertisement summary
- PlacementProposal / Approval
- Lease
- ExecutorApproval metadata
- Artifact metadata / Grant metadata
- WorkOrder
- Task / Attempt state
- VerificationResult / AcceptedResult metadata
- Contribution metadata
- Audit metadata
- Quota / Budget reservation
- Federation / Policy configuration

Control Planeの標準経路へ、ModelやDataset本体のGB単位byte列を流しません。

## 3.2 Data Plane

Data Planeは大きなbyte列の保存・転送・検証・cacheを扱います。

主な対象は次のとおりです。

- Client upload
- Executor Artifact bytes
- Task Input Artifact
- Result Artifact
- Checkpoint
- Proof / Evidenceのlarge object
- Safe ingest / decode対象
- Worker local cache
- managed site cache
- external object store
- application-owned storage adapter

Data PlaneもAuthorization不要の自由なbyte pipeではありません。

ArtifactGrantやOutput Binding等のControl Plane DecisionをData Plane側で強制します。

## 3.3 Administrative Plane

Storage backend登録、Artifact Store credential設定、Federation export policy、encryption key policy等はAdministrative Planeです。

通常Task requestから変更できません。

---

# 4. Control PlaneをData Proxyにしない理由

すべてのArtifactをControl Plane Service経由にすると、実装初期は単純に見えます。

しかし長期的には、

- Control PlaneのNetwork帯域が全Taskのbottleneckになる
- 大規模Model transferでTask admissionまで影響する
- retry stormがControl Planeを巻き込む
- Data breach時のblast radiusが広がる
- Federation / cloud / site-local storageの利点を失う
- cache localityを活かしにくい
- small control metadataとbulk dataのSLOが衝突する

という問題が生じます。

そのため標準構成は、

~~~text
Control Plane
  metadata / authority / state
        │
        │ ArtifactGrant / Transfer Plan
        ▼
Worker ─────────────── Data Plane Store / Cache
       bulk bytes
~~~

とします。

小規模Deploymentで同一Processが両機能を実装することは許容します。

ただしLogical API、Quota、Credential、AuditはControl / Dataで分離し、後で別Processへ移せる構造を保ちます。

---

# 5. Artifact Content DigestとArtifact Recordを分ける

## 5.1 Content Digest

Content Digestはbyte列のIdentityです。

例は次のとおりです。

~~~text
sha256:<digest>
~~~

実際のhash algorithm negotiationは後続Contractで定義します。

Content Digestは、

- transfer corruption検出
- cache verification
- immutable identity
- duplicate content認識
- Executor exact artifact binding

に利用します。

## 5.2 Artifact Record

同じbyte列でも、Security Contextは異なり得ます。

たとえば同じpublic model fileが、

- Organization Aのapproved model
- Organization Bのunreviewed upload
- Public dataset mirror

として別Recordを持つことがあります。

Artifact Recordは概念的に次を持ちます。

~~~text
Artifact Record ID
Content Digest
Owner / Tenant / Trust Domain
Data Class
Media Type
Size
Provenance refs
Safe Processing status
Retention policy
Encryption metadata
Authorized storage location refs
Created / updated metadata
Policy revision
~~~

## 5.3 同じDigestだからRecordを統合しない

Content-addressed storageを使っても、

> 同じDigestなので同じAuthorizationでよい

とはしません。

Authorization、classification、retention、provenanceはArtifact Record単位です。

物理backendがdedupするかどうかはStorage Adapterの実装詳細です。

## 5.4 Cross-tenant dedup

Cross-tenant physical dedupは、

- Artifact存在のside channel
- cache hit timing
- deletion / retentionの干渉
- classification metadataの混同

を生む可能性があります。

Baselineではcross-tenant dedupをSecurity機能として利用しません。

Storage backendが内部的にdedupする場合も、logical Record / Grant / auditはTenantごとに分離し、Clientへdedup hitを露出しないことを要求します。

---

# 6. Artifact locationはAuthorityではない

Artifact RecordはStorage Locationを参照できます。

例は次のとおりです。

~~~text
location-A
  managed object store / region-a

location-B
  site cache / office-1

location-C
  external application store
~~~

しかしSchedulerやClientへraw URI、bucket name、internal hostを広く公開しません。

Data Authority / Storage Adapterだけがtransport-specific locationを解決します。

Schedulerへ渡すのは必要に応じて、

- locality class
- site / region affinity
- estimated missing bytes
- estimated transfer cost
- cache confidence / freshness

のようなsanitized summaryです。

---

# 7. Storage Adapter

EKI Coreは特定Object Storeへ固定しません。

Storage Adapterは概念的に次を提供します。

- Put / Upload Session
- Get / Read Session
- Head / metadata
- range read capability if supported
- content digest evidence
- immutable object support
- delete / retention operation
- locality description
- optional server-side encryption
- optional multipart / resumable transfer

候補実装は次のとおりです。

- local filesystem
- HTTP object store
- S3-compatible storage
- OCI-compatible registry for suitable artifacts
- application-owned store
- managed cloud object storage

Artifact Contract自体をS3 objectやOCI manifestと同一にしません。

---

# 8. Data Access Broker / Resolver

ArtifactGrantはSecurity Decisionです。

しかし実際のtransportでは、

- mTLS request
- short-lived bearer token
- presigned URL
- signed header
- local IPC capability
- filesystem descriptor

等の違いがあります。

そこでBaselineでは、

~~~text
ArtifactGrant
      │
      ▼
Data Access Broker / Storage Adapter
      │
      ▼
transport-specific short-lived access
~~~

という変換境界を置きます。

Data Access Brokerは独立Microservice必須ではありません。

Data Authority / Store Adapter内のlogical functionとして実装できます。

## 8.1 WorkerがStorage credentialを持たない

Workerへ、

- bucket-wide credential
- account secret
- long-lived cloud key
- organization-wide filesystem credential

を渡しません。

必要Artifact / operation / expiryへscopeした短命accessへ変換します。

## 8.2 Transport credentialをWorkOrderへ永久埋込しない

WorkOrderへ長寿命のpresigned URL等を直接埋め込むと、

- log leak
- queue滞留
- replay
- cancellation後の利用

の問題が増えます。

標準形はWorkOrderがArtifactGrant ID / bindingを持ち、Workerが実行直前にWorker IdentityとGrantを提示してtransport accessを取得する構造を優先します。

小規模Deploymentでdirect tokenを埋める場合も、WorkOrder / Leaseより短いTTLとscopeを要求します。

---

# 9. Read path

WorkerがInput Artifactを取得する標準流れは次です。

~~~text
1. Worker verifies WorkOrder
2. Worker verifies ArtifactGrant binding
3. Worker asks Data Access Broker for read access
4. Broker re-checks:
     Worker ID
     Task / Attempt
     Grant
     Lease / expiry
     operation
     Artifact Record / Data Class
5. Broker resolves Storage Location
6. Worker fetches bytes through Data Plane
7. Worker verifies Content Digest
8. Only verified Artifact is exposed to Executor
~~~

## 9.1 Digest verification before execution

Executor Artifactや通常Inputは、完全性を確認する前にExecutorへ渡さないことを標準にします。

特にExecutable Artifactは、実行直前のactual bytesをDigestへ照合します。

## 9.2 Large streaming input

非常に大きなArtifactを全量downloadしてから処理できない場合があります。

その場合は後続拡張として、

- chunk manifest
- Merkle tree
- independently authenticated chunks
- range-scoped sub-artifact

等を利用できます。

Whole-object Digestしかないのに、未検証streamをSecurity上「Digest確認済み」とは表現しません。

## 9.3 Range read

Range readを許す場合、ArtifactGrantがrange accessを許可している必要があります。

Partial readしたbyte列について、whole-object Digestだけから未取得範囲まで検証済みとは扱いません。

---

# 10. Upload / Ingest path

Clientや外部SystemからのArtifact uploadはControl Plane requestとData transferを分けます。

~~~text
Client
  │ create ingest intent
  ▼
Control Plane
  │ upload authorization / limits
  ▼
Data Plane upload session
  │ bytes
  ▼
quarantine / staging
  │
  ├ content digest / size
  ├ format preflight
  ├ safe processing
  ├ provenance evaluation
  └ policy
  ▼
Artifact Record
~~~

## 10.1 Upload Intent

Control Planeは概念的に次を確定します。

- uploader Principal
- maximum compressed / raw bytes
- expected media type if any
- expected digest if known
- Data Class floor
- allowed ingest profile
- expiry
- quota reservation

## 10.2 Quarantine / staging

Upload完了直後のbyte列を、すぐapproved Task Inputとして公開しません。

少なくともContent Identity確定前、Safe Processing前、malware / parser policy前などの状態を区別できる必要があります。

一つの単純なtrusted=true fieldへ集約しません。

## 10.3 Declared digestを信用しない

Uploaderが宣言したDigestはexpectationとして利用できます。

Final Artifact Content Digestはtrusted ingest boundaryまたはStorage Adapter Evidenceで確認します。

Worker / Clientの自己申告Digestだけでverified contentに昇格しません。

---

# 11. Content digest finalization

Upload方式によって、Digest計算場所は変わり得ます。

## 11.1 Gateway streamed upload

Data Plane Gatewayがbytesを受け取る場合、streamingしながらDigestとbyte数を計測できます。

## 11.2 Direct-to-store upload

Client / Workerがobject storeへ直接uploadする場合、Control Planeをbandwidth pathから外せます。

ただしUploader自己申告だけではContent Digestを確定しません。

Storage Adapterが、

- trusted cryptographic checksum
- immutable object metadata
- finalized object size

をEvidenceとして提供できる場合はPolicyに応じて利用できます。

十分なEvidenceがないbackendでは、Finalizerが再readしてDigestを確認する必要があります。

## 11.3 Finalization state

Artifact Recordは、

~~~text
uploaded
content-identified
safe-processing-evaluated
provenance-evaluated
available-for-profile-X
~~~

のような独立facetを持てる構造にします。

単一のlinear statusだけで、Provenance / Integrity / Safe Processing / Authorizationを混同しません。

---

# 12. Safe Processing Plane

Archive展開、Document parsing、Image decode、Model inspection等はTask本体の外でもResourceを消費し、攻撃面になります。

BaselineではSafe ProcessingをData Plane内の明示的なbounded workloadとして扱います。

## 12.1 Budget

少なくとも必要に応じては次のとおりです。

- input bytes
- expanded bytes
- file count
- nesting
- CPU time
- Memory
- temp disk
- output bytes
- wall time

を制限します。

## 12.2 Parser isolation

正式実装ではParser / ConverterをControl Plane main processへ無制限に組み込みません。

候補は次のとおりです。

- child sandbox
- container
- WASM
- dedicated parser service

は後続比較です。

## 12.3 Failure containment

malicious file一つのparser crash / OOMがTask AdmissionやScheduler全体を落とす構造を標準にはしません。

---

# 13. Output path

Worker outputもuntrusted bytesです。

~~~text
Executor
  │
  ▼
Worker Output Staging
  │ size / local limit
  ▼
authorized Output Upload Session
  │
  ▼
Data Plane Store
  │ digest / size finalization
  ▼
ResultClaim references Output Artifact
  │
  ▼
Verification / Acceptance
~~~

## 13.1 Output Binding

WorkOrderはarbitrary output URLを持ちません。

Output Bindingは、

- Task
- Attempt
- Worker
- maximum output size
- target Artifact Record scope
- operation
- expiry

へbindingします。

## 13.2 Output size

Clientが巨大Resultを生成してQuotaを迂回できないよう、

- Executor local output limit
- Data Plane upload limit
- Control Plane budget reconciliation

の複数境界で制限します。

## 13.3 Result acceptance前のArtifact

UploadされたOutputが存在することと、Accepted Resultであることは別です。

Rejected / conflicting Result Artifactも監査・再検証のためretention policyに従って保持できる必要があります。

---

# 14. Checkpoint path

CheckpointはOutputと似ていますが、再実行に利用されるため追加条件があります。

Checkpoint Artifactを再利用する前に、

- Task / Executor compatibility
- Checkpoint schema / version
- Artifact Digest
- Safe Processing
- Data Class
- Producer provenance
- target Worker authorization

を確認します。

Workerがcheckpoint=trueと自己申告するだけで再実行Inputへ昇格しません。

Checkpoint再利用はoptional optimizationです。

安全に検証できないCheckpointは捨ててTaskを再実行できる構造を優先します。

---

# 15. Cache architecture

## 15.1 CacheはOptimization

Cache hitはSecurity Decisionではありません。

Schedulerはcache localityをPlacement costへ利用できますが、Workerは実byte列をDigestへ検証します。

## 15.2 Worker local cache

Worker Agent配下のcacheは、

- Content Digest
- verified size
- local path / object handle
- last verified time
- security scope
- optional encryption state

等を管理できます。

Clientからcache pathを指定できません。

## 15.3 Managed site cache

Office / site単位で共用cacheを置くことは、WAN traffic削減に有効です。

ただしenterprise defaultでは任意Worker同士をP2P relayにしません。

Managed site cacheは明示的なData Plane componentとして、

- identity
- policy
- quota
- audit
- data class

を持ちます。

## 15.4 P2P

arbitrary worker-to-worker transferは標準Enterprise Profileでは無効です。

将来、明示的なtrusted profileでpeer transferを導入する余地は残しますが、

- Grant enforcement
- peer identity
- topology leak
- cache poisoning
- traffic amplification
- NAT / firewall
- revocation

を別途解決する必要があります。

---

# 16. Cache security scope

同じDigestだから、すべてのTaskから同じcache entryを読めるとは限りません。

少なくとも次を区別できる必要があります。

- public cacheable
- tenant scoped
- organization scoped
- confidential / no-shared-cache
- ephemeral
- executor-only

Confidential Artifactをad-hoc Workerやpublic Browser cacheへ置かないことをPolicyで表現します。

## 16.1 Cache existence side channel

Clientへ、

> digest XはWorker Yにcache済み

というraw情報を返しません。

Scheduler内部でも必要以上にexact topologyを広げません。

Cache hit / miss latencyを通じたcross-tenant existence leakもQuality / Security review対象です。

---

# 17. Locality Observation

Schedulerへ渡すLocality情報はAuthorityではなくObservationです。

概念例は次のとおりです。

~~~text
Artifact Digest
Worker / Site
state = cached / likely / missing / unknown
estimated missing bytes
estimated fetch millis
confidence
observed at
source
~~~

Worker self-reportのcache claimはlow-assurance sourceとして扱います。

Managed cacheやArtifact Store telemetry等、sourceに応じてconfidenceを区別できます。

どのObservationでもArtifact access authorityは作りません。

---

# 18. Network Budget

RQ-008 / RQ-017に従い、Network transferをResource Budgetの一部として扱います。

Task / Principal / Provider / Grant単位で必要に応じて:

- input bytes
- output bytes
- retry transfer bytes
- prefetch bytes
- concurrent transfer
- egress monetary budget
- bandwidth class

を制限できる構造にします。

## 18.1 Reservation

Placement前にmissing inputとexpected outputからNetwork Budgetを予約できます。

見積より実転送が増えた場合、Data Plane側でhard limitを強制できます。

## 18.2 Retry

Retryで同じArtifactを再取得した場合も、元TaskのNetwork Budgetを消費します。

Retryによって新しい無制限Budgetを得ません。

## 18.3 Provider / site budget

業務Networkを守るため、Provider / site側にもlocal traffic limitを置けます。

Central SchedulerのTask priorityが高くても、local network policyを越えません。

---

# 19. Transfer accounting

Data Planeは少なくとも次を計測できる余地を持ちます。

- requested bytes
- actual bytes
- cache hit / miss
- source / destination class
- transfer duration
- retry / resume bytes
- output bytes
- rejected transfer
- throttle reason

Contribution用Resource MeasurementとOperational Telemetryは目的が異なるため、同じCounterでもAssurance / Observerを区別します。

---

# 20. Mass reclaim時のData Plane

大量Workerが同時離脱すると、retryだけでなくArtifact再転送もstormを起こします。

標準Recoveryでは、

1. Task retry budget
2. retry admission window
3. Artifact transfer budget
4. per-store concurrency
5. per-site bandwidth
6. cache locality
7. backoff / jitter
8. priority

を組み合わせます。

## 20.1 Duplicate transfer suppression

同じWorker / Siteへ同じDigestを多数Taskが必要とする場合、Data Planeは可能ならsingle-flight / transfer coalescingを利用できます。

Taskごとに同じ8 GiB Modelを同時downloadすることを避けます。

## 20.2 Recovery prioritization

すべてのTaskを同時に再配置せず、

- deadline
- Task priority
- transfer cost
- available cache
- Result value
- retry budget

でRecovery windowを制御します。

User / Provider reclaim自体を遅延させることはしません。

---

# 21. Prefetch

Prefetchは独立したOptimization Policyです。

通常TaskのArtifact access authorityから、

> 将来使うかもしれないので配ってよい

という権限を自動生成しません。

Prefetchには別途:

- allowed Artifact class
- destination class
- Network Budget
- Storage Budget
- expected reuse
- expiry
- eviction policy
- user / provider policy

を適用します。

Business Endpointへ巨大Modelを無差別に配布しません。

---

# 22. Data Class propagation

OutputのData ClassをWorkerの自己申告で下げません。

Baselineでは原則として、

~~~text
output class
  >= Task effective Data Class
  >= relevant input Data Class
  >= Executor output policy floor
~~~

となる安全側のpropagationを行える構造にします。

## 22.1 Declassification

generic automatic declassificationはBaselineに含めません。

匿名化、集計、変換等によってData Classを下げる場合は、

- approved declassification Executor
- explicit Policy
- validation
- audit
- dedicated Authority

等を必要とする後続拡張として扱います。

「Executorがpublic outputと言った」だけでは下げません。

---

# 23. Encryption

## 23.1 In transit

Control / Data Planeのtransport confidentiality / integrityは企業標準に適合する暗号化通信を前提とします。

具体TLS profileは後続実装で定義します。

## 23.2 At rest

Storage backendのat-rest encryptionを利用できます。

ただしat-rest encryptionの有無とArtifact content integrityは別です。

## 23.3 Application / client-side encryption

高いConfidentiality要件では、Data Planeがciphertextだけを保持し、approved execution environmentへだけkeyをreleaseする構成を将来利用できます。

これはRemote Attestation / Key Releaseと連携するoptional profileです。

Baseline managed deploymentの必須条件にはしません。

---

# 24. Secretは一般Artifact Cacheへ入れない

API key、Database password、private signing material等を通常Artifactとしてcacheしません。

Secret accessはArtifact Data Planeと似たCapability modelを使えますが、

- no persistent worker cache
- narrow Task / Attempt / Executor binding
- shorter expiry
- no contribution / locality optimization
- redacted audit

を標準とする別Secret Plane / Adapterとして扱います。

Secret bytesをSchedulerへ渡しません。

---

# 25. Data Plane credential lifetime

Transport CredentialのTTLは、原則として依存Decisionより長くしません。

~~~text
transport credential expiry
  <= ArtifactGrant expiry
  <= WorkOrder / Lease relevant lifetime
~~~

ただし大容量TransferでLeaseより長いsessionが必要な場合は、renewalを明示的に設計します。

古いdownload URLを長期間再利用できる状態を避けます。

---

# 26. Control Plane outage

Data Plane accessを毎byte Control Planeへ同期依存させると、Control Plane outageで進行中Transferまで即座に壊れます。

一方、長寿命Credentialを発行するとrevocation latencyが増えます。

BaselineではこのTrade-offを、

- short-lived access
- bounded TTL
- renewal
- high-sensitivity profileではonline mediation可能
- already delivered bytesはrevocationできない

として明示します。

Control Plane停止後も、既発行Credentialはexpiryまで有効な場合があります。

これを「即時revocation」とは表現しません。

---

# 27. CancellationとData access

Task cancellation後は、

- future ArtifactGrant issuance停止
- existing Grant revoke where supported
- transport credential renewal停止
- upload session停止
- Worker stop request

を行います。

しかし悪意Workerが既にdownloadしたDataを、

> cancellationしたので削除された

とは保証しません。

Confidential Dataはそもそも適切なWorker Assurance / environmentへしか配置しないことが基本です。

---

# 28. Artifact retention / deletion

Artifact deletionは少なくとも次を分けます。

- logical reference removal
- Artifact Record retention
- physical object deletion
- cache eviction
- external store retention

Content Digestが同じ別Recordが存在する場合、一つのRecord削除で他Recordまで消してはいけません。

## 28.1 Accepted Result

Accepted Resultが参照するArtifactは、Result retention policyと整合して保持します。

## 28.2 Unaccepted Result

Rejected / failed AttemptのOutputも、debug / audit / security investigationのため短期保持する場合があります。

## 28.3 Worker copy

Workerへ渡したbyte列の事後削除は、malicious Workerに対して完全保証できません。

---

# 29. Federation Data Plane

Identity Federationが成立しても、Data sharingは自動成立しません。

外部OrganizationへArtifactを渡すには、別途:

- Beneficiary / Principal
- Data Class
- Grant scope
- purpose / Task
- expiry
- transfer destination
- export policy

を満たす必要があります。

~~~text
Federated identity valid
  != Artifact export authorized
~~~

## 29.1 Cross-organization storage

Federationでは、

- Provider storeからBeneficiary Workerへdirect read
- neutral shared object store
- controlled export copy
- encrypted Artifact transfer

等を選択できます。

Architecture Coreは一方式へ固定しません。

## 29.2 Public-interest Compute Grant

Compute Resource GrantとData Grantを分けます。

Cloud Providerが公益団体へComputeを提供していても、そのWorkerにOrganization内部Data accessが自動で付くことはありません。

---

# 30. Browser / Ad-hoc Worker

Browser WorkerはData Plane上もlower-assurance profileとして扱います。

標準ではは次のとおりです。

- public / low-sensitivity Artifact
- short-lived Grant
- small bounded transfer
- no persistent secret
- restricted cache
- no arbitrary internal endpoint
- aggressive expiry
- explicit participation

を基本にします。

Browser storage APIにArtifactが残る可能性を考慮し、confidential Dataを標準配置しません。

---

# 31. Executor Artifact path

Executor ArtifactもData Planeを通します。

Workerはlogical Executor nameから外部registryを自由に解決しません。

標準流れは次のとおりです。

~~~text
ExecutorApproval
  │ exact Digest
  ▼
ArtifactGrant / Executor Distribution
  │
  ▼
Worker local verified cache
  │ actual digest re-check
  ▼
Sandbox execution
~~~

Mutable tagはAdmission時にDigestへresolveされ、実行時はDigestが正本です。

---

# 32. Model / Dataset reuse

AI / cognition workloadでは巨大Model / Datasetを繰り返し使う可能性があります。

EKIでは、

- model artifact identity
- model approval
- model Data Class / license metadata
- Worker cache
- locality observation
- prefetch policy

を分離します。

Modelをcacheしているという理由だけで、そのWorkerがModelを利用する権限を持つとは限りません。

License / contractual restrictionもArtifact policy extensionとして扱える余地を持ちます。

---

# 33. Data PlaneとContribution

Contribution accountingでnetwork / storage資源を扱う場合も、Operational Telemetryから自動的にCreditを生成しません。

Data Plane Observerは、

- bytes transferred
- cache served bytes
- storage time

等をMeasurement Evidenceとして提供できます。

Contribution AuthorityがAssurance / Policyを確認した後にRecord化します。

---

# 34. Audit

Data Planeの重要Event:

- ingest intent issued
- upload started / completed / rejected
- content digest finalized
- safe processing result
- Artifact Record available
- ArtifactGrant issued / denied / revoked
- transport access issued
- download completed / interrupted
- cache fill / eviction
- output finalized
- retention / deletion decision
- cross-domain export

をTask / Attempt / Artifact / Principal / Workerへ関連付けられる必要があります。

Transport Credential、presigned URL、Secret等をAudit logへ平文記録しません。

---

# 35. Observability

Data Plane metricsは少なくとも:

- transfer bytes
- transfer duration
- active transfer count
- cache hit ratio
- duplicate transfer ratio
- ingest reject count
- safe processing resource use
- output reject / limit count
- store error rate
- per-site network saturation
- reclaim recovery amplification

を取得できる余地を持ちます。

OpenTelemetry / OTLP等へexport可能にしますが、具体Telemetry backendは固定しません。

---

# 36. Failure semantics

## 36.1 Storage unavailable

Taskそのものをinvalidとはしません。

別location / cache / retry可能性をTask Stateが評価します。

## 36.2 Grant denied

Placement候補がData requirementを満たせないため、そのAttempt / Placementを再評価します。

Security requirementを下げて通しません。

## 36.3 Digest mismatch

ArtifactをExecutorへ渡しません。

Cache entryならinvalidateし、source / transfer pathをAudit対象にします。

## 36.4 Upload interrupted

Upload sessionをresume可能にするかはAdapter次第です。

Incomplete uploadをArtifact Recordとして利用可能にしません。

## 36.5 Safe processing failure

Content Identityが分かっていてもapproved inputにはなりません。

Failure reasonとResource Budgetを記録します。

## 36.6 Output budget exceeded

Executor / uploadを停止しAttempt failureとして扱います。

巨大OutputをControl Planeへ返してからrejectしません。

---

# 37. Storage location failureとArtifact identity

Artifactが一つのStoreから消えても、Artifact Identityまで消えたことにはなりません。

Artifact Recordは複数Locationを参照できます。

Replication / durability policyはArtifact ClassやDeployment Profileに応じて選択します。

EKIはpersistent source-of-truth databaseそのものではないため、Application-owned source dataのdurabilityをFabricが自動的に引き受けるとは限りません。

---

# 38. Application-owned data

すべてのDataをEKI Artifact Storeへcopyする必要はありません。

Application-owned storageを利用する場合、

- registered Storage Adapter
- explicit Artifact / object reference
- Data Authority
- scoped access
- integrity expectation
- locality
- retention owner

を定義します。

WorkerへDatabase master credentialを渡してApplication storageを自由に探索させません。

必要ならApplication側Adapterが限定query / object retrievalを提供します。

---

# 39. External Adapter

TaskがExternal APIやSystemとData交換する場合、arbitrary network accessではなくregistered External Adapterを利用できます。

External Adapterは、

- allowed destination
- request schema
- credential handling
- rate limit
- side-effect semantics
- receipt / confirmation
- audit

を持ちます。

これによりWorker sandboxへ広いInternet accessを与えずに外部連携できます。

---

# 40. Network policy

Workerのnetwork capabilityはdefault denyを基本とします。

許可類型は次のとおりです。

1. Control Plane endpoint
2. Data Plane endpoint
3. registered External Adapter
4. explicitly approved network profile

Data Plane access先もGrantから解決し、Client supplied destinationをそのままallowlistにしません。

---

# 41. Site / Region affinity

EnterpriseやFederationでは、Dataをsite / region外へ出せない場合があります。

Artifact Record / Policyは、

- allowed region
- allowed site
- allowed trust domain
- egress prohibited
- residency requirement

等を表現できる余地を持ちます。

Schedulerのlocality scoreより前にhard constraintとして適用します。

---

# 42. Cost modelへの入力

Control / Data PlaneからSchedulerへ提供するcost inputは、Authorityを持たないObservationです。

例は次のとおりです。

~~~text
missing input bytes
expected output bytes
estimated download time
estimated upload time
cache confidence
egress cost class
site network pressure
store availability
~~~

learned estimatorを利用しても、Data access / residency / Security constraintを上書きしません。

---

# 43. Small deployment

10台程度のmanaged endpoint poolでは、次のような単純構成を許容します。

~~~text
eki-control
  metadata / authority
  data-access resolver

local object store
  artifact bytes

endpoint agents
  verified local cache
~~~

Control PlaneとData Access Resolverを同一Binaryにしても構いません。

専用CDN、Kafka、Service Mesh、HSMを必須にしません。

---

# 44. Enterprise deployment

Enterpriseでは必要に応じて:

- redundant Control Plane
- dedicated object store
- site cache
- enterprise PKI / IdP
- KMS / HSM
- egress control
- outbound-only Worker
- proxy-aware transfer
- regional storage
- security monitoring

を利用します。

Workerは原則outbound connectionでControl / Data endpointへ到達できる構成を優先します。

---

# 45. Cloud / Public-interest provider

Cloud Provider等が外部Compute Grantを提供する場合、Provider側Data PlaneへBeneficiary dataを置くかどうかは別Policyです。

Computeだけを提供し、DataはBeneficiary-controlled Storeからshort-lived Grantで取得する構成も可能です。

Contribution RecordはCompute / transfer Evidenceを記録できますが、Data sharing agreementを代替しません。

---

# 46. Browser deployment

Browser参加では、

- HTTPS origin
- short-lived Worker identity
- bounded fetch
- public Artifact CDN / object store
- no internal raw topology
- no long-lived cloud secret
- tab close / suspension前提

を基本にします。

Service Worker等のbackground behaviorをstable availabilityとして扱いません。

---

# 47. 非目標

このBaselineは次を目指しません。

- distributed shared filesystem
- arbitrary Worker-to-Worker P2P network
- global coherent cache
- distributed database
- object store replacement
- CDN replacement
- database replication system
- universal secret manager
- unlimited direct Internet access
- cross-tenant dedupをSecurity propertyとして利用
- Data deletionをmalicious Workerへ完全保証

---

# 48. Architecture Invariants

Control / Data Planeで維持する不変条件をまとめます。

1. Control Plane metadata pathへbulk Artifactを標準集約しない。
2. Content DigestはAccess Authorityではない。
3. Artifact RecordのAuthorization / ClassificationをDigestだけで統合しない。
4. Storage Locationを知るだけでData access authorityを得ない。
5. ArtifactGrantはTask / Attempt / Worker / operation / expiryへscopeする。
6. Workerへlong-lived storage credentialを配らない。
7. Executorへ渡す前にArtifact Integrityを確認する。
8. Safe Processing前のArtifactをapproved inputと同一視しない。
9. Cache localityはOptimizationでありSecurity Evidenceではない。
10. Clientへinternal topology / cache inventoryを不要に露出しない。
11. Retry transferも同じResource Budgetへ計上する。
12. mass reclaimでunbounded duplicate transferを起こさない。
13. Outputはauthorized sink / size boundを持つ。
14. Output Data ClassをWorker自己申告で下げない。
15. Secretを通常Artifact cacheへ置かない。
16. Federation IdentityだけでData Export Authorityを作らない。
17. Browser / ad-hoc Workerへconfidential dataを標準配置しない。
18. cancellation後も既送信Dataの事後削除を保証しない。
19. mutable aliasを実行時Content Identityにしない。
20. arbitrary Worker-side fetchを標準動作にしない。

---

# 49. 次のWork Unitへの入力

次はDeployment ProfilesをArchitecture正本として具体化します。

この文書から引き継ぐもの。

- logical AuthorityはDeployment規模によらず維持
- Control / Data Planeはlogical separationを維持
- small deploymentではProcess統合可能
- enterpriseではsite cache / KMS / proxy等を追加可能
- browser / ad-hocはlower-assurance profile
- federationではIdentityとData Exportを分離
- public-interest grantではCompute GrantとData Grantを分離

Deployment Profileの後、Task / Attempt / Lease / Recovery state model、Extension SPIへ進みます。
