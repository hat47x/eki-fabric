# External / Internal Contract Boundaries

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

この文書は、EKI Fabricで情報がどの境界を越えるときに、

- 誰がその値を作れるか
- どの値をuntrusted inputとして扱うか
- どのAuthorityが意味を確定するか
- 何へbindingされるか
- replay / stale / downgradeをどう防ぐか
- どの情報をClientやWorkerへ見せないか

を定義します。

対象は特定のJSON schema、HTTP endpoint、gRPC service、Go structではありません。

Wire formatを決める前に、論理Contractを確定します。

関連は次のとおりです。

- AUTHORITY_BOUNDARIES.md
- ../requirements/PRODUCT_REQUIREMENTS.md
- ../requirements/THREAT_MODEL.md
- ../requirements/ASSURANCE_MODEL.md
- ../requirements/QUALITY_ATTRIBUTES.md

## 2. 基本原則

### 2.1 External ContractとInternal Execution Contractを分ける

Clientから受け取ったTaskRequestを、そのままWorkerへ渡しません。

~~~text
Untrusted TaskRequest
        ↓
strict parse / authenticate / authorize / budget
        ↓
Admitted Task / TaskIR
        ↓
Attempt / Placement / Lease / Grants
        ↓
signed WorkOrder
        ↓
Worker
~~~

External Contractは利用者が「何をしたいか」を表します。

Internal ContractはEKIが「何を、どの権限で、どこまで実行してよいか」を確定したものです。

### 2.2 Client controlled valueとAuthority derived valueを分ける

Clientは要求を送れますが、Security上の事実を確定しません。

Clientが指定できる例:

- logical Executor requirement
- input Artifact reference
- CPU / RAM / GPU等の必要量
- deadline
- priority classの要求
- retry可能性の宣言
- side-effect semanticsの申告
- stronger Data Classの要求

Clientが直接確定できない例:

- concrete Worker
- Worker Trust / Assurance
- effective Data Classの引下げ
- Executor Artifact Digest
- Artifact Capability
- Trust Root
- Policy Revision
- WorkOrder signer
- internal retry budgetの上限超過
- network destinationの自由指定
- Result Acceptance
- Contribution quantity

### 2.3 Worker inputもuntrustedである

Workerはauthenticatedであっても、Resource Advertisement、ResultClaim、Telemetry等の内容を自己申告できます。

したがって、

~~~text
Worker says GPU=24GB
  != verified GPU capability

Worker says result=true
  != AcceptedResult

Worker says 60s GPU used
  != Contribution quantity
~~~

です。

Self-reportはEvidenceの一種として保持できますが、保証水準を別に記録します。

### 2.4 Signed objectはin-placeで書き換えない

Admission Decision、Placement Approval、Lease、WorkOrder等は、発行後にFieldを書き換えて意味を変更しません。

変更が必要な場合は、

- 新しいrevision
- 新しいAttempt
- revoke / supersede
- expiry

として表現します。

Audit上、

> 当時何が許可されていたか

を後から復元できることを重視します。

### 2.5 Retryは新しいAttempt

Task自体は同じでも、再実行は新しいAttemptです。

~~~text
Task T1
  Attempt A1
    WorkOrder W1
    failed / lost / unknown

  Attempt A2
    WorkOrder W2
~~~

A1のWorkOrderを書き換えてA2に再利用しません。

ただしidempotent external effectでは、外部Operation IDを同じIntentへbindingしたまま再利用する場合があります。

Task Attempt IdentityとExternal Operation Identityは別概念です。

---

# 3. Contract層

EKIのRuntime Contractを、次の層に分けます。

## 3.1 External Client Plane

- TaskRequest
- Task status query
- Cancellation request
- Artifact ingest request
- Result retrieval request

Clientから見えるSurfaceです。

## 3.2 Internal Control Contracts

- AdmittedTask / TaskIR
- AttemptContext
- PlacementProposal
- PlacementApproval
- Lease
- ExecutorApproval
- ArtifactGrant
- WorkOrder

Clientから直接生成できません。

## 3.3 Worker Plane

- ResourceAdvertisement
- WorkOrder delivery / acceptance
- Lease renewal
- Checkpoint / progress claim
- ResultClaim
- Worker telemetry

Workerはこれらを送信できますが、Security上の意味はAuthorityが評価します。

## 3.4 Verification / Outcome Contracts

- AttestationResult
- VerificationResult
- AcceptedResult
- ExternalEffectReceipt
- ContributionRecord

Claimとaccepted factを分けます。

## 3.5 Administrative Contracts

- Federation configuration
- Executor admission configuration
- Authority trust / policy configuration

通常Task requestから触れない別Surfaceです。

---

# 4. 共通Identifier

## 4.1 Principal ID

Identity Providerが検証した主体のnamespace付き識別子です。

Local user nameだけで一意性を仮定しません。

少なくともTrust DomainまたはIssuerとのbindingを維持します。

## 4.2 Task ID

Admission成功後にEKIが発行するFabric内のTask identityです。

Clientが自由にTask IDを選び、他Taskへ衝突させる構造にはしません。

## 4.3 Client Request ID / Idempotency Key

Clientがretry-safeなsubmitを行うために利用できる、Principal scopeのidempotency identityです。

同じIdempotency Keyについて、

~~~text
same request digest
  → same admission result / Task IDへ収束可能

different request digest
  → conflictとして拒否
~~~

します。

Keyだけを再利用してTask intentをすり替えられないようにします。

## 4.4 Attempt ID

Task State Authorityが、各実行Attemptごとに発行します。

Retry / migration / reclaimで新しいAttempt IDになります。

## 4.5 Decision ID

Admission、Placement、Lease、Grant、Verification等のDecisionを一意に識別します。

Auditやrevoke / supersedeの参照に使います。

## 4.6 Digest

Immutable contentやnormalized Contractを識別する場合はDigestを利用します。

例は次のとおりです。

- Executor Artifact
- Artifact input
- TaskIR canonical digest
- Result bytes
- Proof Artifact
- Evidence

表示名やtagをContent Identityとして扱いません。

---

# 5. Versioning

## 5.1 Core schema version

各Contractにはschema / kindのversion identityを持たせます。

概念例は次のとおりです。

~~~text
eki.task-request/v1
eki.admitted-task/v1
eki.work-order/v1
eki.result-claim/v1
~~~

実際のwire表現は後続設計で決めます。

## 5.2 Silent downgradeをしない

受信側が未知のsecurity-critical versionを受け取った場合、

> 分かるFieldだけ読んでv1として扱う

といったsilent downgradeを行いません。

未知Versionはrejectするか、明示的なtranslatorを通します。

## 5.3 Translation boundary

External TaskRequestのversion変換が必要な場合、Ingress / Admission boundaryでnormalizationします。

Workerへ複数世代のExternal request semanticsを直接解釈させません。

## 5.4 Internal versionも明示する

Internalだから安全という理由でschema versionを省略しません。

長期間queueされたWorkOrderやfederated componentが、異なるversionを扱う可能性があります。

---

# 6. Extension model

未知の将来OSSがCore変更なしに利用できるよう、Extensionを許容します。

ただしExtensionでSecurity Coreを迂回できてはいけません。

## 6.1 Namespaced extension

Extensionはnamespace付き識別子を持ちます。

例は次のとおりです。

~~~text
org.example.video-transcode/v1
dev.sui.similarity/v2
~~~

## 6.2 Critical / optional

Extensionには少なくとも概念的に、

- critical: 理解できなければreject
- optional: 理解できなければ無視可能

の区別を持たせます。

ただしoptional extensionは、

- Authorization
- Data Class
- Sandbox Capability
- Artifact access
- Worker Assurance
- retry safety
- side-effect semantics

を弱める意味を持てません。

Security-critical semanticsは可能な限りCore Contractへ置きます。

## 6.3 Unknown extension preservation

Gatewayが理解しないoptional extensionを中継する必要がある場合、canonical bytes / digestを保つ余地を持ちます。

ただしopaque dataをWorker executorへ自動的に渡すことはしません。

Executor-specific parameterはAdmission済みExecutor schemaで別途検証します。

---

# 7. TaskRequest

## 7.1 性質

TaskRequestは**hostile input**です。

認証済みClientから来ても、構造・値・意味を信用しません。

## 7.2 Clientが表現できるもの

概念的には次を表現できます。

~~~text
client request identity
logical executor requirement
input artifact references
executor parameters
resource needs
execution semantics
deadline
priority request
placement preference
result requirement
optional extension
~~~

## 7.3 Clientが指定できないもの

TaskRequestに次を直接持たせない、または持っていてもhintとしてしか扱いません。

- Worker ID
- hostname / IP
- internal queue
- WorkOrder signer
- Artifact Grant
- Executor Artifact path / arbitrary binary path
- arbitrary container registry URL
- arbitrary local filesystem path
- raw secret value
- internal Policy Revision
- trust root
- verification authority public key

## 7.4 Executor requirement

Clientはlogical Executor ID / version range等を要求できます。

例は次のとおりです。

~~~text
dev.example.transform@1
~~~

Admissionはその要求を登録済みExecutorへresolveします。

Workerへ渡るWorkOrderでは、実Artifact DigestとRuntime Classが固定されます。

Clientが、

~~~text
./run-this.exe
https://attacker/image:latest
~~~

のような任意code locationを標準Task APIから指定することはできません。

## 7.5 Input Artifact

Inputはauthorized logical Artifact referenceとして表現します。

Artifactが未登録なら、先にArtifact Ingest Surfaceを通します。

TaskRequestからWorkerへ任意URLを直接渡しません。

## 7.6 Resources

Clientは必要Resourceを宣言できます。

例は次のとおりです。

- CPU cores
- memory
- GPU requirement
- VRAM
- temporary disk
- maximum expected output

AdmissionはOrganization PolicyとQuotaへ照合し、内部Budget / Limitを確定します。

Client要求は上限を突破するAuthorityではありません。

## 7.7 Network

TaskRequestが外部接続を必要とする場合、raw destinationをCapabilityとして直接許可しません。

基本形は、

- no network
- registered network profile
- registered external adapter

のいずれかです。

具体endpoint allowlistはPolicy / Executor profileからInternal WorkOrderへ解決します。

## 7.8 Secret

Secretが必要なTaskでは、Secret bytesをTaskRequestへ埋め込むことを標準にしません。

Logical Secret Referenceまたはregistered capabilityを要求し、後段AuthorityがTask / Attempt / Worker / ExecutorへscopeしたGrantへ解決します。

Secret Contractの詳細はData Plane設計で決めます。

---

# 8. AdmissionResult / AdmittedTask / TaskIR

## 8.1 役割

TaskRequestを、Fabric内部で扱ってよい意味へnormalizationしたContractです。

Clientが生成できません。

## 8.2 主な内容

概念上、少なくとも次を保持します。

~~~text
Task ID
Requester Principal
Client request digest
resolved logical Executor requirement
authorized input Artifact refs
effective Data Class
resource budget
retry / fan-out budget
side-effect semantics
deadline / priority ceiling
result assurance requirement
allowed placement classes
extension normalization result
Admission Policy Revision
issued time / decision identity
~~~

## 8.3 Client inputとderived fieldを区別する

TaskIRには、

- client requested value
- effective / derived value

を必要に応じて区別して残します。

例は次のとおりです。

~~~text
requested priority = high
effective priority = normal

requested data class = public
effective data class = internal
~~~

後から、

> Clientがpublicと言ったのでpublicだった

という誤解を防ぎます。

## 8.4 Classification monotonicity

Admission以降のContractは、正当なreclassification processなしにeffective security classificationを弱められません。

Artifact intrinsic classificationの方が高い場合は、Data Authority側でも再確認します。

## 8.5 Digest

TaskIRのcanonical representationにDigestを持てる設計を採ります。

後続Decisionは、

~~~text
TaskID
TaskIRDigest
~~~

へbindingします。

これによりTask IDを同じまま意味だけ差し替えることを防ぎます。

---

# 9. AttemptContext

## 9.1 役割

Taskの一回の実行試行を表します。

## 9.2 主な内容

~~~text
Task ID
TaskIR Digest
Attempt ID
attempt number
remaining retry budget
created time
deadline
previous attempt reference
recovery reason
side-effect operation identity if applicable
~~~

## 9.3 Retry

RetryはAttemptContextを新規発行します。

既存AttemptのLeaseやWorkOrderを延命して別Workerへ移し替えません。

## 9.4 External operation identity

idempotent external effectでは、Attemptが変わっても同じOperation IDを維持する場合があります。

その場合もRequest Intent Digestを固定します。

~~~text
Attempt A1
  External Operation O1
Attempt A2
  External Operation O1
~~~

はあり得ますが、

~~~text
O1 + different intent
~~~

は禁止します。

---

# 10. ResourceAdvertisement

## 10.1 性質

Workerから来るResourceAdvertisementはauthenticated self-reportです。

そのままSecurity Evidenceにはしません。

## 10.2 内容

概念例は次のとおりです。

~~~text
Worker ID
Worker Class
CPU architecture / capacity
memory
GPU vendor / API / memory
runtime availability
cache summary
storage
network condition
power / battery
local willingness
timestamp
~~~

## 10.3 Performance claimとSecurity claimを分ける

~~~text
GPU memory = 24 GiB
~~~

という性能Claimと、

~~~text
confidential GPU execution available
~~~

というSecurity Claimは別です。

後者を利用する場合はAttestation / managed policy等のEvidenceが必要です。

## 10.4 Staleness

ResourceAdvertisementは短命なObservationです。

古い値を永続Capabilityとして扱いません。

Schedulerがstale informationを使った場合は主にperformance degradationへ閉じるようにし、Security AuthorizationはPlacement Authorityが最新Evidenceで再評価します。

---

# 11. PlacementProposal

## 11.1 性質

PlacementProposalはOptimization outputです。

Security tokenではありません。

## 11.2 主な内容

~~~text
Task / Attempt
candidate Worker
predicted cost
locality estimate
transfer estimate
deadline estimate
optional ranking reason
scheduler version / model version
~~~

## 11.3 learned estimator

将来Issue #19のmodelを使う場合は、

- model ID
- model version
- feature schema
- prediction
- uncertainty / calibration metadata

等をAudit用に残せます。

ただしProposalの意味は変わりません。

Policy hard filterを越えるAuthorityにはなりません。

---

# 12. PlacementApproval

## 12.1 役割

Task AttemptとWorkerの組合せがSecurity / Grant / Assurance条件を満たすことを確定します。

## 12.2 Binding

~~~text
Task ID
TaskIR Digest
Attempt ID
Worker ID
relevant Worker assurance refs
Compute Grant ref
placement policy revision
issued / expiry
Decision ID
~~~

## 12.3 具体Resource limitとは分ける

PlacementApprovalは、

> このWorkerへ置いてよい

という判断です。

最終CPU / Memory / Network / Sandbox limitはAdmission Budget、Worker capacity、Local Governor等からWorkOrder compilation時にさらに狭められます。

---

# 13. Lease

## 13.1 役割

Task AttemptとWorkerの時間制限付き割当を表します。

## 13.2 Binding

~~~text
Task ID
Attempt ID
Worker ID
Lease ID
issued
expires
renewal sequence
~~~

## 13.3 Lease expiry

Lease失効は、

> WorkerがTaskを完了していない可能性が高い

ことを示します。

External side-effectが失敗した証明ではありません。

したがってLease expiryから直接non-idempotent Taskをretryしません。

---

# 14. ExecutorApproval

## 14.1 役割

実行可能なExecutor Artifactを確定します。

## 14.2 主な内容

~~~text
logical Executor ID
Artifact Digest
Media Type / Size
Runtime Class
Publisher / Builder provenance
allowed capability profile
Admission Policy Revision
issued / expiry / revocation state
~~~

## 14.3 Taskへのbinding

TaskRequestではlogical Executor requirementを使えますが、WorkOrderでは具体Digestへ固定します。

Mutable tagをWorkerが実行時に解決しません。

---

# 15. ArtifactGrant / DataGrant

## 15.1 役割

特定Artifact / Dataへ、特定Task Attempt / Workerが限定操作できるCapabilityを表します。

## 15.2 Binding

~~~text
Artifact Digest / Object ID
Task ID
Attempt ID
Worker ID
allowed operation
Data Class
optional range / object subset
issued
expires
Grant ID
policy revision
~~~

## 15.3 Grantの最小化

Grantは必要なInputだけに限定します。

Workerが同じTenantの他Artifactを列挙できるCapabilityを標準にはしません。

## 15.4 Output

Output先も、arbitrary URLではなくAuthorized Output Bindingとして扱います。

WorkOrderは、

- result return to Control Plane
- authorized Artifact Store output
- application-owned registered sink

等の解決済みtargetを持てます。

---

# 16. WorkOrder

## 16.1 性質

WorkOrderはWorkerが実行できる唯一の標準Internal Execution Contractです。

ClientRequestそのものではありません。

## 16.2 Immutable envelope

WorkOrderには必要に応じて次を含めます。

~~~text
WorkOrder ID
Task ID
TaskIR Digest
Attempt ID
Worker ID
Placement Approval ref / digest
Lease ref / digest
Executor Approval ref / digest
Executor Artifact Digest
Artifact Grants
Output Binding
resource limits
Sandbox capabilities
network policy
side-effect profile
checkpoint policy
max wall time
issued / expires
nonce
WorkOrder Authority identity
~~~

## 16.3 Limitは安全側に合成する

WorkOrderのResource Limitは、複数の上限の最小値です。

概念上は次のとおりです。

~~~text
effective execution limit
  = min(
      admitted task budget,
      organization policy,
      placement / grant constraints,
      worker advertised capacity,
      provider willingness,
      local governor limit
    )
~~~

Local GovernorはWorkOrder受領後でもさらに狭められます。

## 16.4 Capabilityはdefault deny

WorkOrderにないCapabilityは使えないことを標準にします。

特に、

- filesystem
- network
- secret
- device / GPU
- child process
- temp storage
- external effect adapter

を暗黙許可しません。

## 16.5 WorkOrder expiry

WorkOrderの有効期限は、依存する前提Decisionより長くできない方向を基本とします。

概念上は次のとおりです。

~~~text
workorder expiry
  <= min(
       placement expiry,
       lease expiry,
       executor approval validity,
       artifact grant expiry
     )
~~~

例外が必要ならContract上で明示し、暗黙延長しません。

---

# 17. Worker WorkOrder Acceptance

WorkerはWorkOrderを受信したら、すぐ実行開始するのではなくlocal verificationを行います。

少なくともは次のとおりです。

1. WorkOrder Authority
2. WorkOrder schema version
3. Worker ID
4. Task / Attempt binding
5. Lease
6. Placement Approval
7. Executor Approval
8. actual Executor Digest
9. Artifact Grant
10. Resource / Sandbox / Network limit
11. expiry / revocation
12. Local Governor

を確認します。

不一致時はfail closedします。

Workerが一部のDecisionを理解できないVersionなら、分かる範囲だけで実行しません。

---

# 18. Progress / Checkpoint Claim

ProgressやCheckpointはWorker由来のClaimです。

~~~text
Attempt X is 80% complete
Checkpoint digest Y exists
~~~

というWorker申告を、Control Planeはそのまま事実にしません。

Checkpoint Artifactを再利用する場合は、

- Artifact Digest
- Executor compatibility
- Attempt / Task binding
- safe ingest
- authorization

を確認します。

Progress percentageはScheduling hintには使えてもSecurity Authorityにはしません。

---

# 19. ResultClaim

## 19.1 性質

Workerが

> このAttemptの結果はこれです

と主張するContractです。

まだAccepted Resultではありません。

## 19.2 主な内容

~~~text
Task ID
Attempt ID
Worker ID
Executor Digest
Input / TaskIR references
Output Artifact / Digest
execution metadata
optional proof / validator evidence refs
completed time
Worker signature / authenticated channel provenance
~~~

## 19.3 Worker signatureの意味

Worker Identityにbindingできることは重要ですが、

~~~text
signed by Worker
  != result is correct
~~~

です。

Worker署名はResult provenanceであり、Result Assuranceそのものではありません。

---

# 20. AttestationResult

AttestationResultはRaw EvidenceをVerifierが評価したnormalized Contractです。

主な内容は次のとおりです。

~~~text
Worker / Attester ID
Trust Domain
Environment Class
Verifier ID
Verifier Policy Revision
Evidence Digest
verified time
expiry
~~~

PlacementやData Authorityは、このResultをTask固有条件へ再評価します。

ClientへRaw Evidenceを返すことを標準にはしません。

---

# 21. VerificationResult

## 21.1 役割

ResultClaimに対して、特定Verification Methodが何を確認したかを表します。

## 21.2 主な内容

~~~text
Task ID
Attempt ID
Result / Output Digest
verification method
Verifier ID
Verifier Policy Revision
evidence / proof digest
verification outcome
assurance properties
verified time
expiry if applicable
~~~

## 21.3 Method-specific data

Proof SystemやApplication Validator固有情報はnamespaced extensionまたはEvidence referenceへ置けます。

Relying Partyが必要とする共通FieldはCoreに残します。

---

# 22. AcceptedResult

## 22.1 役割

Result Acceptance AuthorityがTask固有Result Assurance Policyを満たしたと判断した最終Contractです。

## 22.2 主な内容

~~~text
Task ID
accepted Result / Output Digest
source Attempt IDs
Verification Result refs
Acceptance Policy Revision
Accepted At
Acceptance Decision ID
optional supersedes / correction
~~~

## 22.3 複数Attempt

複数Worker Resultを比較した場合、AcceptedResultは一つのAttemptだけに依存しないことがあります。

その場合、根拠Attempt / Verification Resultを列挙します。

## 22.4 Durable factとcorrection

AcceptedResultを後からin-placeで書き換えません。

誤受理が判明した場合は、

- revoke
- supersede
- corrected AcceptedResult

として履歴を残します。

---

# 23. ExternalEffectReceipt

## 23.1 役割

External Effect Authorityが、外部副作用の成立を確認したContractです。

## 23.2 Binding

~~~text
External Operation ID
Request Intent Digest
effect identity
status
authority identity
observed / applied time
optional expiry
~~~

## 23.3 Worker Resultとは分ける

ResultClaimに

~~~text
email sent = true
~~~

と書いてあってもReceiptの代わりになりません。

Side-effect RecoveryではReceiptの有無をTask Stateが評価します。

---

# 24. ContributionRecord

## 24.1 役割

検証可能なResource MeasurementとWork Contextを、価値換算前の記録として残します。

## 24.2 主な内容

~~~text
Provider
Worker
Task / Attempt
Resource Class
Metric
Unit
Quantity
Measurement Assurance
Observer
Measurement Digest
optional Accepted Result reference
Contribution Policy Revision
issued time
~~~

## 24.3 非目標

Contractに共通の

- EKI points
- 円換算
- market price
- social score

を追加しません。

外部制度が独自Policyで利用します。

---

# 25. Cancellation

## 25.1 Cancel requestとCancel decisionを分ける

ClientはCancelを要求できます。

Task State Authorityが、Principal authorizationとTask stateを確認してCancel Decisionを作ります。

## 25.2 Cancelで消えないもの

Cancellationは過去の事実を消しません。

- すでにAcceptedされたResult
- すでに成立したExternal Effect
- すでに発行されたContribution Evidence
- Audit history

を削除したことにはなりません。

## 25.3 Cancel後

将来実行を止めるため、

- new Attempt creation停止
- Lease revoke / no renewal
- Artifact Grant revoke / expiry
- Workerへbest-effort stop signal
- Local Governor / runtime kill

等を行います。

ただしdispatch済みnon-idempotent effectのoutcomeが不明ならUNKNOWNを維持します。

---

# 26. Replay / Stale / Downgrade防止

Security-sensitive Contractは、次の観点を組み合わせます。

## 26.1 Identity binding

別Principal / Workerへ流用できない。

## 26.2 Subject binding

別Task / Attempt / Artifact / Executorへ流用できない。

## 26.3 Time binding

expiry / issued time / lease duration。

## 26.4 Sequence / nonce

Lease renewal、Attestation challenge等、必要なContractでreplayを検出。

## 26.5 Policy revision

古いPolicyで発行されたApprovalを、現在Policyが拒否できる。

## 26.6 Content digest

Mutable aliasやbyte substitutionを検出。

## 26.7 Revocation

緊急停止が必要なExecutor、Grant、Credential等を失効可能にする。

---

# 27. Canonicalization

署名やDigestを使うContractは、曖昧なserializationを避ける必要があります。

Wire format決定時に、少なくとも次を満たすcanonical representationを選びます。

- duplicate fieldの意味が一意
- integer rangeが明確
- string / Unicode normalizationが明確
- map orderingへ依存しない
- floating-pointをSecurity-critical fieldへ安易に使わない
- unknown fieldの署名対象範囲が明確
- extensionのcanonical bytesを保持可能

JSON、CBOR、Protobuf等の採否は後続比較です。

---

# 28. Strict parsing

External boundaryではparser自体をattack surfaceとして扱います。

少なくともは次のとおりです。

- body size
- nesting depth
- array length
- string length
- metadata count
- integer overflow
- duplicate field
- compression
- content type
- unknown core field

を制限します。

巨大Bodyを全量Memoryへ読み込んでからrejectする構造を標準にはしません。

Artifact uploadはstreaming / declared size / digest検証を利用できるようにします。

---

# 29. Information disclosure

## 29.1 Client Plane

Clientへ原則返さないもの。

- Worker hostname
- Worker IP
- logged-in user
- installed software inventory
- cache content list
- exact internal topology
- Raw Attestation Evidence
- other tenant quota
- other tenant task
- internal trust root

Task statusは必要な範囲へ縮約します。

## 29.2 Worker Plane

Workerへ原則渡さないもの。

- Client credential
- other Task metadata
- organization-wide Artifact index
- other Worker topology
- unrelated secret
- Federation admin configuration

WorkOrderに必要なscopeだけ渡します。

---

# 30. Failure semantics

Contract errorとTask failureを混同しません。

## 30.1 Admission rejection

Taskは作成されない、またはRejected stateとして記録されます。

## 30.2 Placement rejection

Task自体は有効ですが、そのWorker candidateが不適切です。

別candidateを試せます。

## 30.3 Worker rejection

WorkOrder検証失敗はWorker上で実行しません。

Control Planeへ理由codeを返しますが、secret detailを過剰に返さないようにします。

## 30.4 Execution failure

Attempt failureです。

Task retry policyに従います。

## 30.5 UNKNOWN

External side-effect outcome等、成功・失敗を確定できない状態です。

failureへ丸めません。

---

# 31. Error Contract

運用者が判断できるErrorを返すため、単なる文字列だけでなく概念的に次を持たせます。

~~~text
error code
contract / phase
retryability
public message
operator detail reference
correlation / task / attempt ID
optional policy reason category
~~~

Security理由で詳細をClientへ返せない場合でも、Audit側では原因を追跡できるようにします。

例は次のとおりです。

~~~text
PLACEMENT_ASSURANCE_UNSATISFIED
ARTIFACT_GRANT_DENIED
WORKORDER_STALE
EXECUTOR_APPROVAL_REVOKED
SIDE_EFFECT_OUTCOME_UNKNOWN
~~~

具体code一覧は後続Contract schemaで定義します。

---

# 32. Client PlaneとWorker Planeを混ぜない

同じHTTP server processを使うことは可能でも、logical API surfaceは分離します。

Client tokenがWorker endpointを呼べたり、Worker credentialがTask submission authorityを得たりしないよう、

- audience
- scope
- route / service identity
- policy

を分けます。

~~~text
Client credential
  aud = task-api

Worker credential
  aud = worker-api
~~~

のようなsender / audience separationを実現できる構造を選びます。

具体OAuth / mTLS方式は後続比較です。

---

# 33. Administrative Plane

Executor Admission、Federation Administration、Trust Root更新等を通常Task APIへ含めません。

Administrative operationは、

- stronger authentication
- narrow scope
- explicit audit
- versioned configuration
- optional approval workflow

を適用できる別Surfaceにします。

Task Clientが、

~~~text
submit task + trust this new federation root
~~~

を一つのrequestで行える構造にしません。

---

# 34. Contract dependency graph

主要Contractの依存関係をまとめると次です。

~~~text
TaskRequest
   │
   ▼
AdmittedTask / TaskIR
   │
   ├──────────────► AttemptContext
   │                    │
   │                    ├────► PlacementProposal
   │                    │            │
   │                    │            ▼
   │                    │       PlacementApproval
   │                    │            │
   │                    └────► Lease │
   │                                 │
ExecutorApproval ─────────────────────┤
ArtifactGrant ────────────────────────┤
   │                                 ▼
   └────────────────────────────── WorkOrder
                                      │
                                      ▼
                                  ResultClaim
                                      │
                             VerificationResult
                                      │
                                AcceptedResult
                                      │
                              ContributionRecord
~~~

AttestationResultはPlacementApproval / ArtifactGrantのEvidenceとして参照されます。

ExternalEffectReceiptはTask State / Recoveryへ戻ります。

---

# 35. 何を外部Contractへ露出しないか

Internal概念を全部Client APIへ公開すると、将来Architectureを変更しにくくなります。

次は原則Internalです。

- PlacementProposal
- concrete Worker identity
- Lease internals
- ArtifactGrant token
- WorkOrder
- internal Policy Revision
- Authority graph
- raw verification evidence

Clientが必要なのは主に、

- Task accepted / rejected
- current public state
- Result / Error
- cancellation
- optionally high-level assurance summary

です。

Internal contractをexternal compatibility burdenにしないことを重視します。

---

# 36. Language neutrality

External ContractはGo固有型へ依存しません。

避けるもの。

- Go package pathをtype identityにする
- time.Durationの内部表現をwire契約にする
- uintptr / platform int
- Go interface serialization
- Go-specific error wrapping

Contract specから、

- Go SDK
- Rust SDK
- Python SDK
- TypeScript SDK
- CLI
- direct HTTP / RPC client

を生成・実装できることを目標にします。

---

# 37. Contract security testの方向

実装段階では、happy pathだけでなくContract境界ごとにnegative testを用意します。

## TaskRequest

- unknown core field
- oversized metadata
- duplicate field
- integer overflow
- client-selected Worker
- arbitrary URL
- quota bypass
- classification downgrade

## Internal Decision

- wrong issuer
- wrong Task
- wrong Attempt
- wrong Worker
- stale Policy Revision
- expired
- revoked
- mutated Digest

## WorkOrder

- missing prerequisite
- WorkOrder signer only forged
- wrong Executor Digest
- stale Lease
- wrong ArtifactGrant
- wrong Worker
- capability widening

## Result

- ResultClaim replay
- different Attempt
- modified Output
- fake Verifier
- stale Verification Result
- conflicting Accepted Result

## Contribution

- worker self-report under stronger policy
- duplicate record
- different quantity same Attempt / Metric
- fake Observer

これらをProtocol conformance suiteへ発展させます。

---

# 38. 未決事項

このBaselineでは次をまだ固定しません。

- HTTP REST / gRPC / Connect / message protocol
- JSON / CBOR / Protobuf
- exact Field names
- field number
- pagination
- streaming protocol
- OAuth / mTLS / DPoP組合せ
- canonical signature format
- error code全集
- Artifact upload protocol
- Event / webhook
- admin API topology
- SDK generation tooling

これらはこのContract Boundaryを壊さない範囲で後続Architecture比較を行います。

---

# 39. 次のWork Unitへの入力

次のArchitecture Work UnitではControl Plane / Data Planeを具体化します。

この文書から、特に次を引き継ぎます。

1. Control PlaneはTask / Decision metadataを扱う。
2. bulk Artifact bytesをControl Planeの中央proxyへ集約することを標準にはしない。
3. ArtifactGrantでData Plane accessを限定する。
4. WorkerはWorkOrderからData capabilityを得る。
5. Result / Checkpointのbulk dataとcontrol metadataを分離する。
6. Client / Worker / Admin Planeのcredential scopeを分離する。
7. Cache localityはOptimizationでありData authorityではない。

その後、Deployment Profile、State / Lease semantics、Extension SPIへ進みます。
