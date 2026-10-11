# EKI Fabricの受け入れシナリオ（Acceptance Scenarios）

## この文書の役割

この文書は、Architectureに入る前の上流工程で使うAcceptance Scenarioです。Product Requirementsを、具体的な利用、攻撃、離脱の場面に当てはめて、要件の不足や矛盾を見つけます。

ここでScenarioを満たせる見通しが立ったとしても、実装済みという意味ではありません。確認するのは、「特定の実装方式を先に決めなくても、必要な判断をRequirementから導けるか」という点です。

Scenarioから、新しい本質的な要求が見つかった場合は、Scenarioだけに条件を書き足して終わりにしません。Product RequirementsかThreat Modelに戻して、正本を更新します。

末尾にある「候補Scenario（評価待ち）」は、正式なAcceptance Scenarioではありません。PAS番号を持たず、PAS-01〜PAS-27にも数えません。

---

## PAS-01 — 管理された社員ワークステーション群

### 状況

中小企業が、10〜30台ほどの社員PCを導入している。その一部には、価格競争力の高いGPUが載っている。PCの主な用途は、社員の日常の業務である。利用者が使っていない時間だけ、Embedding、文書の解析、画像の処理などに、余力を提供する。

### 受け入れ条件

- 社員が操作している間は、background computeが停止するか、十分に縮退する。
- 専用のGPUサーバがなくても、端末の余力を利用できる。
- PCがsleepやshutdownをしても、Task全体が破綻しない。
- GPUの性能だけでなく、データの局所性、model cache、networkの負荷も含めて配置できる。
- 社員が、明示的に提供を停止できる。

Trace: RQ-003, RQ-005〜RQ-010, RQ-013, RQ-014

---

## PAS-02 — 利用者の復帰と、即時のopt-out

### 状況

夜間のTaskを実行している業務端末に、利用者が戻ってくる。または、利用者が「再び有効にするまで、提供を停止する」を選ぶ。Control Planeとの通信は、一時的に不安定になっている。

### 受け入れ条件

- ローカルの判断だけで、新しいTaskの受け付けを止められる。
- 実行中のTaskは、Task contractに従って、pause、checkpoint、abortなどに移れる。
- 中央の設定が、ローカルの安全の上限を上書きして、処理を続けることはできない。
- 離脱を、Security Incidentとしてではなく、通常の状態の変化として処理できる。

Trace: RQ-005〜RQ-007, RQ-010〜RQ-013

---

## PAS-03 — 高速なGPUよりも、データの局所性を優先する

### 状況

Worker Aは、必要な20GBのmodelとdatasetを保持している、中速のGPUを持つ。Worker Bは高速なGPUを持つが、Artifactを持っていない。LANは、業務時間帯で混雑している。

### 受け入れ条件

- GPUの性能だけを理由に、Worker Bを選ばない。
- 転送量とcacheの局所性を、配置の判断に反映できる。
- 分散処理によって、元の処理よりも大きな業務networkの負荷を生むことを、避けられる。

Trace: RQ-008, RQ-014, RQ-027

---

## PAS-04 — 悪意のあるClientによる、資源の増幅

### 状況

正規のCredentialを持つClientが、数十万のTask、巨大なmetadata、fan-out、意図的に失敗するinputを送り、retryを誘発する。

### 受け入れ条件

- 許可されたprincipalのbudgetを超えるcompute、network、storageを、予約したり消費したりできない。
- retryや子のTaskも、元のbudgetの制約を迂回できない。
- 大量のRequestを、すべて永続的なQueueに入れてから、はじめて拒否する構成を、必須としない。
- 一つの主体が、他のClientのavailabilityを、無制限に壊せない。

Trace: RQ-009, RQ-015〜RQ-017, SEC-006, SEC-007

---

## PAS-05 — SSRFに相当する操作と、内部の探索を試みるClient

### 状況

Clientが、metadata service、社内の管理画面、UNC path、localhost、他のWorkerなどを、input locationとして指定する。

### 受け入れ条件

- URLやpathを文字列として指定しただけで、Workerのnetworkやfilesystemに対するauthorityが成立しない。
- Clientが、WorkerのIPアドレス、hostname、cacheの内容を探索するための、APIにならない。
- 必要な外部へのアクセスは、明示的なauthorityとPolicyを経る。

Trace: RQ-016, RQ-018, RQ-028, RQ-029, SEC-008

---

## PAS-06 — 悪意のあるWorkerが、偽のResultを返す

### 状況

Workerが計算を実行せずに、もっともらしい偽のResult、偽のexecution time、偽のGPU usageを返す。

### 受け入れ条件

- Worker Identityや署名だけでは、Accepted Resultにならない。
- Taskが要求するValidator、replication、Proofなどの検証を通るまでは、Result Claimとして扱う。
- Contributionを、自己申告のusageだけから確定しない。

Trace: RQ-019〜RQ-023, RQ-036, SEC-003, SEC-013

---

## PAS-07 — Sybilによる、quorumの支配

### 状況

一人の攻撃者が、100個のWorker Identityを生成して、同じ偽のResultを返し、多数決を支配する。

### 受け入れ条件

- 匿名のWorkerの数の単純な多数を、高い保証のquorumの根拠にしない。
- Taskのassuranceと、independence requirementを、結び付けられる余地がある。
- Sybilへの耐性を十分に得られない場合は、その保証の上限を明示する。

Trace: RQ-021〜RQ-023, SEC-012

---

## PAS-08 — 公開データにアドホックに参加する、Volunteer Worker

### 状況

イベントの参加者が、BrowserまたはNative Clientを一時的に起動して、公益の研究のための公開データの処理に、CPUやGPUを提供する。参加者の身元は、強くは審査しない。

### 受け入れ条件

- 参加者を、内部networkに加入させる必要がない。
- 公開または低機密のTaskだけを、割り当てられる。
- Browserを閉じることや、Workerが消えることを、通常の状態として扱える。
- Resultが正しいことが、参加者の善意だけに依存しない。
- 資源を提供している間は、そのことを参加者が認識でき、停止できる。

Trace: RQ-003, RQ-010, RQ-019〜RQ-025, RQ-032, RQ-033, RQ-042

---

## PAS-09 — 機密のTaskを、通常のVolunteer Workerに配置しない

### 状況

機密の文書を処理するTaskがある。近くに高性能な匿名のWorkerがいるが、そのWorkerは、機密性を保証する信頼の境界を持っていない。

### 受け入れ条件

- 性能が高くても、そのWorkerには平文を配置しない。
- managed boundaryや、検証されたconfidential runtimeなど、Taskのconfidentiality requirementを満たすWorkerだけを、候補にできる。
- 「暗号化して送信したから、実行中もWorker ownerから安全だ」と、誤認しない。
- Workerのcapabilityの自己申告だけで、高い保証の配置を成立させない。

Trace: RQ-024, RQ-025, RQ-047, SEC-011

---

## PAS-10 — 複数の組織の間の、限定的なFederation

### 状況

Company AとCompany Bが、それぞれのIdentity Authorityを維持している。そして、一部のExecutor、Data Class、Compute Grantだけを、相互に利用する。

### 受け入れ条件

- Federationによって、相手のTrust Domain全体に対する、包括的なauthorityは発生しない。
- Identity namespaceの衝突や混同を、防げる。
- AのClient Credentialが、BのWorkerやControl PlaneのRoleに、変換されることはない。

Trace: RQ-030, RQ-031, RQ-049, SEC-001, SEC-010, SEC-019

---

## PAS-11 — 公益を目的とした、Compute Grant

### 状況

大手の企業またはCloud Providerが、通常の顧客の需要に影響しない範囲の余剰のGPUを、公益団体に、期間を限って提供する。

### 受け入れ条件

- Providerは、Resource Class、上限、期間、priorityやpreemptibility、Data Classなどを制約できる。
- Beneficiaryを、Providerの内部networkに入れずに、利用できる。
- 通常の需要が増えた場合は、将来のcapacityをreclaimできる。
- BeneficiaryのTaskも、通常のClientと同じAdmissionとSecurityの境界を通る。

Trace: RQ-030〜RQ-035

---

## PAS-12 — Contributionを、外部のCredit Policyに接続する

### 状況

Providerが提供したcomputeについて、Contribution Recordを生成する。外部のコミュニティが、それを独自の相互利用のCreditに変換する。

### 受け入れ条件

- EKIは、観測され、検証されたContributionの情報とassuranceを、外部に渡せる。
- EKI自身は、1 compute unitの金銭の価値を決めない。
- EKI自身に、wallet、cash-out、exchange marketを、必須の実装として持たせない。
- 別の主体は、同じContributionを、寄付の実績など、別の意味に対応づけられる。

Trace: RQ-036〜RQ-038

---

## PAS-13 — Go SDKを使わない、未知の第三者のOSS

### 状況

EKIの開発時には存在しなかった他社のOSSが、Go以外の言語からTaskを投入して、結果を受け取りたい。

### 受け入れ条件

- Go SDKの内部の型をreverse engineeringしなくても、利用できる。
- アプリケーション固有のsemanticsを、EKI Coreに追加せずに、接続できる。
- 新しいExecutorが必要な場合でも、Taskをsubmitする権限と、codeをadmissionする権限とを、分離できる。

Trace: RQ-001, RQ-002, RQ-004, RQ-026

---

## PAS-14 — 非決定的なAIのResult

### 状況

同じmodelとinputでも、完全には一致しない可能性がある、AIによる候補生成のTaskを、複数のWorkerで実行する。

### 受け入れ条件

- bitwise equalityを、一般的なResult Verificationとして強制しない。
- アプリケーション固有のvalidator、provenance、複数の候補、低いassuranceなどを、明示できる。
- 「複数回、実行したから正解だ」という保証をしない。
- EKIが意味の上での正しさを確認できない場合は、その最終的な判断を、利用するアプリケーションまたは人間に残せる。

Trace: RQ-021, RQ-025, RQ-045

---

## PAS-15 — Executorのsupply-chainでのすり替え

### 状況

承認済みのExecutorと同じtagやnameを使って、攻撃者が、別のbinary、image、moduleにすり替えようとする。

### 受け入れ条件

- 人間が読める名前だけで、実行するArtifactを識別しない。
- Admissionのときに承認したArtifactと、Workerが取得するArtifactとの、完全性を照合できる。
- Taskをsubmitする権限だけでは、新しい実行コードを承認できない。

Trace: RQ-026, RQ-027, SEC-004

---

## PAS-16 — Contributionのfarming

### 状況

攻撃者が、自分で意味のないTaskを生成する。そして、そのTaskを自分のWorkerに割り当てて、大量のContributionを獲得しようとする。

### 受け入れ条件

- Workが実行されたという事実と、外部の制度がそのWorkに価値を認めることとを、分離できる。
- Contribution Recordに、Workのprovenance、Beneficiary、測定の方法、assuranceなどを、残せる。
- EKI Core自身が、「このContributionには○円の価値がある」と保証しない。

Trace: RQ-036〜RQ-038, SEC-013

---

## PAS-17 — Browser Workerの停止、suspension、明示的な参加

### 状況

利用者が、Webページの上で、公開の研究のための計算資源の提供を始める。その後、別のタブに移動し、ブラウザによるbackground throttlingが発生する。最後に、タブを閉じる。

別のWebサイトが、利用者に十分に説明せずに、同じようなWorkerを起動しようとする。

### 受け入れ条件

- Browserのbackground executionを、一定の時間を保証される資源として扱わない。
- throttlingやタブを閉じることによって、Taskが進まなくなっても、Fabric全体で回復できるTaskだけを、適切に割り当てられる。
- EKIに準拠したアドホックな参加では、資源を提供している間は、そのことを利用者が認識できる。
- 利用者が、ページを閉じることや、停止の操作をすることを、妨げない。
- hidden iframeなどによる、意図しない資源の提供を、標準の参加の方法として扱わない。

Trace: RQ-005, RQ-006, RQ-010, RQ-032, RQ-042, SEC-018

---

## PAS-18 — Workerが「入力を削除した」と偽る

### 状況

公開ではない入力をWorkerに渡した後、Workerが、Taskの完了時に「入力を完全に削除した」と報告する。しかし、実際には、コピーを保存している。

### 受け入れ条件

- Workerの削除の報告だけで、機密性が回復したとは扱わない。
- あとから削除することを保証できないWorkerには、削除の保証を前提とするDataを、最初から配置しない。
- retention policyを守ることへの要求と、技術的に検証できる削除の保証とを、区別できる。

Trace: RQ-024, RQ-043, SEC-017

---

## PAS-19 — ValidatorまたはVerifierが侵害される

### 状況

Workerが偽のResultを返している。さらに、そのTaskで使われるValidatorが侵害されていて、偽のResultを「正しい」と判定する。

### 受け入れ条件

- Accepted Resultが、どのValidator、Verifier、Policy authorityを根拠に確定したかを、追跡できる。
- Worker自身が任意のValidatorを指定するだけでは、保証が成立しない。
- そのValidatorを信頼の基盤に置くProfileでは、Validatorが侵害されたときの保証の限界を、明示できる。
- より高い保証が必要なTaskでは、別の独立した検証やProofなどを、組み合わせられる余地がある。

Trace: RQ-039, RQ-044, SEC-016

---

## PAS-20 — 副作用を持つTaskの、実行済みかどうかが不明になる

### 状況

外部のシステムに、一度だけ通知を送るTaskを、Workerが実行する。通知を送った直後に、Workerとの通信が切れる。Control Planeからは、通知が成功したかどうかを確認できない。

### 受け入れ条件

- 「Workerが消えたから、必ず再実行する」とは扱わない。
- Taskが副作用を持つことと、idempotencyなどの再実行の条件とを、明示できる。
- 実行済みかどうか不明な状態を表現して、人間または上位のアプリケーションのRecovery判断に渡せる余地がある。

Trace: RQ-011, RQ-012, RQ-046

---

## PAS-21 — 古いAttestation EvidenceやLeaseを、replayする

### 状況

あるWorkerは、過去には要求された状態を満たしていたが、現在は失効しているか、Policyに違反した状態にある。攻撃者が、以前に取得したEvidence、Lease、内部の権限をreplayして、Taskを再び受けようとする。

### 受け入れ条件

- 過去に正しかったEvidenceを、無期限に、現在の保証として使わない。
- expiry、revocation、freshnessが求められる情報について、古い情報を検出して拒否できるArchitectureを、選べる。
- replayを防ぐ方式そのものは、Requirementとして固定しない。

Trace: RQ-048, SEC-015

---

## PAS-22 — WorkerがSecurity capabilityを偽る

### 状況

匿名のWorkerが、高性能なGPUを持つと申告する。さらに、「Confidential Runtimeに対応している」「強い隔離がある」と自己申告する。性能の値の一部は、実際に正しいが、機密の実行環境は存在しない。

### 受け入れ条件

- 性能についての自己申告が正しいことを理由に、Security capabilityのClaimまで信頼しない。
- 高い保証のTaskの配置に使うcapabilityは、要求する水準に応じて、管理されたPolicy、Attestation、外部のEvidenceなどの根拠を持つ。
- 根拠が不明な場合は、安全な側に評価できる。

Trace: RQ-019, RQ-024, RQ-025, RQ-047

---

## PAS-23 — Federation namespaceとTrust Materialのすり替え

### 状況

Company AはCompany BとFederationしている。攻撃者が、Federationの設定を改変して、Company CのTrust Materialを、Company Bのnamespaceに対応づけようとする。

### 受け入れ条件

- Identityを検証するときに、Trust DomainやIdentity AuthorityとTrust Materialとのbindingを、保持する。
- 「登録済みのどれかの鍵で検証できた」ことだけで、BのIdentityとして扱わない。
- Federationの設定の変更を、通常のClientの権限からは行えない。
- BがCとFederationしていることを理由に、AからCへの包括的なTrustを、自動的に生成しない。

Trace: RQ-031, RQ-049, RQ-051, SEC-019

---

## PAS-24 — 圧縮したArtifactによる、展開爆弾とpath escape

### 状況

Clientが、数MBの圧縮したArtifactを投入する。展開すると数十GBになり、膨大な数のfileと、深い入れ子を含んでいる。さらに、一部のentryは、展開先の外を指すpathを持っている。

### 受け入れ条件

- 受信したbyte数だけを、Resource Budgetに使わない。
- 展開後のサイズ、fileの数、入れ子の深さ、処理時間などに、上限を持てる。
- Archive entryの名前だけで、Sandboxの外へ書き込めない。
- ParserやConverterが失敗しても、Control Plane全体が巻き込まれない境界を、選べる。
- Artifactの署名やDigestが正しいことを、安全にparseできる根拠だと、誤認しない。

Trace: RQ-009, RQ-017, RQ-027, RQ-029, RQ-050, SEC-020

---

## PAS-25 — Schedulerだけが侵害される

### 状況

Control PlaneのScheduler componentが侵害されて、攻撃者が、任意のWorkerへの配置を試みる。一方で、Artifact capabilityの発行、Executor admission、Federation administrationの、それぞれのauthorityは侵害されていない。

### 受け入れ条件

- Schedulerの侵害だけでは、任意のArtifact read capabilityやExecutor approvalを発行できない構成を、選べる。
- 機密のTaskを、Securityの要件を満たさないWorkerに配置しようとした場合に、別のEnforcement boundaryで拒否できる余地を持つ。
- どのComponentやauthorityが、Placementやcapabilityの発行を行ったかを、追跡できる。
- 「Control Plane内部だから、すべての権限を持つ」という、単一のCredentialへの依存を、必須としない。

Trace: RQ-039〜RQ-041, RQ-044, RQ-051, SEC-021

---

## PAS-26 — 大規模なCompute Grantの、一斉のreclaim

### 状況

大手のProviderが、多数のGPUを、公益のCompute Grantとして提供している。通常の需要が急に増える。そして、数千のTaskを実行しているWorkerの群から、短い時間でcapacityをreclaimする。

### 受け入れ条件

- Providerは、Taskの完了を待たずに、許可された条件でcapacityを取り戻せる。
- 大量のTaskが離脱することを、Fabricで起こり得る通常のEventとして扱う。
- 未完了のTaskを一斉にretryして、残ったWorker、Artifact Store、Network、Control Planeに、無制限の負荷を掛けない。
- Taskのpriority、deadline、retryabilityなどに応じて、再配置を調整できる余地を持つ。

Trace: RQ-010, RQ-013, RQ-017, RQ-035, RQ-052, SEC-023

---

## PAS-27 — Endpointの温度の上昇と、電力Policyの変更

### 状況

業務用のGPUワークステーションが、夜間のTaskを処理している。GPUの温度が上昇して、同時に、端末がBatteryでの駆動に切り替わる。中央のSchedulerは、まだ高いcapacityを利用できると見ている。

### 受け入れ条件

- Local Governorだけで、capacityを縮小したり、0にしたりできる。
- 中央のScheduler、Taskのpriority、Contributionの獲得を理由に、ローカルの安全の上限を上書きしない。
- Energy budget、電源の種類、thermal stateなどを、Resource Policyに反映できる。
- Task側は、Workerの離脱として、安全に扱える。

Trace: RQ-005〜RQ-007, RQ-010, RQ-053, SEC-022

## 次の圧力試験

第三巡目で、前回残していた次の項目を、Scenarioにしました。Federation namespace、Artifactの展開、Control Planeの部分的な侵害、mass reclaim、Energy / thermalの境界。

Architecture Entry Gateに向けて残る主な論点は、Scenarioの数を増やすことよりも、次の横断的なテーマを、比べられるQuality Attributeに落とすことです。

- Worker independenceとSybilへの耐性の、保証の水準
- Browser、Native、管理されたEndpointごとの、Resource Enforcementの精度
- Artifact processingの最大の影響と、隔離の境界
- Control Plane authority separationの最小構成
- FederationのTrust bindingとrevocation
- Result Verificationのコストと、Security assuranceの釣り合い
- Contribution measurementの精度とPrivacy
- mass reclaimのときの回復性と、Networkのoverhead

---

## 候補Scenario（評価待ち）— 製品ファミリーから来るworkload

この節のScenarioは、評価待ちの候補です。PAS番号を付けず、上の27個のScenarioにも数えません。

目的は、同じ作者の関連製品群（以下、製品ファミリー）が実際に出し得るworkloadを、既存のRequirementだけで扱えるかを確かめることです。ここから、新しいRequirementは作りません。検討の中で本質的な不足が見つかった場合は、この文書の原則どおり、Product RequirementsかThreat Modelに戻します。候補を正式なScenarioにする場合は、レビューを経てPAS番号を付けます。

候補は、利用する側の意味をEKIに持ち込まずに、workload contractの性質だけで書きます。具体的には、再実行してよいか、期限、データの所在、信頼と隔離の条件、結果の検証の方法です。各製品にとっての意味は、状況の説明にとどめます。

候補は2種類あります。

- **PAS-CAND-01〜04**: SEI Cognitionがバックグラウンドで行う、認知処理です。SEI側でも、まだ評価の段階にある設計の仮説に位置づけられています（`hat47x/sei-cognition:evaluation/sei-cognition-orchestration/EMERGENT_COGNITIVE_ARCHITECTURE.md` §10〜§11）。SEIのCognitive Envelopeからの投影は、[SEI Cognitionとの対応ノート](../research/sei-cognition-mapping.md)を参照してください。
- **PAS-CAND-05〜06**: SEIの認知処理ではない、兄弟の製品のworkloadです。EKIが、初期の利用者である認知系の製品だけに合わせ込まれていないかを、確かめます（Product Valueの「Interoperability before ecosystem lock-in」）。

どちらも、M0とMVPが成り立つための条件ではありません。`plan/UPSTREAM_PLAN.md`の優先順位も変えません。

### PAS-CAND-01 — 仮説の分岐探索（評価待ち）

#### 状況

SEI Cognitionが、ある仮説について、役割の異なる探索を12本、同時に依頼する。たとえば、支持する証拠の探索、反例の探索、別の因果モデルの組み立てなどである。どの探索も、同じ入力の記録を読む。SEIは、結果を多数決で一つにまとめず、食い違いや少数の見方も残したいと考えている。

#### workload contractとしての性質

- 探索ごとに独立したTaskとする。役割の違いは、登録済みのExecutorのparameterとしてだけ渡す。
- 再実行してよく（retryable）、途中で止めてよい（preemptible）。優先度はbackgroundで、期限は緩い。
- データの所在: 12本が同じ入力を読むため、入力の転送量とcacheの位置が、配置に効く。
- 信頼と隔離: 入力の記録のData Classに従う。
- 結果の検証: 出力の形式の確認まではできるが、内容の正しさは確認できない。

#### 受け入れ条件

- 12本への展開と、そのretryが、依頼した主体に許可されたbudgetを超えない。
- 結果を、一致や多数決で一つにまとめず、各Resultを、使ったExecutor、モデル、入力の来歴とともに、個別に返せる。
- 一部の探索が期限内に終わらなくても、終わったTaskの結果は受け取れる。終わらなかったTaskを、成功とも、結果が空だったとも、取り違えない。
- 同じ入力を12回転送するより、入力を持つWorkerや近くのWorkerに、まとめて配置する判断ができる。
- EKIは、探索の役割の意味を知らない。役割から、Resourceの要求や優先度を推し量らない。

Trace: RQ-001, RQ-008, RQ-011, RQ-017, RQ-021, RQ-039, RQ-045

### PAS-CAND-02 — 反例探索（評価待ち）

#### 状況

SEI Cognitionが、ある前提が成り立たない例を、組合せの探索空間から探す。空間を1000の区画に分けて、多数の業務端末やアドホックな参加者のWorkerに配る。見つかった反例は、安く確かめられる。一方で、「見つからなかった」という結果は、確かめにくい。

#### workload contractとしての性質

- 区画ごとの小さなTaskで、再実行してよい。探索が決定的であれば、同じ入力から同じ結果が出る（deterministic）。
- データの所在: 探索空間の定義は小さく、転送量は少ない。
- 信頼と隔離: 入力が公開または低機密であれば、アドホックな参加者も候補にできる。
- 結果の検証: 「反例あり」は、Validatorで確かめられる。「反例なし」は、Workerの申告だけでは確かめられない。

#### 受け入れ条件

- 「反例あり」のResultは、Taskが指定したValidatorで反例を確かめられた場合に、それを根拠にAccepted Resultにできる。
- 「反例なし」のResultを、Workerの申告だけで、区画全体を調べた証拠として扱わない。悪意のあるWorkerは、反例を隠して「なし」と返せる。
- 「なし」に必要な保証（独立した複製の実行、管理下のWorkerでの再実行など）を、Taskの検証の条件として指定できる。一人の攻撃者が多数のWorkerを作れる環境では、その保証の上限を明示する。
- 期限までに調べ切れなかった区画を、「反例なし」と区別して返せる。
- EKIは、前提の意味を知らず、探索空間を、入力のArtifactとparameterとしてだけ扱う。「なし」をSEI側で未確認として扱うかどうかは、SEIが判断する。

Trace: RQ-019, RQ-021, RQ-022, RQ-032, RQ-044, RQ-045

### PAS-CAND-03 — 夜間の統合（評価待ち）

#### 状況

SEI Cognitionが、社内サーバに蓄積した判断の記録を、夜間に読み直して、繰り返し現れるパターンや、古くなった前提の候補を抜き出す。処理には数時間かかり、30台の社員PCの余力を使う。元の記録は、書き換えてはならない。始業の時刻には、多くの社員が、ほぼ同時に端末に戻ってくる。

#### workload contractとしての性質

- 再実行してよく、途中で止めてよい。区切りごとに続きから再開できる（checkpointable）ことが望ましい。
- 期限は翌朝の始業前で、優先度はbackground。
- データの所在: 記録は社内サーバにあり、量が多い。転送が業務networkを圧迫する時間帯は、避ける必要がある。
- 信頼と隔離: 記録は社内限定であり、Data Classを満たす管理下の端末だけが、候補になる。
- 結果の検証: 出力の形式の確認と、入力の記録のdigestとの対応づけまで。

#### 受け入れ条件

- 始業のときに、多くの端末が一斉に提供をやめても、retryや別のWorkerへの移し替えが、一度に集中しない。checkpointできるTaskは、続きから再開できる余地を持つ。
- 期限に間に合わない場合は、終わった分の成果物と、未完了の部分とを区別して返す。利用者が戻った端末で、処理を続けない。
- 入力の記録は、内容を固定した読み取り専用のArtifactとして渡す。Taskを実行できる権限だけで、元の記録を書き換えられない。成果は、新しいArtifactとして返る。
- 社内限定の記録を、Data Classを満たさない端末に配置しない。転送量が大きい場合は、記録のある場所の近くで処理するか、分散しないことを選べる。
- EKIは、パターンや前提の意味を知らない。成果をSEI側で提案として扱うか、どこまで採り入れるかは、SEIが判断する。

Trace: RQ-005, RQ-007, RQ-008, RQ-010, RQ-011, RQ-014, RQ-024, RQ-027, RQ-028, RQ-052

### PAS-CAND-04 — 複数モデルの相互検証（評価待ち）

#### 状況

SEI Cognitionが、同じ入力について、3種類の言語モデルに、互いの出力を批評させる。2つはローカルで動くモデルで、1つは外部のモデルAPIである。SEIは、どのモデルが何を出したかを、あとから正確に辿りたいと考えている。

#### workload contractとしての性質

- 再実行してよいが、生成結果が、実行のたびに一致するとは限らない（非決定的）。
- データの所在: モデルの重みは数GB〜数十GBあり、すでに持っている端末があれば、その近くで動かす方が有利。
- 信頼と隔離: 外部のAPIを使う批評は、入力をEKIの管理の外に送ることになる。
- 結果の検証: 意味の上での正しさは確認できない。確認できるのは、どのExecutorとモデルのArtifactで実行したか、という来歴まで。

#### 受け入れ条件

- 各批評を実行したExecutorとモデルのArtifactを、digestで区別して、Resultの来歴として残せる。表示名やtagだけで、モデルを識別しない。
- EKIが保証できるのは、Artifactの違いまでである。互いに独立した系譜のモデルかどうかの判断は、利用する側に残す。同じ重みを別の名前で登録したものを、EKIが異なるモデルとして保証しない。
- モデルどうしの一致を、意味の上での正しさの根拠にしない。
- モデルの重みを持つWorkerの位置を、配置の判断に反映できる。
- 外部のAPIを使う批評は、任意のURLではなく、登録されたnetwork profileまたはexternal adapterを経る。入力のData Classが外部への送信を許さなければ、その批評は実行しない。

Trace: RQ-008, RQ-021, RQ-024, RQ-026, RQ-027, RQ-029, RQ-039, RQ-045

### PAS-CAND-05 — vs-bearの蒸留学習と、一括の挙動の試行（評価待ち）

#### 状況

vs-bear-simulator（人とクマの身体の差を体験するゲーム）の開発で、次の2つを行う。

- ローカルの教師モデルを、固定したcheckpointと質問の形式で一括して評価する。その出力から、CPU上で動く小さな決定木のモデルを学習する（蒸留）。
- 1000件規模の固定したfixtureと多数のseedを使い、クマの挙動を画面なし（headless）で一括して試行する。

（`hat47x/vs-bear-simulator:docs/BEAR_CPU_BODY_AND_DISTILLATION.md` §5〜§7、`docs/M5_VALIDATION_HARNESS.md`）

#### workload contractとしての性質

- 試行は、seedごとに独立したTaskにでき、再実行してよい。学習は、thread数とseedを固定した、決定的な設定で行う。
- 専用のGPUがなくても、CPUで完走できることが前提である。GPUは、教師の一括の推論を速める選択肢にとどまる。
- データの所在: 学習用のdatasetと教師のcheckpointは、内容のhashで固定される。成果物には、hashと、元のGit SHAが記録される。
- 信頼と隔離: 教師のcheckpointや、元の動作データには、再配布の条件が付くことがある。
- 結果の検証: 同じ実行環境での再実行と、数値の照合。ただし、OSやライブラリのバージョンが違うと、bit単位の一致は保証されない。

#### 受け入れ条件

- 専用のGPUを持たないWorkerだけでも、完走できる。GPUは配置の希望として扱い、必須の条件と取り違えない。
- 実行環境（Runtime Class）、thread数、seedを固定したTaskとして、表現できる。実行環境の違いによって、bit単位で一致しないことを、偽のResultと誤って判定しない。許容誤差での比較や、同じRuntime Classでの再実行など、Task側が選んだ検証の方法を使える。
- 再配布の条件があるArtifactを、その条件を満たさないWorker（アドホックな参加者など）に配らない。これを、現在のData ClassとData authorityの枠組みで表現できるかを確かめる。できなければ、Requirement側の論点として戻す。
- EKIは、クマの挙動の意味も、学習したモデルを製品に採用するかどうかも知らない。採否は、vs-bear側が判断する。

Trace: RQ-001, RQ-003, RQ-011, RQ-021, RQ-026, RQ-027, RQ-028, RQ-039

### PAS-CAND-06 — MMIのCPUによる範囲探索の評価（評価待ち）

#### 状況

markdown-matrix-injector（Markdownの文書から、Excelの表の範囲を読み取り専用で参照する記法と、VS Codeの拡張）で、表の領域を探す複数の検索方式を、比べて評価する。固定したseedで合成した、業務を模したデータ（最大で約10万領域）を使う。凍結した、未見の評価セットを一度だけ評価し、遅延（p50 / p95）とメモリを測る。GPUとnetworkは使わない（`hat47x/markdown-matrix-injector:02_Architecture/input-assistance-cpu-search.md` §5、§7。同じ文書の状態はProposed）。

#### workload contractとしての性質

- 合成データの生成と評価は、固定したseedによる、決定的な純粋な計算であり、再実行してよい。
- Resource: 約10万領域の規模では、ピーク時のメモリが約4.4GBに達するため、メモリの小さいWorkerには置けない。
- データの所在: 合成データは作り直せるため、転送するよりも、Workerの側で生成した方が安い場合がある。
- 信頼と隔離: 合成データは低機密である。一方、利用者の実際のブックを対象とする探索は、社内の機密を含み得る。
- 結果の検証: 候補の再現率は、同じ入力から計算し直して確かめられる。遅延やメモリの測定値は、測ったWorkerの申告である。

#### 受け入れ条件

- 合成データの生成と評価を、再実行してよい純粋な計算のTaskとして、表現できる。必要なメモリの量を、Resourceの要求として宣言できる。それを満たさないWorkerには、配置しない。
- 遅延やメモリの測定値、CPUの型番などの測定の条件を、Workerの申告のまま、検証済みの事実として扱わない。測定値どうしを比べる必要がある場合は、管理下の特定のRuntime Classに、配置を限定できる。
- 業務端末の余力で測った値は、利用者の作業や他のTaskの影響を受ける。そうした値を、性能の基準値として扱わない。
- 利用者の実際のブックを対象とする探索では、分散した方が不利な場合や、機密性を満たせない場合に、ローカルでの実行を選べる。EKIに出さないことも、正当な選択である。
- EKIは、表の範囲や記法の意味を知らない。

Trace: RQ-001, RQ-003, RQ-009, RQ-011, RQ-014, RQ-019, RQ-024, RQ-047

---

## 候補Scenario（評価待ち）— AI Agentの普及と、管理クラスタとの統合

この節のScenarioも、評価待ちの候補です。PAS番号を付けず、PAS-01〜PAS-27にも数えません。

前の節が「製品ファミリーから来るworkload」を扱うのに対して、この節は、2つの状況の変化から来る圧力を扱います。

- **PAS-CAND-07〜11**: 生成AIとAI Agentが普及して、Clientが機械になり、生成物の実行と確認が増える状況です。背景は、[AI時代の価値と位置づけ](../research/ai-era-value-positioning.md)にあります。
- **PAS-CAND-12〜16**: KubernetesやSlurmのような既存のオーケストレータが管理するクラスタを、資源の供給元として、EKIに接続する状況です（[Product Value](../product/value.md)の「Managed and opportunistic resources on one fabric」）。背景は、[既存オーケストレータとの接続、およびSEI・TEIとの層構造](../research/orchestrator-integration-and-layering.md)にあります。

評価の結果は、[PAS-CAND-07〜16の評価](../plan/PAS_CAND_07_16_EVALUATION_2026-10-06.md)にあります。10件のうち、5件は既存の要求で扱えます。3件は、既存の要求の文言を補います。1件には、新しいRequirementの草案があります。1件は、設計の判断として残しました。

ここから、新しいRequirementは作りません。既存のRequirementで扱えない点は、「Requirement側へ戻す論点」として記します。レビューを経て、Product RequirementsかThreat Modelに反映します。どれも、M0とMVPが成り立つための条件ではありません。`plan/UPSTREAM_PLAN.md`の優先順位も変えません。

### PAS-CAND-07 — AI Agentの連鎖による、依頼の増殖（評価待ち）

#### 状況

認証済みのAI Agentが、文書の要約を依頼する。その結果を読んだ別のAgentが、10件の追加のTaskを出し、それぞれがさらに10件を出す。どのAgentにも悪意はなく、すべて正規の資格情報を使っている。しかし、数分で数千件のTaskになる。

#### 受け入れ条件

- 依頼の数と、資源の消費が、Clientに与えられた予算の範囲を超えない。Agentが別のAgentに依頼を委ねても、委ねられた側の予算と権限は、委ねた側を超えない。
- 増殖を止めたときに、すでに実行中のTaskを一斉に失敗させて、再試行の嵐を起こさない。
- 「認証済みだから」「悪意がないから」を理由に、上限を緩めない。

#### Requirement側へ戻す論点

- 委譲の連鎖で、予算と権限が必ず縮小することを、RQ-016とRQ-017の具体化として扱えるか。それとも、独立したRequirementが要るか。

Trace: RQ-015, RQ-016, RQ-017, RQ-052

### PAS-CAND-08 — 相関した確認の一致（評価待ち）

#### 状況

非決定的なAI生成のTaskについて、同じモデル、同じ重み、同じ量子化で、3台に実行させた。さらに、同じ系統のモデルで判定させたところ、すべてが一致した。しかし、実際には、同じ誤りを繰り返しただけだった。悪意のあるWorkerも、Sybilも、関わっていない。

#### 受け入れ条件

- 複数の実行や判定が一致したことを、それだけで高い保証として扱わない。
- 何が独立していたか（Executor、モデルのArtifactのdigest、資源提供者の運営者、検証者）を、Resultの来歴として記録できる。
- モデルの系譜が独立しているかどうかの判断は、利用する側に残す（PAS-CAND-04と同じ）。EKIが担うのは、判断に使える記録を残すところまでである。

#### Requirement側へ戻す論点

- 「悪意のない相関による、誤った一致」を、Sybilやcollusion（RQ-022）と区別して、Assurance Modelに書く必要があるか。

Trace: RQ-021, RQ-022, RQ-039, RQ-044, RQ-045

### PAS-CAND-09 — 端末上のローカルAIとの、資源の競合（評価待ち）

#### 状況

利用者は、キーボードもマウスも操作していない。しかし、本人のAI Assistantが、端末のNPUとメモリを使って、長い処理を続けている。EKIのWorkerは、人の操作がないことを理由に、背景の計算を始めようとする。

#### 受け入れ条件

- 人の操作がないことだけを根拠に、端末が空いているとみなさない。
- 本人のために動いている処理（ローカルAIを含む）を、EKIの背景の計算より優先する。
- Local Governorが、本人の処理の資源の利用を把握できない場合は、安全な側（背景の計算を控える側）に判断する。

#### Requirement側へ戻す論点

- RQ-005〜007の「利用者の本来の作業」に、利用者のために自律的に動く処理が含まれることを、明記する必要があるか。

Trace: RQ-005, RQ-006, RQ-007, RQ-053

### PAS-CAND-10 — AIが生成したコードの実行（評価待ち）

#### 状況

AI Agentが、変換のスクリプトを生成する。そして、それを大量のfileに適用するTaskとして、EKIに依頼する。スクリプトの内容は、誰も確認しておらず、その出力は、次のTaskの入力になる。

#### 受け入れ条件

- 生成されたコードを、作者の分からない外部の入力と、同じ扱いにする。EKIは、安全性を判定しない。しかし、判定できなくても、被害を閉じ込められる。
- 実行体のdigestを来歴として残し、どの生成物がどの出力を作ったかを、辿れる。
- 生成されたコードからの、任意のnetworkへのアクセスや、Taskの権限を超えるDataの参照を、許さない。
- 出力が次のTaskに渡るときも、Artifactとして、Resourceの上限と展開の安全性を満たす。

Trace: RQ-024, RQ-026, RQ-027, RQ-029, RQ-039, RQ-050

### PAS-CAND-11 — 配置の証跡を、第三者に示す（評価待ち）

#### 状況

取引先または監査人から、ある顧客のデータが、どの組織の、どの種類の資源で、どの実行体によって処理され、どの根拠で結果が受理されたかの説明を、求められる。ただし、他の顧客のTask、Workerの所在、社内の構成は、開示できない。

#### 受け入れ条件

- 特定の入力について、処理した資源の種類と所属する組織、実行体のdigest、Resultを受理した根拠を、示せる。
- 説明に不要な、Workerの所在、他のTask、社内の構成を、開示しない。
- EKIの記録は、法令や契約への適合を保証するものではない。説明の材料を提供するところまでを担う。

Trace: RQ-018, RQ-028, RQ-039, RQ-040, RQ-044

### PAS-CAND-12 — 下位のオーケストレータの、見えない再試行（評価待ち）

#### 状況

外部のシステムへの通知を含むTaskを、Kubernetesが管理するクラスタに配置した。実行中にノードの障害が起き、Kubernetes Jobが、既定の設定で自動的にPodを作り直した。EKIから見ると、一つのAttemptの中で、通知が2回送られた可能性がある。

#### 受け入れ条件

- 副作用を持つTask、またはidempotencyの根拠がないTaskは、下位のオーケストレータの自動の再試行を無効にした状態でしか、配置しない。無効にできない資源提供者には、置かない。
- 下位が内部で再試行したことを観測できる場合は、それを記録する。観測できない場合は、実行の回数を「一度だけ」と主張しない。
- 実行済みかどうか分からない場合は、UNKNOWNとして保持する。

Trace: RQ-012, RQ-046

### PAS-CAND-13 — 下位のオーケストレータによる、一斉の横取り（評価待ち）

#### 状況

管理クラスタで、優先度の高い学習ジョブが始まった。クラスタのキュー管理（Kueueなど）が、そのクラスタで動いていた、EKI経由の作業数百件を、一斉に止めた。

#### 受け入れ条件

- 下位による横取り（preemption）を、業務端末で利用者が戻ったときの停止と、同じ種類の出来事（reclaim）として扱う。失敗として丸めない。
- 止められた作業を、一斉に別の資源提供者に再配置して、再試行の嵐を起こさない。
- 副作用の有無が分からない作業は、UNKNOWNとして保持する。

Trace: RQ-010, RQ-013, RQ-035, RQ-052

### PAS-CAND-14 — 資源提供者に接続する資格情報の漏えい（評価待ち）

#### 状況

あるKubernetesクラスタにジョブを投入するために、EKIが持つ資格情報が漏れた。攻撃者は、その資格情報でクラスタに直接ジョブを投入し、さらに、EKIの他の権限を得ようとする。

#### 受け入れ条件

- 資格情報は、資源提供者ごと、名前空間やpartitionごとに限定されていて、クラスタ全体の管理権限を持たない。
- 一つの接続先の資格情報が漏れても、他の資源提供者、Lease、Resultの受理、Policyの管理の権限には、波及しない。
- 漏えいを検知したときに、その資源提供者への接続だけを止められる。

Trace: RQ-041, RQ-048, RQ-051

### PAS-CAND-15 — 下位のエラーからの、内部構成の漏えい（評価待ち）

#### 状況

Slurmのジョブが失敗し、エラーメッセージに、ノード名、partition名、共有ファイルシステムのpathが含まれていた。Clientは、失敗の理由を知るために、Taskの状態を問い合わせる。

#### 受け入れ条件

- 下位のエラー情報を、Clientに返す情報と、監査のための情報とに、分ける。
- Clientの権限では、ノード名、IPアドレス、名前空間、社内のpathを、列挙できない。
- Clientは、失敗の種類（資源の不足、時間切れ、実行体の異常終了など）を、内部の構成を知らずに判断できる。

Trace: RQ-018, RQ-040

### PAS-CAND-16 — 統治基盤を名乗るClientからの、方針の注入（評価待ち）

#### 状況

企業の統治の基盤として使われている製品が、EKIのClientとして依頼を出す。依頼に、「この判断は経営上重要」「承認済み」という情報を添えて、予算の上限の引き上げや、Assuranceの低い資源への機密データの配置を、求める。

#### 受け入れ条件

- 依頼に添えられた情報によって、予算、配置の条件、Dataの権限を、広げない。
- 統治の方針をEKIで有効にするのは、組織の管理者権限（Administrative Plane）の経路に限る。
- 統治基盤の製品が、方針の作成や審査を支援すること自体は、妨げない。

#### Requirement側へ戻す論点

- 一つの製品のコンポーネントが、Client権限と管理者権限の両方を持つ構成を、許すか。許す場合、両者をどう分けて記録するか。

Trace: RQ-015, RQ-016, RQ-028, RQ-051
