# EKI Fabric 上流工程計画

## 目的

EKI Fabricでは、実装しやすい技術を先に選び、その制約へRequirementを合わせる進め方を避けます。

Product Value、利用形態、Threat Model、Product Requirements、Acceptance Scenario、Assurance Modelを十分に具体化し、特にリスクの高い点だけを小さな実証で確認してからArchitectureへ入ります。

期限は設けません。各Phaseの終了条件とArchitecture Entry Gateの成立をもって進みます。

## Phase 0 — Product framing

### 成果物

- `product/value.md`
- `product/principles.md`
- naming / terminology baseline
- 初見読者向けの背景説明

### 終了条件

- クラウド利用削減だけではないEKIの中心価値を説明できる。
- enterprise / ad-hoc / federation / public-interestの各用途が、同じ価値体系の中で矛盾しない。
- 通貨・市場をEKI Coreの責務にしない境界が明確である。
- 分散システム専門家でない開発者でも、背景からRequirementへ辿れる入口がある。

状態: **初版成立 / 継続レビュー**

`docs/for-readers.md`、`docs/glossary.md`、`requirements/README.md`を追加し、参画に必要な前提知識を文書側で補う導線を整備しました。

## Phase 1 — Requirements & threat discovery

### 成果物

- `requirements/PRODUCT_REQUIREMENTS.md`
- `requirements/THREAT_MODEL.md`
- `requirements/REQUIREMENT_GAP_REVIEW.md`
- 利用主体、保護対象、攻撃者、Security Invariant一覧
- Open Questions

### 終了条件

- malicious Client / malicious Worker / modified Agent / Sybil / collusion / resource exhaustionを扱う。
- compromised Validator / Verifier、replay、意図しないBrowser resource useもThreat Modelで扱う。
- endpoint opt-out / human-first / network impactがRequirement化されている。
- unknown OSS / native / browser / external organizationがRequirement上で扱える。
- Compute Grant / Contribution / external value mappingがCore economic systemへ逸脱していない。
- 実現不能な絶対保証を避け、保証不能条件を記述している。
- 副作用Taskや非決定的AI Taskについて、EKIが保証できない部分を誇張しない。

状態: **第二巡目進行中**

初版41 Requirementから第二巡目で48 Requirementへ拡張しました。現在はRequirement同士の重複、保証水準の整合、追加PASからのgapを継続確認します。

## Phase 2 — Acceptance Scenarios

抽象的なRequirementを、実利用、攻撃、離脱、組織間協力の具体場面へ当てはめます。

### 現在の主要Scenario

1. 中小企業のmanaged workstation pool
2. 業務中Endpointのuser return / opt-out
3. 高速GPUよりdata localityを優先する配置
4. malicious Clientによるresource amplification
5. SSRF相当と内部探索
6. malicious Workerによる偽Result
7. Sybil Workerによるquorum支配
8. public dataへのad-hoc volunteer参加
9. confidential workloadの配置拒否
10. 複数企業Trust Domain間の限定Federation
11. Public-interest Compute Grant
12. Contribution Recordと外部Credit Policyの分離
13. Go SDKを使わない未知OSS
14. non-deterministic AI result
15. Executor supply-chain substitution
16. malicious contribution farming
17. Browser Workerのsuspensionと明示的参加
18. Workerによる虚偽の削除Claim
19. compromised Validator / Verifier
20. side-effect Taskの実行済み状態不明
21. stale Evidence / Leaseのreplay
22. Security capabilityの虚偽自己申告

### 終了条件

- 各ScenarioからRequirement IDへtraceできる。
- Scenarioが新しい本質要求を発見した場合、Requirementを先に更新する。
- 特定Architectureでしか成立しないScenario記述を避ける。
- 主要Scenarioについて、正常経路だけでなくabuse pathと残る保証限界を説明できる。

状態: **第一巡目成立 / 第二巡目継続**

次はFederation namespace confusion、Artifact parser bomb、Control Plane内部component侵害、Compute Grantの大量reclaimなどを追加候補として圧力試験します。

## Phase 3 — Quality attributes / assurance model

次の品質特性を、少なくともArchitecture候補同士で比較できる形にします。

- Security assurance
- Result integrity assurance
- Confidentiality assurance
- Availability / retry behavior
- Endpoint user impact
- Network overhead
- Data locality
- Resource accounting accuracy
- Scalability
- Operability
- Interoperability

Worker Assuranceは、単純な`trusted / untrusted`や一本のLevelではなく、Identity、Execution Environment、Confidentiality、Result Integrity、Independence、Operational History、Resource Measurementを分けて評価します。

### 成果物

- `requirements/ASSURANCE_MODEL.md`
- Assurance gapとOpen Question
- Architecture比較へ使えるQuality Attribute一覧

### 終了条件

- 「Workerが信頼できる」という曖昧な表現を、必要な保証軸へ分解できる。
- Attestation、reputation、replication、proofの役割を混同しない。
- Sybil / collusion条件下で、単純多数決の保証上限を説明できる。
- non-deterministic AI Taskについて、EKIが保証できる範囲と上位判断へ残す範囲を説明できる。
- Contributionに利用するResource Measurementのassuranceを表現できる。

状態: **初版成立 / 深掘り継続**

## Phase 4 — Feasibility research / spikes

Architecture採用前に、Requirement上のリスクが高く、文書検討だけでは判断できない点を小さなSpikeで確認します。

優先候補:

- heterogeneous Workerのresource detectionとlocal governor
- Browser Worker lifecycleと実効resource limit
- Result Validator / replication cost
- Sybil-resistant independence constraints
- signed Executor / content digest admission
- Artifact locality / transfer-cost scheduling
- malicious input parser / resource bounds
- TEE remote attestation abstraction
- verifiable computationの現実的適用範囲
- side-effect Taskのuncertain outcome model

Spikeは採用Architectureを既成事実化しません。確認したいRequirement上の仮説、実験条件、観測結果、一般化してよい範囲を残します。

状態: **未着手**

## Gate U — Architecture Entry Gate

次を満たした時点で、初めてArchitecture baselineを作成します。

- Phase 0〜3の主要成果物がレビュー可能である。
- Phase 4の高リスク項目について、少なくとも採用判断に必要な実現可能性情報がある。
- Requirement間に重大な矛盾が残っていない。
- Security Invariantを破るArchitecture候補を、比較初期に除外できる。
- Reference Systemを「似ているから採る」のではなく、EKI Requirementに照らして採否判断できる。
- Browser / Native / managed / ad-hoc / federated Workerの差を、単一の曖昧なTrust Levelへ押し込めず説明できる。
- Result VerificationとConfidentialityの信頼基盤を分けて説明できる。
- 初見の開発者が文書導線から主要な設計判断へ辿れる。

## Architecture段階で比較する領域

Gate Uまでは採用を確定しませんが、後段で少なくとも次を比較します。

- external API contract: HTTP / gRPC / schema format
- Task Admission / internal WorkOrder representation
- queue / lease / scheduling substrate
- Artifact addressing / registry / cache
- Worker Identity / Workload Identity / Federation
- Executor packaging: Native / WASM / Container / Browser
- Sandbox enforcement
- Result Verification Provider
- Telemetry / Audit
- Contribution accounting event model
- Browser participation UX / local resource control

## 作業原則

- Requirementの不確実性を減らす作業を優先する。
- 実装しやすさだけを理由にRequirementを弱めない。
- 既存OSSで十分な部分は積極的に再利用する。
- 既存OSSの前提がEKIと異なる場合は、その差を明記し、Adapterまたは独自実装の必要性を判断する。
- 一つのReference Systemへ全体Architectureを寄せすぎない。
- 文書の自然な日本語と背景説明を重視し、専門知識がないことを読者側の責任にしない。
- 説明を厚くしても、Requirement、Threat、ADRの正本を複製しない。
