# EKI Fabric 上流Acceptance Scenarios

## この文書の役割

この文書は、Product Requirementsを具体的な利用場面・攻撃場面・離脱場面へ当てはめ、要件の不足や矛盾を見つけるための上流Acceptance Scenarioです。

ここでScenarioを満たせる見通しが立ったことは、実装済みであることを意味しません。Architectureへ入る前に、「特定の実装方式を先に決めなくても、必要な判断をRequirementから導けるか」を確認します。

Scenarioから新しい本質要求が見つかった場合、Scenarioだけへ条件を書き足して終わらせず、Product RequirementsまたはThreat Modelへ戻して正本を更新します。

末尾の「候補Scenario（評価待ち）」は、正式なAcceptance Scenarioではありません。PAS番号を持たず、PAS-01〜PAS-27にも数えません。

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

Trace: RQ-030, RQ-031, RQ-049, SEC-001, SEC-010, SEC-019

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

---

## PAS-23 — Federation namespace / Trust Materialのすり替え

### 状況

Company AはCompany BとFederationしている。攻撃者はFederation設定を改変し、Company CのTrust MaterialをCompany Bのnamespaceへ対応付けようとする。

### 受け入れ条件

- Identity verification時に、Trust Domain / Identity AuthorityとTrust Materialのbindingを保持する。
- 「登録済みのどれかの鍵で検証できた」ことだけでBのIdentityとして扱わない。
- Federation設定の変更を通常Client権限から行えない。
- BがCとFederationしていることを理由に、AからCへの包括的Trustを自動生成しない。

Trace: RQ-031, RQ-049, RQ-051, SEC-019

---

## PAS-24 — 圧縮Artifactによる展開爆弾とpath escape

### 状況

Clientが数MBの圧縮Artifactを投入する。展開すると数十GBになり、膨大なfile数と深い入れ子を含む。さらに一部entryは展開先の外を指すpathを持つ。

### 受け入れ条件

- 受信byte数だけをResource Budgetに使わない。
- 展開後サイズ、file count、nesting、処理時間などに上限を持てる。
- Archive entry名だけでSandbox外へ書き込めない。
- Parser / Converterが失敗してもControl Plane全体が巻き込まれない境界を選べる。
- Artifactの署名やDigestが正しいことを、安全にparseできる根拠と誤認しない。

Trace: RQ-009, RQ-017, RQ-027, RQ-029, RQ-050, SEC-020

---

## PAS-25 — Schedulerだけが侵害される

### 状況

Control PlaneのScheduler componentが侵害され、攻撃者は任意Workerへの配置を試みる。一方、Artifact capability発行、Executor admission、Federation administrationの各authorityは侵害されていない。

### 受け入れ条件

- Scheduler侵害だけで任意Artifact read capabilityやExecutor approvalを発行できない構成を選べる。
- 機密TaskをSecurity要件未達Workerへ配置しようとした場合、別Enforcement boundaryで拒否できる余地を持つ。
- どのComponent / authorityがPlacementやCapability発行を行ったか追跡できる。
- 「Control Plane内部だから全権」という単一Credential依存を必須としない。

Trace: RQ-039〜RQ-041, RQ-044, RQ-051, SEC-021

---

## PAS-26 — 大規模Compute Grantの一斉reclaim

### 状況

大手Providerが多数GPUを公益Compute Grantへ提供している。通常需要が急増し、数千Taskを実行中のWorker群からcapacityを短時間でreclaimする。

### 受け入れ条件

- ProviderはTask完遂を待たず、許可された条件でcapacityを取り戻せる。
- 大量のTask離脱をFabricで起こり得る通常Eventとして扱う。
- 未完了Taskを一斉retryし、残存Worker、Artifact Store、Network、Control Planeへ無制限な負荷を掛けない。
- Taskのpriority、deadline、retryability等に応じて再配置を調整できる余地を持つ。

Trace: RQ-010, RQ-013, RQ-017, RQ-035, RQ-052, SEC-023

---

## PAS-27 — Endpointの温度上昇と電力Policy変更

### 状況

業務用GPUワークステーションが夜間Taskを処理している。GPU温度が上昇し、同時に端末はBattery駆動へ切り替わる。中央Schedulerはまだ高いcapacityを利用可能と見ている。

### 受け入れ条件

- Local Governorだけでcapacityを縮小または0へできる。
- 中央Scheduler、Task priority、Contribution獲得を理由にローカル安全上限を上書きしない。
- Energy budget、power source、thermal state等をResource Policyへ反映できる。
- Task側はWorker離脱として安全に扱える。

Trace: RQ-005〜RQ-007, RQ-010, RQ-053, SEC-022

## 次の圧力試験

第三巡目で、前回残していたFederation namespace、Artifact expansion、Control Plane部分侵害、mass reclaim、Energy / thermal boundaryをScenario化しました。

Architecture Entry Gateへ向けて残る主要論点は、Scenario数を増やすことよりも、次の横断テーマを比較可能なQuality Attributeへ落とすことです。

- Worker independence / Sybil耐性の保証水準
- Browser / Native / managed EndpointごとのResource Enforcement精度
- Artifact processingの最大影響と隔離境界
- Control Plane authority separationの最小構成
- FederationのTrust bindingとrevocation
- Result Verification costとSecurity assuranceの釣り合い
- Contribution measurement accuracyとPrivacy
- mass reclaim時の回復性とNetwork overhead

---

## 候補Scenario（評価待ち）— 製品ファミリーから来るworkload

この節のScenarioは、評価待ちの候補です。PAS番号を付けず、上の27 Scenarioにも数えません。

目的は、同じ作者の関連製品群（以下、製品ファミリー）が実際に出し得るworkloadを、既存のRequirementだけで扱えるかを確かめることです。ここから新しいRequirementは作りません。検討の中で本質的な不足が見つかった場合は、この文書の原則どおりProduct RequirementsまたはThreat Modelへ戻します。候補を正式なScenarioにする場合は、レビューを経てPAS番号を付けます。

候補は、利用側の意味をEKIへ持ち込まず、workload contractの性質だけで書きます。具体的には、再実行してよいか、期限、データの所在、信頼・隔離の条件、結果の検証方法です。各製品にとっての意味は、状況の説明に留めます。

候補は2種類あります。

- **PAS-CAND-01〜04**：SEI Cognitionが背景で行う認知処理です。SEI側でも、まだ評価段階の設計仮説に位置づけられています（`hat47x/sei-cognition:evaluation/sei-cognition-orchestration/EMERGENT_COGNITIVE_ARCHITECTURE.md` §10〜§11）。SEIのCognitive Envelopeからの投影は、[SEI Cognitionとの対応ノート](../research/sei-cognition-mapping.md) を参照してください。
- **PAS-CAND-05〜06**：SEIの認知処理ではない、兄弟製品のworkloadです。EKIが、初期利用者である認知系の製品だけに合わせ込まれていないかを確かめます（Product Value「Interoperability before ecosystem lock-in」）。

いずれもM0 / MVPの成立条件ではなく、`plan/UPSTREAM_PLAN.md`の優先順位も変えません。

### PAS-CAND-01 — 仮説の分岐探索（評価待ち）

#### 状況

SEI Cognitionが、ある仮説について、支持する証拠の探索、反例の探索、別の因果モデルの組み立てなど、役割の異なる探索を12本同時に依頼する。どの探索も同じ入力記録を読む。SEIは結果を多数決で一つにまとめず、食い違いや少数の見方も残したいと考えている。

#### workload contractとしての性質

- 探索ごとに独立したTaskとし、役割の違いは登録済みExecutorのparameterとしてだけ渡す。
- 再実行してよく（retryable）、途中で止めてよい（preemptible）。優先度はbackgroundで、期限は緩い。
- データの所在：12本が同じ入力を読むため、入力の転送量とcacheの位置が配置に効く。
- 信頼・隔離：入力記録のData Classに従う。
- 結果の検証：出力形式の確認まではできるが、内容の正しさは確認できない。

#### 受け入れ条件

- 12本への展開とそのretryが、依頼主体に許可されたbudgetを超えない。
- 結果を一致や多数決で一つに畳まず、各Resultを、使ったExecutor、モデル、入力の来歴とともに個別に返せる。
- 一部の探索が期限内に終わらなくても、終わったTaskの結果は受け取れる。終わらなかったTaskを、成功とも、結果が空だったとも取り違えない。
- 同じ入力を12回転送するより、入力を持つWorkerや近くのWorkerへまとめて配置する判断ができる。
- EKIは探索の役割の意味を知らず、役割からResource要求や優先度を推し量らない。

Trace: RQ-001, RQ-008, RQ-011, RQ-017, RQ-021, RQ-039, RQ-045

### PAS-CAND-02 — 反例探索（評価待ち）

#### 状況

SEI Cognitionが、ある前提が成り立たない例を、組合せの探索空間から探す。空間を1000の区画に分け、多数の業務端末やアドホック参加者のWorkerへ配る。見つかった反例は安く確かめられるが、「見つからなかった」という結果は確かめにくい。

#### workload contractとしての性質

- 区画ごとの小さなTaskで、再実行してよい。探索が決定的なら、同じ入力から同じ結果が出る（deterministic）。
- データの所在：探索空間の定義は小さく、転送量は少ない。
- 信頼・隔離：入力が公開または低機密であれば、アドホック参加者も候補にできる。
- 結果の検証：「反例あり」はValidatorで確かめられる。「反例なし」は、Workerの申告だけでは確かめられない。

#### 受け入れ条件

- 「反例あり」のResultは、Taskが指定したValidatorで反例を確かめられた場合に、それを根拠にAccepted Resultにできる。
- 「反例なし」のResultを、Workerの申告だけで、区画全体を調べた証拠として扱わない。悪意あるWorkerは、反例を隠して「なし」と返せる。
- 「なし」に必要な保証（独立した複製実行、管理下Workerでの再実行など）をTaskの検証条件として指定できる。一人の攻撃者が多数のWorkerを作れる環境では、その保証の上限を明示する。
- 期限までに調べ切れなかった区画を、「反例なし」と区別して返せる。
- EKIは前提の意味を知らず、探索空間を入力Artifactとparameterとしてだけ扱う。「なし」をSEI側で未確認として扱うかどうかは、SEIが判断する。

Trace: RQ-019, RQ-021, RQ-022, RQ-032, RQ-044, RQ-045

### PAS-CAND-03 — 夜間の統合（評価待ち）

#### 状況

SEI Cognitionが、社内サーバに蓄積した判断記録を夜間に読み直し、繰り返し現れるパターンや、古くなった前提の候補を抜き出す。処理には数時間かかり、30台の社員PCの余力を使う。元の記録は書き換えてはならない。始業時刻には、多くの社員がほぼ同時に端末へ戻る。

#### workload contractとしての性質

- 再実行してよく、途中で止めてよい。区切りごとに続きから再開できる（checkpointable）ことが望ましい。
- 期限は翌朝の始業前で、優先度はbackground。
- データの所在：記録は社内サーバにあり、量が多い。転送が業務ネットワークを圧迫する時間帯を避ける必要がある。
- 信頼・隔離：記録は社内限定であり、Data Classを満たす管理下の端末だけが候補になる。
- 結果の検証：出力形式の確認と、入力記録のdigestとの対応づけまで。

#### 受け入れ条件

- 始業時に多くの端末が一斉に提供をやめても、retryや別のWorkerへの移し替えが一度に集中しない。checkpointできるTaskは、続きから再開できる余地を持つ。
- 期限に間に合わない場合、終わった分の成果物と未完了の部分を区別して返す。利用者が戻った端末で処理を続けない。
- 入力記録は、内容を固定した読み取り専用のArtifactとして渡す。Taskを実行できる権限だけで、元の記録を書き換えられない。成果は新しいArtifactとして返る。
- 社内限定の記録を、Data Classを満たさない端末へ配置しない。転送量が大きい場合は、記録のある場所の近くで処理するか、分散しないことを選べる。
- EKIはパターンや前提の意味を知らない。成果をSEI側で提案として扱うか、どこまで採り入れるかは、SEIが判断する。

Trace: RQ-005, RQ-007, RQ-008, RQ-010, RQ-011, RQ-014, RQ-024, RQ-027, RQ-028, RQ-052

### PAS-CAND-04 — 複数モデルの相互検証（評価待ち）

#### 状況

SEI Cognitionが、同じ入力について3種類の言語モデルに互いの出力を批評させる。2つはローカルで動くモデル、1つは外部のモデルAPIである。SEIは、どのモデルが何を出したかを、後から正確に辿りたいと考えている。

#### workload contractとしての性質

- 再実行してよいが、生成結果は実行のたびに一致するとは限らない（非決定的）。
- データの所在：モデルの重みは数GB〜数十GBあり、すでに持っている端末があれば、その近くで動かす方が有利。
- 信頼・隔離：外部APIを使う批評は、入力をEKIの管理外へ送ることになる。
- 結果の検証：意味上の正しさは確認できない。確認できるのは、どのExecutorとモデルArtifactで実行したかという来歴まで。

#### 受け入れ条件

- 各批評を実行したExecutorとモデルArtifactをdigestで区別し、Resultの来歴として残せる。表示名やtagだけでモデルを識別しない。
- EKIが保証できるのはArtifactの違いまでであり、互いに独立した系譜のモデルかどうかの判断は利用側に残す。同じ重みを別の名前で登録したものを、EKIが異なるモデルとして保証しない。
- モデルどうしの一致を、意味上の正しさの根拠にしない。
- モデルの重みを持つWorkerの位置を、配置の判断に反映できる。
- 外部APIを使う批評は、任意のURLではなく、登録されたnetwork profileまたはexternal adapterを経る。入力のData Classが外部への送信を許さなければ、その批評は実行しない。

Trace: RQ-008, RQ-021, RQ-024, RQ-026, RQ-027, RQ-029, RQ-039, RQ-045

### PAS-CAND-05 — vs-bearの蒸留学習と一括の挙動試行（評価待ち）

#### 状況

vs-bear-simulator（人とクマの身体差を体験するゲーム）の開発で、ローカルの教師モデルを、固定したcheckpointと質問の形式で一括評価し、その出力からCPU上で動く小さな決定木モデルを学習する（蒸留）。あわせて、1000件規模の固定fixtureと多数のseedを使い、クマの挙動を画面なし（headless）で一括試行する（`hat47x/vs-bear-simulator:docs/BEAR_CPU_BODY_AND_DISTILLATION.md` §5〜§7、`docs/M5_VALIDATION_HARNESS.md`）。

#### workload contractとしての性質

- 試行はseedごとに独立したTaskにでき、再実行してよい。学習はthread数とseedを固定した決定的な設定で行う。
- 専用GPUがなくてもCPUで完走できることが前提であり、GPUは教師の一括推論を速める選択肢に留まる。
- データの所在：学習用のdatasetと教師checkpointは内容のhashで固定され、成果物にはhashと元のGit SHAが記録される。
- 信頼・隔離：教師checkpointや元の動作データには、再配布の条件が付くことがある。
- 結果の検証：同じ実行環境での再実行と、数値の照合。ただし、OSやライブラリの版が違うとbit単位の一致は保証されない。

#### 受け入れ条件

- 専用GPUを持たないWorkerだけでも完走できる。GPUは配置の希望として扱い、必須条件と取り違えない。
- 実行環境（Runtime Class）、thread数、seedを固定したTaskとして表現できる。実行環境の違いによってbit単位で一致しないことを、偽Resultと誤判定しない。許容誤差での比較や、同じRuntime Classでの再実行など、Task側が選んだ検証方法を使える。
- 再配布の条件があるArtifactを、その条件を満たさないWorker（アドホック参加者など）へ配らない。これを現在のData ClassとData authorityの枠組みで表現できるかを確かめ、できなければRequirement側の論点として戻す。
- EKIは、クマの挙動の意味も、学習したモデルを製品に採用するかどうかも知らない。採否はvs-bear側が判断する。

Trace: RQ-001, RQ-003, RQ-011, RQ-021, RQ-026, RQ-027, RQ-028, RQ-039

### PAS-CAND-06 — MMIのCPU範囲探索の評価（評価待ち）

#### 状況

markdown-matrix-injector（Markdown文書からExcelの表範囲を読み取り専用で参照する記法とVS Code拡張）で、表の領域を探す複数の検索方式を比較評価する。固定seedで合成した業務風のデータ（最大で約10万領域）を使い、凍結した未見の評価セットを一度だけ評価し、遅延（p50 / p95）とメモリを測る。GPUとネットワークは使わない（`hat47x/markdown-matrix-injector:02_Architecture/input-assistance-cpu-search.md` §5、§7。同文書の状態はProposed）。

#### workload contractとしての性質

- 合成データの生成と評価は、固定seedによる決定的な純粋計算であり、再実行してよい。
- Resource：約10万領域の規模ではピークメモリが約4.4GBに達するため、メモリの小さいWorkerには置けない。
- データの所在：合成データは作り直せるため、転送するより、Workerの側で生成する方が安い場合がある。
- 信頼・隔離：合成データは低機密である。一方、利用者の実際のブックを対象とする探索は、社内の機密を含み得る。
- 結果の検証：候補の再現率は、同じ入力から計算し直して確かめられる。遅延やメモリの測定値は、測ったWorkerの申告である。

#### 受け入れ条件

- 合成データの生成と評価を、再実行してよい純粋計算のTaskとして表現できる。必要なメモリ量をResource要求として宣言でき、それを満たさないWorkerへ配置しない。
- 遅延やメモリの測定値、CPU型番などの測定条件を、Workerの申告のまま検証済みの事実として扱わない。測定値どうしを比べる必要がある場合は、管理下の特定のRuntime Classへ配置を限定できる。
- 業務端末の余力で測った値は、利用者の作業や他のTaskの影響を受ける。そうした値を、性能の基準値として扱わない。
- 利用者の実際のブックを対象とする探索では、分散した方が不利な場合や機密性を満たせない場合に、ローカルでの実行を選べる。EKIへ出さないことも正当な選択である。
- EKIは、表の範囲や記法の意味を知らない。

Trace: RQ-001, RQ-003, RQ-009, RQ-011, RQ-014, RQ-019, RQ-024, RQ-047

---

## 候補Scenario（評価待ち）— AI Agentの普及と、管理クラスタとの統合

この節のScenarioも評価待ちの候補です。PAS番号を付けず、PAS-01〜PAS-27にも数えません。

前節が「製品ファミリーから来るworkload」を扱うのに対し、この節は二つの状況変化から来る圧力を扱います。

- **PAS-CAND-07〜11**：生成AIとAI Agentの普及により、Clientが機械になり、生成物の実行と確認が増える状況です。背景は [AI時代の価値と位置づけ](../research/ai-era-value-positioning.md) にあります。
- **PAS-CAND-12〜16**：KubernetesやSlurmのような既存のオーケストレータが管理するクラスタを、資源の供給元としてEKIへ接続する状況です（[Product Value](../product/value.md)「Managed and opportunistic resources on one fabric」）。背景は [既存オーケストレータとの接続、およびSEI・TEIとの層構造](../research/orchestrator-integration-and-layering.md) にあります。

ここから新しいRequirementは作りません。既存のRequirementで扱えない点は「Requirement側へ戻す論点」として記し、レビューを経てProduct RequirementsまたはThreat Modelへ反映します。いずれもM0 / MVPの成立条件ではなく、`plan/UPSTREAM_PLAN.md`の優先順位も変えません。

### PAS-CAND-07 — AI Agentの連鎖による依頼の増殖（評価待ち）

#### 状況

認証済みのAI Agentが文書要約を依頼する。その結果を読んだ別のAgentが10件の追加Taskを出し、それぞれがさらに10件を出す。どのAgentにも悪意はなく、すべて正規の資格情報を使っているが、数分で数千件のTaskになる。

#### 受け入れ条件

- 依頼の数と資源の消費が、Clientに与えられた予算の範囲を超えない。Agentが別のAgentへ依頼を委ねても、委ねられた側の予算と権限は、委ねた側を超えない。
- 増殖を止めたときに、すでに実行中のTaskを一斉に失敗させて再試行の嵐を起こさない。
- 「認証済みだから」「悪意がないから」を理由に、上限を緩めない。

#### Requirement側へ戻す論点

- 委譲の連鎖で予算と権限が必ず縮小することを、RQ-016とRQ-017の具体化として扱えるか。それとも独立したRequirementが要るか。

Trace: RQ-015, RQ-016, RQ-017, RQ-052

### PAS-CAND-08 — 相関した確認の一致（評価待ち）

#### 状況

非決定的なAI生成のTaskについて、同じモデル・同じ重み・同じ量子化で3台に実行させ、さらに同系統のモデルで判定させたところ、すべてが一致した。しかし、実際には同じ誤りを繰り返しただけだった。悪意あるWorkerもSybilも関わっていない。

#### 受け入れ条件

- 複数の実行や判定が一致したことを、それだけで高い保証として扱わない。
- 何が独立していたか（Executor、モデルArtifactのdigest、資源提供者の運営者、検証者）を、Resultの来歴として記録できる。
- モデルの系譜が独立しているかどうかの判断は利用側へ残す（PAS-CAND-04と同じ）。EKIは、判断に使える記録を残すところまでを担う。

#### Requirement側へ戻す論点

- 「悪意のない相関による誤った一致」を、Sybil・collusion（RQ-022）と区別してAssurance Modelへ書く必要があるか。

Trace: RQ-021, RQ-022, RQ-039, RQ-044, RQ-045

### PAS-CAND-09 — 端末上のローカルAIとの資源の競合（評価待ち）

#### 状況

利用者はキーボードもマウスも操作していないが、本人のAI Assistantが端末のNPUとメモリを使って長い処理を続けている。EKIのWorkerは、人の操作がないことを理由に背景計算を始めようとする。

#### 受け入れ条件

- 人の操作がないことだけを根拠に、端末を空いているとみなさない。
- 本人のために動いている処理（ローカルAIを含む）を、EKIの背景計算より優先する。
- Local Governorが本人の処理の資源利用を把握できない場合は、安全側（背景計算を控える）に倒す。

#### Requirement側へ戻す論点

- RQ-005〜007の「利用者の本来作業」に、利用者のために自律的に動く処理が含まれることを明記する必要があるか。

Trace: RQ-005, RQ-006, RQ-007, RQ-053

### PAS-CAND-10 — AIが生成したコードの実行（評価待ち）

#### 状況

AI Agentが変換スクリプトを生成し、それを大量のファイルへ適用するTaskとしてEKIへ依頼する。スクリプトの内容は誰も確認しておらず、その出力は次のTaskの入力になる。

#### 受け入れ条件

- 生成されたコードを、作者の分からない外部入力と同じ扱いにする。EKIは安全性を判定しないが、判定できなくても被害が閉じ込められる。
- 実行体のdigestを来歴として残し、どの生成物がどの出力を作ったかを辿れる。
- 生成コードからの任意のネットワークアクセスや、Taskの権限を超えるData参照を許さない。
- 出力が次のTaskへ渡るときも、ArtifactとしてResource上限と展開の安全性を満たす。

Trace: RQ-024, RQ-026, RQ-027, RQ-029, RQ-039, RQ-050

### PAS-CAND-11 — 配置の証跡を第三者へ示す（評価待ち）

#### 状況

取引先または監査人から、ある顧客データが、どの組織の、どの種類の資源で、どの実行体によって処理され、どの根拠で結果が受理されたかの説明を求められる。ただし、他の顧客のTask、Workerの所在、社内構成は開示できない。

#### 受け入れ条件

- 特定の入力について、処理した資源の種類と所属組織、実行体のdigest、Resultを受理した根拠を示せる。
- 説明に不要なWorkerの所在、他のTask、社内構成を開示しない。
- EKIの記録は、法令や契約への適合を保証するものではない。説明の材料を提供するところまでを担う。

Trace: RQ-018, RQ-028, RQ-039, RQ-040, RQ-044

### PAS-CAND-12 — 下位オーケストレータの見えない再試行（評価待ち）

#### 状況

外部システムへの通知を含むTaskを、Kubernetesが管理するクラスタへ配置した。実行中にノード障害が起き、Kubernetes Jobが既定の設定で自動的にPodを作り直した。EKIから見ると、一つのAttemptの中で通知が2回送られた可能性がある。

#### 受け入れ条件

- 副作用を持つTask、またはidempotencyの根拠がないTaskでは、下位のオーケストレータの自動再試行を無効にした状態でしか配置しない。無効にできない資源提供者へは置かない。
- 下位が内部で再試行したことを観測できる場合は、それを記録する。観測できない場合は、実行回数を「一度だけ」と主張しない。
- 実行済みかどうか分からない場合はUNKNOWNとして保持する。

Trace: RQ-012, RQ-046

### PAS-CAND-13 — 下位オーケストレータによる一斉の横取り（評価待ち）

#### 状況

管理クラスタで優先度の高い学習ジョブが始まり、クラスタのキュー管理（Kueue等）が、そのクラスタで動いていたEKI経由の作業数百件を一斉に止めた。

#### 受け入れ条件

- 下位による横取り（preemption）を、業務端末で利用者が戻ったときの停止と同じ種類の出来事（reclaim）として扱う。失敗として丸めない。
- 止められた作業を、一斉に別の資源提供者へ再配置して再試行の嵐を起こさない。
- 副作用の有無が分からない作業はUNKNOWNとして保持する。

Trace: RQ-010, RQ-013, RQ-035, RQ-052

### PAS-CAND-14 — 資源提供者へ接続する資格情報の漏えい（評価待ち）

#### 状況

あるKubernetesクラスタへジョブを投入するためにEKIが持つ資格情報が漏れた。攻撃者は、その資格情報でクラスタへ直接ジョブを投入し、さらにEKIの他の権限を得ようとする。

#### 受け入れ条件

- 資格情報は、資源提供者ごと、名前空間・partitionごとに限定されており、クラスタ全体の管理権限を持たない。
- 一つの接続先の資格情報の漏えいが、他の資源提供者、Lease、Result受理、Policy管理の権限へ波及しない。
- 漏えいを検知したとき、その資源提供者への接続だけを止められる。

Trace: RQ-041, RQ-048, RQ-051

### PAS-CAND-15 — 下位のエラーからの内部構成の漏えい（評価待ち）

#### 状況

Slurmのジョブが失敗し、エラーメッセージにノード名、partition名、共有ファイルシステムのパスが含まれていた。Clientは、失敗の理由を知るためにTaskの状態を問い合わせる。

#### 受け入れ条件

- 下位のエラー情報を、Clientへ返す情報とAudit向けの情報に分ける。
- Clientの権限で、ノード名、IPアドレス、名前空間、社内のパスを列挙できない。
- Clientは、失敗の種類（資源不足、時間切れ、実行体の異常終了など）を、内部構成を知らずに判断できる。

Trace: RQ-018, RQ-040

### PAS-CAND-16 — 統治基盤を名乗るClientからの方針の注入（評価待ち）

#### 状況

企業統治の基盤として使われている製品が、EKIのClientとして依頼を出す。依頼に「この判断は経営上重要」「承認済み」という情報を添え、予算上限の引き上げや、Assuranceの低い資源への機密データの配置を求める。

#### 受け入れ条件

- 依頼に添えられた情報によって、予算、配置条件、Data権限を広げない。
- 統治の方針をEKIへ有効化するのは、組織の管理者権限（Administrative Plane）の経路に限る。
- 統治基盤の製品が、方針の作成や審査を支援すること自体は妨げない。

#### Requirement側へ戻す論点

- 一つの製品のコンポーネントが、Client権限と管理者権限の両方を持つ構成を許すか。許す場合、両者をどう分けて記録するか。

Trace: RQ-015, RQ-016, RQ-028, RQ-051
