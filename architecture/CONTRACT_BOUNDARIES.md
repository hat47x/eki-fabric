# 外部・内部の契約境界（External / Internal Contract Boundaries）

Status: **Baseline 0.2 — 2026-10-21**（Baseline 0.1: 2026-09-21。0.2での変更点は§7.9と§7Aの追加で、理由は[ADR-0019](../plan/adr/0019-sandbox-face-isolation-levels-and-frontends.md)にあります）

## 1. この文書の目的

この文書では、EKI Fabricで情報が境界を越えるときに確認する点を定めます。

- 誰が、その値を作れるか
- どの値を、untrustedな入力として扱うか
- どのAuthorityが、意味を確定するか
- 何に結び付けるか
- replay、古い情報、downgradeを、どう防ぐか
- どの情報を、ClientやWorkerに見せないか

特定のJSON schema、HTTP endpoint、gRPC service、Goのstructは、この文書では選びません。

通信形式（Wire format）を決める前に、境界を越えて受け渡す情報の意味と責任を定めます。

関連する文書は、次のとおりです。

- AUTHORITY_BOUNDARIES.md
- ../requirements/PRODUCT_REQUIREMENTS.md
- ../requirements/THREAT_MODEL.md
- ../requirements/ASSURANCE_MODEL.md
- ../requirements/QUALITY_ATTRIBUTES.md

## 2. 基本原則

### 2.1 External ContractとInternal Execution Contractを分ける

Clientから受け取ったTaskRequestを、そのままWorkerに渡しません。

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

External Contractが表すのは、利用者が「何をしたいか」です。

Internal Contractが表すのは、EKIが「何を、どの権限で、どこまで実行してよいか」を確定した内容です。

### 2.2 Clientが制御する値と、Authorityが導く値を分ける

Clientは、要求を送れます。しかし、Security上の事実は確定しません。

Clientが指定できるものの例は、次のとおりです。

- 論理的なExecutorの要求
- inputのArtifactの参照
- CPU、RAM、GPUなどの必要量
- deadline
- priority classの要求
- retryできるという宣言
- side-effectのsemanticsの申告
- より強いData Classの要求

Clientが直接確定できないものの例は、次のとおりです。

- 具体的なWorker
- WorkerのtrustとAssurance
- effective Data Classを引き下げること
- Executor ArtifactのDigest
- Artifact Capability
- Trust Root
- Policy Revision
- WorkOrderの署名者
- 内部のretry budgetの上限を超えること
- network destinationの自由な指定
- Result Acceptance
- Contributionの量

### 2.3 Workerからの入力もuntrustedである

Workerは、認証されていても、Resource Advertisement、ResultClaim、Telemetryなどの内容を自己申告できます。

したがって、次のようになります。

~~~text
Worker says GPU=24GB
  != verified GPU capability

Worker says result=true
  != AcceptedResult

Worker says 60s GPU used
  != Contribution quantity
~~~

自己申告は、Evidenceの一種として保持できます。ただし、保証の水準を別に記録します。

### 2.4 署名済みのobjectは、その場で書き換えない

Admission Decision、Placement Approval、Lease、WorkOrderなどは、発行した後にFieldを書き換えて、意味を変えることはしません。

変更が必要な場合は、次のものとして表現します。

- 新しいrevision
- 新しいAttempt
- revokeとsupersede
- expiry

Auditの上で、

> 当時、何が許可されていたか

を、あとから復元できることを重視します。

### 2.5 Retryは、新しいAttemptである

Task自体は同じでも、再実行は新しいAttemptです。

~~~text
Task T1
  Attempt A1
    WorkOrder W1
    failed / lost / unknown

  Attempt A2
    WorkOrder W2
~~~

A1のWorkOrderを書き換えて、A2に再利用することはしません。

ただし、冪等な外部のeffectでは、外部のOperation IDを同じIntentに結び付けたまま、再利用する場合があります。

Task Attempt Identityと、External Operation Identityは、別の概念です。

---

# 3. Contractの層

EKIのRuntime Contractを、次の層に分けます。

## 3.1 External Client Plane

- TaskRequest
- Task statusの問い合わせ
- Cancellationの要求
- Artifactのingestの要求
- Resultの取得の要求

Clientから見える面です。

## 3.2 Internal Control Contracts

- AdmittedTask / TaskIR
- AttemptContext
- PlacementProposal
- PlacementApproval
- Lease
- ExecutorApproval
- ArtifactGrant
- WorkOrder

Clientは、これらを直接生成できません。

## 3.3 Worker Plane

- ResourceAdvertisement
- WorkOrderの配送と受け入れ
- Leaseのrenewal
- CheckpointとProgressのClaim
- ResultClaim
- Workerのtelemetry

Workerは、これらを送信できます。ただし、Security上の意味は、Authorityが評価します。

## 3.4 Verification / Outcome Contracts

- AttestationResult
- VerificationResult
- AcceptedResult
- ExternalEffectReceipt
- ContributionRecord

Claimと、受理された事実とを、分けます。

## 3.5 Administrative Contracts

- Federation configuration
- Executor admission configuration
- Authorityのtrustとpolicyの設定

通常のTask requestからは触れない、別の面です。

---

# 4. 共通のIdentifier

## 4.1 Principal ID

Identity Providerが検証した主体の、namespace付きの識別子です。

ローカルのuser nameだけで、一意であるとは仮定しません。

少なくとも、Trust DomainまたはIssuerとのbindingを維持します。

## 4.2 Task ID

Admissionに成功した後に、EKIが発行する、Fabric内のTask identityです。

Clientが自由にTask IDを選んで、他のTaskと衝突させられる構造にはしません。

## 4.3 Client Request ID / Idempotency Key

Clientが、retryしても安全なsubmitを行うために使える、Principalの範囲のidempotency identityです。

同じIdempotency Keyについて、次のようにします。

~~~text
same request digest
  → same admission result / Task IDに収束できる

different request digest
  → conflictとして拒否
~~~

Keyだけを再利用して、Taskのintentをすり替えられないようにします。

## 4.4 Attempt ID

Task State Authorityが、各実行のAttemptごとに発行します。

Retry、migration、reclaimのたびに、新しいAttempt IDになります。

## 4.5 Decision ID

Admission、Placement、Lease、Grant、VerificationなどのDecisionを、一意に識別します。

Auditや、revokeとsupersedeの参照に使います。

## 4.6 Digest

変更できないcontentや、正規化したContractを識別する場合は、Digestを使います。

たとえば、次のものです。

- Executor Artifact
- inputのArtifact
- TaskIRのcanonical digest
- Resultのbytes
- Proof Artifact
- Evidence

表示名やtagを、Content Identityとして扱いません。

---

# 5. Versioning

## 5.1 Core schemaのversion

各Contractには、schemaとkindのversion identityを持たせます。

概念上の例は、次のとおりです。

~~~text
eki.task-request/v1
eki.admitted-task/v1
eki.work-order/v1
eki.result-claim/v1
~~~

実際のwire表現は、後続の設計で決めます。

## 5.2 Silent downgradeをしない

受信側が、未知のsecurity-criticalなversionを受け取った場合に、

> 分かるFieldだけを読んで、v1として扱う

といった、Silent downgradeを行いません。

未知のversionは、rejectするか、明示的なtranslatorを通します。

## 5.3 変換の境界

External TaskRequestのversionを変換する必要がある場合は、IngressまたはAdmissionの境界で正規化します。

Workerに、複数の世代のExternal requestのsemanticsを、直接解釈させません。

## 5.4 Internalのversionも明示する

Internalだから安全だという理由で、schema versionを省略しません。

長い間queueに入っているWorkOrderや、federatedなcomponentが、異なるversionを扱う可能性があります。

---

# 6. Extension model

まだ知られていない将来のOSSが、Coreを変更せずに使えるよう、Extensionを認めます。

ただし、Extensionによって、Security Coreを迂回できてはいけません。

## 6.1 名前空間付きのextension

Extensionは、namespace付きの識別子を持ちます。

たとえば、次のとおりです。

~~~text
org.example.video-transcode/v1
dev.sui.similarity/v2
~~~

## 6.2 CriticalとOptional

Extensionには、少なくとも概念上、次の区別を持たせます。

- critical: 理解できなければ、rejectする。
- optional: 理解できなければ、無視してよい。

ただし、optionalなextensionは、次のものを弱める意味を持てません。

- Authorization
- Data Class
- Sandbox Capability
- Artifactへのaccess
- Worker Assurance
- retryの安全性
- side-effectのsemantics

Security-criticalなsemanticsは、可能な限り、Core Contractに置きます。

## 6.3 未知のextensionの保存

Gatewayが理解しないoptionalなextensionを、中継する必要がある場合には、canonical bytesやdigestを保つ余地を持ちます。

ただし、中身が分からないdataを、自動的にWorkerのexecutorに渡すことはしません。

Executor固有のparameterは、Admission済みのExecutor schemaで、別に検証します。

---

# 7. TaskRequest

## 7.1 性質

TaskRequestは、**hostile input**です。

認証済みのClientから来ても、構造、値、意味を信用しません。

## 7.2 Clientが表現できるもの

概念上、次のものを表現できます。

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

TaskRequestには、次のものを直接持たせません。持っていても、hintとしてしか扱いません。

- Worker ID
- hostnameとIP
- 内部のqueue
- WorkOrderの署名者
- Artifact Grant
- Executor Artifactのpathや、任意のbinaryのpath
- 任意のcontainer registryのURL
- 任意のローカルfilesystemのpath
- 生のsecretの値
- 内部のPolicy Revision
- trust root
- verification authorityの公開鍵

## 7.4 Executorの要求

Clientは、論理的なExecutor IDやversionの範囲などを要求できます。

たとえば、次のとおりです。

~~~text
dev.example.transform@1
~~~

Admissionは、その要求を、登録済みのExecutorに解決します。

Workerに渡るWorkOrderでは、実際のArtifact DigestとRuntime Classが固定されます。

Clientが、

~~~text
./run-this.exe
https://attacker/image:latest
~~~

のような、任意のcodeの場所を、標準のTask APIから指定することはできません。

## 7.5 Input Artifact

Inputは、認可された論理的なArtifactの参照として表現します。

Artifactが未登録の場合は、先にArtifact Ingest Surfaceを通します。

TaskRequestから、Workerに任意のURLを直接渡すことはしません。

## 7.6 Resources

Clientは、必要なResourceを宣言できます。

たとえば、次のものです。

- CPUのcore数
- memory
- GPUの要求
- VRAM
- temporary disk
- 想定する最大のoutput

Admissionは、Organization PolicyとQuotaに照らして、内部のBudgetとLimitを確定します。

Clientの要求は、上限を突破するAuthorityではありません。

## 7.7 Network

TaskRequestが外部への接続を必要とする場合に、生のdestinationを、Capabilityとして直接許可しません。

基本の形は、次のいずれかです。

- no network
- 登録済みのnetwork profile
- 登録済みのexternal adapter

具体的なendpointのallowlistは、PolicyとExecutor profileから、Internal WorkOrderに解決します。

## 7.8 Secret

Secretが必要なTaskでは、Secretのbytesを、TaskRequestに埋め込むことを標準にしません。

論理的なSecret Referenceや、登録済みのcapabilityを要求します。そして、後段のAuthorityが、Task、Attempt、Worker、Executorに範囲を限ったGrantに解決します。

Secret Contractの詳細は、Data Planeの設計で決めます。

## 7.9 生成されたコード

（Baseline 0.2で追加。[ADR-0019](../plan/adr/0019-sandbox-face-isolation-levels-and-frontends.md)）

AIが生成したコードのように、依頼ごとに変わるコードは、Executorとして扱いません。入力のArtifactとして扱います。

Executorは、事前に承認したインタプリタや実行環境です。たとえば、決まった版のPythonの実行環境のdigestです。生成コードのdigestは、入力の来歴として記録します。

こうして、§7.4の「任意のcodeの場所をExecutorとして指定できない」原則を保ったまま、生成コードを隔離して実行できます。隔離して試す型（Sandbox）では、ISO-0の実行環境を使いません（[Extension SPI](EXTENSION_SPI.md) §32）。

---

# 7A. 外部の入口（Frontend）

（Baseline 0.2で追加。[ADR-0019](../plan/adr/0019-sandbox-face-isolation-levels-and-frontends.md)）

## 7A.1 役割

利用者が既存の道具からEKIを使えるように、標準の入口を置けます。たとえば、AI AgentのためのMCP、OpenAI互換の推論API、ビルドの遠隔実行のREAPI、既存のジョブ投入の互換APIです。

## 7A.2 原則

- 入口は、受けた依頼をTaskRequest、Session、Sandboxの意味に写すだけです。権限を持ちません。
- 入口から来た依頼も、ほかの依頼と同じAdmission、予算、委任の等級の判断を通ります。
- 入口は、依頼者のcredentialだけを持ちます。管理者の操作を提供しません。
- 入口の仕様にあって、EKIの契約で表せない機能は、受け付けたうえで無視することはせず、その依頼を受理しません。
- 入口が返す出力は、§19のResultClaimと§22のAcceptedResultの区別を保ちます。隔離実行の出力には、信頼できない生成物であることを示す印を付けます。
- 入口の引数や、ツールの説明文の中の指示を、権限として扱いません。

## 7A.3 MCP

MCPの入口は、隔離して試す型を中心にします。一台の構成（DP-7）では、AI Agentの実行環境が起動するローカルのMCPサーバとして、依頼者のOSのユーザーで動き、依頼者のcredentialだけを持ちます。AI Agentが別のAgentへ仕事を渡す場合は、委譲の連鎖として予算を縛ります（RQ-017）。

---

# 8. AdmissionResult / AdmittedTask / TaskIR

## 8.1 役割

TaskRequestを、Fabric内部で扱ってよい意味に正規化したContractです。

Clientは、これを生成できません。

## 8.2 主な内容

概念上、少なくとも次のものを保持します。

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

## 8.3 Clientの入力と、導いたfieldを区別する

TaskIRには、必要に応じて、次の2つを区別して残します。

- Clientが要求した値
- 有効な値、または導いた値

たとえば、次のとおりです。

~~~text
requested priority = high
effective priority = normal

requested data class = public
effective data class = internal
~~~

あとから、

> Clientがpublicと言ったので、publicだった

という誤解を防ぎます。

## 8.4 Classificationの単調性

Admission以降のContractは、正当なreclassificationのprocessなしに、effective security classificationを弱められません。

Artifact固有のclassificationのほうが高い場合は、Data Authority側でも再確認します。

## 8.5 Digest

TaskIRのcanonical representationに、Digestを持てる設計を採ります。

後続のDecisionは、次のものに結び付けます。

~~~text
TaskID
TaskIRDigest
~~~

これによって、Task IDを同じままにして、意味だけをすり替えることを防ぎます。

---

# 9. AttemptContext

## 9.1 役割

Taskの、一回の実行の試行を表します。

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

Retryでは、AttemptContextを新しく発行します。

既存のAttemptのLeaseやWorkOrderを延命して、別のWorkerに移し替えることはしません。

## 9.4 External operation identity

冪等な外部のeffectでは、Attemptが変わっても、同じOperation IDを維持する場合があります。

その場合も、Request Intent Digestを固定します。

~~~text
Attempt A1
  External Operation O1
Attempt A2
  External Operation O1
~~~

これはあり得ます。しかし、

~~~text
O1 + different intent
~~~

は、禁止します。

---

# 10. ResourceAdvertisement

## 10.1 性質

Workerから来るResourceAdvertisementは、認証された自己申告です。

そのまま、Security Evidenceにはしません。

## 10.2 内容

概念上の例は、次のとおりです。

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

## 10.3 性能のclaimと、Securityのclaimを分ける

~~~text
GPU memory = 24 GiB
~~~

という性能のClaimと、

~~~text
confidential GPU execution available
~~~

というSecurityのClaimは、別です。

後者を利用する場合は、Attestationや管理されたpolicyなどのEvidenceが必要です。

## 10.4 古さ

ResourceAdvertisementは、短い寿命のObservationです。

古い値を、永続的なCapabilityとして扱いません。

Schedulerが古い情報を使った場合の影響は、主に性能の劣化にとどめます。Security Authorizationは、Placement Authorityが最新のEvidenceで再評価します。

---

# 11. PlacementProposal

## 11.1 性質

PlacementProposalは、Optimizationの出力です。

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

## 11.3 Learned estimator

将来、Issue #19のmodelを使う場合は、次のものを、Audit用に残せます。

- model ID
- model version
- feature schema
- prediction
- uncertaintyとcalibrationのmetadata

ただし、Proposalの意味は変わりません。

Policyの厳しいfilterを越える、Authorityにはなりません。

---

# 12. PlacementApproval

## 12.1 役割

Task AttemptとWorkerの組み合わせが、Security、Grant、Assuranceの条件を満たすことを、確定します。

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

## 12.3 具体的なResource limitとは分ける

PlacementApprovalは、

> このWorkerに置いてよい

という判断です。

最終的なCPU、Memory、Network、Sandboxのlimitは、Admission Budget、Workerのcapacity、Local Governorなどから、WorkOrderをcompileするときに、さらに狭められます。

---

# 13. Lease

## 13.1 役割

Task AttemptとWorkerに対する、時間制限付きの割り当てを表します。

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

## 13.3 Leaseのexpiry

Leaseが失効したことが示すのは、

> WorkerがTaskを完了していない可能性が高い

ということです。

External side-effectが失敗した証明ではありません。

そのため、Leaseのexpiryから、冪等でないTaskを、直接retryしません。

---

# 14. ExecutorApproval

## 14.1 役割

実行できるExecutor Artifactを確定します。

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

TaskRequestでは、論理的なExecutorの要求を使えます。WorkOrderでは、具体的なDigestに固定します。

変更できるtagを、Workerが実行時に解決することはしません。

---

# 15. ArtifactGrant / DataGrant

## 15.1 役割

特定のArtifactやDataに対して、特定のTask AttemptとWorkerが、限定された操作を行えるCapabilityを表します。

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

Grantは、必要なInputだけに限定します。

同じTenantの他のArtifactを、Workerが列挙できるCapabilityは、標準にはしません。

## 15.4 Output

Outputの宛先も、任意のURLではなく、認可されたOutput Bindingとして扱います。

WorkOrderは、次のような、解決済みのtargetを持てます。

- Control Planeへの、Resultの返却
- 認可されたArtifact Storeへのoutput
- アプリケーションが所有する、登録済みのsink

---

# 16. WorkOrder

## 16.1 性質

WorkOrderは、Workerが実行できる、唯一の標準のInternal Execution Contractです。

ClientRequestそのものではありません。

## 16.2 変更できないenvelope

WorkOrderには、必要に応じて、次のものを含めます。

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

## 16.3 Limitは、安全な側に合成する

WorkOrderのResource Limitは、複数の上限の最小値です。

概念上は、次のとおりです。

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

Local Governorは、WorkOrderを受け取った後でも、さらに狭められます。

## 16.4 Capabilityは、default denyである

WorkOrderにないCapabilityは、使えないことを標準にします。

特に、次のものを、暗黙には許可しません。

- filesystem
- network
- secret
- deviceとGPU
- child process
- temp storage
- external effect adapter

## 16.5 WorkOrderのexpiry

WorkOrderの有効期限は、依存する前提のDecisionより長くできない方向を、基本とします。

概念上は、次のとおりです。

~~~text
workorder expiry
  <= min(
       placement expiry,
       lease expiry,
       executor approval validity,
       artifact grant expiry
     )
~~~

例外が必要な場合は、Contractの上で明示します。暗黙に延長することはしません。

---

# 17. WorkerによるWorkOrderの受け入れ

Workerは、WorkOrderを受信しても、すぐには実行を開始しません。先に、ローカルでverificationを行います。

少なくとも、次のものを確認します。

1. WorkOrder Authority
2. WorkOrderのschema version
3. Worker ID
4. TaskとAttemptのbinding
5. Lease
6. Placement Approval
7. Executor Approval
8. 実際のExecutor Digest
9. Artifact Grant
10. Resource、Sandbox、Networkのlimit
11. expiryとrevocation
12. Local Governor

一致しない場合は、fail closedします。

Workerが、一部のDecisionを理解できないversionの場合は、分かる範囲だけで実行しません。

---

# 18. Progress / Checkpoint Claim

ProgressやCheckpointは、Worker由来のClaimです。

~~~text
Attempt X is 80% complete
Checkpoint digest Y exists
~~~

というWorkerの申告を、Control Planeは、そのまま事実にしません。

Checkpoint Artifactを再利用する場合は、次のものを確認します。

- Artifact Digest
- Executorの互換性
- AttemptとTaskのbinding
- 安全なingest
- authorization

Progressのpercentageは、Schedulingのhintには使えます。しかし、Security Authorityにはしません。

---

# 19. ResultClaim

## 19.1 性質

Workerが、

> このAttemptの結果は、これです

と主張するContractです。

まだ、Accepted Resultではありません。

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

## 19.3 Worker署名の意味

Worker Identityに結び付けられることは、重要です。しかし、

~~~text
signed by Worker
  != result is correct
~~~

です。

Workerの署名は、Resultのprovenanceです。Result Assuranceそのものではありません。

---

# 20. AttestationResult

AttestationResultは、Raw EvidenceをVerifierが評価した、正規化したContractです。

主な内容は、次のとおりです。

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

PlacementとData Authorityは、このResultを、Task固有の条件に照らして、再評価します。

ClientにRaw Evidenceを返すことは、標準にはしません。

---

# 21. VerificationResult

## 21.1 役割

ResultClaimに対して、特定のVerification Methodが、何を確認したかを表します。

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

## 21.3 Methodに固有のdata

Proof SystemやApplication Validatorに固有の情報は、namespace付きのextensionや、Evidenceの参照に置けます。

Relying Partyが必要とする共通のFieldは、Coreに残します。

---

# 22. AcceptedResult

## 22.1 役割

Result Acceptance Authorityが、Task固有のResult Assurance Policyを満たしたと判断した、最終的なContractです。

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

## 22.3 複数のAttempt

複数のWorkerのResultを比較した場合、AcceptedResultが、一つのAttemptだけに依存しないことがあります。

その場合は、根拠となったAttemptとVerification Resultを列挙します。

## 22.4 永続的な事実と、訂正

AcceptedResultを、あとからその場で書き換えることはしません。

誤って受理したことが判明した場合は、次のものとして、履歴を残します。

- revoke
- supersede
- 訂正したAcceptedResult

---

# 23. ExternalEffectReceipt

## 23.1 役割

External Effect Authorityが、外部への副作用が成立したことを確認した、Contractです。

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

ResultClaimに、

~~~text
email sent = true
~~~

と書いてあっても、Receiptの代わりにはなりません。

Side-effectのRecoveryでは、Receiptがあるかどうかを、Task Stateが評価します。

---

# 24. ContributionRecord

## 24.1 役割

検証できるResource MeasurementとWork Contextを、価値に換算する前の記録として残します。

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

Contractに、次のような共通のものを追加しません。

- EKI points
- 円への換算
- market price
- social score

外部の制度が、独自のPolicyで利用します。

---

# 25. Cancellation

## 25.1 Cancelの要求と、Cancelの判断を分ける

Clientは、Cancelを要求できます。

Task State Authorityが、Principalのauthorizationと、Taskのstateを確認して、Cancel Decisionを作ります。

## 25.2 Cancelで消えないもの

Cancellationは、過去の事実を消しません。

次のものを、削除したことにはなりません。

- すでにAcceptedされたResult
- すでに成立したExternal Effect
- すでに発行されたContribution Evidence
- Auditの履歴

## 25.3 Cancelの後

将来の実行を止めるために、次のことを行います。

- 新しいAttemptの作成を止める
- Leaseをrevokeし、renewしない
- Artifact Grantをrevokeするか、期限切れにする
- Workerに、best-effortの停止の信号を送る
- Local Governorやruntimeでkillする

ただし、dispatch済みの冪等でないeffectのoutcomeが不明であれば、UNKNOWNを維持します。

---

# 26. Replay / 古い情報 / Downgradeの防止

Security上、重要なContractでは、次の観点を組み合わせます。

## 26.1 Identity binding

別のPrincipalやWorkerに、流用できない。

## 26.2 Subject binding

別のTask、Attempt、Artifact、Executorに、流用できない。

## 26.3 Time binding

expiry、issued time、lease duration。

## 26.4 Sequenceとnonce

Leaseのrenewal、Attestationのchallengeなど、必要なContractで、replayを検出する。

## 26.5 Policy revision

古いPolicyで発行されたApprovalを、現在のPolicyが拒否できる。

## 26.6 Content digest

変更できるaliasや、byteのすり替えを検出する。

## 26.7 Revocation

緊急に止める必要があるExecutor、Grant、Credentialなどを、失効させられる。

---

# 27. Canonicalization

署名やDigestを使うContractでは、あいまいなserializationを避ける必要があります。

Wire formatを決めるときには、少なくとも、次の条件を満たすcanonical representationを選びます。

- 重複したfieldの意味が、一意に定まる。
- integerの範囲が明確である。
- stringとUnicodeの正規化が明確である。
- mapの順序に依存しない。
- floating-pointを、Security-criticalなfieldに安易に使わない。
- 未知のfieldが、署名の対象かどうかが明確である。
- extensionのcanonical bytesを保持できる。

JSON、CBOR、Protobufなどを採用するかどうかは、後続で比べます。

---

# 28. 厳格なparse

External boundaryでは、parser自体を、攻撃を受ける面として扱います。

少なくとも、次のものを制限します。

- bodyのsize
- 入れ子の深さ
- 配列の長さ
- 文字列の長さ
- metadataの数
- integerのoverflow
- 重複したfield
- 圧縮
- content type
- 未知のcore field

巨大なBodyを、すべてMemoryに読み込んでからrejectする構造は、標準にはしません。

Artifactのuploadでは、streaming、宣言されたsize、digestの検証を使えるようにします。

---

# 29. 情報の開示

## 29.1 Client Plane

Clientには、原則として、次のものを返しません。

- Workerのhostname
- WorkerのIP
- ログインしているuser
- インストールされているsoftwareの一覧
- cacheの内容の一覧
- 正確な内部のtopology
- Raw Attestation Evidence
- 他のtenantのquota
- 他のtenantのtask
- 内部のtrust root

Task statusは、必要な範囲に縮約します。

## 29.2 Worker Plane

Workerには、原則として、次のものを渡しません。

- Clientのcredential
- 他のTaskのmetadata
- 組織全体のArtifactのindex
- 他のWorkerのtopology
- 無関係なsecret
- Federationの管理の設定

WorkOrderに必要な範囲だけを渡します。

---

# 30. 失敗のsemantics

Contractのerrorと、Taskの失敗を、混同しません。

## 30.1 Admissionのrejection

Taskは、作成されません。または、Rejected stateとして記録されます。

## 30.2 Placementのrejection

Task自体は有効です。ただし、そのWorkerの候補が不適切です。

別の候補を試せます。

## 30.3 Workerのrejection

WorkOrderの検証に失敗した場合は、Worker上で実行しません。

Control Planeに、理由のcodeを返します。ただし、secretの詳細を、過剰に返さないようにします。

## 30.4 実行の失敗

Attemptの失敗です。

Taskのretry policyに従います。

## 30.5 UNKNOWN

External side-effectのoutcomeなど、成功か失敗かを確定できない状態です。

失敗に丸めません。

---

# 31. Error Contract

運用者が判断できるErrorを返すために、単なる文字列だけでなく、概念上、次のものを持たせます。

~~~text
error code
contract / phase
retryability
public message
operator detail reference
correlation / task / attempt ID
optional policy reason category
~~~

Securityの理由で、詳細をClientに返せない場合でも、Audit側では、原因を追跡できるようにします。

たとえば、次のようなものです。

~~~text
PLACEMENT_ASSURANCE_UNSATISFIED
ARTIFACT_GRANT_DENIED
WORKORDER_STALE
EXECUTOR_APPROVAL_REVOKED
SIDE_EFFECT_OUTCOME_UNKNOWN
~~~

具体的なcodeの一覧は、後続のContract schemaで定義します。

## 31.1 代わりの案

（2026-10-21追記。[ADR-0023](../plan/adr/0023-autonomy-scope-and-request-interfaces.md)）

受けられない依頼や、期限に間に合わない見込みの依頼には、理由に加えて、拘束力のない代わりの案を返せます。たとえば、「期限を延ばせば余力で間に合う」「この等級なら受けられる」です。

- 代わりの案は、自動では適用しません。依頼者が新しい依頼として出し直します。
- 代わりの案から、内部の構成を推測できる情報を出しません（§29）。

---

# 32. Client PlaneとWorker Planeを混ぜない

同じHTTP serverのprocessを使うことは、可能です。ただし、論理的なAPI surfaceは分離します。

Client tokenでWorkerのendpointを呼べたり、Worker credentialがTask submissionのauthorityを得たりしないように、次のものを分けます。

- audience
- scope
- routeとservice identity
- policy

~~~text
Client credential
  aud = task-api

Worker credential
  aud = worker-api
~~~

このような、senderとaudienceの分離を実現できる構造を選びます。

Workerの認証と登録の方式は、[ADR-0027](../plan/adr/0027-worker-authentication-and-enrollment.md)で、管理されたenrollmentとWorkerごとのmTLSを既定に仮承認した。Clientの認証の本番向けの方式は、後続で比べます。

---

# 33. Administrative Plane

Executor Admission、Federation Administration、Trust Rootの更新などを、通常のTask APIに含めません。

Administrativeな操作は、次のものを適用できる、別のSurfaceにします。

- より強い認証
- 狭いscope
- 明示的なaudit
- versionを持つ設定
- 任意の承認のworkflow

Task Clientが、

~~~text
submit task + trust this new federation root
~~~

を、一つのrequestで行える構造にはしません。

## 33.1 方針の出どころ

（2026-10-21追記。[ADR-0023](../plan/adr/0023-autonomy-scope-and-request-interfaces.md)）

Administrative Planeで出される方針は、人間の管理者が出したものか、どの委任状のもとで委任状を持つ主体が出したものかを、必ず記録します（[Authority Boundaries](AUTHORITY_BOUNDARIES.md)のADM-1の委任状）。

## 33.2 承認の要求

EKIの自律の判断や、委任状を持つ主体の提案のうち、人間の承認が要るものは、承認の要求として一件の記録にします。

~~~text
approval request ID
proposer（EKIの自律の判断 / 委任状とその主体）
operation
impact summary
approval deadline
state（pending / approved / rejected / expired）
~~~

- 人間の管理者のcredentialでだけ承認できます。
- 期限までに承認されなければ、承認されなかったもの（expired）として扱います。
- 承認を待つ間、操作は有効になりません。

## 33.3 照会

照会は、読み取り専用の経路で、Observation Planeから答えます（[Control / Data Plane](CONTROL_DATA_PLANE.md) §3.4）。開示の段階に従い、見込みは見込みであると示します。

## 33.4 通知

EKIから外部へ、判断や状況の変化を知らせます。少なくとも、期限に間に合わない見込み、外部への提供の縮小、結果の取り消し、委任状の期限切れと既定の方針への切り替え、承認の要求を扱います。

- 通知は、受け手の権限に応じて内容を絞ります。
- 通知は、届かないことや二度届くことがあります。受け手は、照会で現在の状態を確かめます。
- 通知の内容を、権限や事実の根拠にしません。

---

# 34. Contractの依存関係

主要なContractの依存関係をまとめると、次のとおりです。

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

AttestationResultは、PlacementApprovalとArtifactGrantのEvidenceとして参照されます。

ExternalEffectReceiptは、Task StateとRecoveryに戻ります。

---

# 35. 外部のContractに公開しないもの

Internalの概念をすべてClient APIに公開すると、将来、Architectureを変更しにくくなります。

次のものは、原則としてInternalです。

- PlacementProposal
- 具体的なWorker identity
- Leaseの内部
- ArtifactGrantのtoken
- WorkOrder
- 内部のPolicy Revision
- Authorityのgraph
- 生のverification evidence

Clientが必要とするのは、主に次のものです。

- Taskがacceptedされたか、rejectされたか
- 現在の公開のstate
- ResultとError
- cancellation
- 任意で、高い水準でのassuranceの要約

Internal contractを、外部との互換性の負担にしないことを重視します。

---

# 36. 言語に中立であること

External Contractは、Go固有の型に依存しません。

避けるものは、次のとおりです。

- Goのpackage pathを、type identityにすること
- time.Durationの内部の表現を、wireの契約にすること
- uintptrや、platformに依存するint
- Goのinterfaceのserialization
- Go固有のerror wrapping

Contractの仕様から、次のものを生成または実装できることを、目標にします。

- Go SDK
- Rust SDK
- Python SDK
- TypeScript SDK
- CLI
- 直接のHTTPやRPCのclient

---

# 37. Contractのsecurity testの方向

実装の段階では、happy pathだけでなく、Contractの境界ごとに、negative testを用意します。

## TaskRequest

- 未知のcore field
- 大きすぎるmetadata
- 重複したfield
- integer overflow
- Clientが選んだWorker
- 任意のURL
- quotaの迂回
- classificationの引き下げ

## Internal Decision

- 誤ったissuer
- 誤ったTask
- 誤ったAttempt
- 誤ったWorker
- 古いPolicy Revision
- 期限切れ
- revoke済み
- 改変されたDigest

## WorkOrder

- 前提が欠けている
- WorkOrderの署名者だけが偽造された
- 誤ったExecutor Digest
- 古いLease
- 誤ったArtifactGrant
- 誤ったWorker
- capabilityの拡大

## Result

- ResultClaimのreplay
- 別のAttempt
- 改変されたOutput
- 偽のVerifier
- 古いVerification Result
- 競合するAccepted Result

## Contribution

- より強いpolicyの下での、Workerの自己申告
- 重複したrecord
- 同じAttemptとMetricで、異なる量
- 偽のObserver

これらを、Protocol conformance suiteに発展させます。

---

# 38. 未決事項

このBaselineでは、次のことを、まだ固定していません。

- HTTP RESTか、gRPCか、Connectか、message protocolか
- JSONか、CBORか、Protobufか
- 正確なField名
- field番号
- pagination
- streaming protocol
- OAuth、mTLS、DPoPの組み合わせ
- canonicalな署名のformat
- error codeの全体の一覧
- Artifactのupload protocol
- Eventとwebhook
- admin APIのtopology
- SDKの生成のtooling

これらは、このContract Boundaryを壊さない範囲で、後続のArchitectureの比較を行います。

---

# 39. 次のWork Unitへの入力

次のArchitecture Work Unitでは、Control PlaneとData Planeを具体化します。

この文書から、特に次のことを引き継ぎます。

1. Control Planeは、TaskとDecisionのmetadataを扱う。
2. 大きなArtifactのbytesを、Control Planeの中央のproxyに集約することは、標準にはしない。
3. ArtifactGrantで、Data Planeへのaccessを限定する。
4. Workerは、WorkOrderからData capabilityを得る。
5. ResultとCheckpointの大きなdataと、controlのmetadataを分離する。
6. Client、Worker、Admin Planeのcredential scopeを分離する。
7. Cache localityは、Optimizationであり、Data authorityではない。

そのあとで、Deployment Profile、StateとLeaseのsemantics、Extension SPIに進みます。
