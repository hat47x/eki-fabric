# EKI Fabric Quality Attributes

## この文書の役割

この文書は、EKI FabricのArchitecture候補を比べるときに使う、品質特性の上流の基線です。

Product Requirementsが定めるのは、「何を満たす必要があるか」です。この文書では、複数の実現案がRequirementの上ではどれも成り立っている場合に、**どの観点でより良い案を選ぶか**を整理します。

ここに書く数値は、まだ本番のSLOではありません。現段階の目的は、次の3つを明らかにすることです。測れるもの。比べるべきもの。保証を誇張してはいけないもの。

## なぜQuality Attributeを先に決めるのか

分散計算の基盤では、一つの性質だけを最大にすると、別の価値を壊しやすくなります。

たとえば、Workerを大量に使えば、throughputは上がるかもしれません。しかし、Network trafficやResult verificationのコストも増えます。セキュリティを理由に、すべてのWorkerに強いAttestationを要求すれば、アドホックに参加しやすいという性質を失うかもしれません。

EKIでは、こうしたTrade-offを、「実装してから気づく」のではなく、Architectureを比べる段階から明示します。

---

## QA-01 — Security containment

### 見たいこと

一つのClient、Worker、Credential、Artifact、Control Plane componentが侵害されたとき、被害がどこまで広がるか。

### 比較する観点

- Roleを越えた、authority escalationを防げるか。
- Data authorityとTask authorityが、分離されているか。
- Federationのnamespaceの取り違えを、防げるか。
- untrustedなWorkerの偽のResultを、Accepted Resultに直接昇格させないか。
- Artifact parserやExecutorの障害が、高い権限の領域へ波及しないか。
- Control Planeの一部が侵害されたときの、blast radius。
- Executorのsupply-chainでのすり替えが、実行の前に、どの境界で止まるか。
- 古い、または失効したExecutor approvalが、再利用されないか。
- Workerの自己申告を、Attestation Resultとして受理しないか。
- Attestation Resultが、別のWorkerやTrust Domainに流用されないか。
- 古いEvidence、古いVerifier Policy、期限切れのAttestation Resultを、拒否できるか。

### 将来の測定候補

- `single_component_reachable_authorities`
- `cross_role_forgery_rejection`
- `high_value_authority_overlap`
- `executor_digest_mismatch_rejection`
- `stale_executor_approval_rejection`
- `executor_revocation_propagation_delay`
- `attestation_replay_rejection`
- `stale_attestation_result_rejection`
- `attestation_policy_revision_rejection`

### 将来の測定候補

- `single_component_reachable_authorities`
- `cross_role_forgery_rejection`
- `high_value_authority_overlap`
- `executor_digest_mismatch_rejection`
- `stale_executor_approval_rejection`
- `executor_revocation_propagation_delay`

### 注意

「暗号化している」「mTLSである」「Attestationがある」といった、技術の有無だけでは評価しません。どの攻撃の経路が、どの境界で止まるのかを確認します。特に、Schedulerのように複雑なOptimization Componentに、Placementの提案以外の、価値の高いAuthorityをどこまで同居させるかを、blast radiusの観点で比べます。

---

## QA-02 — Result assurance honesty

### 見たいこと

Taskごとに必要なResult Assuranceを提供できているか。そして、確認できない正しさを、確認済みとして扱っていないか。

### 比較する観点

- 決定的なTaskの、比較と再計算
- アプリケーション固有のvalidation
- replicationのindependence
- Attestationと、意味の上での正しさとの分離
- 暗号学的なproofを利用できるか
- 非決定的なAIのResultに対する、保証の上限
- Verificationのコスト
- ProofがProgram、Input、Output、Attemptに結び付けられているか
- 有効なProofが、Executor / Program Admissionを迂回しないか
- Proof System、Verifier、Policy Revisionの違いを、追跡できるか
- Verifiable Computationと、private-inputやzero-knowledgeとを、混同していないか
- proverのコスト、verifierのコスト、proofの転送のコスト、ピーク時のメモリを、native実行と比べられるか

### 将来の測定候補

- `proof_program_binding_rejection`
- `proof_input_binding_rejection`
- `proof_output_binding_rejection`
- `proof_verifier_forgery_rejection`
- `proof_policy_revision_rejection`
- `prover_slowdown_ratio`
- `proof_verification_ratio`
- `proof_size_bytes`
- `proof_peak_memory_ratio`

### 注意

高いAssurance Profileを用意できることと、すべてのTaskを高い保証にできることとは、別のことです。

暗号学的なproofを使えるかどうかは、proofの検証に成功したかどうかだけでは決めません。Program Admission、アプリケーションのvalidation、副作用の確認、Contributionの測定といった、別の保証を、必要に応じて組み合わせます。また、proofを生成するコストは、Task、Proof System、Hardware、Privacy modeによって変わります。そのため、公開されている単一のbenchmarkの値を、EKIでの実測値として扱いません。

---

## QA-03 — Confidentiality fit

### 見たいこと

Data Classに応じて、Workerに開示してよい情報を、適切に制限できるか。

### 比較する観点

- public、managed、confidential executionの分離
- Dataを渡す前の、placementの判断
- Worker側のSecurity capabilityに、根拠があるか
- Raw Attestation Evidenceなどの、不要な開示を避けられるか
- Vendor固有のRaw EvidenceをSchedulerやClientに渡さずに、正規化したResultに縮約できるか
- Federation先へのDataの流出の範囲

### 注意

一度、untrustedなWorkerに渡した平文を、事後の削除だけで回収できるとは評価しません。

Remote Attestationを使う場合も、Raw Evidenceの量が多いほど保証が高いとは評価しません。Relying Partyが必要とする判断の結果とprovenanceだけを渡します。Vendor固有のEvidenceや、端末を識別する情報の、露出の範囲を比べます。

---

## QA-04 — Endpoint user impact

### 見たいこと

業務端末や個人の端末で、EKIの存在を、利用者が過度に意識することなく、本来の作業を続けられるか。

### 比較する観点

- 利用者のactivityを検知してから、capacityを縮小するまでの速さ
- opt-outが反映されるまでの時間
- CPU、GPU、Memoryへの圧迫
- UIの応答性への影響
- ファンの騒音と、発熱への影響
- Batteryで動いているときの振る舞い

### 将来の測定候補

- `time_to_local_quiescence_after_opt_out`
- `foreground_latency_delta`
- `background_cpu_share`
- `background_gpu_share`

具体的なSLOは、実装して検証した後に決めます。

---

## QA-05 — Network efficiency

### 見たいこと

分散することで、元の計算よりも大きなNetworkのコストを生まないか。

### 比較する観点

- inputとoutputのbyte数
- cacheのhit率
- modelやdatasetの局所性
- 重複した転送
- Artifact prefetchの制御
- 大量のretryやreclaimのときの、trafficの増幅

### 将来の測定候補

- `bytes_transferred_per_accepted_result`
- `artifact_cache_hit_ratio`
- `duplicate_artifact_transfer_ratio`
- `reclaim_recovery_network_amplification`
- `recovery_transfer_bytes_per_window`

---

## QA-06 — Recoverability under churn

### 見たいこと

Workerのsleep、shutdown、Browserを閉じること、拠点の停止、大規模なGrantのreclaimを、通常の状態として吸収できるか。

### 比較する観点

- Leaseやretryから回復するまでの時間
- checkpointを利用できるか
- retry stormを抑えられるか
- 副作用を持つTaskで、状態が不明になった場合の扱い
- 外部への副作用のUNKNOWNを、自動でretryできる失敗に、誤って分類しないか
- 冪等なretryで、Operation IdentityとRequest Intentを、Attemptをまたいで維持できるか
- Control Planeを再起動した後の、状態の復元

### 将来の測定候補

- `reclaim_reentry_rate`
- `recovery_queue_age`
- `retry_attempt_amplification_ratio`
- `mass_reclaim_recovery_time`
- `unknown_side_effect_outcomes`
- `automatic_retry_suppressed_unknown_outcome`
- `idempotent_effect_duplicate_application_count`
- `side_effect_reconciliation_time`

### 注意

「すぐに再実行する」ことを、常に高いRecoverabilityとは評価しません。副作用を持つTaskでは、再実行しないほうが安全な場合があります。

また、backoffによって時刻を散らしただけでは、残っているcapacityやNetwork Budgetを超える再入場を、防げるとは限りません。Recovery Queueから実行系へ戻す境界そのもののrateや転送の制約も、比較の対象にします。

外部への副作用を伴うTaskでは、Workerが消えたこと、timeout、cancelを、「副作用が失敗した」という証拠にしません。冪等でない操作の結果を確認できなくなった場合は、回復の速さよりも、二重に副作用が起きるのを避けることを優先します。UNKNOWNやreconciliationの状態へ退避できる案を、高く評価します。

---

## QA-07 — Resource efficiency

### 見たいこと

遊休資源を利用するために、過大な管理のoverheadを払っていないか。

### 比較する観点

- Control Planeのoverhead
- Agentがidleのときのoverhead
- Verificationのoverhead
- Artifactの転送のコスト
- ProofやAttestationのコスト
- Workerの発見とheartbeatのコスト

### 注意

Cloudのコストの削減だけで評価しません。Network、電力、Endpointの摩耗、運用の工数も、Resourceのコストです。

---

## QA-08 — Energy and device safety

### 見たいこと

遊休computeを利用することが、端末の保護やProviderのEnergy Policyと、矛盾していないか。

### 比較する観点

- BatteryかACかという条件
- thermal throttling
- Providerのenergy budget
- 利用する時間帯
- ローカルのSafety Governorが持つ強制力
- Contributionによるincentiveからの独立性

### 注意

hardware固有のsensorの精度まで、共通の仕様にはしません。EKIが必要とするのは、ローカルが安全な側へcapacityを狭められることです。

---

## QA-09 — Interoperability

### 見たいこと

EKIを開発した時点では存在しなかったソフトウェアやRuntimeが、Coreを変更せずに参加できるか。

### 比較する観点

- external contractが、言語に中立であること
- version negotiation
- 未知のextensionの扱い
- Executor Runtimeの差し替え
- Identity、Artifact、Queueの実装の差し替え
- Browser、Native、Serverの共存

### 注意

「SDKが多くの言語に対応している」だけでは、十分ではありません。Protocolそのものが、特定のSDKの内部の型に依存していないことを確認します。

---

## QA-10 — Operability and explainability

### 見たいこと

中小企業を含む運用者が、何が起きているのかを理解でき、問題を切り分けられるか。

### 比較する観点

- Task、Attempt、Result、Verificationの追跡
- placementをrejectした理由
- opt-outやreclaimの理由
- Security eventを見られるか
- FederationとGrantの変更の履歴
- 監査の情報に、過剰な機密のDataが含まれていないか

### 文書の品質との関係

EKIでは、運用上の概念を理解するために、分散システムの専門家であることを前提にしません。状態の名前やErrorは、実装の内部の言葉だけでなく、運用者が判断できる説明につなげられることを重視します。

---

## QA-11 — Contribution measurement integrity

### 見たいこと

資源を提供した記録を、外部の制度が利用するときに、測定の方法と保証の水準を、誤解しないか。

### 比較する観点

- self-reported、control-observed、attestedなどの測定方法
- Workのprovenance
- Accepted Resultとの関係
- Resource Classによる違い
- self-dealingやfarmingを検出するための材料
- Privacyとの両立
- Observerが主張できるAssuranceに、Policyで上限を設けられるか
- Accepted Resultと、Resourceの量の測定とを、別のEvidenceとして扱えるか
- 同じAttemptやMetricの、重複または競合するRecordを、二重に計上しないか
- 測定のMetricやUnitを、保証の範囲より強い名前に読み替えていないか
- EKI Coreが、Universal Scoreや金銭の価値を、暗黙のうちに生成していないか

### 将来の測定候補

- `self_report_rejected_for_stronger_policy`
- `measurement_observer_forgery_rejection`
- `measurement_assurance_ceiling_rejection`
- `duplicate_contribution_record_suppression`
- `conflicting_measurement_rejection`
- `measurement_basis_preservation`

### 注意

Contributionの記録の精度と、そのContributionに外部の制度が与える価値とは、別のことです。

Accepted Resultの有無を、Contributionの発行の条件に使う場合でも、それをCPUやGPUの時間などの測定値そのものの根拠にはしません。また、高い保証のContributionの測定を導入しても、Local Safety Governor、利用者のopt-out、thermalやenergyの制約を、Contributionのincentiveで上書きできるとは評価しません。

---

## QA-12 — Deployment proportionality

### 見たいこと

小規模な社内の利用から、複数組織にまたがるFederationまで、同じSecurity principleを保てるか。そのうえで、不要な運用の負荷を強いていないか。

### 比較する観点

- 小規模な構成で必要な、常駐するcomponentの数
- 専用のDB、Queue、PKIなどが必須かどうか
- 企業が既に持つIdentity基盤との接続
- Firewallの追加の要件
- Federationのときだけ必要になるcomponentを、分離できるか

### 注意

大規模な構成で必要になる機能を、10台程度のEndpoint poolにまで、無条件に要求することは避けます。

---

# Architectureの比較での使い方

Architecture候補を比べるときは、単一の総合点だけで優劣を決めません。

たとえば次のように、Requirementを満たすかどうかと、Quality Attributeの上でのTrade-offとを、分けて考えます。

```text
Candidate A
  Requirement: PASS
  Network efficiency: strong
  Deployment proportionality: weak

Candidate B
  Requirement: PASS
  Network efficiency: medium
  Deployment proportionality: strong
```

Requirementを破る案を、「性能が高いから」という理由で採用することはしません。一方で、すべてのRequirementを満たす案どうしでは、利用の形態によってQuality Attributeの重みが変わることを認めます。

## 次に具体化する項目

Architecture Entry Gateまでに、少なくとも次の項目について、小さなSpikeやReferenceでの検証から、比較の材料を得ます。

- opt-outの後のlocal quiescence
- Browser lifecycleとresource enforcement
- Artifactの局所性と転送のコスト
- Resultのvalidationとreplicationのoverhead
- 悪意のあるArtifactの展開が及ぼす、最大の影響
- mass reclaimの後のretryの増幅
- Control Plane authority separationの最小構成
- Federation trust binding
- Contribution measurementの、最低限のassurance
