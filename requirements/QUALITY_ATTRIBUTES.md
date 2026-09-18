# EKI Fabric Quality Attributes

## この文書の役割

この文書は、EKI FabricのArchitecture候補を比較するときに使う品質特性の上流基線です。

Product Requirementsが「何を満たす必要があるか」を定めるのに対し、この文書では、複数の実現案がRequirement上は成立している場合に、**どの観点でより良い案を選ぶか**を整理します。

ここに記す数値は、まだproduction SLOではありません。現段階では、測れるもの、比較すべきもの、保証を誇張してはいけないものを明らかにすることを目的とします。

## なぜQuality Attributeを先に決めるのか

分散計算基盤では、ある一つの性質だけを最大化すると別の価値を壊しやすくなります。

たとえば、Workerを大量に使えばthroughputは上がるかもしれませんが、Network trafficやResult verification costも増えます。Securityを理由にすべてのWorkerへ強いAttestationを要求すれば、アドホック参加のしやすさを失うかもしれません。

EKIでは、こうしたTrade-offを「実装してから気付く」のではなく、Architecture比較の段階から明示します。

---

## QA-01 — Security containment

### 見たいこと

一つのClient、Worker、Credential、Artifact、Control Plane componentが侵害されたとき、どこまで被害が広がるか。

### 比較観点

- Roleを越えたauthority escalationを防げるか
- Data authorityとTask authorityが分離されているか
- Federation namespaceの取り違えを防げるか
- untrusted Workerの偽ResultをAccepted Resultへ直接昇格させないか
- Artifact parser / Executor failureが高権限領域へ波及しないか
- Control Plane部分侵害時のblast radius
- Executor supply-chain substitutionが実行前にどの境界で止まるか
- stale / revoked Executor approvalが再利用されないか

### 将来の測定候補

- `single_component_reachable_authorities`
- `cross_role_forgery_rejection`
- `high_value_authority_overlap`
- `executor_digest_mismatch_rejection`
- `stale_executor_approval_rejection`
- `executor_revocation_propagation_delay`

### 注意

「暗号化している」「mTLSである」「Attestationがある」といった技術の有無だけで評価しません。どの攻撃経路が、どの境界で止まるかを確認します。特にSchedulerのような複雑なOptimization Componentへ、Placement提案以外の高価値Authorityをどこまで同居させるかをblast radiusの観点で比較します。

---

## QA-02 — Result assurance honesty

### 見たいこと

Taskごとに必要なResult Assuranceを提供し、確認できない正しさを確認済みとして扱わないか。

### 比較観点

- deterministic Taskの比較・再計算
- application-specific validation
- replicationのindependence
- Attestationとsemantic correctnessの分離
- cryptographic proofの利用可能性
- 非決定的AI Resultの保証上限
- Verification cost

### 注意

高いAssurance Profileを用意できることと、すべてのTaskを高保証にできることは別です。

---

## QA-03 — Confidentiality fit

### 見たいこと

Data Classに応じて、Workerへ開示してよい情報を適切に制限できるか。

### 比較観点

- public / managed / confidential executionの分離
- Dataを渡す前のplacement判断
- Worker側のSecurity capabilityに根拠があるか
- Raw Attestation Evidence等の不要な開示を避けられるか
- Federation先へのData流出範囲

### 注意

一度untrusted Workerへ渡した平文を、事後削除だけで回収できるとは評価しません。

---

## QA-04 — Endpoint user impact

### 見たいこと

業務端末や個人端末で、EKIの存在を利用者が過度に意識せず本来作業を継続できるか。

### 比較観点

- user activity検知後のcapacity縮小速度
- opt-out反映時間
- CPU / GPU / Memory pressure
- UI responsivenessへの影響
- Fan noise / thermal impact
- Battery時の振る舞い

### 将来の測定候補

- `time_to_local_quiescence_after_opt_out`
- `foreground_latency_delta`
- `background_cpu_share`
- `background_gpu_share`

具体的SLOは実装検証後に決めます。

---

## QA-05 — Network efficiency

### 見たいこと

分散することで、元の計算より大きなNetwork costを生まないか。

### 比較観点

- input / output bytes
- cache hit rate
- model / dataset locality
- duplicate transfer
- Artifact prefetchの制御
- mass retry / reclaim時のtraffic amplification

### 将来の測定候補

- `bytes_transferred_per_accepted_result`
- `artifact_cache_hit_ratio`
- `duplicate_artifact_transfer_ratio`
- `reclaim_recovery_network_amplification`
- `recovery_transfer_bytes_per_window`

---

## QA-06 — Recoverability under churn

### 見たいこと

Workerのsleep、shutdown、Browser close、拠点停止、大規模Grant reclaimを通常状態として吸収できるか。

### 比較観点

- Lease / retryの回復時間
- checkpoint利用可能性
- retry storm抑制
- side-effect Taskの不明状態の扱い
- 外部副作用のUNKNOWNを自動retry可能なfailureへ誤分類しないか
- idempotent retryでOperation IdentityとRequest IntentをAttempt間で維持できるか
- Control Plane restart後の状態復元

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

「すぐ再実行する」ことを常に高いRecoverabilityとは評価しません。副作用Taskでは再実行しないことが安全な場合があります。

また、backoffで時刻を散らしただけでは、残存capacityやNetwork Budgetを超える再入場を防げるとは限りません。Recovery Queueから実行系へ戻す境界自体のrate / transfer制約も比較対象にします。

外部副作用を伴うTaskでは、Worker disappearance、timeout、cancelを「副作用が失敗した」という証拠にしません。non-idempotentな操作で結果確認不能となった場合、回復速度より二重副作用を避けることを優先し、UNKNOWN / reconciliationへ退避できる案を高く評価します。

---

## QA-07 — Resource efficiency

### 見たいこと

遊休資源を利用するために、過大な管理overheadを支払っていないか。

### 比較観点

- Control Plane overhead
- Agent idle overhead
- Verification overhead
- Artifact transfer cost
- Proof / Attestation cost
- Worker discovery / heartbeat cost

### 注意

Cloud cost削減だけで評価しません。Network、電力、Endpoint wear、運用工数もResource costです。

---

## QA-08 — Energy and device safety

### 見たいこと

遊休computeの利用が、端末保護やProviderのEnergy Policyと矛盾しないか。

### 比較観点

- Battery / AC条件
- thermal throttling
- Provider energy budget
- 利用時間帯
- local Safety Governorの強制力
- Contribution incentiveとの独立性

### 注意

hardware固有のsensor precisionまで共通仕様にしません。EKIが必要とするのは、ローカルが安全側へcapacityを狭められることです。

---

## QA-09 — Interoperability

### 見たいこと

EKI開発時に存在しなかったソフトウェアやRuntimeが、Core変更なしに参加できるか。

### 比較観点

- external contractの言語中立性
- version negotiation
- unknown extensionの扱い
- Executor Runtime差し替え
- Identity / Artifact / Queue implementation差し替え
- Browser / Native / Serverの共存

### 注意

「SDKが多言語である」だけでは十分ではありません。Protocolそのものが特定SDK内部型へ依存していないことを確認します。

---

## QA-10 — Operability and explainability

### 見たいこと

中小企業を含む運用者が、何が起きているかを理解し、問題を切り分けられるか。

### 比較観点

- Task / Attempt / Result / Verificationの追跡
- rejected placement理由
- opt-out / reclaim理由
- Security eventの可視性
- Federation / Grantの変更履歴
- 監査情報が過剰な機密Dataを含まないか

### 文書品質との関係

EKIでは、運用上の概念を理解するために分散システム専門家であることを前提にしません。状態名やErrorは、実装内部語だけでなく、運用者が判断できる説明へつなげられることを重視します。

---

## QA-11 — Contribution measurement integrity

### 見たいこと

資源提供の記録を外部制度が利用するとき、測定方法と保証水準を誤解しないか。

### 比較観点

- self-reported / control-observed / attested等の測定方法
- Work provenance
- Accepted Resultとの関係
- Resource Class差
- self-dealing / farmingの検出材料
- Privacyとの両立

### 注意

Contributionの記録精度と、そのContributionに外部制度が与える価値は別です。

---

## QA-12 — Deployment proportionality

### 見たいこと

小規模な社内利用から複数組織のFederationまで、同じSecurity principleを保ちながら、不要な運用負荷を強制しないか。

### 比較観点

- 小規模構成で必要な常駐component数
- 専用DB / Queue / PKI等の必須性
- 企業既存Identity基盤との接続
- 追加Firewall要件
- Federation時だけ必要になるcomponentを分離できるか

### 注意

大規模構成で必要な機能を、10台程度のEndpoint poolにも無条件で要求することは避けます。

---

# Architecture比較での使い方

Architecture候補を比較するときは、単一の総合点だけで優劣を決めません。

たとえば次のように、Requirementを満たすかどうかとQuality Attribute上のTrade-offを分けます。

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

Requirementを破る案を「性能が高いから」で採用することはしません。一方、すべてのRequirementを満たす案同士では、利用形態ごとにQuality Attributeの重みが変わることを認めます。

## 次に具体化する項目

Architecture Entry Gateまでに、少なくとも次について小さなSpikeまたはReference検証から比較材料を得ます。

- opt-out後のlocal quiescence
- Browser lifecycleとresource enforcement
- Artifact locality / transfer cost
- Result validation / replication overhead
- malicious Artifact expansionの最大影響
- mass reclaim後のretry amplification
- Control Plane authority separationの最小構成
- Federation trust binding
- Contribution measurementの最低限のassurance
