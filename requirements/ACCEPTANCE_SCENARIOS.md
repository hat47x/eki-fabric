# EKI Fabric 上流Acceptance Scenarios

## この文書の役割

この文書は、Product Requirementsを具体的な利用場面・攻撃場面・離脱場面へ当てはめ、要件の不足や矛盾を見つけるための上流Acceptance Scenarioです。

ここでScenarioを満たせる見通しが立ったことは、実装済みであることを意味しません。Architectureへ入る前に、「特定の実装方式を先に決めなくても、必要な判断をRequirementから導けるか」を確認します。

Scenarioから新しい本質要求が見つかった場合、Scenarioだけへ条件を書き足して終わらせず、Product RequirementsまたはThreat Modelへ戻して正本を更新します。

---

## PAS-01 — 管理された社員ワークステーション群

### 状況

中小企業が10〜30台程度の社員PCを導入し、その一部に価格競争力の高いGPUを搭載している。PCは社員の日常業務が主用途であり、利用者が使っていない時間だけ、Embedding、文書解析、画像処理などへ余力を提供する。

### 受け入れ条件

- 社員が操作中はbackground computeが停止または十分に縮退する。
- 専用GPUサーバがなくても、端末の余力を利用できる。
- PCがsleep / shutdownしてもTask全体が破綻しない。
- GPU性能だけでなく、データ局所性、model cache、network負荷を含めて配置できる。
- 社員が明示的に提供を停止できる。

Trace: RQ-003, RQ-005〜RQ-010, RQ-013, RQ-014

---

## PAS-02 — 利用者の復帰と即時opt-out

### 状況

夜間Taskを実行中の業務端末に利用者が戻る。または利用者が「再度有効にするまで提供停止」を選ぶ。Control Planeとの通信は一時的に不安定である。

### 受け入れ条件

- ローカル判断だけで新規Task受付を停止できる。
- 実行中TaskはTask contractに従いpause / checkpoint / abortなどへ移行できる。
- 中央設定がローカル安全上限を上書きして処理を継続できない。
- 離脱をSecurity Incidentではなく、通常の状態変化として処理できる。

Trace: RQ-005〜RQ-007, RQ-010〜RQ-013

---

## PAS-03 — 高速GPUよりデータ局所性を優先する

### 状況

Worker Aは必要な20GBのmodel / datasetを保持する中速GPU、Worker Bは高速GPUだがArtifactを持たない。LANは業務時間帯で混雑している。

### 受け入れ条件

- GPU性能だけを理由にWorker Bを選ばない。
- 転送量とcache localityを配置判断へ反映できる。
- 分散処理によって、元の処理より大きな業務network負荷を生むことを避けられる。

Trace: RQ-008, RQ-014, RQ-027

---

## PAS-04 — 悪意あるClientによる資源増幅

### 状況

正規Credentialを持つClientが、数十万Task、巨大metadata、fan-out、意図的に失敗するinputを送り、retryを誘発する。

### 受け入れ条件

- 許可されたprincipal budgetを超えるcompute、network、storageを予約・消費できない。
- retryとchild Taskも元のbudget制約を迂回できない。
- 大量Requestを永続Queueへすべて入れてから初めて拒否する構成を必須としない。
- 単一主体が他Clientのavailabilityを無制限に破壊できない。

Trace: RQ-009, RQ-015〜RQ-017, SEC-006, SEC-007

---

## PAS-05 — SSRF相当と内部探索を試みるClient

### 状況

Clientがmetadata service、社内管理画面、UNC path、localhost、他Workerなどをinput locationとして指定する。

### 受け入れ条件

- URLやpathを文字列として指定しただけで、Workerのnetwork / filesystem authorityが成立しない。
- ClientがWorker IP、hostname、cache内容を探索するAPIにならない。
- 必要な外部accessは明示的なauthorityとPolicyを経る。

Trace: RQ-016, RQ-018, RQ-028, RQ-029, SEC-008

---

## PAS-06 — 悪意あるWorkerが偽Resultを返す

### 状況

Workerが計算を実行せず、もっともらしい偽Result、偽execution time、偽GPU usageを返す。

### 受け入れ条件

- Worker Identityや署名だけでAccepted Resultにならない。
- Taskが要求するValidator、replication、Proofなどの検証を通るまでResult Claimとして扱う。
- Contributionを自己申告usageだけから確定しない。

Trace: RQ-019〜RQ-023, RQ-036, SEC-003, SEC-013

---

## PAS-07 — Sybilによるquorum支配

### 状況

一人の攻撃者が100個のWorker Identityを生成し、同じ偽Resultを返して多数決を支配する。

### 受け入れ条件

- 匿名Worker数の単純多数を、高保証quorumの根拠にしない。
- Task assuranceとindependence requirementを結びつけられる余地がある。
- Sybil耐性を十分に得られない場合、その保証上限を明示する。

Trace: RQ-021〜RQ-023, SEC-012

---

## PAS-08 — 公開データへアドホック参加するVolunteer Worker

### 状況

イベント参加者がBrowserまたはNative Clientを一時的に起動し、公益研究向けの公開データ処理へCPU/GPUを提供する。参加者の身元は強く審査しない。

### 受け入れ条件

- 参加者を内部networkへ加入させる必要がない。
- 公開または低機密のTaskだけを割り当てられる。
- Browser closeやWorker disappearanceを通常状態として扱える。
- Result correctnessが参加者の善意だけに依存しない。
- 資源提供中であることを参加者が認識でき、停止できる。

Trace: RQ-003, RQ-010, RQ-019〜RQ-025, RQ-032, RQ-033, RQ-042

---

## PAS-09 — 機密Taskを通常のVolunteer Workerへ配置しない

### 状況

機密文書処理Taskがあり、近くに高性能な匿名Workerがいるが、機密性を保証する信頼境界を持たない。

### 受け入れ条件

- 性能が高くても、そのWorkerへ平文を配置しない。
- managed boundaryやverified confidential runtimeなど、Taskのconfidentiality requirementを満たすWorkerだけを候補にできる。
- 「暗号化して送信したから、実行中もWorker ownerから安全」と誤認しない。
- Workerのcapability自己申告だけで高保証配置を成立させない。

Trace: RQ-024, RQ-025, RQ-047, SEC-011

---

## PAS-10 — 複数組織間の限定Federation

### 状況

Company AとCompany Bが、それぞれのIdentity Authorityを維持し、一部Executor、Data Class、Compute Grantだけを相互利用する。

### 受け入れ条件

- Federationによって、相手Trust Domain全体への包括的authorityが発生しない。
- Identity namespaceの衝突や混同を防げる。
- AのClient CredentialがBのWorker / Control Plane Roleへ変換されない。

Trace: RQ-030, RQ-031, SEC-001, SEC-010

---

## PAS-11 — 公益目的のCompute Grant

### 状況

大手企業またはCloud Providerが、通常顧客需要に影響しない範囲の余剰GPUを、公益団体へ期間限定で提供する。

### 受け入れ条件

- ProviderはResource Class、上限、期間、priority / preemptibility、Data Classなどを制約できる。
- BeneficiaryをProvider内部networkへ入れずに利用できる。
- 通常需要が増えた場合、将来のcapacityをreclaimできる。
- BeneficiaryのTaskも通常Clientと同じAdmission / Security境界を通る。

Trace: RQ-030〜RQ-035

---

## PAS-12 — Contributionを外部Credit Policyへ接続する

### 状況

Providerが提供したcomputeについてContribution Recordを生成し、外部コミュニティが独自の相互利用Creditへ変換する。

### 受け入れ条件

- EKIは、観測・検証されたContribution情報とassuranceを外部へ渡せる。
- EKI自身は1 compute unitの金銭価値を決めない。
- EKI自身にwallet、cash-out、exchange marketを必須実装しない。
- 別主体は同じContributionを、寄付実績など別の意味へ写像できる。

Trace: RQ-036〜RQ-038

---

## PAS-13 — Go SDKを使わない未知の第三者OSS

### 状況

EKI開発時には存在しなかった他社OSSが、Go以外の言語からTaskを投入し、結果を受け取りたい。

### 受け入れ条件

- Go SDKの内部型をreverse engineerしなくても利用できる。
- application-specific semanticsをEKI Coreへ追加せず接続できる。
- 新しいExecutorが必要な場合でも、Task submissionとcode admissionの権限を分離できる。

Trace: RQ-001, RQ-002, RQ-004, RQ-026

---

## PAS-14 — 非決定的なAI Result

### 状況

同じmodel / inputでも完全一致しない可能性があるAI候補生成Taskを、複数Workerで実行する。

### 受け入れ条件

- bitwise equalityを一般的なResult Verificationへ強制しない。
- application-specific validator、provenance、複数候補、低いassuranceなどを明示できる。
- 「複数回実行したから正解」という保証をしない。
- EKIがsemantic correctnessを確認できない場合、その最終判断を利用アプリケーションまたは人間へ残せる。

Trace: RQ-021, RQ-025, RQ-045

---

## PAS-15 — Executor supply-chain substitution

### 状況

承認済みExecutor名と同じtag / nameを使い、攻撃者が別binary、image、moduleへ差し替えようとする。

### 受け入れ条件

- 人間可読名だけで実行Artifactを識別しない。
- Admission時に承認したArtifactと、Workerが取得するArtifactの完全性を照合できる。
- Task submit権限だけでは新しい実行コードを承認できない。

Trace: RQ-026, RQ-027, SEC-004

---

## PAS-16 — Contribution farming

### 状況

攻撃者が自分で無意味なTaskを生成し、自分のWorkerへ割り当て、大量のContributionを獲得しようとする。

### 受け入れ条件

- Workが実行されたという事実と、外部制度がそのWorkへ価値を認めることを分離できる。
- Contribution RecordにWork provenance、Beneficiary、測定方法、assuranceなどを残せる。
- EKI Core自身が「このContributionには○円の価値がある」と保証しない。

Trace: RQ-036〜RQ-038, SEC-013

---

## PAS-17 — Browser Workerの停止・suspension・明示的参加

### 状況

利用者がWebページ上で公開研究向けの計算資源提供を開始する。その後、別タブへ移動し、ブラウザによるbackground throttlingが発生し、最後にタブを閉じる。

別のWebサイトは、利用者へ十分に説明せず同様のWorkerを起動しようとする。

### 受け入れ条件

- Browserのbackground executionを一定時間保証される資源として扱わない。
- throttlingやtab closeによってTaskが進まなくても、Fabric全体で回復可能なTaskだけを適切に割り当てられる。
- EKI準拠のアドホック参加では、資源提供中であることを利用者が認識できる。
- 利用者がページを閉じたり停止操作をしたりすることを妨げない。
- hidden iframe等による意図しない資源提供を標準参加方法として扱わない。

Trace: RQ-005, RQ-006, RQ-010, RQ-032, RQ-042, SEC-018

---

## PAS-18 — Workerが「入力を削除した」と偽る

### 状況

公開ではない入力をWorkerへ渡した後、WorkerはTask完了時に「入力を完全削除した」と報告する。しかし、実際にはコピーを保存している。

### 受け入れ条件

- Workerの削除報告だけで機密性が回復したと扱わない。
- 事後削除を保証できないWorkerへ、削除保証を前提とするDataを最初から配置しない。
- retention policyの遵守要求と、技術的に検証できる削除保証を区別できる。

Trace: RQ-024, RQ-043, SEC-017

---

## PAS-19 — ValidatorまたはVerifierが侵害される

### 状況

Workerは偽Resultを返している。さらに、そのTaskで使われるValidatorが侵害され、偽Resultを「正しい」と判定する。

### 受け入れ条件

- Accepted ResultがどのValidator / Verifier / Policy authorityを根拠に確定したか追跡できる。
- Worker自身が任意のValidatorを指定するだけで保証が成立しない。
- 当該Validatorを信頼基盤に置くProfileでは、Validator侵害時の保証限界を明示できる。
- より高い保証が必要なTaskでは、別の独立検証やProof等を組み合わせられる余地がある。

Trace: RQ-039, RQ-044, SEC-016

---

## PAS-20 — 副作用Taskの実行済み状態が不明になる

### 状況

外部システムへ一度だけ通知を送るTaskをWorkerが実行する。通知送信直後にWorkerとの通信が切れ、Control Planeからは、通知が成功したかどうか確認できない。

### 受け入れ条件

- 「Workerが消えたから必ず再実行する」とは扱わない。
- Taskが副作用を持つことと、idempotency等の再実行条件を明示できる。
- 実行済みか不明な状態を表現し、人間または上位アプリケーションのRecovery判断へ渡せる余地がある。

Trace: RQ-011, RQ-012, RQ-046

---

## PAS-21 — 古いAttestation EvidenceやLeaseをreplayする

### 状況

あるWorkerは過去には要求された状態を満たしていたが、現在は失効またはPolicy違反状態にある。攻撃者が以前取得したEvidence、Lease、内部権限をreplayし、Taskを再び受けようとする。

### 受け入れ条件

- 過去に正しかったEvidenceを無期限に現在の保証として使わない。
- expiry、revocation、freshnessが要求される情報について、staleな情報を検出・拒否できるArchitectureを選べる。
- replay防止方式そのものはRequirementで固定しない。

Trace: RQ-048, SEC-015

---

## PAS-22 — WorkerがSecurity capabilityを偽る

### 状況

匿名Workerが高性能GPUを持つと申告し、さらに「Confidential Runtime対応」「強い隔離あり」と自己申告する。性能値の一部は実際に正しいが、機密実行環境は存在しない。

### 受け入れ条件

- 性能上の自己申告が正しいことを理由に、Security capabilityのClaimまで信頼しない。
- 高保証Taskの配置に使うcapabilityは、要求水準に応じて管理Policy、Attestation、外部Evidenceなどの根拠を持つ。
- 根拠が不明な場合、安全側へ評価できる。

Trace: RQ-019, RQ-024, RQ-025, RQ-047

## 次の圧力試験

次の巡回では、各PASへ必要に応じて以下を加えます。

- 前提条件とActor
- 保護対象Asset
- 正常経路
- Abuse path
- 必要なEvidence
- 残るRequirement gap

また、次の場面は追加候補として残します。

- malicious Federation peerがIdentity namespaceを混同させる
- Artifact decompression bombがWorker resource limitを迂回する
- Control Plane内部componentの一つだけが侵害された場合のblast radius
- 大規模Compute GrantでProvider reclaimが連鎖し、大量Taskが同時再配置される
- Energy / thermal budgetを含むProvider Policyが変動する
