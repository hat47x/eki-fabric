# Architecture Baseline 0.1 横断レビュー — 2026-09-21

Status: **Reviewed / Implementation Planning Ready**

## 1. 目的

本書は、EKI FabricのArchitecture Baseline 0.1を構成する6文書を横断し、実装計画へ進む前に責務・用語・状態・Authorityの矛盾がないかを確認したレビュー記録です。

対象は次のとおりです。

- `AUTHORITY_BOUNDARIES.md`
- `CONTRACT_BOUNDARIES.md`
- `CONTROL_DATA_PLANE.md`
- `DEPLOYMENT_PROFILES.md`
- `TASK_STATE_MODEL.md`
- `EXTENSION_SPI.md`

RequirementやArchitectureの第二正本を作ることが目的ではありません。各正本文書の関係を確認し、曖昧な箇所は実装前の解釈として固定します。

## 2. 結論

**Architecture Baseline 0.1は、MVP implementation planningへ進める整合性があります。**

重大な責務矛盾、Security Invariantの反転、State ModelとContractの不整合は確認されませんでした。

残る論点は主に、具体的なtransport / schema / database / queue / KMS / runtime等の技術選択と、高度Optional Assuranceの実測です。

Issue #19 learned scheduling、Issue #23 real zkVM benchmarkは継続研究とし、MVP必須Dependencyにはしません。

---

# 3. 横断Consistency Matrix

| 観点 | 正本間の関係 | 判定 |
| --- | --- | --- |
| Client hostile input | Contract → Admission Authority | 整合 |
| Scheduler authority | Authority → Deployment → SPI | Proposalのみで一貫 |
| Worker execution authority | WorkOrder + prerequisite decisions + local verification | 整合 |
| Data authority | ArtifactGrant / Data AuthorityをTask authorityから分離 | 整合 |
| Bulk data path | Contract → Control/Data Plane → Deployment | 整合 |
| Task / Attempt | Contract → State Model | Retry=new Attemptで整合 |
| Lease | Authority → Contract → State | time-bounded assignmentで整合 |
| Queue | Deployment → State → SPI | delivery mechanismのみで整合 |
| Result | ResultClaim → VerificationResult → AcceptedResult | 整合 |
| External side effect | Contract → State → Effect SPI | UNKNOWN保持で整合 |
| Contribution | Measurement → Contribution Authority → Record | Result / valueと分離 |
| Federation | Identity namespace + local admission + Data Export分離 | 整合 |
| Browser / Ad-hoc | lower-assurance profile + short lease + explicit participation | 整合 |
| Local Governor | Authority → Deployment → State | centralより強いvetoで整合 |
| Extensions | Admin-managed / semantics non-overridable | 整合 |

---

# 4. 用語のCanonical整理

## 4.1 ArtifactGrant と Data Grant

両者を同一語へ無理に統合しません。

**ArtifactGrant**:

- 特定Artifact / objectへの具体的なruntime capability
- Task / Attempt / Worker / operation / expiryへbinding
- WorkOrder / Data Access Resolverで利用

**Data Grant**:

- より上位のData authorization概念
- Federation export、Application-owned data、Compute GrantとData accessの分離等を含む
- 必要に応じて一つ以上のArtifactGrant / query capabilityへ具体化

したがってMVP runtime contractの具体Object名は`ArtifactGrant`を優先し、`Data Grant`はpolicy / federation層の上位概念として扱います。

## 4.2 Task Admission Authority

`ADM-2 Task Admission Authority`をcanonical名称とします。

文脈上`Admission Authority`と短縮してよいですが、Executor AdmissionやExtension Administrationと混同する箇所では`Task Admission`と明記します。

## 4.3 WorkOrder Authority

WorkOrder Authorityは最終execution envelopeを組み立て、署名します。

ただし独立したroot policy authorityではありません。

不足するPlacement / Lease / Executor Approval / ArtifactGrantを自分で生成してはいけません。

Worker側はWorkOrder Authority署名だけでなく、security-critical prerequisiteのbinding / issuer / freshnessを再確認します。

## 4.4 Result Verification と Result Acceptance

**Result Verification Authority**:

- ResultClaimに対するEvidenceを評価
- VerificationResultを生成

**Result Acceptance Authority**:

- Task固有Result Assurance PolicyへVerificationResultを照合
- AcceptedResultを生成

**Task State Authority**:

- durable AcceptedResultを確認してTask LifecycleをCOMPLETEDへ遷移

この三段階を維持します。

## 4.5 Data Classification / Declassification

Output Data Classは安全側へ伝播します。

Baseline 0.1にはgenericなDeclassification Authorityを置きません。

将来、匿名化・集計等でClassを下げる必要がある場合は、approved Executor、専用Policy、Validation、Auditを伴う明示的なArchitecture追加として扱います。

Worker / Executorの自己申告だけではClassを下げません。

## 4.6 Extension Administration

Extension registration / activation / revokeはAdministrative Planeの責務です。

Baseline 0.1では新しいruntime Authorityを増やさず、Trust / Policy Administrationおよび対象KindのAdmission responsibilityの下で管理します。

高Privilege Extensionを将来別Admin Authorityへ分割する余地は残します。

---

# 5. Authority ↔ Contractの整合

## 5.1 Client → TaskRequest

ClientはIntentを表現できますが、Worker、Trust Root、internal Policy Revision、ArtifactGrant、Executor Artifact Digest等を確定できません。

Task Admission AuthorityがTaskRequestをstrict parse / authenticate / authorize / budgetし、AdmittedTask / TaskIRを確定します。

## 5.2 Scheduler → PlacementProposal

Schedulerの出力はProposalです。

Placement Authorization Authorityがsecurity / assurance / grant / data locality hard constraintを再評価します。

learned estimatorを将来導入してもこの関係は変えません。

## 5.3 WorkOrder

WorkOrderはTaskIR、Attempt、PlacementApproval、Lease、ExecutorApproval、ArtifactGrant、resource/capability limitを束ねます。

Worker-local WorkOrder Verifierが重要な前提Decisionを再検証します。

この二重境界により、WorkOrder signer単独侵害のblast radiusを抑えます。

---

# 6. Contract ↔ State Modelの整合

## 6.1 TaskRequest / TaskIR

Admission成功後にTask identityを生成し、TaskIR Digestへ後続Decisionをbindingします。

Task StateはClient Requestをmutable正本にしません。

## 6.2 Attempt

Retry / migration / speculative / verification replicaはすべて別Attemptです。

Queue duplicate deliveryは別Attemptではありません。

## 6.3 Lease

LeaseはAttempt / Workerへのtime-bounded authorityであり、execution outcomeではありません。

Lease expiry後のlate heartbeatでAuthorityを復活させません。

## 6.4 Result

ResultClaim受信はAttempt outcomeのClaimです。

Verification / Acceptanceを経てAcceptedResultが成立し、Task StateがCOMPLETEDへ収束します。

---

# 7. Control/Data Plane ↔ Deploymentの整合

Small ManagedではControl PlaneとData Access Resolverを同一Binaryへ同居できます。

EnterpriseではWorker Gateway、Data Authority、Store、Site Cache等を物理分離できます。

どちらでもは次のとおりです。

- ArtifactGrant scope
- Content Digest verification
- short-lived access
- no bucket-wide worker credential
- Control Plane metadata pathへbulk Artifactを標準集約しない

という論理境界は同じです。

---

# 8. Deployment ↔ State Modelの整合

## DP-1 Small

単一Control Planeでもdurable state / revision / restart reconciliationを要求します。

## DP-2 Enterprise

複数instanceでもTask / Attempt / Lease fencingを維持します。

## DP-3 Browser

short Lease / high churn / stale session rejectionをState Modelで吸収します。

## DP-4 Federation

Provider domainがlocal Attempt / Lease authorityを持ち、remote disconnectを即Task failureへしません。

## DP-5 Compute Grant

mass reclaimをRecovery Eventとして扱い、bounded re-admissionします。

---

# 9. State Model ↔ Extension SPIの整合

Queue / State Store AdapterがState semanticsを所有しないことを再確認します。

**Queue SPI**:

- at-least-once delivery
- duplicate / delay / backpressure処理
- Attemptやretry authorityは生成しない

**State Store SPI**:

- durability
- conditional update / revision
- unique identity
- transaction / equivalent atomicity
- recovery scan

を提供しますが、Lease expiryからTask failureを決めるのはCore State Modelです。

---

# 10. Security上の横断確認

## 10.1 Trust Root injection

Trust Rootを通常request / adapter callerが選べない原則は全領域で一致しています。

## 10.2 Credential role separation

Client / Worker / Admin / Authority credentialを分け、role credentialを推移させません。

## 10.3 Classification downgrade

Client / Worker / Adapter / Schedulerのいずれも単独でclassificationを下げられません。

## 10.4 arbitrary fetch

WorkerはClient supplied URL / pathを標準Data authorityとして利用しません。

## 10.5 arbitrary plugin

ClientはControl Plane Extension packageを指定・installできません。

---

# 11. Application Planeとの境界

EKI Coreはcompute placement / execution fabricを担当します。

Application固有の業務意味、認知処理、画面・業務構造をCoreへ持ち込みません。

Unknown ApplicationはExternal Client Contractとapproved Executorで統合します。

Application PlaneでTEI等を利用することはできますが、EKI scheduler、worker、protocol、lease/retry、sandbox、verification kernelをTEIへ移すことはBaselineの前提にしません。

---

# 12. Residual Researchとの整合

## Issue #19 — Learned scheduling

接続先はScheduler / Learned Estimator SPIです。

Hard Security / Authorization / Data residency filteringの後段でProposal rankingへ使います。

MVP correctnessはlearned modelなしで成立させます。

## Issue #23 — Real zkVM benchmark

接続先はCryptographic Proof Verifier SPIです。

Proofはoptional Result Assurance methodです。

MVP correctnessをreal zkVMへ依存させません。

---

# 13. Implementation前に固定すべき最小Concept

次をmachine-readable schemaへ進める優先Contractとします。

1. TaskRequest / public Task status / error
2. Worker identity / registration / resource advertisement
3. TaskIR / Attempt identity
4. PlacementApproval / Lease
5. ExecutorApproval reference
6. ArtifactDescriptor / ArtifactGrant reference
7. WorkOrder
8. ResultClaim
9. VerificationResult / AcceptedResult reference

Contribution、Federation、Attestation、Proof等はCore extension pointを保ちつつMVP初回schemaから分離できます。

---

# 14. MVP cutの原則

MVPは「全Architecture機能の縮小版」ではなく、重要Invariantを実際に通す最小vertical sliceとします。

必須は次のとおりです。

- malicious ClientからWorkerまでのauthority-separated path
- durable Task / Attempt / Lease
- 2台以上のWorkerでplacement / loss / retry
- local opt-out / reclaim
- approved Executor
- ArtifactGrant + Digest verification
- ResultClaim / Verification / Acceptance
- bounded retry / transfer
- structured audit / telemetry

初回MVPで必須にしない:

- Federation
- Browser Worker
- TEE
- real zkVM
- learned scheduler
- Contributionの外部Credit連携
- multi-party approval
- site cache
- cloud-specific integration

詳細は`MVP_IMPLEMENTATION_PLAN.md`で定義します。

---

# 15. Protocol technology選定は別Work Unit

BaselineはHTTP / gRPC / JSON / Protobuf等をまだ固定しません。

ただし外部Client Contractはlanguage-neutralで、Internal Queue製品を直接公開しないことは固定済みです。

Protocol technology比較では少なくとも:

- strict parsing / canonicalization
- streaming
- schema evolution
- multi-language codegen
- browser compatibility
- enterprise proxy compatibility
- fuzzability
- signature / digest対象の明確さ

を比較します。

---

# 16. ADR候補

長期に残る判断として、少なくとも次はADR化候補です。

1. Logical Authority separation vs physical service count
2. External TaskRequestとinternal WorkOrderの分離
3. Task / Attempt / Lease state model
4. Control Plane / Data Plane分離
5. Worker outbound-first / no arbitrary P2P default
6. Extension SPI cannot override Core semantics
7. Contribution Record is not currency/value

ADRはBaselineレビュー後、実装技術の比較で選択肢とTrade-offが明確になったものから作成します。

---

# 17. 判定

Architecture Baseline 0.1は**cross-document coherent**と判定します。

次工程はMVP implementation planningです。

ただし実装都合によってSecurity / State semanticsを弱める必要が生じた場合、実装へ合わせて正本文書を黙って変更せず、Requirement / Architectureへ戻って再評価します。
