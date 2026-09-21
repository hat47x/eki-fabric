# Authority / Trust Boundary Map

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricでは、Client、Worker、Control Planeのいずれも「同じSystemの一部だから信頼する」とは考えません。

特にControl Plane内部は、一つの巨大な完全信頼領域として扱うと、SchedulerやParserのような複雑なComponentが侵害されたときに、Artifactの読出し、Executor承認、Federation設定変更、WorkOrder発行まで一度に奪われる危険があります。

そこでArchitectureの最初に、Process数や採用製品より先に、

- 誰が事実を検証するか
- 誰がSecurity上の判断を下すか
- 誰がCapabilityや実行権限を発行できるか
- その判断を誰が再検証するか
- 一つのComponentが侵害されたとき、どこまで影響が届くか

を定義します。

この文書は、その論理的なAuthorityとTrust BoundaryのBaselineです。

関連する正本:

- ../requirements/PRODUCT_REQUIREMENTS.md
- ../requirements/THREAT_MODEL.md
- ../requirements/ASSURANCE_MODEL.md
- ../requirements/QUALITY_ATTRIBUTES.md
- ../plan/ARCHITECTURE_ENTRY_REVIEW_2026-09-21.md

## 2. AuthorityはService数ではない

この文書でいうAuthorityとは、Security上意味のある事実を確定したり、権限を発行したり、状態を変更したりできる論理的な役割です。

一つのAuthorityを必ず一つのMicroserviceにする、という意味ではありません。

小規模構成では、

~~~text
eki-control
  Admission
  Task State
  Scheduler
  Placement Guard
  Lease
  WorkOrder Assembly
~~~

を一つのBinaryへ同居させても構いません。

ただし同居していても、次は分けます。

- Authority identity
- signing key / key handle
- Policy input
- persistent stateへのwrite authority
- 呼び出せるInterface
- Audit event
- failure / compromiseの影響範囲

Production Profileでは、意味の異なるAuthorityが同一の署名Credentialを共有することを標準にはしません。

同じProcessに同居する場合も、Scheduler moduleがExecutor Admission keyやArtifact Capability keyを直接利用できる設計にはしません。

## 3. 基本原則

### 3.1 AuthenticationはAuthorityを増やさない

認証済みClientも悪意を持ち得ます。

認証は、

> このrequestがどのPrincipalから来たか

を確認するだけです。

それだけで、

- 任意Executorの実行
- 任意Artifactの読出し
- 具体Workerの指定
- Federation設定変更
- Resource Budget超過

を許可しません。

### 3.2 SchedulerはPolicy Authorityではない

Schedulerは性能・locality・deadline等を使って候補を順位付けします。

Schedulerが返すのはPlacement Proposalです。

Proposalは、

> このWorkerがよさそうだ

という提案であり、

> このWorkerへ置いてよい

というSecurity Authorizationではありません。

Issue #19のlearned estimatorを将来利用する場合も同じです。

~~~text
model score
  != permission
predicted reliability
  != Worker Assurance
~~~

です。

### 3.3 WorkOrder署名だけを万能なTrust Tokenにしない

WorkOrder Authorityは最終Envelopeへ署名しますが、独力でTask semantics、Data authority、Executor approval、Placement authorizationを作りません。

WorkerはWorkOrder署名だけでなく、WorkOrderが参照する前提Decisionのissuer、subject、digest、policy revision、expiryを再検証します。

したがってWorkOrder signing keyの単独侵害が、他Authorityの権限を自動獲得する構造を避けます。

### 3.4 Authority結果は対象へbindingする

Security上意味のあるDecisionは、少なくとも必要な範囲で次へbindingします。

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

別Taskや別Worker向けの正しい署名を流用できないようにします。

### 3.5 Trust Rootをcallerに選ばせない

検証用公開鍵やTrust Rootを通常requestのparameterとして渡し、

> 今回はこの鍵を信じてください

と差し替えられるAPIを作りません。

AuthorityのTrust Rootは、別の管理経路で設定・rotationします。

### 3.6 Local Governorは中央Authorityに対するVetoを持つ

Worker側のLocal Governorは、Central Schedulerがcapacityを使いたいと判断しても、より厳しい上限を適用できます。

~~~text
effective capacity
  = min(
      organization permitted capacity,
      provider/user willingness,
      local safety judgement
    )
~~~

中央側には、このVetoを無効化する通常実行権限を持たせません。

---

# 4. Authorityの分類

EKIのAuthorityを、性質の違いから次の四群に分けます。

## 4.1 Administrative Authorities

頻繁なTask実行経路から分離する高価値な設定Authorityです。

- Trust / Policy Administration
- Federation Administration
- Executor Admission Administration

## 4.2 Runtime Decision Authorities

通常のTask lifecycleでSecurity上の判断やCapabilityを発行します。

- Task Admission Authority
- Task State / Recovery Authority
- Placement Authorization Authority
- Lease Authority
- Artifact / Data Capability Authority
- WorkOrder Authority

## 4.3 Verification / Acceptance Authorities

Workerや外部主体が出したClaimを、そのまま事実へ昇格させないためのAuthorityです。

- Attestation Verifier
- Result Verification Authority
- Result Acceptance Authority
- External Effect Confirmation Authority
- Contribution Authority

## 4.4 Worker-local Enforcement

中央Decisionを受け取った後でも、実行直前・実行中に境界を守ります。

- WorkOrder Verifier
- Local Governor
- Sandbox / Capability Enforcer

Scheduler、Learned Estimator、Telemetry Collector、Cache locality estimator等は重要Componentですが、それ自体にはSecurity Authorityを与えないことを標準とします。

---

# 5. Authority一覧

## ADM-1 — Trust / Policy Administration

### 役割

EKI内部Authorityが信頼するissuer、key、policy revision等の管理Rootです。

例:

- Task Admission Authorityのtrust root
- WorkOrder Authorityのtrust root
- Verification Authorityのtrust root
- Organization policy version
- key rotation / revocation

### 持てる権限

- Authority trust configurationの更新
- key rotation
- policy activation / rollback
- emergency revocation

### 持たせない権限

- ClientとしてTaskをsubmitする
- Workerを直接選ぶ
- Artifactを直接読む
- ResultをAcceptedへする
- Contribution量を生成する

### 注意

このAuthorityの侵害は影響が大きいため、通常Task execution pathから分離します。

将来、high-value変更へmulti-party approvalを導入する余地を残しますが、Baseline 0.1では方式を固定しません。

---

## FED-1 — Federation Administration

### 役割

外部Trust DomainとのIdentity検証関係を明示的に構成します。

### 入力

- Trust Domain
- Trust Material / Bundle
- version / sequence
- federation policy
- administrative approval

### 出力

- versioned Federation Configuration

### 持たせない権限

- Federationした外部Principalへ自動的にTask権限を付与する
- Artifact accessを自動許可する
- Contribution価値を決める
- 推移的Trustを自動生成する

~~~text
Identity can be verified
  !=
Resource access is authorized
~~~

です。

Federation Trust Binding Spikeの原則を維持します。

---

## EXE-1 — Executor Admission Authority

### 役割

Worker上で実行可能なExecutor Artifactを審査し、実行可能なDigest、Runtime Class、Provenance条件を確定します。

### 主なEvidence

- Artifact Digest / Size / Media Type
- Publisher Evidence
- Build Provenance
- Source / Commit
- SBOM / scan等のoptional evidence
- Runtime Class

### 出力

概念上のExecutor Approval。

### 持たせない権限

- Taskをsubmitする
- 具体Workerを選ぶ
- Artifact input dataを読む権限を与える
- WorkOrderを単独発行する

正しく署名されたArtifactやProvenanceも、Executor Admissionそのものではありません。

---

## IDV-1 — Identity Verification

### 役割

Client / Worker / Workload / Administrative PrincipalのIdentityを検証します。

OIDC、enterprise PKI、SPIFFE等はAdapter候補です。

### 出力

normalized Principal Context。

### 重要な境界

Identity VerificationはAuthenticationの根拠です。

Authorization、Result correctness、Worker Assuranceを自動的には作りません。

Client Identity、Worker Identity、Control Plane Workload Identityは別namespace / roleとして扱います。

---

## ADM-2 — Task Admission Authority

### 役割

外部から来たuntrusted TaskRequestを、Fabric内部で扱ってよいAdmitted Taskへ変換します。

### 主な入力

- authenticated Principal Context
- strict external TaskRequest
- organization policy
- quota / budget
- Artifact metadata
- Executor logical requirement
- side-effect semantics
- requested security / data class

### 主な処理

- schema validation
- authorization
- quota / budget reservation
- effective security classification
- retry / fan-out budget
- side-effect contract validation
- allowed Executor requirementの確定
- unknown / missing security fieldを安全側へ処理

Clientが指定したData Classは下限ではなく、他の根拠と合わせて安全側へ計算します。

例:

~~~text
effective data class
  >= artifact intrinsic class
  >= organization policy minimum
  >= executor requirement
  >= client requested class
~~~

Clientが値を小さくしてSecurityを弱めることはできません。

### 出力

概念上のAdmitted Task / TaskIR Approval。

### 持たせない権限

- 具体Workerの選択
- Executor Artifactの新規承認
- Artifact capabilityの発行
- Federation変更
- WorkOrder最終署名

---

## TSK-1 — Task State / Recovery Authority

### 役割

TaskのlifecycleとAttemptを管理します。

### 管理するもの

- Task state
- Attempt ID
- retry count / budget
- cancellation
- deadline
- UNKNOWN side-effect outcome
- mass reclaim後のrecovery queue
- parent / child budget

### 重要な境界

Worker disappearanceだけでretry権限を作りません。

外部副作用がUNKNOWNなら、Task Stateはreconciliationへ送り、自動retryを止めます。

同時に、idempotent Taskでもretry budgetを越えて無限再実行しません。

### 持たせない権限

- Task security classificationの変更
- Artifact data grantの発行
- Executor Admission
- Federation変更

---

## SCH-1 — Scheduler / Placement Planner

### 位置付け

**Security Authorityではありません。**

### 役割

Policy上候補になり得るResource情報を使い、Placement Proposalを作ります。

### 利用できる情報の例

- Worker class
- declared / verified capability summary
- current capacity
- locality / cache hint
- expected transfer bytes
- deadline
- queue pressure
- energy / provider availability signal
- historical operational metrics

### 出力

Placement Proposal。

### 禁止する権限

- Data Classを下げる
- Worker Assuranceを自己生成する
- Artifact Grantを発行する
- Executorを承認する
- WorkOrderを署名する
- Federation設定を変更する
- learned modelのscoreをAuthorizationへ変換する

Scheduler compromiseはavailabilityや効率へ影響し得ますが、他Authorityの署名を偽造しない限り、Security requirementを単独で迂回できない構造を目指します。

---

## PLC-1 — Placement Authorization Authority

### 役割

Scheduler ProposalをSecurity / Safety / Grant上の制約へ照合し、そのTask AttemptをそのWorkerへ置いてよいかを判断します。

### 主な入力

- Admitted Task
- Attempt Context
- Placement Proposal
- Worker Identity
- Resource Capability Evidence
- Assurance / Attestation Result
- Compute Grant constraints
- Data locality / network policy
- Provider state

### 出力

Placement Approval。

### 重要な境界

Scheduler Proposalは入力の一つにすぎません。

Placement Authorityは、Schedulerが

> このWorkerは速い

と提案しても、Data ClassやAssuranceを満たさなければ拒否します。

### 持たせない権限

- Artifact Capabilityの直接発行
- Executor Admission
- Federation変更
- WorkOrder最終署名

Artifact / Data Authorityは、Placement Approvalだけを唯一のSecurity EvidenceとしてDataを解放しません。必要なWorker Identity / Assurance Evidenceを独立に確認できる構造を選びます。

---

## LSE-1 — Lease Authority

### 役割

特定Task Attemptを特定Workerへ一定時間割り当てるLeaseを発行・renew・revokeします。

### Leaseがbindingするもの

- Task ID
- Attempt ID
- Worker ID
- lease ID / nonce
- issued time
- expiry
- renewal constraints

### 目的

- disconnected Workerの検出
- stale assignmentの失効
- retry / reschedule境界
- replay抑制

### 非目標

Leaseだけでexactly-onceを保証しません。

Side-effect UNKNOWNやidempotencyはTask Contract側の別概念です。

---

## DAT-1 — Artifact / Data Capability Authority

### 役割

ArtifactやDataを、誰が、どのTask Attemptで、どのWorkerから、どの操作として利用できるかを限定したCapability / Grantとして発行します。

### 主な入力

- Artifact intrinsic classification
- requesting Principal / Task
- Admitted Task
- Worker Identity
- Placement Approval
- Worker Assurance / Attestation Evidence
- Lease
- organization data policy

### Grantがbindingするもの

- Artifact Digest
- Task ID
- Attempt ID
- Worker ID
- allowed operation
- optional byte / range / object scope
- expiry
- Data Class
- policy revision

### 重要な境界

WorkerはClient supplied URLやpathをData Authorityとして扱いません。

~~~text
https://internal-service/
file:///etc/passwd
\\host\share
~~~

のような文字列だけではArtifact access authorityが成立しません。

### Compromise containment

DAT-1が侵害されると、そのAuthorityが管理するData Scopeの漏えいriskは重大です。

そのため、

- Tenant / Data Class scopeの分離
- short-lived Grant
- least privilege
- audit
- key separation

をArchitecture比較対象にします。

一方、DAT-1単独ではExecutor AdmissionやWorkOrder署名まで取得しない構造にします。

---

## WOR-1 — WorkOrder Authority

### 役割

すでに独立Authorityで確定されたDecisionを、一つのimmutable execution envelopeへ整合的に束ね、Workerへ渡すWorkOrderへ署名します。

### WorkOrderの前提

少なくとも必要に応じて次を参照・内包します。

- Admitted Task / TaskIR identity
- Task / Attempt ID
- Placement Approval
- Lease
- Executor Approval / Executor Digest
- Artifact / Data Grants
- Resource limits
- Sandbox capability
- network policy
- side-effect profile
- policy revisions
- expiry / nonce

### WOR-1がしてはいけないこと

不足するApprovalを自分で作りません。

- Data GrantがないからWOR-1がData accessを足す
- Executor ApprovalがないからWOR-1がDigestを承認する
- Placement ApprovalがないからWOR-1がWorkerを許可する

といった挙動を禁止します。

### Worker側の再検証

WorkerはWorkOrder envelope署名だけを確認して実行してはいけません。

WOR-1 compromise単独のblast radiusを狭めるため、Worker-local WorkOrder Verifierは重要な前提Decisionのissuer / binding / freshnessを再検証します。

---

## ATV-1 — Attestation Verifier

### 役割

Worker / Execution Environmentから得たRaw Evidenceを、Verifier Policyで評価し、normalized Attestation Resultへ変換します。

### 出力

- Attester / Worker binding
- Trust Domain
- normalized Environment Class
- Verifier ID
- Policy Revision
- expiry
- Evidence provenance digest

### 非目標

ATV-1がEvidenceを受理しても、

> このTaskをこのWorkerへ置いてよい

とは決まりません。

Placement AuthorityがTask固有Policyを再評価します。

Raw EvidenceをSchedulerやClientへ広く配布することも標準にはしません。

---

## RVR-1 — Result Verification Authority

### 役割

Workerが返したResultClaimを検証します。

Task Profileに応じて、Adapterとして次を利用できます。

- deterministic equality
- application validator
- independent replication
- managed anchor recomputation
- cryptographic proof
- attested execution evidence

### 出力

normalized Verification Result。

### 重要な境界

Worker Identityが正しいだけではResult correctnessを認めません。

Verifier自身も絶対信頼とはせず、Verifier Identity / Policy Revision / provenanceを追跡します。

高保証Taskでは複数Verifierやindependent anchorを要求する余地を残します。

---

## RAC-1 — Result Acceptance Authority

### 役割

TaskごとのResult Assurance requirementへVerification Resultを照合し、ResultClaimをAccepted Resultへ昇格する最終Relying Partyです。

### 主な入力

- Admitted Task
- ResultClaim
- Verification Result群
- Result Assurance requirement
- policy revision

### 出力

Accepted Resultまたは明示的な未確定状態。

### なぜVerificationと分けるか

Proof VerifierがProofをvalidと判定しても、TaskがそのProof Systemを許可していない場合があります。

Attestation VerifierがEnvironmentをvalidと判定しても、Application Resultの意味的正しさは別です。

したがって、

~~~text
Verifier accepted evidence
  !=
Task result accepted
~~~

をArchitectureで維持します。

---

## EFF-1 — External Effect Confirmation Authority

### 役割

外部副作用が実際に成立したかを確認できるAuthorityです。

これはEKI内部Componentとは限りません。

例:

- 外部APIのoperation status / receipt
- managed effect adapter
- database transaction record
- broker deduplication record

### 重要な境界

Workerが

> 成功した
> 失敗した

と自己申告しただけで外部世界の事実を確定しません。

non-idempotent effectでReceiptを得られない場合、Task StateはUNKNOWNを維持します。

---

## CON-1 — Contribution Authority

### 役割

Resource Measurement Evidence、Work Context、必要に応じたAccepted Resultを照合し、署名済みContribution Recordを発行します。

### 出力

価値換算前のContribution Record。

### 持たせない権限

- EKI Currency発行
- 法定通貨換算
- market price形成
- Local Governor override

~~~text
Contribution Record
  != Credit value
  != money
~~~

です。

価値換算は外部Policyへ残します。

---

# 6. Worker-local Trust Boundary

中央Control Planeが正しくても、Workerはmalicious / modifiedであり得ます。

逆に、業務Endpointでは中央Componentが誤動作する可能性もあります。

そのためWorker側にも明示的な境界を置きます。

## WEV-1 — WorkOrder Verifier

### 実行直前に確認するもの

- WorkOrder Authority署名
- Task / Attempt binding
- Placement Approval
- Lease expiry / Worker binding
- Executor Approval / actual Executor Digest
- Artifact Grant / actual Artifact Digest
- Resource / Capability limits
- network policy
- relevant Policy Revision / expiry

未知のAuthority、期限切れDecision、subject mismatchはfail closedします。

## GOV-1 — Local Governor

### 役割

- user opt-out
- user return
- CPU / Memory / GPU / Network上限
- Battery / AC
- Thermal
- time window
- device safety

に応じて、中央が許可したcapacityをさらに狭めます。

### 権限の方向

Local Governorはcapacityを減らすVetoを持ちます。

Organization PolicyやHardware Safetyが許可していないcapacityを増やすAuthorityにはしません。

## SBX-1 — Sandbox / Capability Enforcer

Executorは、Admission済みでもuntrusted processとして扱います。

Default denyを基本に、WorkOrderで必要なCapabilityだけを渡します。

- filesystem
- network
- device / GPU
- process
- temporary storage
- secret
- artifact input / output

を分離します。

Executor Admissionが侵害されても、Sandbox capabilityまで自動的に拡大しない構造を目指します。

---

# 7. Runtime Trust Flow

標準的なTask lifecycleを、Authorityの流れとして表すと次のようになります。

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

Remote Attestationを使うTaskでは、ATV-1のAttestation ResultがPLC-1やDAT-1の入力になります。

External Side Effectを持つTaskでは、EFF-1のReceiptがTSK-1のRecovery判断へ戻ります。

---

# 8. Decision Objectの共通性質

Wire schemaは後続のTask Contractで決めますが、Security-sensitiveなDecisionには共通して次を持たせる方向をBaselineとします。

- issuer identity
- decision type / schema version
- decision ID
- subject IDs / digests
- policy revision
- issued at
- expiryまたは明示的lifetime
- optional nonce / sequence
- cryptographic integrity / authenticity

必要なDecisionではrevocationも扱います。

「署名が正しい」だけでなく、

- 正しいissuerか
- 正しいsubjectか
- 現在も有効か
- 現Policyで受理可能か

を検証します。

---

# 9. Persistent StateへのAuthority

すべてのComponentが同じDatabase userで全Tableへread/writeできる構成を標準にはしません。

論理的には次のように分けます。

| Authority / Component | 主に書けるState | 原則read-onlyまたは不要なState |
| --- | --- | --- |
| ADM-2 Task Admission | admitted task, quota reservation | raw artifact bytes, federation admin keys |
| TSK-1 Task State | task / attempt / recovery | executor trust root変更 |
| SCH-1 Scheduler | 原則proposal / cacheのみ | artifact secret, signing key |
| PLC-1 Placement | placement decision | raw artifact bytes |
| LSE-1 Lease | lease state | executor admission policy変更 |
| DAT-1 Data Authority | grants / data access audit | scheduler model state |
| WOR-1 WorkOrder | workorder envelope / dispatch audit | federation configuration変更 |
| EXE-1 Executor Admission | executor approvals / revocation | task scheduling state |
| RVR-1 Verification | verification result | artifact grant発行 |
| RAC-1 Acceptance | accepted result state | federation / executor admin |
| CON-1 Contribution | contribution record | Task security classification変更 |
| FED-1 Federation Admin | federation config | task / result state |

物理Databaseを共有する場合も、Repository / transaction / credential boundaryを分けられる設計を優先します。

---

# 10. Compromise Matrix

この表は「侵害されても安全」という意味ではありません。

一つのComponent compromiseが、別Authorityまで自動的に獲得しないことを確認するための設計目標です。

| 侵害対象 | 攻撃者が起こし得ること | 単独では起こせないようにすること |
| --- | --- | --- |
| Client | 自Principal範囲で悪意あるrequest、quota消費を試みる | 任意Worker指定、任意code実行、他Principal Data取得 |
| Identity Provider / IDV-1 | 対象Trust Domain内のPrincipal impersonation | Worker WorkOrder署名、Artifact Grant、Executor Approval |
| ADM-2 Admission | 不適切TaskをAdmit、quota判断を壊す | Artifact intrinsic class低下、Executor Approval、WorkOrder単独発行 |
| TSK-1 Task State | retry/cancel/stateを乱す、availabilityを壊す | Data Grant、Executor Admission、Federation変更 |
| SCH-1 Scheduler | 悪い候補を提案、性能劣化、starvation | Security requirement低下、Data Grant、WorkOrder署名 |
| PLC-1 Placement | 不適切Workerを承認し得る | Attestation偽造、Artifact Authority key、Executor Admission |
| LSE-1 Lease | duplicate / stale lease、availability混乱 | Data read authority、Executor code承認 |
| DAT-1 Data Authority | 管理Scope内Data漏えい・過剰Grant | Executor Admission、Federation設定、WorkOrder前提Decision偽造 |
| EXE-1 Executor Admission | 悪意Executorを承認 | Data Grant、Placement、Sandbox capability自動拡張 |
| WOR-1 WorkOrder | 不正Envelope、replayを試みる | 他Authority署名の有効な前提Decision生成 |
| ATV-1 Attestation Verifier | 偽Environment Result | Artifact Grant、Task Admission、Result semantic validation |
| RVR-1 Result Verifier | 偽Verification Result | Program Admission、Data Grant、Contribution価値決定 |
| RAC-1 Result Acceptance | 不正ResultをAcceptedへ昇格 | Worker実行権限、Data Grant |
| EFF-1 Effect Authority | 外部effect statusを偽る | EKI Data / Executor / Federation Authority |
| CON-1 Contribution | 偽Contribution Record | EKI通貨発行、Local Governor override、Task execution |
| FED-1 Federation Admin | 外部Identity trustを誤設定 | Resource Authorizationの自動付与 |
| GOV-1 / modified Agent | 自Endpointの制御・計測を偽る | Control Plane署名、他Worker Identity、Accepted Result署名 |

## 10.1 特に影響の大きい侵害

次は大きなblast radiusを持ち得ます。

- Trust / Policy Administration
- Artifact / Data Authority
- Executor Admission Authority
- Result Acceptance Authority
- Federation Administration

これらはSchedulerやTelemetryのような複雑・高頻度Componentと同じCredentialを共有しないことを優先します。

## 10.2 Root compromise

Organizationのroot signing key、host OS / kernel、hardware root、Verifier root等まで全面的に侵害された場合、通常の論理分離だけでは防げない範囲があります。

これを隠して「完全安全」とは表現しません。

代わりに、

- key separation
- short-lived derived authority
- rotation / revocation
- offline / HSM等の保護
- multi-party approval候補
- independent verification

によってblast radiusを狭める設計を比較します。

---

# 11. Independent Re-checkの原則

一つのAuthorityの出力を、後段が無条件で信頼しない箇所を明示します。

## Data Release

DAT-1はPlacement Approvalだけを見てDataを解放しません。

少なくともArtifact classification、Task binding、Worker Identity、必要Assurance、Lease等を独立に確認できる情報を持ちます。

## WorkOrder

WOR-1は前提Decisionを束ねますが、Worker WEV-1が重要な署名とbindingを再確認します。

## Result Acceptance

RAC-1はRVR-1の「valid」だけでなく、そのVerifierがTask Policyで受理可能か、ResultがTask / Attempt / Input / Program等へbindingされているかを確認します。

## Contribution

CON-1はAccepted ResultだけからResource quantityを作りません。

Measurement Observer / Assurance / Work Contextを別に確認します。

この再確認は、すべてのComponentが同じPolicy engineを重複実装するという意味ではありません。

「一つの侵害された要約値だけが、後段の高価値Authorityを無条件に動かさない」ことが目的です。

---

# 12. Data Minimization

Authority分離はPrivacyとも関係します。

各Componentへ必要以上の情報を渡さないことを目指します。

## Scheduler

原則として、

- Resource summary
- capability summary
- Artifact locality hint
- transfer size
- deadline / priority
- policyで許可されたoperational history

を使います。

Artifact本文、Client secret、Raw Attestation Evidenceは標準入力にしません。

## Attestation / Verification

Raw Evidenceは必要なVerifier境界に閉じ、Scheduler / Clientにはnormalized resultだけを渡せる設計にします。

## Contribution

Contribution Recordへuser activityのraw telemetryを直接埋め込むことを避けます。

必要なのはResource Measurementの根拠とWork Contextであり、個人行動の詳細そのものではありません。

## Client

ClientへWorker hostname、IP、logged-in user、installed software、cache inventory等を返しません。

Task statusは必要最小限のqueued / running / completed / failed / unknown等へ縮約します。

---

# 13. Deployment Profileへの投影

Authority Mapは全Deploymentで同じ論理構造を保ちますが、物理構成は比例化します。

## 13.1 Small Managed Organization

例:

~~~text
1 Control Plane binary
1 database
several endpoint agents
~~~

でも成立させます。

ただしBinary内部ではAuthority-specific interface / key handleを分けます。

専用のKafka、SPIRE、HSM、複数Microserviceを必須にはしません。

## 13.2 Enterprise Mixed Endpoints

- enterprise IdP / PKI integration
- separate Artifact Store
- stronger key protection
- separate high-value admin path
- proxy / outbound-only worker
- managed endpoint policy

を利用できます。

## 13.3 Browser / Ad-hoc Participation

- short-lived identity
- low-trust / ephemeral Worker profile
- publicまたは低機密Data
- restricted Executor
- aggressive lease expiry
- no local secret
- explicit participation

を基本にします。

Browser Workerが存在するために、managed Worker側のSecurityを弱めません。

## 13.4 Federation / Public-interest Grant

Federation IdentityとResource Authorizationを分離し、Compute Grantのscope、Beneficiary、Data Class、期間、preemptionを明示します。

Contribution Recordは必要に応じて外部制度へ渡せますが、EKI Coreは価値制度を持ちません。

---

# 14. Key / Credential Baseline

具体KMSやPKI方式は後続比較ですが、論理原則は次です。

1. Client、Worker、Control Plane AuthorityのCredentialを分ける。
2. Authorityごとにissuer identityを識別できる。
3. Productionでは異なる高価値Authorityが同一signing credentialを共有しないことを標準とする。
4. short-lived decisionは短いexpiryを持てる。
5. key rotation / revocationを扱える。
6. Trust Rootを通常requestで差し替えられない。
7. signing private keyをScheduler / parser / untrusted executorへ渡さない。
8. Dev / test profileで簡易keyを使っても、Authority identity自体は分離してTestする。

---

# 15. Audit Baseline

Security-sensitiveなAuthority transitionは、少なくとも後から関連付けられる必要があります。

例:

- Task Admission
- quota / budget reservation
- Placement Approval / rejection
- Lease issue / expiry / revoke
- Artifact Grant issue / deny
- Executor Approval / revoke
- WorkOrder issue
- Result Verification
- Result Acceptance
- Federation config change
- Contribution Record issue
- Local Governor reclaim reason

Audit eventは、可能な限りTask / Attempt / Decision IDとPolicy Revisionへbindingします。

ただしAuditのためにRaw secret、Raw Attestation Evidence、不要なPersonal Dataを集約しません。

Audit Store自体のtamper resistanceは後続設計項目です。

---

# 16. 未決事項

このBaselineでは、次をまだ固定しません。

- AuthorityごとのProcess / Service数
- HTTP / gRPC等の内部transport
- PKI / KMS / HSM製品
- OPA / Cedar等のPolicy engine
- exact decision wire schema
- Queue / DB
- Artifact Store
- multi-party approval
- audit log cryptographic chaining
- keyless signature
- TEE vendor
- zkVM
- multiple verifier quorum

後続Architecture Work Unitで、RequirementとQuality Attributeに照らして比較します。

---

# 17. 次のArchitecture Work Unitへの入力

このAuthority Mapを前提に、次はExternal / Internal Contract Boundaryを具体化します。

優先するConceptは次です。

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

次の作業では、これらを単なるGo structの集合として先に固定せず、

1. 誰が生成できるか
2. 誰が検証するか
3. 何へbindingするか
4. どのFieldをClientが制御できるか
5. version / extensionをどう扱うか
6. replay / stale / downgradeをどう防ぐか

をContractごとに定義します。
