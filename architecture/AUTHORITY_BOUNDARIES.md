# 権限と信頼の境界（Authority / Trust Boundary Map）

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricでは、Client、Worker、Control Planeのどれも、同じシステムの一部だからという理由だけで信頼しません。

Control Plane内部を一つの大きな信頼領域にまとめると、SchedulerやParserが侵害された際に、Artifactの読み出し、Executorの承認、Federationの設定変更、WorkOrderの発行まで権限を奪われる危険があります。

そのため、この文書では、プロセスの数や採用製品を決める前に、次の役割を定義します。

- 誰が事実を検証するか
- 誰がSecurity上の判断を下すか
- 誰がCapabilityや実行権限を発行できるか
- その判断を、誰が再検証するか
- 一つのComponentが侵害されたとき、影響がどこまで届くか

この文書は、そのための、論理的なAuthorityとTrust BoundaryのBaselineです。

関連する正本は、次のとおりです。

- ../requirements/PRODUCT_REQUIREMENTS.md
- ../requirements/THREAT_MODEL.md
- ../requirements/ASSURANCE_MODEL.md
- ../requirements/QUALITY_ATTRIBUTES.md
- ../plan/ARCHITECTURE_ENTRY_REVIEW_2026-09-21.md

## 2. AuthorityはServiceの数ではない

この文書でいうAuthorityとは、論理的な役割のことです。Security上、意味のある事実を確定したり、権限を発行したり、状態を変更したりできます。

一つのAuthorityを、必ず一つのMicroserviceにするという意味ではありません。

小規模な構成では、次のものを一つのBinaryに同居させてもかまいません。

~~~text
eki-control
  Admission
  Task State
  Scheduler
  Placement Guard
  Lease
  WorkOrder Assembly
~~~

ただし、同居していても、次のものは分けます。

- Authority identity
- signing keyやkey handle
- Policyの入力
- 永続的な状態への書き込みの権限
- 呼び出せるInterface
- Audit event
- 障害や侵害の影響の範囲

Production Profileでは、意味の異なるAuthorityが、同じ署名Credentialを共有することを標準にはしません。

同じProcessに同居させる場合も、Scheduler moduleがExecutor Admissionのkeyや、Artifact Capabilityのkeyを直接使える設計にはしません。

## 3. 基本原則

### 3.1 Authenticationは、Authorityを増やさない

認証されたClientも、悪意を持ち得ます。

認証が確認するのは、

> このrequestが、どのPrincipalから来たか

ということだけです。

それだけで、次のことを許可しません。

- 任意のExecutorの実行
- 任意のArtifactの読み出し
- 具体的なWorkerの指定
- Federationの設定の変更
- Resource Budgetを超えること

### 3.2 Schedulerは、Policy Authorityではない

Schedulerは、性能、locality、deadlineなどを使って、候補の順位を付けます。

Schedulerが返すのは、Placement Proposalです。

Proposalは、

> このWorkerがよさそうだ

という提案です。

> このWorkerに置いてよい

というSecurity Authorizationではありません。

将来、Issue #19のlearned estimatorを利用する場合も、同じです。

~~~text
model score
  != permission
predicted reliability
  != Worker Assurance
~~~

### 3.3 WorkOrderの署名だけを、万能なTrust Tokenにしない

WorkOrder Authorityは、最終的なEnvelopeに署名します。しかし、自分だけでは、Taskのsemantics、Data authority、Executor approval、Placement authorizationを作りません。

Workerは、WorkOrderの署名だけでなく、WorkOrderが参照している前提のDecisionについて、次の項目を再検証します。issuer、subject、digest、policy revision、expiry。

そのため、WorkOrderのsigning keyだけが侵害されても、他のAuthorityの権限を自動的に得てしまう構造は避けます。

### 3.4 Authorityの結果は、対象に結び付ける

Security上、意味のあるDecisionは、少なくとも必要な範囲で、次のものに結び付けます。

- Principal / Trust Domain
- Task ID
- Attempt ID
- Worker ID
- Executor ID / Digest
- Artifact Digest
- Input / Output Digest
- Policy Revision
- Issued At / Expiry
- Decision / Nonce / Lease ID

別のTaskや別のWorker向けの、正しい署名を流用できないようにします。

### 3.5 Trust Rootを、呼び出し側に選ばせない

検証に使う公開鍵やTrust Rootを、通常のrequestのparameterとして渡し、

> 今回は、この鍵を信じてください

と差し替えられるAPIは、作りません。

AuthorityのTrust Rootは、別の管理の経路で、設定し、rotationします。

### 3.6 Local Governorは、中央のAuthorityに対する拒否権を持つ

Worker側のLocal Governorは、Central Schedulerがcapacityを使いたいと判断しても、より厳しい上限を適用できます。

~~~text
effective capacity
  = min(
      organization permitted capacity,
      provider/user willingness,
      local safety judgement
    )
~~~

中央側には、この拒否権を無効にする、通常の実行権限を持たせません。

---

# 4. Authorityの分類

EKIのAuthorityを、性質の違いから、次の4つのグループに分けます。

## 4.1 Administrative Authorities

頻繁に行われるTaskの実行の経路から分離する、価値の高い設定のAuthorityです。

- Trust / Policy Administration
- Federation Administration
- Executor Admission Administration

## 4.2 Runtime Decision Authorities

通常のTask lifecycleで、Security上の判断やCapabilityを発行します。

- Task Admission Authority
- Task State / Recovery Authority
- Placement Authorization Authority
- Lease Authority
- Artifact / Data Capability Authority
- WorkOrder Authority

## 4.3 Verification / Acceptance Authorities

Workerや外部の主体が出したClaimを、そのまま事実に昇格させないためのAuthorityです。

- Attestation Verifier
- Result Verification Authority
- Result Acceptance Authority
- External Effect Confirmation Authority
- Contribution Authority

## 4.4 Worker-local Enforcement

中央のDecisionを受け取った後でも、実行の直前と実行中に、境界を守ります。

- WorkOrder Verifier
- Local Governor
- Sandbox / Capability Enforcer

Scheduler、Learned Estimator、Telemetry Collector、Cache locality estimatorなどは、重要なComponentです。しかし、それ自体にはSecurity Authorityを与えないことを、標準とします。

---

# 5. Authorityの一覧

## ADM-1 — Trust / Policy Administration

### 役割

EKI内部のAuthorityが信頼する、issuer、key、policy revisionなどを管理する、Rootです。

たとえば、次のものがあります。

- Task Admission Authorityのtrust root
- WorkOrder Authorityのtrust root
- Verification Authorityのtrust root
- Organization policyのversion
- keyのrotationとrevocation

### 持てる権限

- Authorityのtrust configurationの更新
- keyのrotation
- policyのactivationとrevocation
- 過去の内容へ戻す必要がある場合、新しいrevision identityとして再発行するsemantic rollback
- 緊急のrevocation

### 持たせない権限

- ClientとしてTaskをsubmitすること
- Workerを直接選ぶこと
- Artifactを直接読むこと
- ResultをAcceptedにすること
- Contributionの量を生成すること

### 注意

このAuthorityが侵害されると、影響が大きくなります。そのため、通常のTask executionの経路から分離します。

将来、価値の高い変更にmulti-party approvalを導入する余地は残します。ただし、Baseline 0.1では、方式を固定しません。

---

## FED-1 — Federation Administration

### 役割

外部のTrust Domainとの、Identityを検証する関係を、明示的に構成します。

### 入力

- Trust Domain
- Trust Material / Bundle
- versionとsequence
- federation policy
- 管理者の承認

### 出力

- versionを持つFederation Configuration

### 持たせない権限

- Federationした外部のPrincipalに、自動的にTaskの権限を付与すること
- Artifactへのアクセスを、自動的に許可すること
- Contributionの価値を決めること
- 推移的なTrustを、自動的に生成すること

~~~text
Identity can be verified
  !=
Resource access is authorized
~~~

Federation Trust Binding Spikeの原則を維持します。

---

## EXE-1 — Executor Admission Authority

### 役割

Worker上で実行できるExecutor Artifactを審査して、実行できるDigest、Runtime Class、Provenanceの条件を確定します。

### 主なEvidence

- ArtifactのDigest / Size / Media Type
- Publisher Evidence
- Build Provenance
- SourceとCommit
- SBOMやscanなどの、任意のEvidence
- Runtime Class

### 出力

概念上の、Executor Approvalです。

### 持たせない権限

- Taskをsubmitすること
- 具体的なWorkerを選ぶこと
- Artifactのinput dataを読む権限を与えること
- WorkOrderを単独で発行すること

正しく署名されたArtifactやProvenanceがあっても、それがそのままExecutor Admissionになるわけではありません。

---

## IDV-1 — Identity Verification

### 役割

Client、Worker、Workload、Administrative PrincipalのIdentityを検証します。

OIDC、企業のPKI、SPIFFEなどは、Adapterの候補です。

### 出力

正規化したPrincipal Contextです。

### 重要な境界

Identity Verificationは、Authenticationの根拠です。

Authorization、Resultが正しいこと、Worker Assuranceを、自動的には作りません。

Client Identity、Worker Identity、Control Plane Workload Identityは、別のnamespaceやroleとして扱います。

---

## ADM-2 — Task Admission Authority

### 役割

外部から来たuntrustedなTaskRequestを、Fabric内部で扱ってよいAdmitted Taskに変換します。

### 主な入力

- 認証済みのPrincipal Context
- 厳格に検証した外部のTaskRequest
- organization policy
- quotaとbudget
- Artifact metadata
- Executorの論理的な要求
- side-effectのsemantics
- 要求されたsecurityとdata class

### 主な処理

- schemaの検証
- authorization
- quotaとbudgetの予約
- 有効なsecurity classification
- retryとfan-outのbudget
- side-effect contractの検証
- 許可するExecutorの要求の確定
- 未知または欠けているsecurity fieldを、安全な側に処理する

Clientが指定したData Classは、下限ではありません。他の根拠と合わせて、安全な側に計算します。

たとえば、次のとおりです。

~~~text
effective data class
  >= artifact intrinsic class
  >= organization policy minimum
  >= executor requirement
  >= client requested class
~~~

Clientが値を小さくして、Securityを弱めることはできません。

### 出力

概念上の、Admitted TaskとTaskIR Approvalです。

### 持たせない権限

- 具体的なWorkerの選択
- Executor Artifactの新規の承認
- Artifact capabilityの発行
- Federationの変更
- WorkOrderの最終的な署名

---

## TSK-1 — Task State / Recovery Authority

### 役割

TaskのlifecycleとAttemptを管理します。

### 管理するもの

- Task state
- Attempt ID
- retry countとbudget
- cancellation
- deadline
- UNKNOWNのside-effect outcome
- mass reclaimの後のrecovery queue
- 親と子のbudget

### 重要な境界

Workerが消えたことだけで、retryの権限を作りません。

外部への副作用がUNKNOWNの場合、Task Stateはreconciliationに回し、自動のretryを止めます。

同時に、冪等なTaskでも、retry budgetを越えて、無限に再実行しません。

### 持たせない権限

- Taskのsecurity classificationの変更
- Artifact data grantの発行
- Executor Admission
- Federationの変更

---

## SCH-1 — Scheduler / Placement Planner

### 位置付け

**Security Authorityではありません。**

### 役割

Policyの上で候補になり得るResourceの情報を使って、Placement Proposalを作ります。

### 利用できる情報の例

- Worker class
- 申告された、または検証されたcapabilityの要約
- 現在のcapacity
- localityとcacheのhint
- 予想される転送のbyte数
- deadline
- queueの圧力
- energyやproviderのavailabilityの信号
- 過去の運用上の指標

### 出力

Placement Proposalです。

### 禁止する権限

- Data Classを下げること
- Worker Assuranceを自己生成すること
- Artifact Grantを発行すること
- Executorを承認すること
- WorkOrderに署名すること
- Federationの設定を変更すること
- learned modelのscoreを、Authorizationに変換すること

Schedulerが侵害されると、availabilityや効率には影響し得ます。しかし、他のAuthorityの署名を偽造しない限り、Schedulerだけでは、Security requirementを迂回できない構造を、めざします。

---

## PLC-1 — Placement Authorization Authority

### 役割

Scheduler Proposalを、Security、Safety、Grantの制約に照らし合わせます。そして、そのTask AttemptをそのWorkerに置いてよいかを判断します。

### 主な入力

- Admitted Task
- Attempt Context
- Placement Proposal
- Worker Identity
- Resource Capability Evidence
- AssuranceとAttestation Result
- Compute Grantの制約
- データの局所性とnetwork policy
- Providerの状態

### 出力

Placement Approvalです。

### 重要な境界

Scheduler Proposalは、入力の一つにすぎません。

Schedulerが

> このWorkerは速い

と提案しても、Data ClassやAssuranceを満たさなければ、Placement Authorityは拒否します。

### 持たせない権限

- Artifact Capabilityの直接の発行
- Executor Admission
- Federationの変更
- WorkOrderの最終的な署名

Artifact / Data Authorityは、Placement Approvalだけを唯一のSecurity Evidenceとして、Dataを解放しません。必要なWorker IdentityとAssurance Evidenceを、独立に確認できる構造を選びます。

---

## LSE-1 — Lease Authority

### 役割

特定のTask Attemptを、特定のWorkerに、一定の時間だけ割り当てるLeaseを、発行し、renewし、revokeします。

### Leaseが結び付けるもの

- Task ID
- Attempt ID
- Worker ID
- lease IDとnonce
- 発行した時刻
- expiry
- renewalの制約

### 目的

- 切断されたWorkerの検出
- 古い割り当ての失効
- retryとrescheduleの境界
- replayの抑制

### Policy currentnessの境界

署名済みPlacement Approvalが、あるPolicy Revisionで正しく発行されていても、それだけでは新しいLeaseを発行しません。

LSE-1は、Lease予約の直前に、ADM-1が管理する現在のPolicy Stateを読みます。M0ではPolicy Stateを単調増加する`Policy Epoch`としてState Storeに永続化し、Lease予約も同じ永続化境界でexpected epochを再確認します。

~~~text
read current Policy(epoch=N, revision=P)
  ↓
verify Placement against P
  ↓
possible concurrent revoke / replace
  ↓
ReserveLease(expected policy epoch=N)
  ↓ same durable boundary
current epoch still N and P is active?
  ├─ no  -> fail closed
  └─ yes -> reserve FenceEpoch
~~~

Policy Revisionの文字列を読んだ事実だけを、freshnessの保証にはしません。Policyのrevokeや置換とLease reservationが競合した場合は、古いsnapshotを持つ側を失敗させます。

Policy revision identityは一度currentになったら再利用しません。`P1 -> P2 -> P1`のようにepochだけを進めて古いrevisionを復活させることもrollbackとして拒否します。過去のpolicy内容へ戻す必要がある場合は、その内容を新しいrevision identityとして発行します。これにより、revision identityが「どのpolicy snapshotか」を一意に指し続けます。

Policy Stateの読み取りと更新は、Admissionのwrite surfaceとは分離します。同じSQLite fileを使うReference Profileでも、ADM-2がPolicy Authorityを持つことにはしません。

既存のpre-Policy-Epoch Leaseは、履歴として読めても`PolicyEpoch=0`のunfenced stateです。新しい署名済みLease Authorityへ、そのまま昇格しません。

### 非目標

Leaseだけでは、exactly-onceを保証しません。

Side-effectのUNKNOWNやidempotencyは、Task Contract側の、別の概念です。

---

## DAT-1 — Artifact / Data Capability Authority

### 役割

ArtifactやDataを、誰が、どのTask Attemptで、どのWorkerから、どの操作として利用できるかを限定した、Capability / Grantとして発行します。

### 主な入力

- Artifact固有のclassification
- 要求しているPrincipalとTask
- Admitted Task
- Worker Identity
- Placement Approval
- Worker AssuranceとAttestation Evidence
- Lease
- organizationのdata policy

### Grantが結び付けるもの

- Artifact Digest
- Task ID
- Attempt ID
- Worker ID
- 許可する操作
- 任意の、byte、range、objectの範囲
- expiry
- Data Class
- policy revision

### 重要な境界

Workerは、Clientが指定したURLやpathを、Data Authorityとして扱いません。

~~~text
https://internal-service/
file:///etc/passwd
\\host\share
~~~

このような文字列だけでは、Artifactへのアクセスのauthorityは成立しません。

### 侵害されたときの被害の封じ込め

DAT-1が侵害されると、そのAuthorityが管理するData Scopeの漏えいのriskが、重大になります。

そのため、次のものを、Architectureを比べる対象にします。

- TenantとData Class scopeの分離
- 短い有効期間のGrant
- 最小権限
- audit
- keyの分離

一方で、DAT-1だけでは、Executor AdmissionやWorkOrderの署名まで取得できない構造にします。

---

## WOR-1 — WorkOrder Authority

### 役割

すでに独立したAuthorityで確定されているDecisionを、一つの変更できないexecution envelopeに、整合的に束ねます。そして、Workerに渡すWorkOrderに署名します。

### WorkOrderの前提

少なくとも、必要に応じて、次のものを参照または内包します。

- Admitted TaskとTaskIRのidentity
- TaskとAttemptのID
- Placement Approval
- Lease
- Executor ApprovalとExecutor Digest
- ArtifactとData Grant
- Resourceの上限
- Sandbox capability
- network policy
- side-effect profile
- policy revision
- expiryとnonce

### WOR-1がしてはいけないこと

足りないApprovalを、自分で作ってはいけません。

- Data Grantがないから、WOR-1がData accessを足す
- Executor Approvalがないから、WOR-1がDigestを承認する
- Placement Approvalがないから、WOR-1がWorkerを許可する

このような挙動を、禁止します。

### Worker側の再検証

Workerは、WorkOrderのenvelopeの署名だけを確認して、実行してはいけません。

WOR-1だけが侵害された場合のblast radiusを狭めるため、Worker-localのWorkOrder Verifierが、重要な前提のDecisionの、issuer、binding、freshnessを再検証します。

---

## ATV-1 — Attestation Verifier

### 役割

WorkerやExecution Environmentから得たRaw Evidenceを、Verifier Policyで評価して、正規化したAttestation Resultに変換します。

### 出力

- Attester / Workerのbinding
- Trust Domain
- 正規化したEnvironment Class
- Verifier ID
- Policy Revision
- expiry
- Evidenceのprovenance digest

### 非目標

ATV-1がEvidenceを受理しても、

> このTaskを、このWorkerに置いてよい

ということは、決まりません。

Placement Authorityが、Task固有のPolicyを再評価します。

Raw Evidenceを、SchedulerやClientに広く配布することも、標準にはしません。

---

## RVR-1 — Result Verification Authority

### 役割

Workerが返したResultClaimを検証します。

Task Profileに応じて、Adapterとして次のものを利用できます。

- 決定的な処理での、完全な一致
- アプリケーションのvalidator
- 独立した複製の実行
- 管理下のanchorによる再計算
- 暗号学的なproof
- attested executionのevidence

### 出力

正規化したVerification Resultです。

### 重要な境界

Worker Identityが正しいというだけでは、Resultが正しいとは認めません。

Verifier自身も、絶対に信頼できるものとはせず、Verifier Identity、Policy Revision、provenanceを追跡します。

高い保証が必要なTaskでは、複数のVerifierや、独立したanchorを要求する余地を残します。

---

## RAC-1 — Result Acceptance Authority

### 役割

Verification Resultを、Taskごとの、Result Assurance requirementに照らし合わせます。そして、ResultClaimをAccepted Resultに昇格させる、最終的なRelying Partyです。

### 主な入力

- Admitted Task
- ResultClaim
- Verification Resultの群
- Result Assurance requirement
- policy revision

### 出力

Accepted Result、または、明示的な未確定の状態です。

### Verificationと分ける理由

Proof VerifierがProofをvalidと判定しても、TaskがそのProof Systemを許可していない場合があります。

Attestation VerifierがEnvironmentをvalidと判定しても、Application Resultが意味の上で正しいかどうかは、別のことです。

そのため、

~~~text
Verifier accepted evidence
  !=
Task result accepted
~~~

という関係を、Architectureで維持します。

---

## EFF-1 — External Effect Confirmation Authority

### 役割

外部への副作用が、実際に成立したかを確認できるAuthorityです。

これは、EKI内部のComponentとは限りません。

たとえば、次のものがあります。

- 外部APIのoperation statusとreceipt
- managed effect adapter
- databaseのtransaction record
- brokerのdeduplication record

### 重要な境界

Workerが、

> 成功した
> 失敗した

と自己申告しただけで、外部の世界の事実を確定しません。

冪等でないeffectでReceiptを得られない場合、Task StateはUNKNOWNを維持します。

---

## CON-1 — Contribution Authority

### 役割

Resource Measurement Evidence、Work Context、必要に応じてAccepted Resultを照合して、署名済みのContribution Recordを発行します。

### 出力

価値に換算する前の、Contribution Recordです。

### 持たせない権限

- EKI Currencyの発行
- 法定通貨への換算
- market priceの形成
- Local Governorのoverride

~~~text
Contribution Record
  != Credit value
  != money
~~~

価値への換算は、外部のPolicyに残します。

---

# 6. Worker-localのTrust Boundary

中央のControl Planeが正しくても、Workerはmaliciousであったり、改変されていたりし得ます。

逆に、業務のEndpointでは、中央のComponentが誤動作する可能性もあります。

そのため、Worker側にも、明示的な境界を置きます。

## WEV-1 — WorkOrder Verifier

### 実行の直前に確認するもの

- WorkOrder Authorityの署名
- TaskとAttemptのbinding
- Placement Approval
- Leaseのexpiryと、Workerとのbinding
- Executor Approvalと、実際のExecutor Digest
- Artifact Grantと、実際のArtifact Digest
- ResourceとCapabilityの上限
- network policy
- 関連するPolicy Revisionとexpiry

未知のAuthority、期限切れのDecision、subjectの不一致は、fail closedします。

## GOV-1 — Local Governor

### 役割

次の条件に応じて、中央が許可したcapacityを、さらに狭めます。

- user opt-out
- userの復帰
- CPU / Memory / GPU / Networkの上限
- BatteryかACか
- Thermal
- 時間帯
- device safety

### 権限の方向

Local Governorが持つのは、capacityを減らす拒否権です。

Organization PolicyやHardware Safetyが許可していないcapacityを、増やすAuthorityにはしません。

## SBX-1 — Sandbox / Capability Enforcer

Executorは、Admission済みであっても、untrustedなprocessとして扱います。

Default denyを基本にして、WorkOrderで必要なCapabilityだけを渡します。

次のものを分離します。

- filesystem
- network
- deviceとGPU
- process
- temporary storage
- secret
- artifactのinputとoutput

Executor Admissionが侵害されても、Sandbox capabilityまで自動的に拡大しない構造を、めざします。

---

# 7. Runtime Trust Flow

標準的なTask lifecycleを、Authorityの流れとして表すと、次のようになります。

~~~text
Untrusted Client
    │
    ▼
IDV-1 Identity Verification
    │ Principal Context
    ▼
ADM-2 Task Admission
    │ Admitted Task / TaskIR
    ▼
TSK-1 Task State
    │ Attempt Context
    ├───────────────────────────────┐
    │                               │
    ▼                               ▼
SCH-1 Scheduler                 EXE-1 Executor Approval
    │ Proposal                       │
    ▼                                │
PLC-1 Placement Authorization ◄──────┤
    │ Placement Approval             │
    ▼                                │
LSE-1 Lease                          │
    │                                │
    ├──────────────► DAT-1 Data Capability
    │                     │ Grants
    └──────────────┬──────┘
                   ▼
             WOR-1 WorkOrder
                   │
                   ▼
          Worker WEV-1 Verifier
                   │
             GOV-1 Local Veto
                   │
             SBX-1 Sandbox
                   │
                   ▼
              ResultClaim
                   │
             RVR-1 Verify
                   │
             RAC-1 Accept
                   │
             AcceptedResult
                   │
        optional CON-1 Contribution
~~~

Remote Attestationを使うTaskでは、ATV-1のAttestation Resultが、PLC-1やDAT-1の入力になります。

External Side Effectを持つTaskでは、EFF-1のReceiptが、TSK-1のRecoveryの判断に戻ります。

---

# 8. Decision Objectに共通する性質

Wire schemaは、後続のTask Contractで決めます。ただし、Security上重要なDecisionには、共通して次のものを持たせる方向を、Baselineとします。

- issuer identity
- decision typeとschema version
- decision ID
- subjectのIDとdigest
- policy revision
- issued at
- expiry、または明示的なlifetime
- 任意のnonceやsequence
- 暗号学的なintegrityとauthenticity

必要なDecisionでは、revocationも扱います。

「署名が正しい」ことだけでなく、次のことも検証します。

- 正しいissuerか
- 正しいsubjectか
- 現在も有効か
- 現在のPolicyで受理できるか

---

# 9. 永続的な状態に対するAuthority

すべてのComponentが、同じDatabase userで、すべてのTableを読み書きできる構成は、標準にはしません。

論理的には、次のように分けます。

| Authority / Component | 主に書ける状態 | 原則として読み取り専用、または不要な状態 |
| --- | --- | --- |
| ADM-2 Task Admission | admitted task, quota reservation | raw artifact bytes, federation admin keys |
| TSK-1 Task State | task / attempt / recovery | executor trust rootの変更 |
| SCH-1 Scheduler | 原則としてproposalとcacheのみ | artifact secret, signing key |
| PLC-1 Placement | placement decision | raw artifact bytes |
| LSE-1 Lease | lease state | executor admission policyの変更 |
| DAT-1 Data Authority | grants / data access audit | scheduler model state |
| WOR-1 WorkOrder | workorder envelope / dispatch audit | federation configurationの変更 |
| EXE-1 Executor Admission | executor approvals / revocation | task scheduling state |
| RVR-1 Verification | verification result | artifact grantの発行 |
| RAC-1 Acceptance | accepted result state | federation / executor admin |
| CON-1 Contribution | contribution record | Task security classificationの変更 |
| FED-1 Federation Admin | federation config | task / result state |

物理的なDatabaseを共有する場合も、Repository、transaction、credentialの境界を分けられる設計を、優先します。

---

# 10. Compromise Matrix

この表は、「侵害されても安全」という意味ではありません。

一つのComponentが侵害されても、別のAuthorityまで自動的に獲得できないことを確認するための、設計の目標です。

| 侵害の対象 | 攻撃者が起こし得ること | 単独では起こせないようにすること |
| --- | --- | --- |
| Client | 自分のPrincipalの範囲で、悪意のあるrequestやquotaの消費を試みる | 任意のWorkerの指定、任意のcodeの実行、他のPrincipalのDataの取得 |
| Identity Provider / IDV-1 | 対象のTrust Domain内で、Principalをなりすます | WorkerのWorkOrderの署名、Artifact Grant、Executor Approval |
| ADM-2 Admission | 不適切なTaskをAdmitする、quotaの判断を壊す | Artifact固有のclassの低下、Executor Approval、WorkOrderの単独の発行 |
| TSK-1 Task State | retry、cancel、stateを乱す、availabilityを壊す | Data Grant、Executor Admission、Federationの変更 |
| SCH-1 Scheduler | 悪い候補を提案する、性能を劣化させる、starvationを起こす | Security requirementの低下、Data Grant、WorkOrderの署名 |
| PLC-1 Placement | 不適切なWorkerを承認し得る | Attestationの偽造、Artifact Authorityのkey、Executor Admission |
| LSE-1 Lease | 重複した、または古いlease、availabilityの混乱 | Dataを読む権限、Executorのcodeの承認 |
| DAT-1 Data Authority | 管理するScope内のDataの漏えい、過剰なGrant | Executor Admission、Federationの設定、WorkOrderの前提のDecisionの偽造 |
| EXE-1 Executor Admission | 悪意のあるExecutorを承認する | Data Grant、Placement、Sandbox capabilityの自動的な拡大 |
| WOR-1 WorkOrder | 不正なEnvelope、replayを試みる | 他のAuthorityの署名による、有効な前提のDecisionの生成 |
| ATV-1 Attestation Verifier | 偽のEnvironment Result | Artifact Grant、Task Admission、Resultのsemanticな検証 |
| RVR-1 Result Verifier | 偽のVerification Result | Program Admission、Data Grant、Contributionの価値の決定 |
| RAC-1 Result Acceptance | 不正なResultをAcceptedに昇格する | Workerの実行権限、Data Grant |
| EFF-1 Effect Authority | 外部のeffectのstatusを偽る | EKIのData / Executor / Federation Authority |
| CON-1 Contribution | 偽のContribution Record | EKI通貨の発行、Local Governorのoverride、Taskの実行 |
| FED-1 Federation Admin | 外部のIdentityのtrustを誤って設定する | Resource Authorizationの自動的な付与 |
| GOV-1 / 改変されたAgent | 自分のEndpointの制御と計測を偽る | Control Planeの署名、他のWorker Identity、Accepted Resultの署名 |

## 10.1 特に影響の大きい侵害

次のものは、大きなblast radiusを持ち得ます。

- Trust / Policy Administration
- Artifact / Data Authority
- Executor Admission Authority
- Result Acceptance Authority
- Federation Administration

これらは、SchedulerやTelemetryのように、複雑で頻度の高いComponentと、同じCredentialを共有しないことを優先します。

## 10.2 Rootの侵害

Organizationのroot signing key、host OSとkernel、hardwareのroot、Verifierのrootなどまで、全面的に侵害された場合は、通常の論理的な分離だけでは防げない範囲があります。

これを隠して、「完全に安全」とは表現しません。

代わりに、次の手段によって、blast radiusを狭める設計を、比べます。

- keyの分離
- 短い有効期間の、派生したauthority
- rotationとrevocation
- offlineやHSMなどによる保護
- multi-party approvalの候補
- 独立した検証

---

# 11. 独立した再確認の原則

一つのAuthorityの出力を、後段が無条件には信頼しない箇所を、明示します。

## Data Release

DAT-1は、Placement Approvalだけを見て、Dataを解放しません。

少なくとも、次のものを独立に確認できる情報を持ちます。Artifact classification、Task binding、Worker Identity、必要なAssurance、Lease。

## WorkOrder

WOR-1は、前提のDecisionを束ねます。そして、Worker側のWEV-1が、重要な署名とbindingを再確認します。

## Result Acceptance

RAC-1は、RVR-1が「valid」としたことだけでなく、次のことも確認します。そのVerifierが、Task Policyで受理できるか。Resultが、Task、Attempt、Input、Programなどに結び付けられているか。

## Contribution

CON-1は、Accepted Resultだけから、Resourceの量を作りません。

Measurement Observer、Assurance、Work Contextを、別に確認します。

この再確認は、すべてのComponentが同じPolicy engineを重複して実装する、という意味ではありません。

目的は、「侵害された一つの要約値だけで、後段の価値の高いAuthorityを無条件に動かさない」ことです。

---

# 12. Data Minimization

Authorityを分離することは、Privacyとも関係します。

各Componentに、必要以上の情報を渡さないことを、めざします。

## Scheduler

原則として、次のものを使います。

- Resourceの要約
- capabilityの要約
- Artifactのlocality hint
- 転送のsize
- deadlineとpriority
- policyで許可された、運用上の履歴

Artifactの本文、Clientのsecret、Raw Attestation Evidenceは、標準の入力にしません。

## Attestation / Verification

Raw Evidenceは、必要なVerifierの境界の中に閉じます。SchedulerやClientには、正規化した結果だけを渡せる設計にします。

## Contribution

Contribution Recordに、userのactivityのraw telemetryを直接埋め込むことを、避けます。

必要なのは、Resource Measurementの根拠とWork Contextです。個人の行動の詳細そのものではありません。

## Client

Clientには、Workerのhostname、IP、ログインしているuser、インストールされているsoftware、cacheの一覧などを返しません。

Task statusは、必要最小限の、queued、running、completed、failed、unknownなどに縮約します。

---

# 13. Deployment Profileへの投影

Authority Mapは、すべてのDeploymentで同じ論理構造を保ちます。物理的な構成は、規模に見合ったものにします。

## 13.1 Small Managed Organization

たとえば、次の構成でも成り立たせます。

~~~text
1 Control Plane binary
1 database
several endpoint agents
~~~

ただし、Binaryの内部では、Authorityごとのinterfaceとkey handleを分けます。

専用のKafka、SPIRE、HSM、複数のMicroserviceを、必須にはしません。

## 13.2 Enterprise Mixed Endpoints

次のものを利用できます。

- 企業のIdPとPKIとの統合
- 分離したArtifact Store
- より強いkeyの保護
- 価値の高い管理の経路の分離
- proxyとoutbound-onlyのworker
- 管理されたendpointのpolicy

## 13.3 Browser / Ad-hoc Participation

次のことを基本にします。

- 短い有効期間のidentity
- 信頼の低い、使い捨てのWorker profile
- 公開または低機密のData
- 制限されたExecutor
- 積極的なlease expiry
- ローカルのsecretを持たない
- 明示的な参加

Browser Workerが存在するからといって、managed Worker側のSecurityを弱めません。

## 13.4 Federation / Public-interest Grant

Federation IdentityとResource Authorizationを分離します。そして、Compute Grantの、scope、Beneficiary、Data Class、期間、preemptionを、明示します。

Contribution Recordは、必要に応じて外部の制度に渡せます。ただし、EKI Coreは、価値の制度を持ちません。

---

# 14. Key / Credentialの基本

具体的なKMSやPKIの方式は、後続で比較します。論理的な原則は、次のとおりです。

1. Client、Worker、Control Plane AuthorityのCredentialを分ける。
2. Authorityごとに、issuer identityを識別できる。
3. Productionでは、異なる価値の高いAuthorityが、同じsigning credentialを共有しないことを、標準とする。
4. 短い有効期間のdecisionは、短いexpiryを持てる。
5. keyのrotationとrevocationを扱える。
6. Trust Rootを、通常のrequestで差し替えられない。
7. signing private keyを、Scheduler、parser、untrustedなexecutorに渡さない。
8. Devとtestのprofileで、簡易なkeyを使っても、Authority identity自体は分離してTestする。

---

# 15. Auditの基本

Security上、重要なAuthorityの遷移は、少なくとも、後から関連付けられる必要があります。

たとえば、次のものがあります。

- Task Admission
- quotaとbudgetの予約
- Placement Approvalとrejection
- Leaseのissue、expiry、revoke
- Artifact Grantのissueとdeny
- Executor Approvalとrevoke
- WorkOrderのissue
- Result Verification
- Result Acceptance
- Federation configの変更
- Contribution Recordのissue
- Local Governorがreclaimした理由

Audit eventは、可能な限り、Task、Attempt、DecisionのID、Policy Revisionに結び付けます。

ただし、Auditのために、Raw secret、Raw Attestation Evidence、不要なPersonal Dataを集約しません。

Audit Store自体の、改ざんへの耐性は、後続の設計項目です。

---

# 16. 未決事項

このBaselineでは、次のことを、まだ固定していません。

- Authorityごとの、Processとservice数
- HTTPやgRPCなどの、内部のtransport
- PKI、KMS、HSMの製品
- OPAやCedarなどの、Policy engine
- decisionの正確なwire schema
- QueueとDB
- Artifact Store
- multi-party approval
- audit logの暗号学的なchaining
- keyless signature
- TEEのvendor
- zkVM
- 複数のverifierによるquorum

後続のArchitecture Work Unitで、RequirementとQuality Attributeに照らして比べます。

---

# 17. 次のArchitecture Work Unitへの入力

このAuthority Mapを前提に、次は、External / Internal Contract Boundaryを具体化します。

優先するConceptは、次のとおりです。

- TaskRequest
- Admitted Task / TaskIR
- Attempt
- PlacementProposal
- PlacementApproval
- Lease
- ExecutorApproval
- ArtifactGrant
- WorkOrder
- ResultClaim
- VerificationResult
- AcceptedResult
- ContributionRecord

次の作業では、これらを、単なるGoのstructの集合として先に固定することは、しません。Contractごとに、次のことを定義します。

1. 誰が生成できるか
2. 誰が検証するか
3. 何に結び付けるか
4. どのFieldをClientが制御できるか
5. versionとextensionをどう扱うか
6. replay、古い情報、downgradeを、どう防ぐか
