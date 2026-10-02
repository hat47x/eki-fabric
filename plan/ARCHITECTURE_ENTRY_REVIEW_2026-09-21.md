# Architecture Entry Gate Review — 2026-09-21

## 役割

この文書は、EKI FabricがRequirement / Threat / Assurance / Acceptance Scenario / Feasibility Spikeを十分に具体化し、Architecture baselineへ進める状態にあるかをGate Uに沿って確認するレビュー記録です。Architectureそのものではなく、未解決研究を「解決済み」に見せる文書でもありません。

判定基準は、未解決事項を認識したままSecurity InvariantとProduct Principleを壊さずにArchitecture候補を比較できるか、です。

## 結論

**Gate U: 条件付きPASS**

Architecture baselineの作成を開始してよい状態です。ただし、次はBaselineの既成事実にしません。

- Verifiable Computationを全Taskへ必須化しない。
- 特定zkVM / TEE / Identity Provider / Queue / Artifact Storeを必須化しない。
- anonymous participantに一般的なSybil完全耐性があると主張しない。
- AttestationだけでApplication Result correctnessを保証しない。
- LLM / GPU Taskへgenericなcryptographic correctnessを約束しない。
- Contribution RecordをEKI共通Credit / 通貨 / 金銭価値へ変換しない。
- learned schedulerへAuthorization / Trust判断を委譲しない。
- Browser WorkerをNative / Managed Workerと同じAssuranceとして扱わない。

これらを守る限り、Core ArchitectureのAuthority境界、Protocol、Deployment Profile、Extension Pointを設計する材料はそろっています。

---

## Gate U 条件別レビュー

### G-1 — Phase 0〜3の主要成果物

**PASS**

Product Value / Principles、初見読者向け文書、Product Requirements、Threat Model、Requirement Gap Review、Acceptance Scenarios、Assurance Model、Quality Attributes、Reference Systemsがmain上でレビュー可能です。Phase 1は53 Requirementまで第三巡目、Phase 2は27 Scenario、Phase 3は多軸AssuranceとQuality Attributeまで成立しています。

### G-2 — Phase 4の高リスク項目

**PASS。ただし一部はoptional extensionとして研究継続**

mainに統合済みの主要Spikeは次です。

1. Local Governor
2. Browser Worker lifecycle
3. Artifact locality / transfer cost
4. Artifact ingest safety
5. Result verification independence
6. Mass reclaim recovery
7. Control Plane authority separation
8. Federation trust binding
9. Executor provenance / content admission
10. Side-effect uncertain outcome
11. Remote Attestation abstraction
12. Contribution measurement
13. Verifiable Computation contract

これらにより、Local Safety、lower-assurance Browser、data locality、Artifact安全性、malicious Worker Result、bounded recovery、Authority分離、Federation namespace、Executor Admission、side-effect UNKNOWN、Attestation分離、Contribution測定、Proof Contractの各境界についてArchitecture判断に必要な情報が得られています。

Issue #23の実zkVM benchmarkは未実施です。これは特定Proof TechnologyをBaselineへ採用する判断には必要ですが、Verifiable Computationをoptional Result Assurance extensionとして予約することは妨げません。

Local Governorのdelegated host上でのhard enforcement実測等も継続しますが、Architectureは`HARD / BEST_EFFORT / OBSERVE_ONLY / UNAVAILABLE`のように実効保証を表現し、未確認能力を高保証として広告しない構造を選べます。

### G-3 — Requirement間の重大な矛盾

**PASS**

残るOpen Questionは主に実現方式、保証上限、costです。operator-level Sybil detection、multi-component Confidential Computing、non-deterministic AI Result、proof cost、Browser evidence、multiple Validator independence等はProduct Principleと矛盾せず、「保証できない部分を保証済みと表現しない」という共通原則に収束しています。

### G-4 — Security Invariantによる初期除外

**PASS**

少なくとも次の候補はArchitecture比較の初期段階で除外できます。

- Client RequestをそのままWorker execution requestへ流す。
- arbitrary client codeをbusiness endpointで直接実行する。
- Workerが任意URL / local pathをfetchする。
- SchedulerへAuthorization / Data Grant / WorkOrder signingまで同居させる。
- Worker自己申告だけでResult / Attestation / Contributionを確定する。
- Federation keyをnamespaceなしのglobal trust poolへ混ぜる。
- expired / stale Authorityをreplay可能にする。
- user opt-outをCentral Schedulerが拒否できる。
- mass reclaim時に全Taskを即時再投入する。
- non-idempotent side-effectのUNKNOWNを自動retryする。
- Proof validityだけでProgram Admissionを迂回する。

### G-5 — Reference Systemの採否判断

**PASS**

`research/reference-systems.md`ではBOINC、Nomad、SPIFFE / SPIRE、Sigstore / Cosign、TEIの文書運用を、真似る部分、そのまま真似ない部分、EKI固有Requirementへ分けています。「似ているから採用」ではなくProperty単位で比較できます。

### G-6 — Worker差を単一Trust Levelへ押し込めない

**PASS**

Assurance ModelはIdentity、Execution Environment、Confidentiality、Result Integrity、Independence、Operational History、Resource Measurement、Verification Authority、Evidence Freshnessを独立軸として扱います。Browser / Native / managed / ad-hoc / federatedを一本の`trust_score`へ集約しない前提が成立しています。

### G-7 — Result VerificationとConfidentiality

**PASS**

Resultの正しさを高めることと、Worker ownerからInput / Model / intermediateを秘匿することを別保証として説明できます。Remote Attestation、Result Validation、Cryptographic Proofも同一視しません。

### G-8 — Federation namespace binding

**PASS**

Trust DomainごとのBundle分離、Federation Administrator、Bundle rollback拒否、Identity expiry、explicit direct federation、implicit transitive trust禁止、key reuse時のnamespace維持を確認済みです。

### G-9 — Artifact Provenance / Integrity / Safe Processing

**PASS**

Executor AdmissionとArtifact IngestのSpikeにより、Provenance、Integrity、Admission、Safe Processing、Sandbox Executionを別境界として扱えます。

### G-10 — Control Plane部分侵害とroot authority侵害

**PASS**

Scheduler Proposal、Placement Guard、Artifact Authority、WorkOrder Authority等を論理分離できることを確認済みです。ArchitectureではProcess数より先にAuthority graphを定義できます。

### G-11 — mass reclaimとEndpoint Safety

**PASS**

Local opt-out優先、Worker disappearanceを通常事象として扱う、retry budget / rate / transfer制約、mass reclaimのamplification抑制、thermal / power / battery / user activityによるcapacity縮小、Contribution incentiveによるSafety迂回禁止がRequirement / QA / Spikeへ接続されています。

### G-12 — 初見開発者の文書導線

**PASS**

README → docs/for-readers → docs/glossary → product → requirements → research → spikes → plan/UPSTREAM_PLANの導線があります。Architecture文書追加後もこの導線を維持します。

---

## Gateを通過しても未解決の研究

### R-1 — 実zkVM cost

Tracking: Issue #23。Architectureではoptional extension / Proof System Adapter / normalized Verification Resultとして扱い、mandatory baselineにはしません。

### R-2 — Learned scheduling

Tracking: Issue #19。Authorization / Security / Data-locality filterの後段でshadow modeから始め、model rankはpermissionを作りません。baseline schedulerはdeterministic / explainableで成立させます。

### R-3 — Anonymous Sybil / collusion

Worker countだけをindependenceとみなさず、Principal / Trust Domain / managed anchor / cryptographic proof等を選択可能にします。anonymous環境で完全保証を主張しません。

### R-4 — multi-component Confidential Computing

CPU / GPU / I/Oの一部Attestationをend-to-end Confidentialityと読み替えません。Environment capabilityを細分化し、unknownを高保証として扱わず、Key Releaseは別Authorityとして比較します。

### R-5 — non-deterministic AI Result

Model / Executor / Input provenanceは記録しますが、semantic correctnessをFabric共通の完全保証にしません。Application-specific validation extensionを残します。

### R-6 — Contributionの高度な不正耐性

Sybil Provider、self-dealing、useless-work farming、cross-validation、privacy-preserving aggregationは後続研究です。Resource MeasurementとExternal Value Policyを分離したまま設計します。

---

## Architecture開始時のHard Constraints

1. Client PlaneとWorker Planeを直接接続しない。
2. External Task Requestとinternal WorkOrderを分ける。
3. WorkerはControl Plane承認済みWorkOrderだけを実行する。
4. Executorはregistered / admitted artifactへ限定しDigestへbindingする。
5. Artifact accessはCapability / Authorizationを通しarbitrary URL fetchを許可しない。
6. SchedulerはPlacement proposal以外の高価値Authorityを極力持たない。
7. Local GovernorはCentral Schedulerより厳しい上限を適用できる。
8. Worker loss / retry / preemptionを通常状態として扱う。
9. Data locality / transfer costをPlacement costへ含める。
10. ResultClaimとAcceptedResultを分ける。
11. Identity、Result Assurance、Confidentiality、Resource Measurementを一つのTrust Scoreへ潰さない。
12. Federationは明示的・非推移的でTrust Domain namespaceを維持する。
13. Contribution Recordは価値換算前のEvidenceでありCore通貨ではない。
14. Browser Workerはlower-assurance / ephemeral profileとして表現できる。
15. Security / Safety capabilityのunknownは安全側へ評価する。
16. Protocolはlanguage-neutralでGo内部型を外部契約の正本にしない。

---

## Architectureで最初に比較する領域

### A-1 — Authority / Trust Boundary Map

Client / Worker Identity、Control Plane、Executor Admission、Artifact / Data、Verification、Federation Administration、Contribution、Local Governorについて、どのAuthorityがどの署名鍵・Capability・persistent stateへ触れられるかを先に定義します。

### A-2 — External / Internal Contract Boundary

TaskRequest、TaskIR、WorkOrder、ResultClaim、Verification Result、Accepted Result、Contribution Recordを分けます。

### A-3 — Control Plane / Data Plane

Control metadataとbulk Artifact transferを分け、Control Planeをlarge data proxyにしない構造を比較します。

### A-4 — Deployment Profiles

small managed organization、mixed enterprise endpoints、browser / ad-hoc participation、federation / public-interest grantで、同じSecurity Principleを保ちながらComponent数を比例化できるか比較します。

### A-5 — Persistence / Queue / Lease semantics

at-least-once、Lease、Idempotency、UNKNOWN side-effect outcome、mass reclaim、retry budgetを一つの状態モデルへ統合します。

### A-6 — Extension SPI

Queue、Artifact Store、Identity、Executor Runtime、Verification、Attestation、Scheduler Policy、Contribution MeasurementをCore ContractとAdapterへ分けます。

---

## Gate後のBranch Rule

Architecture作業でも1 work unit = 1 branchを維持し、PR / merge後にbranchをmainへ再整列します。未マージbranchから次branchを派生させません。

## 判定

2026-09-21時点で、**Architecture baseline作成を開始します。** Issue #19 / #23および上記Residual Researchは継続します。Architectureが未解決研究を先取りして特定実装へ固定しようとした場合は、Gate U PASSを根拠に押し切らずRequirement / Assurance Modelへ戻って再評価します。
