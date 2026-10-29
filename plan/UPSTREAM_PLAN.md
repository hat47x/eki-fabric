# EKI Fabric 上流工程計画

## 目的

EKI Fabricでは、実装しやすい技術を先に選び、その制約に合わせて要求を変える進め方を避けます。

製品価値、利用形態、脅威モデル、製品要求、受け入れシナリオ、保証モデル、品質特性を具体化します。リスクの高い点は小さな実現可能性検証（Spike）で確かめてから、設計へ進みます。

期限は設けません。各段階の終了条件を満たし、Architecture Entry Gateを通過した時点で次へ進みます。

## Phase 0 — 製品の価値と対象を定める（Product framing）

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

`docs/for-readers.md`、`docs/glossary.md`、`requirements/README.md`を用意し、参画に必要な前提知識を文書側で補う導線を整備済みです。

## Phase 1 — 要求と脅威を洗い出す（Requirements and threat discovery）

### 成果物

- `requirements/PRODUCT_REQUIREMENTS.md`
- `requirements/THREAT_MODEL.md`
- `plan/reviews/REQUIREMENT_GAP_REVIEW.md`
- 利用主体、保護対象、攻撃者、Security Invariant一覧
- Open Questions

### 終了条件

- malicious Client / malicious Worker / modified Agent / Sybil / collusion / resource exhaustionを扱う。
- compromised Validator / Verifier、replay、意図しないBrowser resource useを扱う。
- malicious Artifact / parser abuse、Federation namespace substitutionを扱う。
- Control Plane部分侵害とroot authority全体侵害を区別する。
- endpoint opt-out / human-first / network impact / energy / thermal safetyがRequirement化されている。
- unknown OSS / native / browser / external organizationがRequirement上で扱える。
- Compute Grant / Contribution / external value mappingがCore economic systemへ逸脱していない。
- 実現不能な絶対保証を避け、保証不能条件を記述している。
- 副作用Taskや非決定的AI Taskについて、EKIが保証できない部分を誇張しない。

状態: **第三巡目成立 / Open Questionの収束継続**

初版41 Requirementから、第二巡目で48 Requirement、第三巡目で53 Requirementへ拡張しました。第三巡目ではFederation Identity namespace、Artifact expansion、Control Plane authority separation、mass reclaim、Energy / thermal / device safetyを正式要求へ反映しています。

今後はRequirement数を増やすこと自体を目的にせず、残るOpen QuestionがArchitecture比較へ必要な粒度まで具体化されているかを確認します。

## Phase 2 — 受け入れシナリオを具体化する（Acceptance Scenarios）

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
23. Federation namespace / Trust Material substitution
24. Artifact expansion bomb / path escape
25. compromised Scheduler component
26. Public-interest Compute Grantのmass reclaim
27. Endpoint thermal / power emergency reduction

### 終了条件

- 各ScenarioからRequirement IDへtraceできる。
- Scenarioが新しい本質要求を発見した場合、Requirementを先に更新する。
- 特定Architectureでしか成立しないScenario記述を避ける。
- 主要Scenarioについて、正常経路だけでなくabuse pathと残る保証限界を説明できる。
- enterprise / ad-hoc / federation / public-interestのいずれか一つだけに最適化したRequirement集合になっていない。

状態: **第三巡目成立 / 横断品質評価へ移行**

今後はScenarioを機械的に増やすより、既存27 ScenarioをPhase 3のQuality AttributeとPhase 4のSpikeへ接続します。

## Phase 3 — 品質特性と保証モデルを定める（Quality attributes / assurance model）

### 成果物

- `requirements/ASSURANCE_MODEL.md`
- `requirements/QUALITY_ATTRIBUTES.md`
- Assurance gapとOpen Question
- Architecture比較へ使えるQuality Attribute一覧

Worker Assuranceは、単純な`trusted / untrusted`や一本のLevelではなく、Identity、Execution Environment、Confidentiality、Result Integrity、Independence、Operational History、Resource Measurement、Verification Authority、Evidence Freshnessを分けて評価します。

Quality Attributeでは、Securityだけでなく次を比較対象にします。

- Result assurance honesty
- Confidentiality fit
- Endpoint user impact
- Network efficiency
- Recoverability under churn
- Resource efficiency
- Energy / device safety
- Interoperability
- Operability / explainability
- Contribution measurement integrity
- Deployment proportionality

### 終了条件

- 「Workerが信頼できる」という曖昧な表現を、必要な保証軸へ分解できる。
- Attestation、reputation、replication、proofの役割を混同しない。
- Sybil / collusion条件下で、単純多数決の保証上限を説明できる。
- non-deterministic AI Taskについて、EKIが保証できる範囲と上位判断へ残す範囲を説明できる。
- Contributionに利用するResource Measurementのassuranceを表現できる。
- Requirementを満たす複数Architecture案について、Endpoint impact、Network overhead、Recoverability、Deployment burden等を比較できる。
- 小規模な社内構成と大規模Federationで、必要な運用負荷を無条件に同一化しない。

状態: **Quality Attribute初版成立 / 定量化・Spike接続を継続**

## Phase 4 — 実現可能性を調べる（Feasibility research / spikes）

Architecture採用前に、Requirement上のリスクが高く、文書検討だけでは判断できない点を小さなSpikeで確認します。

### 優先候補

1. **Local Governor**
   - user return / opt-out後の停止時間
   - CPU / GPU / Memory制限
   - Battery / AC / thermal変化への追随

2. **Browser Worker lifecycle**
   - tab background / suspension / close
   - resource limitの実効性
   - explicit participation UX

3. **Artifact locality / transfer cost**
   - cache hit時とmiss時のnetwork cost
   - model / dataset prefetch
   - transfer-aware placement

4. **Result verification cost**
   - deterministic validator
   - replication / quorum
   - managed recomputation
   - assurance向上に対する追加compute量

5. **Sybil-resistant independence constraints**
   - principal / trust domain / device / operatorの表現可能性
   - privacyとのTrade-off

6. **Artifact ingest safety**
   - expanded size / file count / nestingの制限
   - parser crashの隔離
   - path escape防止

7. **Executor provenance / content admission**
   - digest固定
   - signed / attested Artifactのverification
   - provenanceとsafe processingの分離

8. **Federation trust binding**
   - Trust Domain / Issuer / Trust Materialのbinding
   - revocation / rotation
   - namespace collision

9. **Control Plane authority separation**
   - Scheduler、Admission、Capability発行、Executor admissionを最小構成でどこまで分けられるか
   - 一部component compromise時のblast radius

10. **Mass reclaim recovery**
    - 多数Worker同時離脱後のretry amplification
    - Network / Queue / Artifact Storeへの二次負荷

11. **TEE Remote Attestation abstraction**
    - Evidence / Verifier / Relying Partyの分離
    - freshness / revocation
    - Vendor差の吸収範囲

12. **Verifiable computation**
    - 実用的なTask範囲
    - proof生成cost
    - GPU / AI workloadへの適用限界

13. **Side-effect Task uncertain outcome**
    - 実行済みか不明な状態
    - idempotencyあり / なしの差
    - Recovery boundary

14. **Contribution measurement**
    - self-reported / control-observed / attested measurement
    - Privacyと測定精度

Spikeは採用Architectureを既成事実化しません。確認したいRequirement上の仮説、実験条件、観測結果、一般化してよい範囲を残します。

状態: **主要高リスクSpike成立 / 実測研究継続**

最初のSpikeとして `spikes/local-governor/README.md` を作成しました。Windows Job Object / EcoQoS、Linux cgroup v2、ローカルopt-outの停止境界を調査し、Resourceごとに `HARD / BEST_EFFORT / OBSERVE_ONLY / UNAVAILABLE` の保証種別を区別する必要性を確認しています。

現時点ではCPU / Memory / Process数について主要OSの強制機構を利用できる見込みが高い一方、GPU、Network、Disk I/Oは同じ強さの共通制御を仮定できません。

Linuxでは、delegation済みcgroupを対象に制約を書き込み、Workloadをattachし、`cpu.stat`と停止時間を観測する実験Harnessまで追加しました。Unit Testと、制御Authorityがない環境で安全側に失敗するnegative pathは確認済みです。実際のCPU / Memory / PID / I/O hard enforcement値は、delegation済みhostでの測定を残しています。

Phase 4では、このように「APIが存在すること」ではなく、**要求した制約・実際の観測・広告するAssuranceが一致するか**をSpikeの成立条件にします。

Artifact ingestについてもSpikeを開始し、`spikes/artifact-ingest/` にZIPを対象とした最小の安全取込実装を追加しました。file数、展開後size、path深度、compression ratioを事前検査しつつ、実際の展開時にもstreamingでbyte上限を再確認します。directory traversal、backslashによる越境、drive-qualified path、symlink、special file、既存destinationへの上書きも初期状態では拒否します。

このSpikeでは、Artifactの署名・Digest・provenanceが正しいことと、安全に展開・解析できることを別の保証として扱えることを確認しています。現段階ではZIPだけを対象としており、TAR / gzip / 7z、nested archive、PDF / Office / Image / Model parserのProcess隔離、OS levelのCPU / Memory制約などは後続検証に残します。

Result Verification / Sybil耐性については、`spikes/result-verification-independence/` に決定論的な完全一致Resultを対象とする最小モデルを追加しました。単純なWorker台数quorumが同一Principalの複数Sybilに支配され得ることを再現し、matching Claim数とは別にdistinct Principal、distinct Trust Domain、managed anchor等をPolicy条件として扱えることを確認しています。

このSpikeはPrincipalやTrust Domainの独立性そのものを証明する方式ではありません。重要なのは、Result equivalenceとindependence evidenceを別の保証として保持し、複数の競合Result群が同時に条件を満たす場合には無理にAccepted Resultへ昇格させないことです。Identity / Attestation / Federationから独立性根拠をどう作るか、collusionへの保証限界、fuzzy / non-deterministic Result、追加replication costは後続検証に残します。

Artifact locality / transfer costについては、`spikes/artifact-locality-cost/` に最小のPlacement cost modelを追加しました。Input ArtifactのDigest / size、Worker側cache、download / upload throughput、予測compute time、Taskのtransfer budgetから、missing input transfer・expected output transfer・computeを合わせた概算完了時間を比較します。8 GiB級Artifactではcache済みの遅いWorkerが高速なremote Workerより有利になり得る一方、小さなArtifactではcompute性能差が勝つことをUnit Testで確認しています。

LocalityはOptimizationに限定し、Security / Data Class / Resource feasibilityのhard constraintを先に適用します。Cache情報もContent IntegrityやAuthorityの根拠にはせず、staleまたは誤った場合にPerformance degradationへ閉じる構造を目指します。現段階ではcongestion、shared link、prefetch、cache eviction、egress cost、mass retry時のduplicate transferなどを扱っておらず、後続検証に残します。

Browser Worker lifecycleについては、`spikes/browser-worker-lifecycle/` にBrowser参加状態とControl Plane側Leaseを分離した最小state machine、および実Browserでvisibility / freeze / resume / pagehide / discard復帰とheartbeat gapを観察するHarnessを追加しました。Background timer throttlingやdiscard時の通知不能を前提に、Lifecycle eventは早期回収のhintとして使い、最終的な回収は短いLeaseのheartbeat timeoutに依存する構造を確認しています。

初期Profileはvisible-onlyとし、tabがhiddenになればLocal Workerを停止して、visible復帰時にも古いLeaseを再利用せず新しいLeaseを取得します。明示的なStopは参加intent自体を解除し、後のvisibility changeで自動復帰しません。background-opt-inも表現できますが、可用性保証は引き上げません。Node 22で7 Unit Testが通過済みです。実Browserごとのheartbeat gap、freeze / discard、mobile backgrounding、multi-tab ownershipは後続の実測に残します。

Mass reclaim recoveryについては、`spikes/mass-reclaim-recovery/` に1,000 Taskの同時reclaimを扱う最小Modelとsimulationを追加しました。retryable / attempt budgetを満たすTaskだけをRecovery Queueへ入れ、backoff + jitterでretry eligibilityを散らしたうえで、時間窓ごとのrelease件数とestimated Artifact transfer bytesを別々に上限化します。64 MiB transferを要する1,000 Taskでは、素朴な即時retryが約62.5 GiBのtransfer demandを同時に作るのに対し、2 GiB / windowの例では32 Taskだけをreleaseし、残り968 Taskをdeferできることを確認しました。

このSpikeではplanned reclaim noticeをOptimization、無通知reclaimでも成立するbounded recoveryを保証側として分離しています。またpriority / deadlineは再入場順序に利用しても、Network BudgetやRetry Budgetを越えるauthorityにはしません。実Queue、dynamic capacity recovery、cache locality変化、priority starvation、複数Provider同時reclaimは後続検証に残します。

Control Plane authority separationについては、`spikes/control-plane-authority-separation/` にAdmission、Scheduler Proposal、Placement Guard、Artifact Authority、WorkOrder Authorityを分けた署名付き最小Modelを追加しました。Scheduler自身には署名Authorityを与えず、Confidential TaskをSecurity要件未達Workerへ提案してもPlacement Guardが拒否し、SchedulerがData Classを下げた偽Admission、任意Artifact Grant、WorkOrderを単独生成できないことをUnit Testで確認しています。

初期レビューでは、Placement Guardの検証用Trust Rootを呼び出し側から渡すAPIが権限境界を壊し得ることを検出し、依存Authorityの公開鍵をGuard内部へ固定しました。このSpikeはMicroservice数やEd25519採用を決めるものではなく、一つのBinaryでもAuthority / key handle / interface / audit eventを論理分離できるかをArchitecture比較へ持ち越します。

Federation trust bindingについては、`spikes/federation-trust-binding/` にTrust DomainごとのBundle分離、Federation Administrator署名、Bundle Version、Identity expiryを扱う最小Modelを追加しました。Company CのIdentityをCompany Bとして受理しないこと、同一Keyをprod / stageで共有していてもnamespaceを統合しないこと、未Federation Domainを推移的にTrustしないこと、古いBundle Versionのrollbackを拒否すること、rotation overlap後に旧Keyを削除できることをUnit Testで確認しています。

SPIFFE Federationの`<Trust Domain, Bundle>` binding原則を参考にしつつ、SPIFFE / SPIRE採用はまだ決めません。Bundle内容の正当性と「このBundleをこのTrust Domainへ対応付けてよい」という設定Authorityを分け、callerがTrust Rootを差し替えられないことをArchitecture比較の条件として残します。

Executor provenance / content admissionについては、`spikes/executor-provenance-admission/` にContent Digest、Publisher署名、Build Provenance、EKI Executor Admission、実行時の取得byte列再検証を分離した最小Modelを追加しました。可変alias / tagの参照先がAdmission後に別内容へ変わっても、ApprovalをDigest / Size / Runtime Class / Policy Revision / Expiryへbindingし、Worker側で実byte列を再hashすることで拒否できることをUnit Testで確認しています。

Publisher署名やProvenanceが暗号学的に正しいこと自体はExecutor Admissionを意味しないものとし、信頼Publisher、Builder、Source、Commit、Runtime ClassをPolicyへ照合してから別のAdmission AuthorityがApprovalを発行します。Digest revocation、Approval expiry、Policy Revision更新も実行時Guardで確認します。OCI Descriptor、Sigstore、SLSA / in-totoは有力なAdapter候補ですが、Core Contractへ固定せず、Supply-chain EvidenceとSafe Executionを別保証としてArchitecture比較へ持ち越します。

Side-effect Task uncertain outcomeについては、`spikes/side-effect-uncertain-outcome/` に外部副作用の有無、dispatch済みか、外部側の権威あるReceiptで確認できたかを分離する最小Recovery Modelを追加しました。non-idempotentな副作用をdispatchした後にWorkerが消え、Receiptを確認できない場合は`failed`や`retryable`へ丸めず`UNKNOWN`として保持します。一方、外部System側でidempotencyが成立するTaskは、同じOperation IDとRequest Digestを維持する場合に限ってretry可能とします。

Worker自己申告は外部副作用の確定根拠にせず、Effect AuthorityのReceiptをTask Intentへbindingして検証します。Cancellationもdispatch済みnon-idempotent effectのUNKNOWNを消しません。またidempotencyがあってもretry budgetは別に適用します。このSpikeはgeneric exactly-onceを保証するものではなく、effect semantics、stable operation identity、authoritative confirmation、UNKNOWN stateをTask Contractへどう表現するかをArchitecture比較へ残します。

Remote Attestation abstractionについては、`spikes/remote-attestation-abstraction/` にAttester / Evidence / Verifier / Attestation Result / Relying Partyを分離した最小Modelを追加しました。Evidence署名鍵をAttester / Trust Domainへbindingし、Challenge Nonce、Evidence age、Nonce replay、Verifier Policy Revision、Attestation Result expiryを別々のfreshness境界として扱います。Go 1.23.2でUnit Testを通過し、Worker自己署名の偽Attestation Result、別WorkerへのResult流用、Trust Domainすり替え、stale Evidence / Resultを拒否できることを確認しました。

VerifierがEvidenceを受理したことと、Task固有のPlacementを許可することは別Decisionとして維持します。Relying PartyはPinned Verifier Trust Root、Worker binding、必要Environment Class、最低Policy Revision、expiryを再評価します。またRaw MeasurementをRelying Party contractへ出さず、normalized Environment ClassとEvidence Digestだけを渡せることを確認しました。RFC 9334 / RFC 9711を参照しつつ、特定TEEやEAT形式はCoreへ固定せず、Vendor-specific Evidence Adapter、Endorsement / Reference Value、Key Release、GPU / I/O AttestationはArchitecture比較へ残します。

Contribution measurementについては、`spikes/contribution-measurement/` にResource Measurement、Result Evidence、Contribution Decisionを分離した最小Modelを追加しました。Measurement Observerごとに公開鍵と最大Assuranceをbindingし、Worker self-reportがcontrol-observed Policyを満たせないこと、WorkerがControl Plane Observerを偽装できないこと、Control observerがpayloadだけでattested-meteringへ昇格できないことをUnit Testで確認しています。Accepted ResultはResource量の測定根拠へ読み替えず、必要な場合はContribution Policyの別条件として扱います。

同一Provider / Worker / Task / Attempt / Metricの同一Measurement再送は同じRecordへ収束させ、競合するMeasurementは拒否します。RecordにはResource Class、Metric、Unit、Quantity、Measurement Assurance、Observer、Work provenanceを保持し、EKI Core共通のUniversal Score、Credit、金銭価値は生成しません。外部価値制度への換算はRQ-037 / RQ-038どおり外部Policyへ委ねます。実OS / GPU meter、cross-validation、Sybil / self-dealing、Privacy-preserving aggregationは後続検証へ残します。

Verifiable computationについては、`spikes/verifiable-computation-contract/` にExecution Claim、Proof Artifact、Proof System Adapter相当、Verification Authority、normalized Verification Result、Relying Party Requirementを分離した最小Contractを追加しました。Program / Input / Output / Attempt binding、Verifier署名、Proof System許可、Policy Revision / expiry、private-input requirementをUnit Testで確認し、valid Proofでも未承認Programを実行許可へ昇格できないことを実証しています。MockProofはContract Test用のstand-inであり、実zkVM proofではありません。

実prover benchmarkは現在runnerにRust / Cargo / GPU環境がないため未実施です。`BENCHMARK_PLAN.md` にnative baseline、guest cycles / trace、preprocessing、prove / verify time、peak RSS、proof bytes、Privacy mode、Hardware / version固定を含む再現可能な測定Protocolを定義しました。Phase 4の`proof生成cost`および`GPU / AI workloadへの適用限界`は、この実測が得られるまで未解決として残します。

## Gate U — Architecture Entry Gate

次を満たした時点で、初めてArchitecture baselineを作成します。

- Phase 0〜3の主要成果物がレビュー可能である。
- Phase 4の高リスク項目について、少なくとも採用判断に必要な実現可能性情報がある。
- Requirement間に重大な矛盾が残っていない。
- Security Invariantを破るArchitecture候補を、比較初期に除外できる。
- Reference Systemを「似ているから採る」のではなく、EKI Requirementに照らして採否判断できる。
- Browser / Native / managed / ad-hoc / federated Workerの差を、単一の曖昧なTrust Levelへ押し込めず説明できる。
- Result VerificationとConfidentialityの信頼基盤を分けて説明できる。
- Federation Identityのnamespace bindingを説明できる。
- ArtifactのProvenance / Integrity / Safe Processingを分けて説明できる。
- Control Planeの一部侵害とroot authority侵害を分けて説明できる。
- mass reclaimとEndpoint Safetyを通常運用の一部として扱える。
- 初見の開発者が文書導線から主要な設計判断へ辿れる。


状態: **条件付きPASS — 2026-09-21**

判定根拠は `plan/reviews/ARCHITECTURE_ENTRY_REVIEW_2026-09-21.md` にまとめました。Phase 0〜3の主要成果物と、Phase 4の主要高リスクSpikeについて、Architecture候補を比較するための境界・失敗条件・保証上限が説明可能な状態です。

Issue #19（learned scheduling / resource prediction）とIssue #23（実zkVM prover benchmark）は継続研究として残します。ただし、前者はSecurity / Authorization / Data localityのhard filter後段でshadow modeから扱い、後者はoptional Result Assurance extensionの実測です。いずれも未解決部分をBaselineへ先取りして固定しないことを条件に、Architecture baselineの作成開始を妨げないと判断しました。

Issue #19はその後、deterministic locality baselineを正本とした0A shadow observation、0B residual simulation、0C-1 comparison dataset freezeまで進みました。0C-1で見つかった「concrete split-group identityとgeneralization familyを同一視するとTest-A / Test-Bを作れない」という阻害要因は、Issue #53 / PR #54 / #55で分離・整合性検査までmainへ統合済みです。

次の研究work unitはIssue #50の0C-2（M1 linear residual / M2 completion probability）ですが、production Schedulerの優先順位を上げる作業ではありません。着手条件は、現在のmainに対してresidual-simulation / model-datasetのexact-head test Evidenceを残し、`model_fit_ready`が示すものを「preregistered dataset shapeの成立」に限定して確認することです。M0 durable State Storeを中心とするMVP主線の優先順位は変更しません。

Gate PASSは全研究の完了を意味しません。Architectureが未解決研究を前提に特定実装・特定保証へ固定しようとした場合は、Requirement / Assurance Modelへ戻って再評価します。

## Architecture段階で比較する領域

Gate Uは2026-09-21に条件付きPASSとなり、Architecture baseline作成を開始しました。`architecture/AUTHORITY_BOUNDARIES.md` にAuthority / Trust Boundary Map、`architecture/CONTRACT_BOUNDARIES.md` にExternal / Internal Contract Boundary、`architecture/CONTROL_DATA_PLANE.md` にControl Plane / Data Plane、`architecture/DEPLOYMENT_PROFILES.md` にDeployment Profile、`architecture/TASK_STATE_MODEL.md` にTask / Attempt / Lease / Recovery State Model、`architecture/EXTENSION_SPI.md` にExtension SPIをBaseline 0.1として定義しています。

Contract Boundaryでは、TaskRequestをhostile inputとして扱い、AdmittedTask / TaskIR、Attempt、PlacementProposal / Approval、Lease、ExecutorApproval、ArtifactGrant、WorkOrder、ResultClaim、VerificationResult、AcceptedResult、ContributionRecordの生成主体・binding・freshness・Client可視性を分離しました。Client / WorkerからのClaimをAuthorityが確定したDecisionへ直接読み替えず、signed objectはin-placeで書き換えずnew Attempt / revision / revoke / supersedeとして履歴を残します。

Control / Data Planeでは、Control Planeをmetadata / authority / stateへ限定し、bulk Artifact transferをData Plane Store / Cacheへ分離しました。Content Digest、Artifact Record、Storage Location、ArtifactGrant、transport-specific短命access、Cache localityを別概念として扱い、same digestによるcross-tenant authorization統合を禁止します。Worker read pathではGrant再検証→短命Data access→Digest検証→Executor公開の順序を標準とし、Upload / Result / Checkpointでもsize budget、safe processing、finalizationを別境界として扱います。

Deployment Profileでは、Logical AuthorityとContract semanticsを全Profileで共通に保ちながら、Process / Host / Network境界だけを規模に応じて比例化しました。DP-1 Small Managedでは単一`eki-control`への物理統合を許容しつつkey / interface / state / auditを論理分離し、DP-2 EnterpriseではGateway / Authority / Data Plane分離を強化できます。DP-3 Browser / Ad-hoc、DP-4 Federation、DP-5 Public-interest Compute Grant、DP-6 Dedicated Poolを定義し、Worker outbound-first、internal queue非公開、Federation非推移、Compute GrantとData Grant分離を共通Invariantとしています。

Task State Modelでは、Task Lifecycle、Attempt、Lease、Result Verification、External Effect、Cancellation Intent、Recoveryを直交する状態として分離しました。Retryは新Attemptを作り、Queue delivery retryでは新Attemptを作りません。Lease expiryやWorker disappearanceをTask failure / external effect failureへ読み替えず、non-idempotent effect dispatch後はauthoritative Receiptがない限りUNKNOWN / RECONCILINGを維持します。Mass reclaimはbackoffだけでなくrelease件数・transfer byte・compute budgetで再Admissionし、Task / Attempt / Lease revisionとfencingでstale writerやold sessionのAuthority復活を防ぎます。

Extension SPIでは、未知のApplication / OSSは原則External Client Contract + approved Executorで参加し、Control Plane pluginを要求しない境界を固定しました。SPIはState Store、Delivery、Artifact Store、Identity、Policy、Signer、Executor Runtime、Scheduler、Verification、Attestation、External Effect、Contribution Measurement、Telemetry等のFabric基盤差し替えに限定します。各AdapterはCore State / Authority semanticsを変更できず、silent weaker fallback、Client-driven plugin install、arbitrary DB / signing key accessを禁止し、Privilege Class・version・capability・Admin activation・conformance testを明示します。

P0 External Schemaは `spec/external/openapi.json` でmachine-readable化しました。Task submit/status/cancel/result、Artifact ingest intent/upload/descriptor、RFC 9457系Problem shapeを定義し、core objectをstrictに閉じています。`worker_id`、Lease、WorkOrder、ArtifactGrant token等はExternal schemaへ含めず、Executor固有parameterとnamespaced extensionのみを明示的拡張点としています。Artifact descriptorはContent Identity / Safe Processing / Provenance / Availabilityを別facetにし、単一trusted bitを作りません。

Protocol Technologyは `plan/adr/0001-protocol-surfaces-and-canonical-encoding.md` で初期選定しました。External Client PlaneはHTTPS + JSON + OpenAPI、Worker / out-of-process Adapter PlaneはProtobuf IDL + Connect reference transport、Authority署名対象はdeterministic CBOR + CDDL + COSE_Sign1とします。Raw JSON / raw Protobuf bytesをidempotencyやAuthority fingerprintのcanonical bytesへ使わず、strict parse / validation後のsemantic projectionをdeterministic CBORへencodeしてhashする方針です。

Architecture Baseline横断レビューでは、Authority / Contract / Control-Data Plane / Deployment / State / Extension SPIの間に重大な矛盾がないことを確認しました。`plan/reviews/BASELINE_REVIEW_2026-09-21.md` でArtifactGrantとData Grant、Result Verification / Acceptance / Task State、generic declassification非搭載等のcanonical解釈を整理しています。`architecture/MVP_IMPLEMENTATION_PLAN.md` では、malicious Client→Admission→Placement→signed WorkOrder→2+ Native Worker→ArtifactGrant→Result Verification / Acceptance→reclaim / bounded retryというE2E vertical sliceをMVPの中心に置き、Federation / Browser / TEE / zkVM / learned scheduler等は初回MVP必須から外しています。

この段階でも未解決研究を先取りして特定実装へ固定しません。Architectureの各作業単位は最新`main`から開始し、PR / merge後に履歴を収束させます。

後段で少なくとも次を比較します。

- external API contract: HTTP / gRPC / schema format
- Task Admission / internal WorkOrder representation
- queue / lease / scheduling substrate
- Artifact addressing / registry / cache / safe ingest
- Worker Identity / Workload Identity / Federation
- Executor packaging: Native / WASM / Container / Browser
- Sandbox enforcement
- Result Verification Provider
- Control Plane authority / signing boundary
- Telemetry / Audit
- Contribution accounting event model
- Browser participation UX / local resource control
- reclaim / backpressure / recovery model

## TEI適用方針 — Control / Application Planeは候補、Compute Kernelは個別判断

自作OSS群では、UI、Interaction、設定・Policy編集、Projection、業務／domain logic等のApplication PlaneをTEIで実装する方針を基本とする。

ただしEKIは本質的にdistributed compute infrastructureであり、全体をTEIへ移すことを既定にはしない。

### TEI-firstで検討する領域

将来、次のような人間向け・管理向けsurfaceを実装するときは、独自Web UIや管理frameworkを先に増やさず、TEIで実現できるかを第一候補として評価する。

- operator / administrator console
- Worker / Provider参加状態のinspection
- Policy / placement constraint / budget等の設定・review
- Contribution / Grant / Evidenceの可視化
- Task / Result / UNKNOWN / recovery状態のProjection
- local endpoint participation UXのうちApplicationとして表現できる部分
- audit / diagnostic / operational review surface

### native-firstで扱う領域

次は、TEI採用を既定としない。

- scheduler / lease / queue / retry / backpressure
- Worker Agent / Local Governor
- protocol / identity / federation / attestation
- sandbox / executor / artifact safe processing
- result verification kernel
- network / cache / locality optimization
- Browser Workerのlifecycle controlそのもの
- device / OS level resource enforcement
- security root / signing authority

これらはEKIのInfrastructure / Kernel Planeであり、TEI Application surfaceから操作・観測されることはあっても、TEIのApplication modelへ内部意味を押し込まない。

### 不足機能の扱い

EKIの管理surfaceでTEIに不足が見つかった場合も、EKI専用要求を直接TEI Coreへ追加しない。

```text
existing TEI
  -> reusable Plugin / Adapter / Capability
  -> native EKI integration
  -> cross-application Evidenceがある場合だけTEI Core gap候補
```

SUI SensemakingのReference Adoptionで得られるPlugin / Target / Projectionの知見を先に再利用し、EKI独自の管理UI基盤を新設する必要があるかを後から判断する。

この方針はGate U以前にTEIをArchitectureへ採用する決定ではない。EKI Core Architectureは引き続きRequirement / Scenario / Assurance / SpikeのEvidenceから比較し、TEI利用は主にApplication / Control surfaceの実装候補として扱う。

## Implementation Entry

Architecture Baseline 0.1とP0 External Protocolまで収束したため、実装入口へ進んでいます。

- `plan/adr/0002-reference-implementation-language.md` — Reference ImplementationにGoを採用。External Contractは引き続きlanguage-neutralな`spec/`を正本とする。
- `plan/IMPLEMENTATION_REPOSITORY_LAYOUT.md` — `cmd/`、`internal/core`、`internal/control`、`internal/agent`、`internal/spi`、`internal/adapters`、`spec/`、`conformance/`の責務と依存方向を定義。
- 初期Repositoryでは`pkg/`を作らず、Go内部型をpublic compatibility boundaryへ昇格させない。
- 空packageを大量作成せず、M0で必要になった順にdirectoryを追加する。

M0 executable skeletonの最初の作業単位として、`go.mod`、buildableな`eki-control` / `eki-agent`、typed Task / Attempt / Lease ID、SHA-256 Digest、TaskRecordの基本不変条件、State Store SPI、revision CASを持つin-memory reference adapterを追加しました。in-memory Storeはrestart耐性を持たないためM0完成条件には含めず、次のwork unitでdurable Storeを追加します。

ローカルでは`go test ./...`、`go vet ./...`、両binaryの`--version`を確認しています。検証環境のGo 1.23.2でもsource compatibility smoke testが通りましたが、Repositoryの要求versionはGo 1.27.0です。

`plan/adr/0003-m0-reference-state-store.md` で、M0 / DP-1向けReference durable State StoreとしてSQLiteを採用し、Go driverはcgo不要の`modernc.org/sqlite`を第一候補としました。適用範囲は単一Control Plane host + local filesystemに限定し、DP-2 multi-instance HAやnetwork filesystem共有をSQLiteへ背負わせません。WALは同一host制約を明示し、将来のPostgreSQL等は同じState Store SPIの別Adapterとして扱います。

SQLite Adapter実装に先立ち、`conformance/statestoretest`へBackend共通のState Store conformance harnessを追加しました。Create / Load / duplicate / CAS / stale revision / concurrent CAS / invalid record / cancellationを全Adapter共通契約とし、durable Adapter向けにはclose/reopen後のTask / revision保持を別契約として定義しています。in-memory Adapterも共通契約へ移行し、ローカルで`go test ./...`と`go vet ./...`を通しています。

次のConcrete Work UnitはSQLite durable State Store reference adapterの実装です。依存はGo toolingで`go.mod` / `go.sum`を生成・検証できる環境で導入し、共通conformanceに加えてmigration / WAL / restart testを通してからmerge対象にします。現在の実行環境は外部Go module取得を許可していないため、未検証のdependency lockはcommitしません。

SQLite依存導入と並行せずに進められるProtocol workとして、P1 Worker Planeを`spec/worker/v1/worker.proto`へ定義しました。Worker Identityは認証contextに置き、messageに`worker_id`を持たせません。Worker→Controlはidempotentな`PostEvents`、Control→Workerは`WatchCommands` server-streamingまたは`PollCommands` fallbackとし、HeartbeatとLease Renewalを分離しています。WorkOrder / Lease等のSecurity-sensitive payloadはProtobufへ署名せず、P2 COSE payloadを`SignedDecisionCarrier`で運ぶ構造です。

P2 Signed Authority Decisionsは`spec/decisions/v1/decisions.cddl`へ定義しました。Placement Approval、Lease、Executor Approval、Artifact Read/Write Grant、WorkOrderをdeterministic CBOR payloadとして閉じたschemaにし、Architecture上のAuthority role identifier（EXE-1 / PLC-1 / LSE-1 / DAT-1 / WOR-1）を署名対象へ直接bindingします。WorkOrderの前提Decision refはkind-specific型とし、Decision IDに加えてdeterministic payload digestへbindingします。P1 ProtobufはCOSE_Sign1 bytesのcarrierに限定し、Security Authorityをtransport serializationへ依存させません。

Signed Decision Security Profileは`plan/adr/0004-signed-decision-cryptographic-profile.md`で定義しました。M0 software referenceはEd25519 / EdDSA、Enterprise/KMS互換profileはES256 / P-256を第一級にし、Authority roleごとに別keyを持ちます。`kid`はAdmin-managed Key Registryのopaque selectorであり、COSE messageからTrust Rootを持ち込みません。Key stateはPENDING / ACTIVE / VERIFY_ONLY / REVOKED / COMPROMISEDを区別し、rotation・revocation・trust bundle freshness・Verifier順序を明文化しています。

Go Coreでは`internal/core/authority`と`internal/core/decisiontrust`へ最小domain boundaryを追加しました。P2 Decision kindとAuthority roleの対応をGo constantsでも固定し、Signer/Verifierのkey selectionはDecision kindを含むtyped requestだけを公開しています。Key RegistryはEdDSA/Ed25519とES256/P-256のalgorithm/curve整合、`kid`再利用禁止、primary signing key、PENDING→ACTIVE→VERIFY_ONLY等のlifecycleを扱います。初回activation以前のDecision、およびVERIFY_ONLY移行後に新規発行されたDecisionはverification対象にしません。現在のRegistryはM0 in-memory semantics / worker trust snapshotであり、Administrative durable source-of-truthではありません。

Signed Decision verifier boundaryのpre-crypto層を追加しました。Trust Bundle revision/freshness、Deployment algorithm allowlist、Decision issued/expires、種類別maximum lifetimeをstandard libraryだけで検証し、Registry lookupをmachine-readableなverification reasonへ正規化します。signature validityをAuthorizationへ読み替えず、COSE/CBOR/cryptographic verificationより前にfail-closedで停止できる境界です。

その後段としてstandard-library cryptographic primitive boundaryを追加しました。M0 Reference Software SignerはRegistry-selected primary keyだけを使い、EdDSA/Ed25519とES256/P-256を実装します。ES256はCOSEの64-byte raw `R || S`形式へ固定し、private/public key binding、policy revision、sign前後のRegistry再検査を行います。Verifierはpre-crypto gate→signature verification→pre-crypto再検査の順とし、途中revocationを可能な範囲でfail-closedにします。Software Signerはprocess memoryへprivate keyを保持するM0/DP-1 referenceに限定し、DP-2はKMS/HSM Adapterを前提とします。

COSE decoder実装に先立ち、`plan/adr/0005-strict-cose-sign1-decoder-boundary.md`と`spec/decisions/v1/cose-profile.cddl`でwire contractを固定しました。EKI Baselineはtag 18付きCOSE_Sign1、empty unprotected map、attached payload、protected `alg` / `crit` / `kid` / private-use profile label `-70000`、exact profile `eki.signed-decision/v1`へ閉じます。Go constantsとCDDLのdriftはconformance testで検出します。

Concrete CBOR codecの第一候補はMIT Licenseの`github.com/fxamacker/cbor/v2`です。Core Deterministic Encoding、duplicate map key rejection、indefinite-length rejection、parser resource limitsをEKI strict profileへ利用します。`github.com/veraison/go-cose`はCOSE_Sign1 / Sig_structureの参照実装として照合しますが、EKI固有のAuthority / key lifecycle / algorithm policy / cryptographic primitiveと責務が重なるためBaselineの直接dependencyにはしません。

現在の実行環境では`proxy.golang.org`へのegressが拒否され、dependencyを取得・lock・testできないことを再確認しました。そのため未検証の`go.mod` / `go.sum`は追加していません。

dependency取得不能時の先行作業として、strict COSE conformance vectorsを`conformance/vectors`へ追加し、envelope / protected / payload / cryptoのfirst-failure semanticsをstable `VerificationReason`とともに固定しました。続いて`internal/spi/cosedecoder`へConcrete codecの最小interfaceを追加し、decoded Envelopeのdefensive copy、attached empty bstrとdetached nilの区別、64-byte signature、algorithm / key ID invariantを型境界で保持します。`conformance/cosedecodertest`は同一fixtureを`go:embed`経由でConcrete Adapterへ適用し、正常系Sig_structureとtampered signatureの層分離まで検査します。

次のConcrete Work Unitは、dependency取得可能環境でfxamacker/cborをversion固定し、`internal/adapters/reference/cosedecoder/fxamacker`（仮）としてstrict COSE_Sign1 decoder / deterministic re-encode check / Sig_structure encoderを実装し、このSPIと共通conformance harnessへ接続することです。

### 2026-09-27 mainline priority guard

current main `9e990e7c89924fb4005264e7556b41e0e3fb62b1` を計画と再照合した。

現状は次のとおりです。

- `go.mod`はGo 1.27.0だけを宣言し、外部module requirementはまだ固定していない。
- SQLite durable State Store adapterは2026-10-06に実装済み（ADR-0003 §14）。restart後のTask identityとrevisionの保持を確認した。Attempt / Lease、delivery intent等のtableは未実装。
- strict COSE decoderのConcrete Adapter（fxamacker/cbor）は2026-10-06に実装済み（ADR-0005 §18）。全conformance vectorを通り、`kid_too_long` vectorのbytesを修正した。
- learned scheduling関連のresearch codeはmainへ入っているが、MVPではResearch / laterの位置づけを維持し、M0 blockerより先に昇格させない。

依存取得可能な検証環境が戻った場合の優先順位を次で固定する（1と2は2026-10-06に完了）。

1. ~~**SQLite durable State Store**~~ **完了** — `modernc.org/sqlite`をversion固定し、共通State Store conformance、migration、WAL、close/reopen restart testを通す。これはM0 gateの直接blocker。
2. ~~**strict COSE_Sign1 Concrete Adapter**~~ **完了** — `fxamacker/cbor/v2`をversion固定し、deterministic re-encode / Sig_structure / negative vectorを既存conformanceへ接続する。
3. ~~Task submit / status / cancel~~ は2026-10-06に完了（ADR-0006）。次にAttempt / Lease の永続化とLSE-1、Worker registration、one Worker executionをdurable state上で接続する。

依存取得不能の間は、未検証の`go.mod` / `go.sum`、placeholder adapter、実行していないverification結果をcommitしない。Research laneを進める場合も、M0 blockerの代替進捗として扱わない。

### 2026-10-06 位置づけの見直し — 管理クラスタとの統合とTEIの稼働基盤

今後2〜5年の生成AI・AI Agentの普及を想定し、Product Valueの位置づけを見直した。検討は [AI時代の価値と位置づけ](../research/topics/ai-era-value-positioning.md) と [既存オーケストレータとの接続、およびSEI・TEIとの層構造](../research/integration/orchestrator-integration-and-layering.md) にある。

決めたこと（正本は[Product Value](../product/value.md)）:

- EKIの価値の中心を、遊休資源の安さではなく、信頼性の異なる多様な資源への統治された配置と検証可能な実行に置く。
- 管理された資源（専用サーバ、クラウド、既存オーケストレータが管理するクラスタ）と遊休・一時資源を、同じTask契約で扱う。既存オーケストレータは置き換えず、資源の供給元として複数系統に接続する。
- TEIはEKIを実行基盤の一つとして使い、その上で稼働する（TEI → EKIの一方向）。
- 統治の方針は、Clientの経路ではなくAdministrative Planeの経路でEKIへ入れる。

まだ決めていないこと:

- 最初のResource Provider Adapterの参照実装の範囲。調査メモではKubernetes + Kueue、次にSlurm、次にNomadを推奨しているが、利用組織の実環境の確認が必要。
- Resource Provider Adapterの契約の語彙、管理クラスタの信頼ドメインの扱い、選択肢B・C（調査メモ§3）の扱い。
- 候補Scenario PAS-CAND-07〜16の評価は完了した（[評価記録](reviews/PAS_CAND_07_16_EVALUATION_2026-10-06.md)）。RQ-054の草案のレビューと、Open Questionの解決が残る（[#120](https://github.com/hat47x/eki-fabric/issues/120)で追う）。

この見直しは、上のmainline priority guardの順位を変えない。Resource Provider Adapterの検討は、M0 vertical sliceの成立後に、Spikeとして始める。

## 作業原則

- Requirementの不確実性を減らす作業を優先する。
- 実装しやすさだけを理由にRequirementを弱めない。
- 既存OSSで十分な部分は積極的に再利用する。
- 既存OSSの前提がEKIと異なる場合は、その差を明記し、Adapterまたは独自実装の必要性を判断する。
- 一つのReference Systemへ全体Architectureを寄せすぎない。
- 文書の自然な日本語と背景説明を重視し、専門知識がないことを読者側の責任にしない。
- 説明を厚くしても、Requirement、Threat、ADRの正本を複製しない。
- 作業ブランチは目的単位で作成し、完了した変更は元の`main`へPR経由で収束させる。後続作業を未マージbranchから派生させて履歴を断片化しない。

### 2026-10-06 product value ADRs

製品価値（`product/value.md`、`product/principles.md`）を設計と評価の判断へつなげるため、次のADRをProposedとして追加した。技術ADR（0001〜0006）の優先順位は変えない。これらのADRを、M0 blockerを解消する作業の代わりの進捗として扱わない。

- `plan/adr/0029-workload-suitability-gate-before-placement.md`: 「EKIへ載せない」判断を、Schedulerより前のGateで行う（RQ-014、#58）。
- `plan/adr/0008-product-value-evidence-metrics.md`: 成功指標を、Worker数やFLOPSではなく、守られた人と検証済みの成果で測る（#65、#66）。
- `plan/adr/0009-managed-resources-as-providers.md`: 管理クラスタを置き換えず、Resource Providerとして接続する。管理されていることを理由に検証を省かない（#67）。
- `plan/adr/0010-contribution-record-is-not-currency.md`: Contribution Recordに残高、移転、価格を持たせない（RQ-038、#68）。

隣接Product（SUI、SEI、TEI）に依存していないことの機械的な検査は、#69で扱う。

### 2026-10-08 製品価値の中核（core）の統合状況

2026-10-08時点で、次のcoreがmainに入っている。いずれも、入力と時刻を外から受け取って判定を返す純粋な実装であり、I/O、時計の読み取り、Goroutineを持たない。Adapterへの接続と、Attempt / Leaseとの統合は含まない。

- Local Governor（`internal/agent/governor`）: 人の操作を最優先し、opt-outと中央Grantによる絞り込みを判定する。古い読み取り値の場合は、より制限的な側に倒して判定する。
- Contribution Record（`internal/core/contribution`）: 測定のassuranceを閉じた単位で表す。残高、通貨、価格の欄は持たない（ADR-0010）。
- Result Acceptance（`internal/core/resultaccept`）: 独立性の次元、票、RA-level、NotGuaranteedを分けて扱う。identityの値はUnicodeの書式文字（Cf）を拒否する。
- Artifact ingest admission（`internal/core/artifactsafety`）: ZIPの展開量、圧縮比、名前の大文字小文字の畳み込み、拡張フィールドの曖昧さを事前に判定する。
- Effect state（`internal/core/effectstate`）とDispatch guard（`internal/core/dispatchguard`、`internal/spi/dispatchlog`）: commit-before-dispatchを守る。非冪等な効果は、dispatchを一度も記録していない世代に限って再実行する（ADR-0007、暫定）。
- External Effect適合試験（`conformance/externaleffecttest`）: 外部呼び出しが記録より先に始まらないことを、失敗系を含めて確認する。参照実装であるdispatchguard.Guardは、メモリ版とSQLite版の両方でこの試験を通る。Adapter SPIの形はまだ決めていない。
- Compute Grant（`internal/core/grant`）: 発行できるのは、Grantが名指しした提供者だけである。資源クラスごとの上限と同時利用の割合を検査する。失効は台帳（Ledger）に記録する。一つのGrantに対しては最初の失効だけが有効になり、後からの失効は拒否する。同じ内容で発行し直したGrantも、同じ失効の対象となる。判定に使う台帳がnilの場合は拒否する。
- Federation trust binding（`internal/core/federation`）: Trust Domainには、一度に一つのBundleだけを対応付ける。Bundleの版番号の巻き戻しと、同じ版番号での内容の食い違いを拒否する。Ed25519の小さな位数の公開鍵（単位元を含む）と、非正規のy座標をもつ公開鍵は、Bundleの鍵としても認可鍵としても受け入れない。

レビューで見つかり、修正した欠陥は次のとおりである。

- Compute Grant: 失効が値ごとのフラグで表されていたため、失効していない複製に対して再び失効させることができ、停止時刻を後から動かせた。失効をDigestごとの台帳で管理する形に修正した（ee224d1）。
- Federation trust binding: Ed25519の検証は単位元を公開鍵として受け入れる。そのような鍵がBundleの鍵や認可鍵に入ると、任意のメッセージに対する署名を偽造できた。小さな位数の鍵と非正規の鍵を拒否するように修正した（4ba45a2）。

まだ決めていない事項、または未着手の事項は次のとおりである。

- Attempt / Leaseの永続化とLSE-1は、別の作業で扱っている。M0 gateの直接blockerのままである。
- External Effect Adapter SPIの形。境界は、ADR-0011で暫定に決めた。記録はCoreが書き、Adapterは宣言と呼び出しだけを提供する。EffectProfileの型と、Receipt検証のSPIは未決である。
- 失効台帳（Compute Grant）とFederation Registryの永続化。プロセスの再起動で状態が失われると、失効した Grant の再受理や、失効した Domain の再 Federationが起きる。ADR-0012に従い、別の作業で進めている。
- 上記coreのAdapterへの接続。具体的には、Compute Grantの発行、Federationの取り込み、Local Governorの実行への組み込み。
- Compute Grant、Federation、Local Governorの上流要件の確定。業務フローに依存する判断は、ADRで暫定として記録する。

### 2026-10-19 TEIからの利用による要件の問い直し

TEIがEKIを実行基盤として使う場面から、EKIの要件と設計を問い直した。検討は[TEIから使う分散基盤としてのEKI](../research/integration/tei-usage-requirements-review.md)、実測は[TEIの仕事の投影Spike](../spikes/tei-workload-projection/README.md)、評価はGitHub Issue [#113](https://github.com/hat47x/eki-fabric/issues/113)とその子Issueで行う。候補Scenario PAS-CAND-17〜23を[Acceptance Scenarios](../requirements/ACCEPTANCE_SCENARIOS.md)へ登録した（評価待ち）。

分かったこと:

- TEIの決定的な操作は5〜11ミリ秒で終わり、EKIへ一件投げる往復（受理の永続化だけ）と同じ大きさだった。小さな仕事を続けて流す形態（RQ-055草案、[#106](https://github.com/hat47x/eki-fabric/issues/106)）が要る。
- TEIの意味のある仕事は、Artifactの経路が未実装のため、現在は一件も受理されない。
- 二経路での照合は、EKIの検証より先に、実行体の設定（`-trimpath`など）で一致・不一致が決まる。

決めていないこと:

- RQ-055〜RQ-058の草案と、RQ-026・RQ-011への追記案のレビュー（[#106](https://github.com/hat47x/eki-fabric/issues/106)〜[#111](https://github.com/hat47x/eki-fabric/issues/111)）。正本には入れていない。Issueで整理し切ったものからADRに起こす。
- 「つなぐ」形態、資源プール、等級を、M1以降のどの段階で扱うか。

この見直しは、mainline priority guardの順位を変えない。

### 2026-10-20 プロダクトの外面とコア

EKIを、利用者が導入する理由になる外面と、どの外面からも共通に使われるコアに分けて捉え直した。判断は[ADR-0013](adr/0013-product-core-and-faces.md)、検討は[プロダクトの外面とコア](../research/topics/product-scope-direction.md)、正本の反映は[Product Value](../product/value.md)の「外面とコア」にある。

決めたこと:

- コアは、分散処理そのものではなく、委任先ごとに信頼の水準を定め、条件の合う委任先へ範囲と期限を限って仕事を任せ、結果を確かめて記録する「委任の核」とする。分散はその使い道の一つで、一台からでも導入できるようにする。
- 信頼の原則を「暗黙には信頼しない。信頼は委任先ごとに、管理者が根拠とともに明示して定める」と言い直した。
- 委任先は、目的に応じた指名か、EKIに委ねる自律的な選択で決める。自律に委ねた依頼では、許された余力、管理資源、クラウドの自動拡張を予算の範囲で一元的に最適化する。これを価値の柱とする。
- 外面は、広域の資源最適化と分散計算、AIの推論とツール実行の統治、AIが生成したコードの隔離実行、常駐する軽量な認知処理の実行面の四つとする。
- 常駐は「存在は終わらないが、権限は有限」として、期限つきで更新されるLeaseの連なりで扱う。ADR-0009の決定4をこの条件の範囲で改めた。
- 核は自前で持ち、推論エンジン、隔離実行、ゲートウェイ、ビルド実行、クラスタ管理などは外部のOSSと連携する。
- 将来は全社・全組織の資源管理を一元化し、SEIの組織レベルの意思決定と連動した自動のオーケストレーションを目指す。SEIは、管理者権限の一部を範囲と期限を限って委任された委任先として扱う。

展開はGitHub Issue [#137](https://github.com/hat47x/eki-fabric/issues/137)（子Issue #129〜#136）で追う。この見直しは、mainline priority guardの順位を変えない。

### 2026-10-20 委任の核の概念モデル

ADR-0013の子Issueが共通に前提とする概念を揃えるため、委任の核を概念モデルにまとめた（[委任の核の概念モデル](../research/topics/delegation-core-model.md)）。八つの概念（委任先、利用の許可、予算の枠、依頼、委任の決定、権限、申告と確定、記録）と、どの外面・型・経路でも守る八つの不変条件（I-1〜I-8）を定めた。

既存のAuthority Boundariesと突き合わせ、SCH-1、PLC-1、GOV-1はそのまま自律の最適化を受け止められる一方、予算（BUD-1）、容量（CAP-1）、委任先と信頼の水準の登録（DLG-1）、管理者権限の委任状が足りないことを確かめた。権限の設計は[#147](https://github.com/hat47x/eki-fabric/issues/147)で扱う。

候補Scenario PAS-CAND-24〜30を加え、評価を[#148](https://github.com/hat47x/eki-fabric/issues/148)に置いた。7件とも不変条件と既存の要件で方向は扱え、新しいRequirementは作っていない。評価から分かった点は、子Issue（#130、#138〜#143、#145）へコメントで返した。

### 2026-10-20 Authority Boundaries 0.2

[ADR-0014](adr/0014-authorities-for-delegation-budget-capacity.md)で、委任先と信頼の水準の登録（DLG-1）、予算（BUD-1）、容量（CAP-1）の権限と、ADM-1の管理者権限の委任状を加え、[Authority Boundaries](../architecture/AUTHORITY_BOUNDARIES.md)をBaseline 0.2へ上げた。権限の有限性（§3.7）と、信頼の水準を登録から読む原則（§3.8）を基本原則に加え、ADM-2、LSE-1、RVR-1を改めた。ほかの五つの設計文書は0.1のまま。

M0とMVPは0.1の権限の上で進めてよく、新しい権限はADR-0013の段階（S1以降）で実装する。mainline priority guardの順位は変えない。

### 2026-10-20 検証環境を整え、回収後の再投入を予算で区切る

これまで「この実行環境ではGoを取得できず、`go test ./...`を実行していない」という理由で、draftのまま止まる作業や、CIを見ないままmergeされる変更がありました。まず、その状態を解消しました。

- Go 1.27.3を用意し、`go mod download`、`go build ./...`、`go vet ./...`、`go test ./...`を実行できるようにしました。
- `scripts/verify.sh`と`scripts/verify.ps1`を追加し、gofmt、`go vet`、`go test`を一度に確かめられるようにしました（[#152](https://github.com/hat47x/eki-fabric/pull/152)）。
- gofmtされていなかったGoのソース30件を整形しました。CRLFの作業ツリーでも同じ判定になるよう、`.gitattributes`で改行を固定しています。
- Windowsでだけ落ちていた`TestPathWithReservedCharacters`を直しました。ファイル名に`?`を使えないOSでは、そのOSで使える文字でファイルを作り、`?`のエスケープはDSNの組み立てを直接確かめるTestで確認します。

この結果、`go test ./...`は全packageで通っています。以後、実装の変更には`scripts/verify`の出力をEvidenceとして付けます。

次に、回収後の再投入の境界をCoreとして実装しました。

- [ADR-0015](adr/0015-reclaim-as-authority-revocation-and-bounded-recovery.md): 回収を実行権限の失効として扱い、回復を新しいAttemptの受理にする。attempt budget、release count、transfer / computeの三つを別々に判定し、priorityは順序だけに使う。
- `internal/core/recovery`: 時計もI/Oも持たない純粋なCore。`Classify`が回収されたAttemptを保留と再投入候補に分け、`Release`が時間窓の予算内で`Permit`を発行する。`Permit`はTask、TaskIR、前のAttempt、次のAttempt番号、policy revision、通し番号、使用可能時刻、期限へbindingし、packageの外から作れない。
- 1,000 Task / 64 MiB / 2 GiB窓の条件で、Spikeと同じく32件しかreleaseしないことをTestで固定しました。count予算、compute予算、単体で窓を超えるTask、重複配送、deadline経過、clockの逆転、予算の破損、Permitの再利用もTestにしています。

決めていないこと:

- `Pending`、窓の消費量、`Permit`の耐久化と、Attempt / Leaseの更新と同じtransaction境界への接続。着手条件は、[#57](https://github.com/hat47x/eki-fabric/issues/57)のvertical sliceで回収からretryまでの状態遷移が一つにつながることです。
- 回収イベントを確定する権限。Workerの自己申告から作らず、Leaseの失効とAttemptのcloseから導きます。
- 窓をまたいだ公平性。urgentが続く場合のaging、fair share、reserved recovery capacityの比較。
- 単体で窓の予算を超えるTaskの扱い。

この作業は、mainline priority guardの順位を変えません。M0 vertical sliceの次の単位は、引き続きWorker Planeとone-Worker executionの接続です。

### 2026-10-20 委任の等級の表し方

ADR-0013で残した「信頼の水準を、Assurance Modelの軸の組み合わせで表すか、名前を付けた段階で表すか」という問いに、[ADR-0016](adr/0016-delegation-class-as-registered-profile.md)で答えました。

- 信頼の水準を、管理者が登録する名前付きで版を持つ等級（Delegation Class）として表します。名前は組織が決め、EKI Coreは普遍的な信頼の順位表を同梱しません。
- 等級は単一のスコアではありません。認めるProfile（RA・CA）、検証の強さ、Data Classの上限、仕事の型、Leaseの寿命、予算と容量の上限、Evidenceの鮮度と失効を明示し、Assurance Modelの軸へ対応づけます。
- 実効の条件は、Taskの要求と等級の範囲のうち厳しい方で決めます。等級は「ここまで任せてよい」という上限であり、Taskの要求を下げる権限ではありません。
- 等級は委任先の自己申告から決めません。未登録は委任不可とし、判断は使った等級の版へbindingします。

M0とMVPは、等級が一つだけの状態として今の方針を移せます。新しい権限と同様、S1以降で実装します。この判断はmainline priority guardの順位を変えません。

実装と運用に残る問いは、[#138](https://github.com/hat47x/eki-fabric/issues/138)で続けます。

### 2026-10-20 WorkOrderのP2 payload

one-Worker executionへ進む前提として、WOR-1が署名する`eki.work-order/v1`の意味と、strictなCBOR codecを追加しました。

- `decisionpayload.WorkOrder`は、Task、TaskIR、Attempt、Worker、Placement Approval、Lease、Executor Approval、read / writeのArtifact Grant、resource limits、sandbox policy、外部副作用のmode、nonceを一つのpayloadへbindingします。
- 参照は型付きの`DecisionRef`とし、期待するDecision kindと一致しない参照、重複したGrant、上限を超えるGrant数を拒否します。
- `v1`では外部副作用のmodeを`none`に限定します。副作用のある仕事は、別の契約と検証が要るためです。
- Reference adapterは、`work-order-v1`のcanonical encodeとstrict decodeを実装しました。非決定的なmap順、未知のfield、上限超過、切り詰めを拒否します。
- `WorkOrder.Valid()`やdecodeの成功は、署名検証でも、参照先のPlacement Approval / Lease / Executor Approvalがcurrentであることの証明でもありません。

次の単位は、Worker側でWorkOrderを検証する境界です。署名、参照先Decisionのcurrentness、nonceの使い捨て、resource limitsとLocal Governorの突き合わせを、Leaseの検証とは別に扱います。

### 2026-10-20 Worker側のWorkOrder検証

`internal/core/workorderverify`を追加し、Workerが受け取ったWorkOrderのbytesを、実行指示として扱う前の境界を実装しました。

- strict COSE_Sign1 decode、strict `work-order-v1` decode、decision kindとAuthority roleの確認。
- 自分（Worker Identity）宛であること。他のWorker宛のWorkOrderは`ReasonSubjectMismatch`で拒否します。
- 発行時刻・期限・Trust Bundleの鮮度を、`decisiontrust.VerifierBoundary`のpre-crypto検査で確認します。
- Trust Domain、WOR-1のprincipal、kid、algorithmを現在のTrust Bundleと鍵の状態に照らし、署名を検証します。
- 失敗はzeroの`Verified`と安定した`VerificationFailure`の理由になり、成功しても`Verified`は一つの観測です。

`Verified`は実行の許可ではありません。参照先のPlacement Approval / Lease / Executor Approvalのcurrentness、Leaseのfencing、nonceの使い捨て、resource limitsとLocal Governorの突き合わせは、まだ別の境界として残しています（[#160](https://github.com/hat47x/eki-fabric/issues/160)）。

Testでは、正しいWorkOrderの受理に加えて、別のWorker宛、発行前、期限後、署名の改ざん、Lease payloadの取り違え、別のTrust Domain、古いTrust Bundle、空のbytes、設定の不足を拒否することを固定しました。

WorkOrderのnonceを使い捨てるための最初の部品として、`internal/spi/replayguard`と`internal/adapters/reference/replayguard/memory`も追加しました。

- `Claim(nonce, until)`は、未使用ならtrue、使用済みで期限内ならfalseを返します。
- 満杯のときは、まだ期限内の記録を追い出さずに失敗します。追い出すと、nonceを再利用できてしまうためです。
- 期限ちょうどの時刻では記録を失効させ、再利用を許します。
- メモリ実装は再起動で記録が消えるため、Worker実行の証跡ではありません。durableなStoreへのAdapterは次の作業です。

その後、WorkOrderの受理を一つの境界にまとめました（`internal/core/workorderaccept`）。

- 署名済みWorkOrder、Worker自身のLease記録、Placement Approval、Executor Approval、nonceの獲得を突き合わせます。
- 署名、宛先、期限、Trust Bundleの検証は`workorderverify`に任せ、この境界はWorkerのcurrent stateとの一致と、一度だけの使用を扱います。
- nonceは、ほかのすべての確認が通った後でだけ獲得します。不正な指示でnonceを消費しません。
- 成功すると`Admission`を返します。`Admission`は、受理時刻における指示のcurrentnessとnonceの未使用を示す証拠であり、Leaseでも実行の許可でもありません。Local Governorの判断を上書きしません。

Local Governorの現在の上限との突き合わせ、Lease更新時の再受理、nonce記録の耐久化は、まだ残しています（[#160](https://github.com/hat47x/eki-fabric/issues/160)）。

### 2026-10-20 開始前のLocal Governorとの突き合わせ

Admissionを受けた後に、端末側の上限と実行を始めてよいかを決める`internal/agent/startgate`を追加しました。

- WorkOrderの上限（Control Planeが許した量）と、Local Governorの上限（端末の利用者が許した割合）を、MachineProfileの実測容量を通して比べ、小さい方を実効上限にします。
- CPUとMemoryは容量×割合で按分し、GPUは台数で按分します。Temporary Disk、Network、出力byteは、この版では使用の可否だけを見ます。
- `StopRunning`、`AcceptNewLeases=false`、必要資源の上限0、端末容量の不明、Admissionの期限切れを拒否します。
- 成功すると`Plan`を返します。`Plan`は実行の開始ではなく、その時点でその上限なら開始してよいという証拠です。以後のLocal Governorの判断を固定しません。

Testでは、按分の計算、WorkOrderの上限が小さい場合、停止・不受理、資源ごとの0、容量不明、期限切れ、GPUの按分、wall timeによる期限の切り下げを固定しました。

残りは、実行環境への上限の適用、実行中のGovernor再評価と停止、Lease更新時の再受理です。

この判断を[ADR-0017](adr/0017-start-and-stop-authority.md)として整理しました。実行の開始は「WorkOrderの受理」と「Local Governorの現在の判断」の両方を要し、実効上限は厳しい方で決めること、停止の判断は常に端末側にあり、停止は実行権限の失効として回復の境界へ渡すことを定めています。`Admission`（受理）、`Plan`（開始）、Runtime（実行）、Governor（停止）を別の型として保ちます。

このADRに沿って、実行中の監督を`internal/agent/executor`へ追加しました。

- `SpecFromPlan`が、`Plan`の実効上限、sandbox、期限、WorkOrderの識別子とnonceを`Spec`へ写します。`Spec`は認可の根拠ではありません。
- `Supervisor.Execute`は、期限つきのcontextで`Runner`を実行し、Local Governorの`StopRunning`、期限、呼出し側のcancelを監視します。
- 終了は`Outcome`として、`completed`、`failed`、`reclaimed`、`deadline_exceeded`、`cancelled`、`runner_stuck`のいずれかに分類します。
- `reclaimed`は、Runnerが直前に完了していた場合でも停止の観測を優先します。利用者の復帰や熱の上昇による停止を、完了として消さないためです。
- 出力byte数は上限と比べます。内容の検証はしません。
- 猶予内にRunnerが止まらない場合は`runner_stuck`とし、強制停止はプラットフォーム側の境界に残します。

Testでは、完了、非ゼロ終了、Runnerのエラー、出力上限超過、Governorによる停止、期限、呼出し側のcancel、停止しないRunner、不正な入力と設定を固定しました。

残りは、Runnerの実装（Native / Container / WASM / Browser）、checkpointと再開、`Outcome`をResultClaimとしてControl Planeへ報告する経路です。

実行の終わり方を回復へ渡す写像も追加しました（`internal/agent/reclaim`）。

- `reclaimed`、`failed`、`deadline_exceeded`は`recovery.Interruption`へ変換し、`recovery.Classify`でattempt budget、deadline、時間窓の予算を判定します。
- `cancelled`は意図的な停止として自動retryしません。
- `runner_stuck`は、先に端末側で止める必要があるため自動retryしません。
- `completed`と`output_exceeded`は回復へ渡しません。
- Taskの契約（retryable、max attempts）は、WorkOrderやOutcomeではなく、耐久化されたTaskの状態から読みます。

これで、回収から再投入までの経路がCoreとして一続きになりました。残るのは、Runnerの実装、OutcomeのResultClaimとしての報告、Lockedな状態（`workorderaccept`の`Admission`やnonce記録）の耐久化です。

最初のRunnerとして、`internal/adapters/reference/executor/native`を追加しました。

- WorkOrderはパスも引数も指定しません。承認された実行体のdigestだけを運びます。
- 運用者がローカルに登録したdigest → パス・引数・環境変数の対応を使い、起動のたびにファイルを再ハッシュします。未登録、または内容が変わった実行体は起動しません。
- 期限はcontextの取消しで止めます。出力が上限を超えたら止め、`ExitCode=-1`と観測したbyte数を返します。非ゼロ終了は`Result`として返し、Agentは`failed`として扱います。
- OSレベルのsandbox、子プロセスの強制停止、環境変数の最小化は、このRunnerの責務にしません。

Testでは、テストバイナリ自身を子プロセスとして起動し、正常終了、非ゼロ終了、出力上限超過、未登録digest、起動前の改ざん、context取消しを固定しました。

残りは、Container / WASM / BrowserのRunner、ResultClaimの報告経路、`Admission`・nonce・`Outcome`の耐久化です。

Workerの実行結果をControl Planeへ戻す最初の境界として、`internal/core/resultclaim`を追加しました。

- Claimは、Task / TaskIR / Attempt / Worker / Lease / fence / WorkOrder Decision / executor digest / nonce / status / exit code / output bytes / 開始・終了時刻を持ちます。
- `Decide`は、Claimと、Control Planeが同じ操作で読んだ現在の状態（Attemptの存在と開閉、現在のfence、記録済みのClaim）だけを比べます。
- 初回は記録してAttemptを閉じ、同じ申告の再送は何も変えず、fenceが古い申告と閉じた後の申告は記録だけ残します。同じAttemptに異なる申告が届いた場合は、最初の申告を採り、後のものを拒否します。
- `completed`はResult Verificationへ送りますが、Accepted Resultにはしません。`runner_stuck`ではAttemptを閉じません。

Testでは、正常な初回、再送、競合、stale、late、別Attempt、zero Claim、statusごとのcloseとverificationの扱いを固定しました。

残りは、Claimの耐久化とWorker Planeのtransport、Attempt / Lease / Taskへの原子的な反映、報告が届かない場合のLease期限からの確定です。

Worker側では、`internal/agent/claim`が`executor.Outcome`を`resultclaim.Claim`へ写像します。

- statusの写像を明示し、未知のstatusは`ErrUnknownStatus`で止めます。黙って`failed`や`completed`へ丸めません。
- `Attempt`のbindingはAdmissionとローカルLease記録から作り、Outcomeから作り直しません。
- `completed`に非ゼロの終了コードが入っている場合などは、`resultclaim`の検証で拒否し、builderは値を直しません。

Testでは、全statusの写像、未知のstatus、bindingの不足、時刻の欠落、`completed`と終了コードの矛盾を固定しました。次は、Claimを送るtransportと、Control Plane側での耐久化です。

Control Plane側では、`internal/control/claimaccept`がClaimをAttemptの状態へ反映します。

- Attempt、Lease（現在のfence）、記録済みのClaimを読み、`resultclaim.Decide`の結果に従います。
- `completed`はA-DISPATCHEDからA-CLAIMEDを経てA-VERIFYINGへ進めます。ほかの終端statusはA-CLOSEDへ進め、close reasonを対応づけます。`runner_stuck`は状態を変えません。
- 適用できない段階で`completed`が届いた場合は、Claimを記録する前に`ErrWrongPhase`で拒否します。
- 同じClaimの再送は記録を増やさず、記録後にcrashした場合の状態遷移だけをやり直します。
- Claimの記録とAttemptのCASはまだ別の操作です。同じtransactionへ置く作業を残しています（#173）。

Testでは、completedの検証への遷移（CLAIMED / DISPATCHED）、reclaimedのclose、runner_stuckの非変更、再送によるcloseのやり直し、競合、stale、ClaimのfenceがLeaseより先の場合、別Worker、late、早すぎる段階、CAS競合、設定不足を固定しました。

残りは、Claimの耐久StoreとWorker Planeのtransport、ClaimとAttemptの原子的な反映、報告が届かない場合のLease期限からの確定です。

### 2026-10-20 要件の正本へのADR-0013〜0016の反映

ADR-0013、0014、0016で定めた方向を、[Product Requirements](../requirements/PRODUCT_REQUIREMENTS.md)へ反映した。新しい節「K. 委任の核、仕事の型、組織の判断との連動」に、RQ-054〜RQ-063の10件を加え、53 Requirementから63 Requirementになった。RQ-011、RQ-016、RQ-026に追記し、非目標（L節）を見直した。

- RQ-054（委任先の内部の再実行）はNeeds researchとし、Spike（#67）で確かめる前にBaselineへ上げない。
- 残りの9件は、ADRで方向が決まっているためBaselineとした。
- 委任状の操作の一覧、委任の等級の引き上げの承認、常駐の上限の既定値、自律の判断の開示の範囲は、未決事項（16〜19）として残した。

### 2026-10-20 脅威モデルへの反映

要件の反映（RQ-054〜063）に合わせて、[Threat Model](../requirements/THREAT_MODEL.md)に攻撃者A17〜A22（委任先の内部の実行管理系、委任の等級の誤登録、委任状を持つ主体の誤りや乗っ取り、組織に費用を負わせる依頼者、外部への提供先、常駐を更新し続ける主体）と、安全上の不変条件SEC-024〜SEC-030を加えた。委任状を持つ主体（SEIなど）は信頼の基盤に含めず、被害を委任状の範囲と期限の中に収めることを不変条件とした。[Worker Assurance Model](../requirements/ASSURANCE_MODEL.md)のAxis Fに、委任先の単位で、Control Planeの観測から履歴を作ることを追記した。

### 2026-10-21 Claimの再送queue

WorkerがControl Planeへ届く前に止まる場合に備え、`internal/agent/claimqueue`を追加しました。

- `Enqueue`は同じAttemptの同じClaimを一件に畳み、同じAttemptの違うClaimは`ErrConflict`で拒否します。
- `DeliverDue`は、`NextAt`を過ぎたClaimを送ります。成功したら削除し、失敗したら指数backoff（上限つき、均等jitter）で再予約します。Claimは捨てません。
- 送信はat-least-onceです。二度届いた場合は、Control Planeの`resultclaim.Decide`が`duplicate`として畳みます。
- 送信成功後に削除で落ちた場合も、次のpassで再送し、Control Plane側の重複判定に任せます。
- memory Storeはプロセス内だけなので、Workerの実行証跡にはなりません。durable StoreへのAdapterを残しています。

Testでは、同一Claimの冪等なenqueue、異なるClaimの衝突、成功時の削除、失敗時の再予約とbackoff後の再送、件数上限、Storeの期限順・digest照合削除・不正entryを固定しました。

次は、Claimのdurable Store（SQLite）と、ClaimとAttempt CASの同一transaction化です。

### 2026-10-21 Claimの耐久化と原子的な受理

Claimの記録とAttemptの遷移を、同じtransactionで確定できるようにしました。

- `internal/spi/statestore/claim.go`に`ResultClaimStore`と`ResultClaimAcceptance`を追加しました。`ApplyResultClaim`は、Claimの記録とAttemptの更新を一つのcommitにまとめます。片方だけが残ることはありません。
- memoryとSQLiteの両Adapterが実装しました。SQLiteはmigration 8で`result_claims`を追加し、Claimを不変の行として保存します。
- `claimaccept`は、Claimの記録とAttemptのCASを別々に行うのをやめ、`ApplyResultClaim`を一度だけ呼ぶようにしました。`completed`をA-DISPATCHEDから進める場合だけ、A-CLAIMEDへの最初の一歩を別のCASで行います。その間に落ちても、A-CLAIMEDから再送でやり直せます。
- 新しいClaimの二重記録は`ErrAlreadyExists`、同じAttemptの異なるClaimは`ErrConflict`、古いAttempt revisionは`ErrConflict`として、transactionごと拒否します。

Testでは、ClaimとAttemptの同時commit、stale revisionでのrollback（Claimが残らない）、二重記録と異なるClaimの拒否、不正な遷移のrollback、SQLiteのclose/reopenを跨いだClaimの復元を固定しました。

残りは、Worker Planeのtransport（Claim送信の実体）、報告が届かないAttemptのLease期限からの確定、Runnerの種類の拡張です。

### 2026-10-21 申告の入口で、認証されたWorkerを確かめる

Worker Planeのtransportが認証したprincipalと、Claimが名乗るWorkerを照合する`internal/control/claimplane`を追加しました。

- transportはWorkerを認証し、`Principal{WorkerIdentity, TrustDomain}`を渡します。認証そのものはtransportの責務です。
- `Submit`は、Claimの`WorkerIdentity`がprincipalと一致しなければ`ErrNotClaimOwner`で拒否し、`claimaccept`を呼びません。
- これがなければ、認証されたWorkerが別のWorkerの名前を書いたClaimを送り、Attemptの名前と一致させることができました。`claimaccept`の束縛だけでは、送信者のidentityを確かめられません。
- Claimの内容の正しさ、Trust Domainの一致、Attemptごとの重複と競合は、それぞれ別の境界の責務です。

Testでは、正しいprincipalの受理、別Workerの拒否（acceptorを呼ばない）、不正なprincipalとzero Claim、nil context、acceptorのエラー伝播を固定しました。

残りは、Worker Planeのtransport実装（認証とClaimの運搬）、報告が届かないAttemptのLease期限からの確定、Runnerの種類の拡張です。

### 2026-10-21 申告が届かないAttemptの扱い

`internal/control/claimreconcile`を追加し、Claimが届かないAttemptを、期限だけで失敗や再実行へ丸めないようにしました。

- Leaseの期限後もgraceの間は`AwaitClaim`とし、遅れて届くClaimを待ちます。claimqueueはat-least-onceなので、直後に閉じると正しいClaimが`late`になります。
- graceの後、外部副作用の可能性がなければ`CloseLeaseExpired`とし、Attemptを閉じます。再実行してよいかは`recovery`がTaskの契約とattempt budgetで決めます。
- 外部副作用の可能性があれば`Reconcile`とし、Attemptを閉じません。Receiptなしに閉じて再実行すると、二重の副作用になり得ます。
- 開いていないAttemptと、Claimが既にあるAttemptは`NoAction`です。

Testでは、closed / claimed、期限前とgrace内、grace境界とその後、外部副作用あり、不正入力（zero now、負のgrace、期限なし）を固定しました。

残りは、Worker Planeのtransport実装（認証とClaimの運搬）、`Reconcile`からReceiptを得た後の状態遷移、Runnerの種類の拡張です。

### 2026-10-21 Worker PlaneのClaim送受信の継ぎ目

Protobuf / Connectの生成コードをまだ入れずに、その両側を接続できる継ぎ目を実装しました。

- `internal/spi/workerplane`に、`Principal`、`ClaimAck`、`ClaimClient`を定義しました。wire protocolは`spec/worker/v1/worker.proto`のままで、この層は接続の意味だけを持ちます。
- `internal/control/workerplane.Server`が、認証済みprincipalとClaimを`claimplane`へ渡し、`ClaimAck{Decision, AttemptPhase, VerificationRequired}`へ写像します。認証とframingはConnect handlerの責務です。
- `internal/agent/claimclient.Client`が、`claimqueue`の`Sender`としてClaimを送ります。Agentのprincipalと一致しないClaimは送らず、queueへエラーを返します。
- 統合Testでは、claimqueue → claimclient → workerplane.Server → claimplane → acceptorを実配線し、成功時の削除と、失敗時の保持・backoff後の再送を確認しました。

残りは、Connect serviceのhandlerと生成コード、認証情報の更新、タイムアウトと流量制限です。

### 2026-10-21 一台で完結する配備形態（DP-7）

[ADR-0018](adr/0018-single-machine-delegation-profile.md)で、RQ-063（一台での利用）に対応する配備形態DP-7を[Deployment Profiles](../architecture/DEPLOYMENT_PROFILES.md)に加え、0.2へ上げた。一台の構成では、依頼者（AI Agentなど）が制御層と同じOSのユーザーとして動くと、鍵やmetadata storeを直接触れてRQ-016を素通りできる。そのため、制御層と実行を専用のOSのアカウントで動かすことをDP-7の条件とした。M0とMVPの優先順位は変えず、S1で実装する。

### 2026-10-21 隔離実行の外面（ADR-0019）

[ADR-0019](adr/0019-sandbox-face-isolation-levels-and-frontends.md)で、隔離の水準（ISO-0〜ISO-3）を管理者が実行環境ごとに登録する語彙として定め、[Extension SPI](../architecture/EXTENSION_SPI.md) §32に加えた。隔離して試す型ではISO-0を使わない。AIが生成したコードは、実行体ではなく入力のArtifactとして扱い、事前に承認したインタプリタの上で動かす（[Contract Boundaries](../architecture/CONTRACT_BOUNDARIES.md) §7.9）。MCP、OpenAI互換、REAPIなどの外部の入口は、依頼をTaskRequestの意味に写すだけで権限を持たないという原則を、§7Aに置いた。Extension SPIとContract Boundariesを0.2へ上げた。

### 2026-10-21 Task State Modelへの新しい仕事の型の反映（ADR-0020）

[ADR-0020](adr/0020-task-state-for-groups-sessions-resident.md)で、[Task State Model](../architecture/TASK_STATE_MODEL.md)を0.2へ上げた。期限切れを、送り出しの記録の有無で「実行されていないことが確か」（`DEADLINE_EXPIRED_NOT_STARTED`）と「実行されたか分からない」（`DEADLINE_EXPIRED_AFTER_DISPATCH`）に分け、期限で終えたTaskが未完了の上限を占め続けないようにした（RQ-011）。Task Group、SessionとSandbox、常駐の状態を、既存のTask、Attempt、Leaseとfencingの延長として加えた。設計基準の6文書のうち、`CONTROL_DATA_PLANE.md`だけが0.1のまま。

### 2026-10-21 観測の面と、設計基準0.2の完成（ADR-0021）

[ADR-0021](adr/0021-observation-plane-and-disclosure-tiers.md)で、[Control / Data Plane](../architecture/CONTROL_DATA_PLANE.md)に観測の面（Observation Plane）を加え、予実、資源の消費の記録、管理画面のための一覧を、権限の判断に使う状態とは別の保存先に置くことにした。観測の値は権限の根拠にしない。開示を利用者、委任先の管理者、Fabric全体の管理者の三段階に分け、管理画面は読み取りだけとし、操作はAdministrative Planeを通す。配置の費用の入力に、ビルドキャッシュ、toolchain、依存の局所性などを加えた。

これで、設計基準の6文書（Authority、Contract、Control-Data Plane、Deployment Profile、Task State Model、Extension SPI）はすべて0.2になった。ADR-0013で定めたプロダクトの方向（委任の核と四つの外面）は、要件（RQ-054〜063）、脅威モデル（A17〜A22、SEC-024〜030）、設計基準0.2まで反映された。

### 2026-10-21 委任状で委任できる操作の区分（ADR-0022）

[ADR-0022](adr/0022-mandate-operation-classes-and-thresholds.md)で、管理の操作を、安全側の操作、範囲内の拡大、人間の承認が要る操作、委任できない操作の四つに分け、[Authority Boundaries](../architecture/AUTHORITY_BOUNDARIES.md)の委任状の節に加えた。閾値の雛形は、一つの配分先への親の枠の30%、一日の配分し直しの合計の20%、委任状の期限の最長30日。SEIは、縮小や停止を広く執行でき、配分の拡大は上限と速さの上限の中で行い、新しい外部の相手への提供などは人間の承認を経る。

### 2026-10-21 自律の判断の範囲と、外部の要求のインターフェース（ADR-0023）

[ADR-0023](adr/0023-autonomy-scope-and-request-interfaces.md)で、EKIの自律の判断を、承認なしで行う、人間の承認を求める、行わないの三つに分け、[Authority Boundaries](../architecture/AUTHORITY_BOUNDARIES.md) §7Aに置いた。区分はADR-0022の委任状の区分と揃えた。外部の要求（依頼、方針、照会、承認、通知）の形を[Contract Boundaries](../architecture/CONTRACT_BOUNDARIES.md) §31.1と§33.1〜33.4に置いた。承認は期限までになければ承認されなかったものとし、通知は権限を持たず、受けられない依頼には拘束力のない代わりの案を返す。

### 2026-10-22 案件の予算の執行と、外部への提供（ADR-0024）

[ADR-0024](adr/0024-project-budget-execution-and-outward-provision.md)で、SEIの判断の執行を二つ具体化した。案件は、BUD-1の入れ子の一段（案件の予算の枠）と、それを使う依頼として表し、案件の意味はSEIの側に残す。予実は照会と通知で返す。外部への提供は、人間が定める提供の方針の範囲でCompute Grantとして行い、量の拡大はSEIに、縮小はSEIとEKIの自律に委ね、新しい相手は人間の承認を経る。提供先の仕事には、既定でISO-2以上の隔離を求める。新しい権限は足していない。

### 2026-10-22 Leaseのpolicyの鮮度と、署名中の鍵の入れ替わり（ADR-0025）

[ADR-0025](adr/0025-lease-policy-currentness-and-key-rotation.md)で、#78に残っていた二点を仮承認で決めた。Workerは、Leaseのpolicyが今も有効かを確かめず、署名、Trust Bundle、有効期限、`policy_revision`の一致だけを確かめる。policyの取り消しはControl PlaneのFenceEpochとreservationを閉じる処理で防ぎ、配送済みのLeaseが止まるまでの上限はLeaseの有効期間とする。署名中に鍵のprimaryが入れ替わった場合はABORTEDとし、署名し直さない。Leaseの有効期間の値と、emergency revokeの経路は未決のまま残す。

### 2026-10-22 WOR-1のWorkOrder発行（#57）

Worker側には署名済みWorkOrderを検証・受理する境界（`workorderverify`、`workorderaccept`、`agent/startgate`）があったが、Control Plane側にWorkOrderへ署名する主体がなかった。`internal/core/workorderissue`を追加し、LSE-1（`leaseissue.Emitter`）と同じ形でWOR-1の発行を線形化した。

- 入力はEMITTEDのLeaseと、Executor Approvalの参照、limits、sandbox。Task、Attempt、Worker、Placement / Leaseの参照、policy revision、fenceは保存済みのLeaseから導く。
- 署名後、commitの直前にPolicy Epoch、WOR-1 key、Trust Bundle、Task、Attempt、Leaseのfenceを確かめ直す。State Storeも同じ書き込みの中で照合する。
- EMITTEDは保存済みbytesをそのまま返し、再署名しない。前提を失った場合はABORTEDを記録し、署名済みbytesを出さない。ABORTEDはAttempt、Lease、FenceEpochを変えない。一時的に読めない状態では何も記録しない。
- State Store SPIに`WorkOrderEmissionStore`を加え、memoryとSQLite（migration 10の`work_order_emissions`）が実装した。
- 共通の適合性試験`RunWorkOrderSigningContract`では、発行したWorkOrderがWorker側の三つの境界を通ること、古いPolicy・失効したkey・期限切れや置き換えられたLease・Workerの不一致がABORTEDになること、crashと再送、SQLiteの再起動を固定した。

#160で未決の論点のうち、一つのLeaseからWorkOrderは一つだけ、`A-ASSIGNED`のAttemptだけが受け取れる、Artifact Grantは含めない、Executor Approvalは呼び出し側の参照を結び付ける、の四点を仮決めとした（[`internal/core/workorderissue/README.md`](../internal/core/workorderissue/README.md)）。署名中にWOR-1 keyのprimaryが入れ替わった場合は、[ADR-0025](adr/0025-lease-policy-currentness-and-key-rotation.md)のLSE-1と同じくABORTEDとし、署名し直さない。

### 2026-10-24 広域の資源最適化（ADR-0026）

[ADR-0026](adr/0026-wide-area-resource-optimization.md)で、広域の最適化を、提案だけを出す構造のまま設計した。制約（持ち主の拒否権、Data Class、信頼の水準、Grant、予算、隔離の水準、利用の許可、指名）と目的（期限、費用、転送量、持ち主への影響、実績）を分け、制約は目的の重みで埋め合わせない。委任先は、余力、管理資源、費用を生む容量の順に使い、間に合う見込みが閾値を下回ったら次へ引き上げる。引き上げは、予算の予約、閾値以下は承認なしの確保、閾値超は承認の要求の順で、確保できなければ期限の前に通知する。学習した予測は入力の一つで、権限を持たない。判断の計算量に上限を置き、止まっても受理や配置の承認は動き続ける。

### 2026-10-25 申告の配送を別プロセスで通す（#57、#173）

ResultClaimの配送は、これまでプロセス内の接続（`workerplane.ClaimClient`）と、TLSの往復を測ったSpike（`spikes/claim-wire-roundtrip`）でしか確かめていなかった。`eki-agent`が耐久queueのClaimを、実行中の`eki-control`へTLS越しに届ける最初のプロセス単位の経路を加えた。

- Control Plane: `service.Config`に任意の`worker`を加え、Client APIとは別のlistenerを開く。TLS必須で、loopbackだけ平文を許す。不正な設定は起動せずに失敗する。同じSQLiteのState Storeの上に`claimaccept`、`claimplane`、`workerplane.Server`を組み、`workerplane/httpserver.go`が認証（トークンのSHA-256の定数時間比較）、本文の上限、固定コードのエラー応答を担う。
- Agent: `claimclient/http.go`が`workerplane.ClaimClient`をHTTP(S)で実装し、`cmd/eki-agent`が`claimqueue.DeliverDue`を一定間隔で回す。SIGINT・SIGTERMで終了する。
- 試験: `conformance/process`が両バイナリを`go build`し、別プロセスで起動して、申告の配送、Control Planeの強制終了中の保持と再起動後の配送、未知のトークンと別のWorkerの拒否を確かめる。

これは**試験用のprofile**であり、#198の決定ではない。Workerの認証・登録（enrollment）と通信契約は未決のままで、`session_token_sha256 → {worker_identity, trust_domain}`の表とClaimのJSON（`internal/spi/workerplane/testprofile`）は、その決定を先取りしない。`worker.proto`との互換性は主張せず、ResultClaimとprotoの対応と申告queueの意味には触れていない。

仮決め（承認待ち）:

- 試験用のURLパスは`/test-profile/v1/claims`、本文は`application/json`とした。
- Worker listenerには、Client APIにある「プロキシがTLSを終端する」ための平文の許可を設けなかった。
- 拒否された申告（401・403など）は、Agentのqueueに残り再送され続ける。恒久的な拒否の扱いは決めていない。
- Agentの設定は、フラグとJSON設定ファイルで、トークンは`--token-file`、環境変数`EKI_AGENT_TOKEN`、`--token`のいずれか一つとした。

未決: Workerの認証・登録とトークンの更新・失効（#198）、通信契約、恒久的な拒否の扱い、複数のAgentと実ネットワークの障害、署名つきの連鎖と組み合わせたプロセス単位の通し試験。

### 2026-10-26 Workerの認証と登録（ADR-0027）

[ADR-0027](adr/0027-worker-authentication-and-enrollment.md)で、Workerの認証と登録の方式を仮承認で決めた（Issue [#222](https://github.com/hat47x/eki-fabric/issues/222)）。これまでは#220の試験用profile（静的Bearerの表）だけがあり、OAuthやmTLSの方式は「後続で比べる」と書いていた。

- 既定は、管理者が発行する一回限り・短命のenrollment資格で登録し、Workerごとの短命なクライアント証明書（mTLS）で認証する形とした。Workerは秘密鍵を端末の中で作り、識別子は登録簿が発行する。
- 認証の結果は既存の`Principal{WorkerIdentity, TrustDomain}`へ正規化し、Claimの所有検査は変えない。登録は主体の同一性だけを作り、Delegation Classは与えない。
- 失効の根拠はWorker登録簿の状態とし、要求ごとに引く。証明書の有効期間は、登録簿を引けない構成の歯止めとする。
- 静的Bearerの表は試験用に限る。企業のPKI、OIDC、SPIFFEは、同じPrincipalへ正規化するAdapterとして受け入れる。
- 保証しないこと: 登録済みのWorkerが自己申告を偽ること、悪意のある端末を管理者が正規に登録すること、端末の侵害による鍵の窃取、すでに配送したLeaseを即座に止めること。

未決: CAの運用、更新の手順と長く眠った端末の再登録、失効の伝播の遅れの上限、端末のattestation、ブラウザ・仮名のWorkerの認証、TLSを終端するプロキシ、enrollment資格の発行の区分、通信契約（#198）への写像、試験用profileの制限の実装。

### 2026-10-27 Client APIの結果取得（`GET /v1/tasks/{task_id}/result`）

Taskの結果を読む経路は`501`のままだった。Taskの状態が`completed`でも、それは受理された結果（AcceptedResult、RAC-1）を意味しない。他のプロジェクト（hat47x/tai-pleroma#67）が、`completed`を受理と取り違えずに委任の結果を読めるよう、`GET /v1/tasks/{task_id}/result`を実装した（#57）。

- 認証・Role・所有の判定は状態の取得と同じで、存在しないTaskと他人のTaskは同じ404を返す。
- Taskが終端でなければ`409 RESULT_NOT_READY`（再試行可）、終端でもAcceptedResultがなければ`409 RESULT_NOT_AVAILABLE`、完了しAcceptedResultが耐久化されていれば`200 TaskResult`を返す。`completed`でも記録がなければ結果にしない。
- 読み出しは既存の`ResultAcceptanceStore.LoadAcceptedResult`を使い、SPI・Adapter・migrationは変えていない。`admission.Config.Results`を渡した構成だけで有効になり、渡さない構成は従来どおり`501`。
- 応答に出さないもの: Worker識別子、Lease、fence、Attempt、検証者、policyのdigest、却下の理由、出力・申告のdigest。
- 試験: Clientのhandler試験（認証、所有と存在の区別がつかないこと、未完了、完了と記録なし、終端の各結果、メソッド、内部情報の不在、store障害の不透明さ）と、縦断スライスでの通し試験（受理の前後と、検証者が割れて保留になった場合）。

仮決め（承認待ち）:

- `TaskResult`に出力digestの欄がなく、`ArtifactDescriptor`の必須項目（大きさ、media type、data class）をAcceptedResultが持たないため、`artifacts`は空の配列とし、digestは返さない。仕様は変えていない。
- `assurance`は`{profile: 受理時の保証水準, status: "satisfied"}`とした。
- 取消・失敗などの終端でAcceptedResultがない場合は、仕様が409しか定めないため、`RESULT_NOT_AVAILABLE`の409とし、公開の終端コードは状態の取得に委ねた。
- 却下された申告は、Taskを終端にしないため`RESULT_NOT_READY`に見える。

未決: 出力digestをClientへ渡す仕様の変更（`TaskResult`への項目の追加か、成果物の記述子の記録）、成果物の経路（`/v1/artifacts/...`）、Adminが他人のTaskの結果を読む規則、`result_profile`の耐久化。

### 2026-10-27 Worker登録簿とenrollment資格のState Store（ADR-0027 段階1）

ADR-0027の段階1として、Worker登録簿とenrollment資格をState Storeに耐久化した（Issue [#222](https://github.com/hat47x/eki-fabric/issues/222)、[#57](https://github.com/hat47x/eki-fabric/issues/57)）。CA、HTTPのendpoint、mTLSは含まない。

- `internal/core/workerregistry`に、Workerの記録（登録簿が発行する識別子、信頼領域、`registered`・`disabled`・`revoked`、段階2用の証明書の通し番号と期限）と、enrollment資格の記録（256bitの秘密のSHA-256だけを保存、接頭辞`eki_enroll_v1_`、信頼領域に束縛、既定24時間・上限7日、一回限り）と、遷移の規則を置いた。`internal/core/workerenrollment`に、発行・失効・消費・状態変更・自己失効・要求ごとの確認を置いた。
- 消費は、資格の使用済みへの変更とWorkerの作成を一つのトランザクションで行う。同時に使っても、Workerは一つだけ作られる。使えない資格（期限切れ、失効済み、使用済み、別の信頼領域、未知）は、同じ外部のエラーで拒否し、理由は監査用にだけ区別する。
- `revoked`は終端で、識別子は再利用しない。Workerは自分の識別子だけを失効できる。更新時刻は巻き戻らない（#226と同じ扱い）。
- `statestore.WorkerRegistryStore`を、memoryとSQLite（migration 15）に実装した。SQLiteは、単一消費、終端、識別子の不変と非再利用、有効期間の上限を、制約とtriggerでも守る。
- 適合性試験（`RunWorkerRegistryContract`、`RunWorkerRegistryDurabilityContract`）を両Adapterに適用した。

仮決め: 信頼領域の書式は`federation.ParseTrustDomain`と同じ。管理者の権限は、型付きの能力`workerenrollment.Admin`で受け取る。`Worker.StateChangedBy`などの操作者の記録は`admin:<id>`、`worker:<identity>`の形。

未決: 管理者の認証（`clientapi`のRole）から`Admin`を作る配線、状態変更の監査ログの出力、資格・Workerの一覧の操作、登録簿の項目の保持期間。

次の段階2: Worker専用のCA、enrollmentのendpoint、mTLSのauthenticator、要求ごとの登録簿の確認の配線、プロセス単位の通し試験を、別のPRで加える。

### 2026-10-27 WorkerごとのmTLS認証とenrollment（ADR-0027 段階2）

ADR-0027の段階2として、Worker専用のCA、enrollmentとmTLS、更新、失効を実装した（Issue [#222](https://github.com/hat47x/eki-fabric/issues/222)、[#57](https://github.com/hat47x/eki-fabric/issues/57)）。Worker listenerに`auth: "mtls"`の方式が加わり、静的Bearerの表は従来どおり試験用に限る。

- **CA**（`internal/adapters/reference/workerca`）: ECDSA P-256。鍵はLeaseとWorkOrderの署名鍵とは別のファイルで、読み込みだけを行う。CA証明書は、パス長0で、許可するURIの領域を1つの信頼領域に限る、重要なName Constraintを持つ。起動時に、制約のないCA、別の信頼領域のCA、P-256以外の鍵、鍵と証明書の不一致を拒否する。発行する証明書のSANは、登録簿の識別子から作る`eki://<信頼領域>/worker/<識別子>`の一つだけ、拡張鍵用途はクライアント認証だけ。CSRの主体・名前・拡張は無視する。通し番号は127bitの乱数で、`RecordWorkerCertificate`に記録してから返す。
- **enrollmentと更新**（`internal/control/workerplane/mtls.go`）: 資格は`Authorization`ヘッダだけで受け、URLのクエリは400で拒否する。使えない資格は、理由によらず同じ401と本文で返し、理由は監査ログだけに残す。CSRは資格を消費する前に検証する。更新は現在の証明書のmTLSで行い、同じ鍵は拒否、期限切れ・無効化・失効のWorkerは拒否する。
- **認証**: TLSでは証明書を要求するだけにして、検証は要求ごとにhandlerで行う（Worker CAだけを信頼、クライアント認証の用途、現在時刻）。信頼領域は完全一致で比べる（GoのURI名制約はサブドメインも通すため）。毎回`Authorize`を呼ぶので、失効・無効化したWorkerは、既存の接続の次の要求から401になり、`Connection: close`で接続を閉じる。Claimの所有検査は変えていない。Worker listenerとClient APIは別のlistenerと別のauthenticatorで、互いの資格を受け付けない。
- **設定**: `worker.auth: "mtls"`と`worker.mtls`（`trust_domain`、`ca_cert_file`、`ca_key_file`、`cert_validity_seconds`）。TLS必須（loopbackでも）。静的な表、`allow_test_profile_off_loopback`、CAとlistenerの鍵の共用などの矛盾は、起動前に拒否する。
- **運用者向けsubcommand**（`eki-control`）: `gen-worker-ca`、`enroll-token`（秘密を一度だけ表示し、DBにはSHA-256だけを残す）、`worker-state`（無効化・再有効化・失効）。設定が指すDBを直接開く、ホスト上の運用者の操作であり、Client APIやWorker APIからは呼べない。
- **Agent**: `eki-agent enroll`が、秘密鍵を端末内で作り、CAの指紋でControl Planeを確かめてから、資格を一度だけ送る。指紋が違えば、資格を送らずに止まる。実行時は、証明書（mTLS）で接続し、残り3分の1で自動更新する。鍵と証明書は一つのファイルに、0600で、置き換えとして書く。
- **試験**: CA（SAN、拡張鍵用途、名前制約、有効期間、通し番号、CSRの検証）、登録・更新の規則、handler（偽のCSR、一様な拒否、失効後の次の要求、期限切れ・未知のCA・別の信頼領域・サブドメイン）、Agent（指紋の固定、更新）、設定の矛盾、運用者のsubcommandの単体試験と、`conformance/process`のプロセス単位の試験（運用者の資格での登録、mTLSでの申告の配送、同じ資格の2回目の拒否、指紋違いで資格を送らない、失効後の次の要求の拒否、自動更新、期限切れ、役割の分離）を加えた。

仮決め（承認待ち）:

- 証明書の有効期間の既定は72時間、更新は残り3分の1。設定できる範囲は10秒から30日（下限は試験のため）。
- 鍵はECDSA P-256だけ。Ed25519はTLSのクライアント認証の互換性を確かめていないため使わない。
- Worker listenerのmTLSは、TLS 1.3以上。
- Control Planeの固定は、Worker listenerのTLS証明書の鎖の最上位の証明書のSHA-256とした。Agentは、鎖の中で指紋が一致する証明書を信頼の起点にして検証し、以後の接続はその証明書だけを信頼する。CAの指紋と呼んでいるが、Worker CAの指紋ではない（Worker CAは、Workerを検証する側）。サーバ証明書の葉だけを固定すると、サーバ証明書の更新で壊れる。
- 更新後も、古い証明書は期限まで有効（登録簿の通し番号との一致は求めない）。更新の応答が失われても、Workerが締め出されないようにするため。Workerの失効で止められる。
- 資格の発行・失効の運用は、`eki-control`のsubcommandとした（Client APIのRoleとは接続しない）。操作者の名前（`admin:<札>`）は、認証ではない札。
- 失敗した登録（資格の消費後にCAの署名が失敗した場合）は、Workerが証明書なしで残り、管理者が新しい資格を発行する。
- CAの鍵ファイルの権限は検査しない（Agentの鍵ファイルは、Unix系で他者が読めると読み込まない）。
- 状態ディレクトリのファイル名、`/v1/worker/enroll`と`/v1/worker/renew`の経路、`application/pkcs10`の本文は、この試験用profileの形で、#198の決定ではない。

未決（ADR-0027 §5のうち、残るもの）:

- CAの運用: 鍵の保管（KMS、HSM）、中間CA、鍵の更新、バックアップ、企業のPKIとの接続、鍵ファイルの権限の検査。
- 失効の伝播の遅れの上限の測定（登録簿を引けない構成を含む）。今の実装は、Control Planeが登録簿を読める前提で、遅れはほぼ0。
- 端末のattestationと、鍵を非エクスポートにする条件。
- TLSを終端するプロキシや、企業のTLS検査での構成（mTLSの素通しか、信頼するGatewayの終端）。
- 有効期間を超えて眠った端末の再登録と、同じ`WorkerIdentity`の引き継ぎ。時計のずれへの対処。
- sessionの識別子を証明書に束縛すること、再接続のたびの新しいsession（2.3 項13）。
- enrollmentへの総当たり対策（流量制限）と、資格の一括発行での人間の承認（ADR-0022の区分）。
- 管理者の認証（`clientapi`のRole）と、`workerenrollment.Admin`の接続。資格・Workerの一覧の操作。
- ブラウザ・仮名のWorker（DP-3）、Federationをまたぐ認証、OIDCやSPIFFEなどのIDV-1のAdapter、通信契約（#198）への写像。

### 2026-10-27 署名済みのLeaseとWorkOrderをControl PlaneからWorkerへ届ける（#57、#160）: Control Plane側

MVPの一続きの流れで、Control Planeが署名して保存したLeaseとWorkOrderは、まだ認証したWorkerへ届く経路を持たなかった（`leasedelivery`は試験用の配送の接合点で、実際の通信を持たない）。この節は、Control Plane側の経路を加えた。Agent側の検証と開始の関門は次のPRで加える。

**これは試験用profileであり、#198のWorker Protocolではない。** `POST /test-profile/v1/assignments`は、申告の`/test-profile/v1/claims`と同じ扱いで、本文はJSONである。`spec/`と申告の通信には触れていない。#198が`worker.proto`を決めたら、この経路は置き換える。

- **割当の読取**: `statestore.AssignmentStore.ListEmittedAssignments`を加えた（memory・SQLite）。Attemptが当のWorkerに結び付き、ASSIGNEDまたはDISPATCHEDで、LeaseとWorkOrderの両方がEMITTEDのものだけを、古い順に返す。既存の表と索引の読取だけで、DBの移行は加えていない。
- **配送の方式**: 「配送済み」の印は作らず、同じ問い合わせには同じbytesを返す（冪等な再配送）。重複した開始は、Worker側のnonceの一回限りの消費で防ぐ。
- **経路**: mTLSのWorker listenerに`/test-profile/v1/assignments`を加えた。Workerは要求ごとに証明書から決まり、本文は空のJSONオブジェクトだけを受け、Workerを名指す欄は400で拒否する。返すのは保存されたbytesそのままで、署名し直さない。`MTLSConfig.Assignments`がnilなら経路は存在しない。静的な表（bearer）のlistenerには加えていない。
- **試験**: 契約試験（memory・SQLite、`RunAssignmentContract`）、handlerの試験（証明書だけで決まる、他のWorkerの資料を返さない、失効後の要求の拒否、本文の検査）、`conformance/process`のプロセス単位の試験（実際のPLC-1・LSE-1・WOR-1の署名を`conformance/signedchain`でState Storeへ書き、`eki-control`がWorkerごとに配る。他のWorkerには渡らない、失効で止まる）。

仮決め（承認待ち）:

- 返す対象をASSIGNEDとDISPATCHEDのAttemptとした。DISPATCHEDを含めるのは、Agentが再起動して、受け取り直せるようにするため。
- 一回の応答は最大64件。Workerが同時に持つAttemptの数としては十分とみなした。
- 経路の名前（`/test-profile/v1/assignments`）と本文（base64のbytesを含むJSON）は、この試験用profileの形で、#198の決定ではない。

未決:

- Worker側が受け取ったことの確認（ackと、DISPATCHEDへの遷移の誰が行うか）。今は印を付けない。
- Placement Approvalの署名つきbytesはState Storeに保存されていない（`leases`はdecision IDとdigestだけを持つ）。Workerが独立に検証するには、配送の対象に加えて保存するか、別の経路が要る。
- #198のWorker Protocolへの写像。

### 2026-10-27 ResultClaimと受理確認の通信仕様への対応（ADR-0028）

[ADR-0028](adr/0028-result-claim-wire-mapping.md)で、Coreの`resultclaim.Claim`・`ClaimAck`と`spec/worker/v1/worker.proto`の対応を仮承認で決めた（Issue [#198](https://github.com/hat47x/eki-fabric/issues/198)、[#173](https://github.com/hat47x/eki-fabric/issues/173)、[#57](https://github.com/hat47x/eki-fabric/issues/57)）。評価の全文はIssue #198のコメントにある。

- 方針は、protoの加算拡張（protocol revision 2）とした。既存の項目の番号・型・意味は変えず、Coreの値も狭めない。adapterは構造の変換だけを行い、結果の種類の統合や、Control Planeによる束縛値の補完をしない。
- 結果の種類は、`DEADLINE_EXCEEDED`・`OUTPUT_EXCEEDED`・`RUNNER_STUCK`を足して7種類を一対一に対応させた。`SANDBOX_LIMIT`はCoreに対応がなく、Control Planeが拒否する。
- TaskIR・Executorのダイジェスト、nonce、WorkOrderのDecision ID、出力のダイジェスト、終了コード（`sint64`）、出力のbyte数を、新しい項目で運ぶ。`FenceEpoch`は`observed_lease_revision`と同じ値（`decisions.cddl`のLeaseの項目6）とし、`RenewalSequence`とは区別する。
- `claim_id`は`Claim.Digest`のhexとし、Control Planeが認証された`Principal`から再計算して照合する。Workerの主体はbodyに置かない。
- 受理確認は、`EventReceipt.result_claim`で、4種類の判定、適用後の段階、検証の要否、`claim_id`の写しを運ぶ。`ACCEPTED`を結果の受理と読まない。
- 未知のenum・項目・不足する項目は、受理しない。`failure_code`と`outputs`は、revision 2では空でなければならない。
- `spec/worker/v1/worker.proto`へ反映した。検証は、既存の静的な検査（`conformance/protocol`）と、protocompileによるコンパイルだけである。生成コード、Connect、実通信、往復は未確認。Goの実装は含まない。

仮決め（承認待ち）: revision 2への加算拡張という方針、`claim_id`を`Claim.Digest`にすること、`SANDBOX_LIMIT`の拒否、`claimed_at`を無視すること、`failure_code`と`outputs`をrevision 2で使えなくすること、`error_code`の仮の語彙。

未決: `outputs`の正規化と解禁（#233、Artifactの確定）、`SANDBOX_LIMIT`と`failure_code`の行き先、拒否の恒久・一時の分類、申告ごとの署名、再登録で主体が変わった場合のqueue、実通信・生成コード・2つのWorkerの分離・session期限切れの確認（#198の完了条件の後半）。

### 2026-10-29 Workerが受け取ったLeaseとWorkOrderを検証し、開始の関門を通す（#57、#160）: Agent側

前の節（Control Plane側、#292）に続き、Agentが割当を受け取り、検証して、開始の関門まで通す。**試験用profileであり、#198のWorker Protocolではない。** Taskは実行しない。

- **受け取り**: `claimclient.HTTPClient.ListAssignments`が、申告と同じmTLSの接続で割当を取る。Workerは証明書で決まる。
- **検証**（`internal/agent/assignment`）: `leaseverify`（署名、信頼の起点、有効期間、Trust Bundleの新しさ）、Policy revisionの一致、`workorderverify`、`workorderaccept`（Leaseへの結び付き、Workerが承認したExecutor、nonceの耐久的な一回限りの消費）、`startgate`の順。通ったものだけを実行側のインターフェースへ渡す。改ざん・食い違いはnonceを消費しない。Local Governorが新規を受けない間は、nonceを消費せずに保留する。
- **信頼の起点**（`internal/agent/trustanchor`）: 公開鍵とbundleをファイルから読み、無ければ、読めなければ、LSE-1・WOR-1の鍵が無ければ起動しない。
- **Agentの設定**: `assignments`（信頼の起点、nonce DB、イベントファイル、承認Executor、機械の容量、天井の割合）。`state_dir`が必須。結果はイベントファイルにJSON Linesで残す。
- **試験**: プロセス内（`internal/agent/assignment`）で、改ざん（nonceを消費しない）、期限切れ、別のWorker宛て、別のWorkerのWorkOrderの混入、再起動後の再生、別の鍵の偽造、Policy revisionの不一致、未承認のExecutor、古いTrust Bundle、荷札の食い違い、Governorの保留、開始の関門の拒否、設定の欠落。プロセス間（`conformance/process`）で、`eki-control`と`eki-agent`を通し、本物の署名の割当の`ready`、期限切れLeaseの拒否、別のWorkerへ届かないこと、再起動後の`replayed`、別の信頼の起点での拒否、失効したWorkerの無受信、信頼の起点なしでは起動しないこと。

仮決め（承認待ち）:

- 信頼の起点のファイルの形（JSON、Ed25519だけ、bundleの`validated_at`が古ければ失敗）。
- 受け入れるPolicy revisionは、信頼の起点のファイルが名指す一つ。
- Local Governorは固定の割合（`ceiling_percent`）、機械の容量は設定値。実際の`agent/governor`との接続は別の作業単位。
- Executor Approvalは、Workerの設定の一覧との一致だけ（EXE-1は署名されていない）。
- 実行側のインターフェースの既定は何もしない。結果の記録は、イベントファイル。

未決:

- Placement Approvalの独立な検証（署名つきbytesの保存と配送）。今のWorkerは、WorkOrder自身の参照を配置の証拠にするので、その比較は意味を持たない。
- 信頼の起点の配布・更新・失効、Policy revisionの現在性（#78）。
- 受領の確認（ack）とDISPATCHEDへの遷移、Taskの実行、結果の申告との接続。
- 開始の関門がnonce消費後に拒否した場合の扱い（今は消費済みで、回収に任せる）。
- #198のWorker Protocolへの写像。
