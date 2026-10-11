# Architecture Entry Gateの審査記録 — 2026-09-21

## 役割

この文書は、Gate Uに沿って、次のことを確認したレビューの記録です。EKI Fabricが、Requirement、Threat、Assurance、Acceptance Scenario、Feasibility Spikeを、十分に具体化して、Architecture baselineに進める状態にあるか。Architectureそのものではありません。未解決の研究を、「解決済み」に見せる文書でもありません。

判定の基準は、未解決の事項を認識したままで、Security InvariantとProduct Principleを壊さずに、Architectureの候補を比べられるかどうかです。

## 結論

**Gate U: 条件付きPASS**

Architecture baselineの作成を、開始してよい状態です。ただし、次のことは、Baselineの既成事実にしません。

- Verifiable Computationを、すべてのTaskに必須としない。
- 特定のzkVM、TEE、Identity Provider、Queue、Artifact Storeを、必須としない。
- 匿名の参加者に、一般的なSybilへの完全な耐性があると、主張しない。
- Attestationだけで、Application Resultが正しいことを保証しない。
- LLMやGPUのTaskに、汎用の暗号学的な正しさを約束しない。
- Contribution Recordを、EKI共通のCredit、通貨、金銭の価値に変換しない。
- learned schedulerに、AuthorizationやTrustの判断を委譲しない。
- Browser Workerを、NativeやManaged Workerと同じAssuranceとして扱わない。

これらを守る限り、Core ArchitectureのAuthorityの境界、Protocol、Deployment Profile、Extension Pointを設計するための材料は、そろっています。

---

## Gate U 条件別レビュー

### G-1 — Phase 0〜3の主要な成果物

**PASS**

Product ValueとPrinciples、初めて読む読者向けの文書、Product Requirements、Threat Model、Requirement Gap Review、Acceptance Scenarios、Assurance Model、Quality Attributes、Reference Systemsが、main上でレビューできます。Phase 1は、53個のRequirementまで第三巡目が済んでいます。Phase 2は、27個のScenarioが済んでいます。Phase 3は、多軸のAssuranceとQuality Attributeまで成り立っています。

### G-2 — Phase 4の高リスクの項目

**PASS。ただし、一部は任意のextensionとして、研究を継続する**

mainに統合済みの、主要なSpikeは、次のとおりです。

1. Local Governor
2. Browser Worker lifecycle
3. Artifactのlocalityと転送のcost
4. Artifact ingestの安全性
5. Result verificationのindependence
6. Mass reclaimからの回復
7. Control Planeのauthorityの分離
8. Federationのtrust binding
9. Executorのprovenanceとcontent admission
10. Side-effectのoutcomeが不明な場合
11. Remote Attestationの抽象化
12. Contributionの測定
13. Verifiable Computationのcontract

これらによって、次の境界について、Architectureの判断に必要な情報が得られています。Local Safety、保証の低いBrowser、dataのlocality、Artifactの安全性、悪意のあるWorkerのResult、上限のある回復、Authorityの分離、Federationのnamespace、Executor Admission、side-effectのUNKNOWN、Attestationの分離、Contributionの測定、Proof Contract。

Issue #23の、実際のzkVMのbenchmarkは、未実施です。これは、特定のProof Technologyを、Baselineに採用する判断には必要です。ただし、Verifiable Computationを、任意のResult Assurance extensionとして予約することは、妨げません。

Local Governorの、delegatedなhost上でのhard enforcementの実測なども、継続します。ただし、Architectureは、`HARD / BEST_EFFORT / OBSERVE_ONLY / UNAVAILABLE`のように、実際に効く保証を表現できます。そして、確認できていない能力を、高い保証として広告しない構造を選べます。

### G-3 — Requirementの間の、重大な矛盾

**PASS**

残っているOpen Questionは、主に、実現の方式、保証の上限、costです。たとえば、operatorの単位でのSybil検出、複数のcomponentにまたがるConfidential Computing、非決定的なAIのResult、proofのcost、Browserのevidence、複数のValidatorのindependenceなどです。これらは、Product Principleと矛盾しません。「保証できない部分を、保証済みと表現しない」という共通の原則に、収束しています。

### G-4 — Security Invariantによる、初期の除外

**PASS**

少なくとも、次の候補は、Architectureの比較の初期の段階で、除外できます。

- Client Requestを、そのままWorkerのexecution requestに流す。
- 任意のclient codeを、business endpointで直接実行する。
- Workerが、任意のURLやlocal pathをfetchする。
- Schedulerに、Authorization、Data Grant、WorkOrderの署名まで同居させる。
- Workerの自己申告だけで、Result、Attestation、Contributionを確定する。
- Federation keyを、namespaceのないglobalなtrust poolに混ぜる。
- 期限切れや古いAuthorityを、replayできるようにする。
- user opt-outを、Central Schedulerが拒否できる。
- mass reclaimのときに、すべてのTaskを即座に再投入する。
- 冪等でないside-effectのUNKNOWNを、自動でretryする。
- Proofが有効であることだけで、Program Admissionを迂回する。

### G-5 — Reference Systemの採否の判断

**PASS**

`research/reference-systems.md`では、BOINC、Nomad、SPIFFE / SPIRE、Sigstore / Cosign、TEIの文書の運用を、取り入れる部分、そのまま取り入れない部分、EKI固有のRequirementに分けています。「似ているから採用する」のではなく、Propertyの単位で比べられます。

### G-6 — Workerの違いを、単一のTrust Levelに押し込めない

**PASS**

Assurance Modelは、Identity、Execution Environment、Confidentiality、Result Integrity、Independence、Operational History、Resource Measurement、Verification Authority、Evidence Freshnessを、独立した軸として扱います。Browser、Native、managed、アドホック、federatedを、一本の`trust_score`に集約しない前提が、成り立っています。

### G-7 — Result VerificationとConfidentiality

**PASS**

Resultの正しさを高めることと、Worker ownerから、Input、Model、途中のdataを秘匿することとを、別の保証として説明できます。Remote Attestation、Result Validation、Cryptographic Proofも、同一視しません。

### G-8 — Federationのnamespace binding

**PASS**

次のことを、確認済みです。Trust Domainごとの、Bundleの分離。Federation Administrator。Bundleのrollbackの拒否。Identityのexpiry。明示的で直接のfederation。暗黙の推移的なtrustの禁止。keyを再利用するときの、namespaceの維持。

### G-9 — ArtifactのProvenance / Integrity / Safe Processing

**PASS**

Executor AdmissionとArtifact IngestのSpikeによって、Provenance、Integrity、Admission、Safe Processing、Sandbox Executionを、別の境界として扱えます。

### G-10 — Control Planeの部分侵害と、root authorityの侵害

**PASS**

Scheduler Proposal、Placement Guard、Artifact Authority、WorkOrder Authorityなどを、論理的に分離できることを、確認済みです。Architectureでは、Processの数より先に、Authority graphを定義できます。

### G-11 — mass reclaimとEndpoint Safety

**PASS**

次のことが、Requirement、QA、Spikeに、接続されています。Local opt-outを優先すること。Workerが消えることを、通常の事象として扱うこと。retry budget、rate、転送の制約。mass reclaimの増幅の抑制。thermal、power、battery、userのactivityによるcapacityの縮小。Contributionのincentiveによって、Safetyを迂回することの禁止。

### G-12 — 初めて読む開発者のための、文書の導線

**PASS**

README → docs/for-readers → docs/glossary → product → requirements → research → spikes → plan/UPSTREAM_PLANという、導線があります。Architectureの文書を追加した後も、この導線を維持します。

---

## Gateを通過しても、未解決の研究

### R-1 — 実際のzkVMのcost

Tracking: Issue #23。Architectureでは、任意のextension、Proof System Adapter、正規化したVerification Resultとして扱います。必須のbaselineにはしません。

### R-2 — Learned scheduling

Tracking: Issue #19。Authorization、Security、Data-localityのfilterの後の段階で、shadow modeから始めます。modelのrankは、permissionを作りません。baselineのschedulerは、決定的で、説明できるものとして、成り立たせます。

### R-3 — 匿名のSybilとcollusion

Workerの数だけを、independenceとはみなしません。Principal、Trust Domain、管理されたanchor、暗号学的なproofなどを、選べるようにします。匿名の環境で、完全な保証を主張しません。

### R-4 — 複数のcomponentにまたがるConfidential Computing

CPU、GPU、I/Oの一部のAttestationを、end-to-endのConfidentialityと読み替えません。Environment capabilityを細分化して、unknownを高い保証として扱いません。Key Releaseは、別のAuthorityとして比べます。

### R-5 — 非決定的なAIのResult

Model、Executor、Inputのprovenanceは記録します。ただし、意味の上での正しさを、Fabric共通の完全な保証にはしません。アプリケーション固有のvalidationのextensionを、残します。

### R-6 — Contributionの、高度な不正への耐性

Sybil Provider、self-dealing、無意味なworkによるfarming、cross-validation、privacyを保護する集計は、後続の研究です。Resource MeasurementとExternal Value Policyを分離したまま、設計します。

---

## Architectureの開始時の、Hard Constraints

1. Client PlaneとWorker Planeを、直接接続しない。
2. External Task Requestと、内部のWorkOrderを分ける。
3. Workerは、Control Planeが承認したWorkOrderだけを実行する。
4. Executorは、登録され、admitされたartifactに限り、Digestに結び付ける。
5. Artifactへのaccessは、CapabilityとAuthorizationを通す。任意のURLのfetchは許可しない。
6. Schedulerは、Placement proposal以外の、価値の高いAuthorityを、できるだけ持たない。
7. Local Governorは、Central Schedulerより厳しい上限を適用できる。
8. Workerの喪失、retry、preemptionを、通常の状態として扱う。
9. Dataのlocalityと転送のcostを、Placementのcostに含める。
10. ResultClaimとAcceptedResultを分ける。
11. Identity、Result Assurance、Confidentiality、Resource Measurementを、一つのTrust Scoreに潰さない。
12. Federationは、明示的で、非推移的であり、Trust Domainのnamespaceを維持する。
13. Contribution Recordは、価値に換算する前のEvidenceであり、Coreの通貨ではない。
14. Browser Workerは、保証の低い、一時的なprofileとして表現できる。
15. SecurityとSafetyのcapabilityのunknownは、安全な側に評価する。
16. Protocolは、言語に中立であり、Goの内部の型を、外部のcontractの正本にしない。

---

## Architectureで、最初に比べる領域

### A-1 — Authority / Trust Boundary Map

Client、Workerのidentity、Control Plane、Executor Admission、ArtifactとData、Verification、Federation Administration、Contribution、Local Governorについて、どのAuthorityが、どの署名鍵、Capability、永続的なstateに触れられるかを、先に定義します。

### A-2 — External / Internal Contract Boundary

TaskRequest、TaskIR、WorkOrder、ResultClaim、Verification Result、Accepted Result、Contribution Recordを分けます。

### A-3 — Control Plane / Data Plane

Controlのmetadataと、大きなArtifactの転送を分けます。Control Planeを、大きなdataのproxyにしない構造を、比べます。

### A-4 — Deployment Profiles

小規模な管理された組織、企業の混在したendpoint、Browserとアドホックな参加、federationと公益のGrantで、同じSecurity Principleを保ちながら、Componentの数を、規模に見合ったものにできるかを、比べます。

### A-5 — Persistence / Queue / Lease semantics

at-least-once、Lease、Idempotency、UNKNOWNのside-effect outcome、mass reclaim、retry budgetを、一つの状態モデルに統合します。

### A-6 — Extension SPI

Queue、Artifact Store、Identity、Executor Runtime、Verification、Attestation、Scheduler Policy、Contribution Measurementを、Core ContractとAdapterに分けます。

---

## Gateを通過した後のBranch Rule

Architectureの作業でも、1つのwork unitを1つのbranchとすることを、維持します。そして、PRとmergeの後に、branchをmainに再整列します。マージされていないbranchから、次のbranchを派生させません。

## 判定

2026-09-21の時点で、**Architecture baselineの作成を開始します。** Issue #19と#23、および上記の残っている研究は、継続します。Architectureが、未解決の研究を先取りして、特定の実装に固定しようとした場合は、Gate UのPASSを根拠に押し切りません。RequirementとAssurance Modelに戻って、再評価します。
