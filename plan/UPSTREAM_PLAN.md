# EKI Upstream Plan

## 目的

Architectureを急いで固定せず、EKI FabricのProduct Value、利用形態、Threat Model、Product Requirements、Acceptance Scenarioを十分に具体化したうえで設計へ移行します。

期限は設けず、各Gateの成立条件で進めます。

## Phase 0 — Product framing

### 成果物

- `product/value.md`
- `product/principles.md`
- naming / terminology baseline

### Exit criteria

- クラウド削減だけでないEKIの中心価値を説明できる。
- enterprise / ad-hoc / federation / public-interestの各用途が同じ価値体系で矛盾しない。
- 通貨・市場をEKI Coreの責務にしない境界が明確である。

状態: **初版あり / 継続レビュー**

## Phase 1 — Requirements & threat discovery

### 成果物

- `requirements/PRODUCT_REQUIREMENTS.md`
- `requirements/THREAT_MODEL.md`
- 利用主体・資産・攻撃者・security invariant一覧
- Open Questions

### Exit criteria

- malicious client / malicious worker / modified agent / Sybil / collusion / resource exhaustionを扱う。
- endpoint opt-out / human-first / network impactがRequirement化されている。
- unknown OSS / native / browser / external organizationがRequirement上で扱える。
- Compute Grant / Contribution / external value mappingがCore economic systemへ逸脱していない。
- 実現不能な絶対保証を避け、保証不能条件を記述している。

状態: **初版あり / 深掘り継続**

## Phase 2 — Acceptance Scenarios

次に、抽象Requirementを実利用場面へ圧力試験します。

最低限次のScenarioを作成します。

1. 中小企業のmanaged workstation pool
2. 業務中のendpointが突然opt-out / user returnするケース
3. GPU modelがcache済みの端末へdata-localityを優先して配置するケース
4. malicious clientが大量Task / oversized Artifact / SSRF相当を試みるケース
5. malicious workerが偽Resultを返すケース
6. 複数Sybil workerが同じ偽Resultへ結託するケース
7. public dataをad-hoc volunteer workerへ流すケース
8. confidential workloadを通常untrusted workerへ配置しないケース
9. 複数企業trust domain間の限定Federation
10. Cloud Provider / 大企業によるpublic-interest Compute Grant
11. Contribution Recordを外部credit制度へ連携するが、EKI自身はexchangeを行わないケース
12. unknown third-party OSSがGo SDKなしにTaskを利用するケース
13. browser workerがtab close / suspensionで消失するケース
14. non-deterministic AI workloadでbitwise quorumが成立しないケース

### Exit criteria

- 各ScenarioからRequirement IDへtraceできる。
- Scenarioが新しい本質要求を発見した場合、Requirementを先に更新する。
- 特定Architectureでしか成立しないScenario記述を避ける。

状態: **未着手**

## Phase 3 — Quality attributes / assurance model

次を定量化または少なくとも比較可能な形にします。

- security assurance
- result integrity assurance
- confidentiality assurance
- availability / retry behavior
- endpoint user impact
- network overhead
- data locality
- resource accounting accuracy
- scalability
- operability
- interoperability

特にWorker Assuranceは単純な`trusted/untrusted`やLevel一本ではなく、identity、attestation、confidentiality capability、verification capability、operational history等の多軸modelを候補として評価します。

状態: **未着手**

## Phase 4 — Feasibility research / spikes

Architecture採用前に、Requirement上リスクの高い箇所だけ小さなSpikeで確認します。

優先候補:

- heterogeneous worker resource detection / governor
- browser worker lifecycleと実効resource limit
- result validator / replication cost
- Sybil-resistant independence constraints
- signed Executor / content digest admission
- Artifact locality / transfer-cost scheduling
- malicious input parser/resource bounds
- TEE remote attestation abstraction
- verifiable computationの現実的適用範囲

Spikeは採用Architectureを既成事実化しません。

状態: **未着手**

## Gate U — Architecture Entry Gate

次を満たした時点で初めてArchitecture baselineを作ります。

- Phase 0〜3の主要成果物がレビュー可能。
- Phase 4の高リスク項目について、少なくとも採用判断に必要な実現可能性情報がある。
- Requirement間の重大な矛盾がない。
- Security invariantsを破るArchitecture候補を早期に除外できる。
- Reference systemを「似ているから採る」のではなく、EKI Requirementに照らして採否判断できる。

## Architecture段階で比較する候補領域

Gate Uまでは採用を確定しませんが、比較対象として次を調査します。

- external API contract: HTTP / gRPC / schema format
- task admission / internal work-order representation
- queue / lease / scheduling substrate
- artifact addressing / registry / cache
- worker identity / workload identity / federation
- executor packaging: native / WASM / container / browser
- sandbox enforcement
- result verification providers
- telemetry / audit
- contribution accounting event model

## 作業原則

- Requirementの不確実性を減らす作業を優先する。
- 実装しやすさだけを理由にRequirementを弱めない。
- 既存OSSで十分な部分は積極的に再利用する。
- 既存OSSの前提がEKIと異なる場合は、その差を明記してAdapterまたは独自実装の必要性を判断する。
- 一つのReference systemへ全体Architectureを寄せすぎない。
