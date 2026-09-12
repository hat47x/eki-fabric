# EKI Upstream Acceptance Scenarios

## 役割

Product Requirementsを特定Architectureへ固定せず、実際の利用・攻撃・離脱場面で圧力試験するための上流Acceptance Scenarioです。

この文書のScenario PASSは実装済みを意味しません。Architecture Entry Gateまでに、各Scenarioを満たせる要件集合になっていることを確認します。

---

## PAS-01 — Managed workstation pool

### Situation

中小企業が10〜30台程度の社員PCを導入し、その一部に価格競争力の高いGPUを搭載している。PCは社員の日常業務が主用途であり、idle時のみembedding、文書解析、画像処理等へ余力を提供する。

### Acceptance

- 社員操作中はbackground computeが停止または十分に縮退する。
- dedicated GPU serverがなくても余力を利用できる。
- PCがsleep/shutdownしてもTask全体が破綻しない。
- GPUを使うこと自体より、data locality / model cache / network負荷を含めて配置できる。
- 社員が明示的に提供停止できる。

Trace: RQ-003, RQ-005〜RQ-010, RQ-013, RQ-014

---

## PAS-02 — Immediate user return / opt-out

### Situation

夜間Taskを実行中の業務端末に利用者が戻る。または利用者が「再度有効にするまで提供停止」を選ぶ。Control Planeとの通信は一時的に不安定である。

### Acceptance

- ローカル判断だけで新規Task受付を停止できる。
- 実行中TaskはTask contractに従いpause/checkpoint/abort等へ移行する。
- 中央設定がローカル安全上限を上書きして処理継続できない。
- 離脱がsecurity incidentではなく通常状態として処理される。

Trace: RQ-005〜RQ-007, RQ-010〜RQ-013

---

## PAS-03 — Data-locality beats faster GPU

### Situation

Worker Aは必要な20GB model / datasetを保持する中速GPU、Worker Bは高速GPUだがartifactを持たない。LANは業務時間帯で混雑している。

### Acceptance

- GPU性能だけでBを選ばない。
- transfer costとcache localityをplacementへ反映できる。
- 分散処理によって元処理以上のnetwork負荷を発生させることを避けられる。

Trace: RQ-008, RQ-014, RQ-027

---

## PAS-04 — Malicious client resource amplification

### Situation

正規credentialを持つClientが、数十万Task、巨大metadata、fan-out、意図的に失敗するinputを送ってretryを誘発する。

### Acceptance

- 許可されたprincipal budgetを超える計算・network・storageを予約/消費できない。
- retryとchild taskも元のbudget制約を迂回できない。
- requestを大量に永続Queueへ入れてから初めて拒否する構成を必須としない。
- 他Clientのavailabilityを単一主体が無制限に破壊できない。

Trace: RQ-015〜RQ-017, RQ-009, SEC-006, SEC-007

---

## PAS-05 — Malicious client attempts SSRF / internal discovery

### Situation

Clientがmetadata service、社内管理画面、UNC path、localhost、他Worker等をinput locationとして指定する。

### Acceptance

- 文字列としてURL/pathを指定しただけでWorker network/filesystem authorityが成立しない。
- ClientがWorker IP/hostname/cache内容を探索するAPIにならない。
- 必要な外部accessは明示的authorityとpolicyを経る。

Trace: RQ-016, RQ-018, RQ-028, RQ-029

---

## PAS-06 — Malicious worker returns fabricated result

### Situation

Workerが計算を実行せず、もっともらしい偽Result、偽execution time、偽GPU usageを返す。

### Acceptance

- Worker identityや署名だけでAcceptedResultにならない。
- Taskが要求するvalidator / replication / proof等の検証を通過するまでResultClaimとして扱う。
- Contributionを自己申告usageだけから確定しない。

Trace: RQ-019〜RQ-023, RQ-036, SEC-003, SEC-013

---

## PAS-07 — Sybil quorum attack

### Situation

一人の攻撃者が100 Worker identityを生成し、同じ偽Resultを返して多数決を支配する。

### Acceptance

- anonymous worker数の単純多数を高保証quorumの根拠にしない。
- independence requirementをTask assuranceと結びつけられる余地がある。
- Sybil耐性が十分でない場合、その保証上限を明示する。

Trace: RQ-021〜RQ-023, SEC-012

---

## PAS-08 — Ad-hoc public-data volunteer

### Situation

イベント参加者がブラウザまたはnative clientを一時的に起動し、公益研究向けの公開データ処理へCPU/GPUを提供する。参加者の身元は強く審査しない。

### Acceptance

- 参加者を内部networkへ参加させる必要がない。
- 公開・低機密Taskだけを割り当てられる。
- browser close / worker disappearanceを通常状態として扱う。
- Result correctnessは参加者の善意に依存しない。

Trace: RQ-003, RQ-010, RQ-019〜RQ-025, RQ-032, RQ-033

---

## PAS-09 — Confidential workload rejects ordinary volunteer

### Situation

機密文書処理Taskがあり、近くに高性能な匿名Workerがいるが、機密性を保証するtrusted boundaryを持たない。

### Acceptance

- 性能が高くてもそのWorkerへ平文を配置しない。
- managed boundary / attested execution等、Taskのconfidentiality requirementを満たすWorkerだけを候補にできる。
- 「暗号化して送ったから実行中も安全」と誤認しない。

Trace: RQ-024, RQ-025, SEC-011

---

## PAS-10 — Limited federation between organizations

### Situation

Company AとCompany Bが各自のidentity authorityを維持し、一部Executor / Data Class / Grantだけを相互利用する。

### Acceptance

- Federationによって相手trust domain全体への包括的authorityが発生しない。
- identity namespaceの衝突・混同を防げる。
- AのClient credentialがBのWorker/Control Plane roleへ変換されない。

Trace: RQ-030, RQ-031, SEC-001, SEC-010

---

## PAS-11 — Public-interest Compute Grant

### Situation

大手企業またはCloud Providerが、通常顧客需要に影響しない範囲の余剰GPUを公益団体へ期間限定で提供する。

### Acceptance

- ProviderはResource Class、上限、期間、priority/preemptibility、Data Class等を制約できる。
- BeneficiaryをProvider内部networkへ入れずに利用できる。
- 通常需要増加時に将来capacityをreclaimできる。
- BeneficiaryのTaskは通常Clientと同じadmission/security境界を通る。

Trace: RQ-030〜RQ-035

---

## PAS-12 — Contribution linked to external credit policy

### Situation

Providerが提供したcomputeについてContribution Recordを生成し、外部コミュニティが独自の相互利用Creditへ変換する。

### Acceptance

- EKIは検証可能なContribution情報を外部へ渡せる。
- EKI自身は1 compute unitの金銭価値を決めない。
- EKI自身にwallet、cash-out、exchange marketを必須実装しない。
- 別主体は同じContributionを寄付実績等の別の意味へ写像できる。

Trace: RQ-036〜RQ-038

---

## PAS-13 — Unknown third-party OSS without Go SDK

### Situation

EKI開発時に存在しなかった他社OSSが、Go以外の言語からTaskを投入し、結果を受け取りたい。

### Acceptance

- Go SDKの内部型をreverse engineerせず利用できる。
- application-specific semanticsをEKI Coreへ追加せずに接続できる。
- 新しいExecutorが必要な場合も、Task submissionとcode admissionの権限を分離できる。

Trace: RQ-001, RQ-002, RQ-004, RQ-026

---

## PAS-14 — Non-deterministic AI result

### Situation

同じmodel/inputでも完全一致しない可能性があるAI候補生成Taskを複数Workerで実行する。

### Acceptance

- bitwise equalityを一般的なResult verificationへ強制しない。
- application-specific validatorや低いassurance level等を明示できる。
- 「複数回実行したから正解」という誤った保証をしない。

Trace: RQ-021, RQ-025

---

## PAS-15 — Executor supply-chain substitution

### Situation

承認済みExecutor名と同じtag/nameを使い、攻撃者が別binary/image/moduleへ差し替えようとする。

### Acceptance

- 人間可読名だけで実行artifactを識別しない。
- admission時に承認したartifactとWorkerが取得するartifactの完全性を照合できる。
- Task submit権限だけでは新しい実行コードを承認できない。

Trace: RQ-026, RQ-027, SEC-004

---

## PAS-16 — Malicious contribution farming

### Situation

攻撃者が自分で無意味なTaskを生成し、自分のWorkerへ割り当て、大量のContributionを獲得しようとする。

### Acceptance

- Work executedという事実と、外部制度が価値を認めることを分離できる。
- Contribution Recordにwork provenance / beneficiary / assurance等を残せる。
- EKI Core自身が「このContributionには○円の価値がある」と保証しない。

Trace: RQ-036〜RQ-038, SEC-013

## 次の圧力試験

今後は各PASについて、次を追加します。

- precondition / actor / asset
- success path
- abuse path
- required evidence
- unresolved requirement gap

Requirementに存在しない本質的な要求が見つかった場合は、PAS内だけで解決せず`PRODUCT_REQUIREMENTS.md`を更新します。
