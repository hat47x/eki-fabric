# EKI Fabric 上流工程計画

## 目的

EKI Fabricでは、実装しやすい技術を先に選び、その制約へRequirementを合わせる進め方を避けます。

Product Value、利用形態、Threat Model、Product Requirements、Acceptance Scenario、Assurance Model、Quality Attributeを十分に具体化し、特にリスクの高い点だけを小さな実証で確認してからArchitectureへ入ります。

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

`docs/for-readers.md`、`docs/glossary.md`、`requirements/README.md`を用意し、参画に必要な前提知識を文書側で補う導線を整備済みです。

## Phase 1 — Requirements & threat discovery

### 成果物

- `requirements/PRODUCT_REQUIREMENTS.md`
- `requirements/THREAT_MODEL.md`
- `requirements/REQUIREMENT_GAP_REVIEW.md`
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

## Phase 3 — Quality attributes / assurance model

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

## Phase 4 — Feasibility research / spikes

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

状態: **着手済み — 高リスクSpikeを順次実証中**

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

## Architecture段階で比較する領域

Gate Uまでは採用を確定しませんが、後段で少なくとも次を比較します。

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

## 作業原則

- Requirementの不確実性を減らす作業を優先する。
- 実装しやすさだけを理由にRequirementを弱めない。
- 既存OSSで十分な部分は積極的に再利用する。
- 既存OSSの前提がEKIと異なる場合は、その差を明記し、Adapterまたは独自実装の必要性を判断する。
- 一つのReference Systemへ全体Architectureを寄せすぎない。
- 文書の自然な日本語と背景説明を重視し、専門知識がないことを読者側の責任にしない。
- 説明を厚くしても、Requirement、Threat、ADRの正本を複製しない。
- 作業ブランチは目的単位で作成し、完了した変更は元の`main`へPR経由で収束させる。後続作業を未マージbranchから派生させて履歴を断片化しない。
