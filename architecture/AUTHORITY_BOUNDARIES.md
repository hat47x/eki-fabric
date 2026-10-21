# 権限と信頼の境界（Authority / Trust Boundary Map）

Status: **Baseline 0.2 — 2026-10-20**（Baseline 0.1: 2026-09-21。0.2での変更点と移行は[ADR-0014](../plan/adr/0014-authorities-for-delegation-budget-capacity.md)にあります）

## 1. この文書の目的

EKI Fabricでは、Client、Worker、Control Planeのどれも、同じシステムの一部だからという理由だけで信頼しません。

Control Plane内部を一つの大きな信頼領域にまとめると、SchedulerやParserが侵害された際に、Artifactの読み出し、Executorの承認、Federationの設定変更、WorkOrderの発行まで権限を奪われる危険があります。

そのため、この文書では、プロセスの数や採用製品を決める前に、次の役割を定義します。

- 誰が事実を検証するか
- 誰がSecurity上の判断を下すか
- 誰がCapabilityや実行権限を発行できるか
- その判断を、誰が再検証するか
- 一つのComponentが侵害されたとき、影響がどこまで届くか

この文書は、そのための、論理的なAuthorityとTrust BoundaryのBaselineです。

関連する正本は、次のとおりです。

- ../requirements/PRODUCT_REQUIREMENTS.md
- ../requirements/THREAT_MODEL.md
- ../requirements/ASSURANCE_MODEL.md
- ../requirements/QUALITY_ATTRIBUTES.md
- ../plan/reviews/ARCHITECTURE_ENTRY_REVIEW_2026-09-21.md

## 2. AuthorityはServiceの数ではない

この文書でいうAuthorityとは、論理的な役割のことです。Security上、意味のある事実を確定したり、権限を発行したり、状態を変更したりできます。

一つのAuthorityを、必ず一つのMicroserviceにするという意味ではありません。

小規模な構成では、次のものを一つのBinaryに同居させてもかまいません。

~~~text
eki-control
  Admission
  Task State
  Scheduler
  Placement Guard
  Lease
  WorkOrder Assembly
~~~

ただし、同居していても、次のものは分けます。

- Authority identity
- signing keyやkey handle
- Policyの入力
- 永続的な状態への書き込みの権限
- 呼び出せるInterface
- Audit event
- 障害や侵害の影響の範囲

Production Profileでは、意味の異なるAuthorityが、同じ署名Credentialを共有することを標準にはしません。

同じProcessに同居させる場合も、Scheduler moduleがExecutor Admissionのkeyや、Artifact Capabilityのkeyを直接使える設計にはしません。

## 3. 基本原則

### 3.1 Authenticationは、Authorityを増やさない

認証されたClientも、悪意を持ち得ます。

認証が確認するのは、

> このrequestが、どのPrincipalから来たか

ということだけです。

それだけで、次のことを許可しません。

- 任意のExecutorの実行
- 任意のArtifactの読み出し
- 具体的なWorkerの指定
- Federationの設定の変更
- Resource Budgetを超えること

### 3.2 Schedulerは、Policy Authorityではない

Schedulerは、性能、locality、deadlineなどを使って、候補の順位を付けます。

Schedulerが返すのは、Placement Proposalです。

Proposalは、

> このWorkerがよさそうだ

という提案です。

> このWorkerに置いてよい

というSecurity Authorizationではありません。

将来、Issue #19のlearned estimatorを利用する場合も、同じです。

~~~text
model score
  != permission
predicted reliability
  != Worker Assurance
~~~

### 3.3 WorkOrderの署名だけを、万能なTrust Tokenにしない

WorkOrder Authorityは、最終的なEnvelopeに署名します。しかし、自分だけでは、Taskのsemantics、Data authority、Executor approval、Placement authorizationを作りません。

Workerは、WorkOrderの署名だけでなく、WorkOrderが参照している前提のDecisionについて、次の項目を再検証します。issuer、subject、digest、policy revision、expiry。

そのため、WorkOrderのsigning keyだけが侵害されても、他のAuthorityの権限を自動的に得てしまう構造は避けます。

### 3.4 Authorityの結果は、対象に結び付ける

Security上、意味のあるDecisionは、少なくとも必要な範囲で、次のものに結び付けます。

- Principal / Trust Domain
- Task ID
- Attempt ID
- Worker ID
- Executor ID / Digest
- Artifact Digest
- Input / Output Digest
- Policy Revision
- Issued At / Expiry
- Decision / Nonce / Lease ID

別のTaskや別のWorker向けの、正しい署名を流用できないようにします。

### 3.5 Trust Rootを、呼び出し側に選ばせない

検証に使う公開鍵やTrust Rootを、通常のrequestのparameterとして渡し、

> 今回は、この鍵を信じてください

と差し替えられるAPIは、作りません。

AuthorityのTrust Rootは、別の管理の経路で、設定し、rotationします。

### 3.6 Local Governorは、中央のAuthorityに対する拒否権を持つ

Worker側のLocal Governorは、Central Schedulerがcapacityを使いたいと判断しても、より厳しい上限を適用できます。

~~~text
effective capacity
  = min(
      organization permitted capacity,
      provider/user willingness,
      local safety judgement
    )
~~~

中央側には、この拒否権を無効にする、通常の実行権限を持たせません。

### 3.7 権限は、実行の権限も管理の権限も有限にする

（Baseline 0.2で追加。[ADR-0014](../plan/adr/0014-authorities-for-delegation-budget-capacity.md)）

実行の権限（Lease）も、管理の権限（ADM-1の委任状）も、期限、範囲、予算を持ちます。期限を持たない権限は発行しません。

常駐する処理は、期限つきで更新されるLeaseの連なりとして扱います（[ADR-0013](../plan/adr/0013-product-core-and-faces.md)）。SEIなどへ管理者権限の一部を委ねる場合も、期限つきの委任状として扱います。更新が止まれば、権限は失効します。誤りや侵害が起きた場合の影響を、時間の上でも閉じ込めるためです。

### 3.8 信頼の水準は、登録から読む

（Baseline 0.2で追加）

委任先（資源のプール、資源提供者、Workerの区分、外部の提供枠）の信頼の水準は、DLG-1が管理者の操作として登録したものを読みます。委任先の自己申告、評判、Schedulerの推定から読みません（RQ-023、RQ-047）。

信頼の水準が高い委任先には、確定に要る検証を軽くできます。ただし、それは管理者がそう定めた場合に限ります。

---

# 4. Authorityの分類

EKIのAuthorityを、性質の違いから、次の4つのグループに分けます。

## 4.1 Administrative Authorities

頻繁に行われるTaskの実行の経路から分離する、価値の高い設定のAuthorityです。

- Trust / Policy Administration（委任状を含む）
- Federation Administration
- Executor Admission Administration
- Delegate / Trust Profile Registry（Baseline 0.2）
- Budget Authority（Baseline 0.2。総額と配分）

## 4.2 Runtime Decision Authorities

通常のTask lifecycleで、Security上の判断やCapabilityを発行します。

- Task Admission Authority
- Task State / Recovery Authority
- Placement Authorization Authority
- Lease Authority
- Artifact / Data Capability Authority
- WorkOrder Authority
- Elastic Capacity Authority（Baseline 0.2）

## 4.3 Verification / Acceptance Authorities

Workerや外部の主体が出したClaimを、そのまま事実に昇格させないためのAuthorityです。

- Attestation Verifier
- Result Verification Authority
- Result Acceptance Authority
- External Effect Confirmation Authority
- Contribution Authority

## 4.4 Worker-local Enforcement

中央のDecisionを受け取った後でも、実行の直前と実行中に、境界を守ります。

- WorkOrder Verifier
- Local Governor
- Sandbox / Capability Enforcer

Scheduler、Learned Estimator、Telemetry Collector、Cache locality estimatorなどは、重要なComponentです。しかし、それ自体にはSecurity Authorityを与えないことを、標準とします。

---

# 5. Authorityの一覧

## ADM-1 — Trust / Policy Administration

### 役割

EKI内部のAuthorityが信頼する、issuer、key、policy revisionなどを管理する、Rootです。

たとえば、次のものがあります。

- Task Admission Authorityのtrust root
- WorkOrder Authorityのtrust root
- Verification Authorityのtrust root
- Organization policyのversion
- keyのrotationとrevocation

### 持てる権限

- Authorityのtrust configurationの更新
- keyのrotation
- policyのactivationとrevocation
- 過去の内容へ戻す必要がある場合、新しいrevision identityとして再発行するsemantic rollback
- 緊急のrevocation

### 持たせない権限

- ClientとしてTaskをsubmitすること
- Workerを直接選ぶこと
- Artifactを直接読むこと
- ResultをAcceptedにすること
- Contributionの量を生成すること

### 注意

このAuthorityが侵害されると、影響が大きくなります。そのため、通常のTask executionの経路から分離します。

将来、価値の高い変更にmulti-party approvalを導入する余地は残します。ただし、Baseline 0.1では、方式を固定しません。

### 委任状（Delegated Administrative Mandate）

（Baseline 0.2で追加）

人間の管理者は、ADM-1の権限の一部と、DLG-1、BUD-1の操作の一部を、委任状として、範囲と期限を限って委任できます。たとえば、SEIに組織レベルの意思決定（部門と案件への予算の配分、利用の許可の調整、外部への提供の方針）を委ねる場合です（[ADR-0013](../plan/adr/0013-product-core-and-faces.md)の決定10）。

委任状が持つもの:

- 委任状を持つ主体の識別。その主体がClientとしても振る舞う場合、Clientとしての識別とは分ける
- 委任できる操作の一覧
- 越えさせない線。少なくとも、Data Classの下限、Local Governorの拒否権、予算の総額、Federationの関係、Trust Rootには届かない
- 人間の承認を求める閾値
- 期限
- 発行した人間の管理者の署名

委任状を持つ主体に持たせない権限:

- 委任状の自己更新
- 越えさせない線と閾値の変更
- 自分が持つ委任状より広い委任状の発行
- 予算の総額の変更

委任状の期限が切れたら、その委任状のもとで出された方針のうち期限のあるものは、期限どおりに失効します。そのあとは、人間の管理者が定めた既定の方針へ、安全側に戻ります。期限の切れた方針で、新しい予算の配分や容量の確保を行いません。

#### 委任できる操作の区分

（2026-10-21追記。[ADR-0022](../plan/adr/0022-mandate-operation-classes-and-thresholds.md)）

管理の操作を、委任したときの危険の大きさで四つに分けます。

| 区分 | 性質 | 委任状での扱い | 例 |
| --- | --- | --- | --- |
| 安全側の操作 | 権限や資源を狭める、止める | 広く委任できる | 利用の許可の取り消し、委任先を一時的に候補から外す、外部への提供の縮小、常駐の失効、予算の枠を減らす |
| 範囲内の拡大 | 人間が定めた枠の中で、配分を変える、許可を与える | 上限つきで委任できる | 予算の配分し直し、案件の予算の枠を作る、登録済みの委任先の利用の許可、等級の優先度の重み、外部への提供量の増加（上限内） |
| 人間の承認が要る操作 | 組織の外に影響する、元に戻しにくい | 提案だけを出し、人間が承認して有効になる | 新しい外部の相手への提供の開始、閾値を超える配分 |
| 委任できない操作 | 信頼の根拠、安全の下限、委任の仕組みそのものに関わる | 委任状に含められない | 予算の総額、委任の等級の定義と引き上げ、委任先を候補へ戻す、隔離の水準の登録、Executorのadmission、Data Classの下限、Trust Root、Federation、容量の確保の閾値、委任状の発行と更新 |

閾値の雛形は、一つの配分先への親の枠の30%、一日の配分し直しの合計の20%、委任状の期限の最長30日です。人間の管理者は委任状ごとに変えられますが、「委任できない」区分の操作を閾値の設定で委任できるようにはなりません。委任状を持つ主体が一時間に出せる「範囲内の拡大」の操作の数にも、上限を置きます。

委任状のもとで出された方針は、安全側の操作も含めて、どの委任状のもとで出されたかを記録し、人間の管理者が個別に取り消せます。

---

## FED-1 — Federation Administration

### 役割

外部のTrust Domainとの、Identityを検証する関係を、明示的に構成します。

### 入力

- Trust Domain
- Trust Material / Bundle
- versionとsequence
- federation policy
- 管理者の承認

### 出力

- versionを持つFederation Configuration

### 持たせない権限

- Federationした外部のPrincipalに、自動的にTaskの権限を付与すること
- Artifactへのアクセスを、自動的に許可すること
- Contributionの価値を決めること
- 推移的なTrustを、自動的に生成すること

~~~text
Identity can be verified
  !=
Resource access is authorized
~~~

Federation Trust Binding Spikeの原則を維持します。

---

## DLG-1 — Delegate / Trust Profile Registry

（Baseline 0.2で追加）

### 役割

委任先と、その信頼の水準、利用の許可を登録し、失効させます。

- 委任先: 資源のプール、資源提供者、Workerの区分、外部の提供枠など、仕事を任せる相手の単位
- 信頼の水準: [Worker Assurance Model](../requirements/ASSURANCE_MODEL.md)の軸に沿った保証の水準と、その根拠（管理者の登録、Attestation Result、運用の記録への参照）
- 利用の許可: どのPrincipalが、どの委任先と等級を使えるか

### 持てる権限

- 委任先、信頼の水準、利用の許可の登録、変更、失効
- 抜き取りの検証で不一致が出た委任先を、管理者が定めた方針に従って、自動で一時的に候補から外すこと

候補から外すことは安全側の操作なので、自動で行えます。候補へ戻すことと、信頼の水準を上げることは、管理者の操作に限ります。

### 持たせない権限

- Taskのadmission
- 具体的なWorkerの選択
- ResultをAcceptedにすること
- 予算の配分

### 重要な境界

信頼の水準を変更したときは、変更の前の水準で確定した結果を、来歴から洗い出せるようにします。信頼の水準の登録と変更は、Audit eventとして残します。

---

## BUD-1 — Budget Authority

（Baseline 0.2で追加）

### 役割

予算の総額と、入れ子の予算の枠（組織、部門、案件、Principal、依頼の群）への配分を持ちます。ADM-2とCAP-1からの予約を受け、精算します。

予算は物理量（CPU時間、メモリ量と時間、呼び出し回数、転送量など）と期間で表します。金額への換算は持ちません（[ADR-0010](../plan/adr/0010-contribution-record-is-not-currency.md)）。

### 持てる権限

- 予算の総額の設定。人間の管理者の操作に限る
- 予算の枠への配分。人間の管理者か、委任状を持つ主体の操作に限る
- 予約、精算、期間ごとの更新

### 持たせない権限

- 委任先の選択
- 容量の確保
- Taskのadmission
- 委任状の範囲を超えた配分

### 重要な境界

予算の枠の予約は、受理（ADM-2）と容量の確保（CAP-1）のどちらからも、事前に行います。後から精算するだけでは、予算を超えた消費を止められないからです（RQ-017）。

---

## EXE-1 — Executor Admission Authority

### 役割

Worker上で実行できるExecutor Artifactを審査して、実行できるDigest、Runtime Class、Provenanceの条件を確定します。

### 主なEvidence

- ArtifactのDigest / Size / Media Type
- Publisher Evidence
- Build Provenance
- SourceとCommit
- SBOMやscanなどの、任意のEvidence
- Runtime Class

### 出力

概念上の、Executor Approvalです。

### 持たせない権限

- Taskをsubmitすること
- 具体的なWorkerを選ぶこと
- Artifactのinput dataを読む権限を与えること
- WorkOrderを単独で発行すること

正しく署名されたArtifactやProvenanceがあっても、それがそのままExecutor Admissionになるわけではありません。

---

## IDV-1 — Identity Verification

### 役割

Client、Worker、Workload、Administrative PrincipalのIdentityを検証します。

OIDC、企業のPKI、SPIFFEなどは、Adapterの候補です。

### 出力

正規化したPrincipal Contextです。

### 重要な境界

Identity Verificationは、Authenticationの根拠です。

Authorization、Resultが正しいこと、Worker Assuranceを、自動的には作りません。

Client Identity、Worker Identity、Control Plane Workload Identityは、別のnamespaceやroleとして扱います。

---

## ADM-2 — Task Admission Authority

### 役割

外部から来たuntrustedなTaskRequestを、Fabric内部で扱ってよいAdmitted Taskに変換します。

### 主な入力

- 認証済みのPrincipal Context
- 厳格に検証した外部のTaskRequest
- organization policy
- quotaとbudget
- Artifact metadata
- Executorの論理的な要求
- side-effectのsemantics
- 要求されたsecurityとdata class

### 主な処理

- schemaの検証
- authorization
- quotaとbudgetの予約
- 有効なsecurity classification
- retryとfan-outのbudget
- side-effect contractの検証
- 許可するExecutorの要求の確定
- 未知または欠けているsecurity fieldを、安全な側に処理する

Clientが指定したData Classは、下限ではありません。他の根拠と合わせて、安全な側に計算します。

たとえば、次のとおりです。

~~~text
effective data class
  >= artifact intrinsic class
  >= organization policy minimum
  >= executor requirement
  >= client requested class
~~~

Clientが値を小さくして、Securityを弱めることはできません。

### 委任先の指名と、Taskの型

（Baseline 0.2で追加）

Clientは、DLG-1の利用の許可で許された委任先（資源のプール、資源提供者）を、目的に応じて指名できます。ADM-2は、指名を「許された範囲を狭める」条件としてだけ受理します。

~~~text
candidate delegates
  = delegates permitted to the principal (DLG-1)
  ∩ delegates whose trust profile satisfies the effective data class and assurance
  ∩ designated delegates (if any)
~~~

指名によって、予算、Data Class、信頼の水準の要求が緩むことはありません。候補が空になる指名は、受理しません。個々のWorkerの選択は、引き続き持たせません。

ADM-2は、Task、Task Groupに加えて、Session、Sandbox、常駐の依頼を受理します。予算は、BUD-1の枠から予約します。

### 出力

概念上の、Admitted TaskとTaskIR Approvalです。

### 持たせない権限

- 具体的なWorkerの選択
- Executor Artifactの新規の承認
- Artifact capabilityの発行
- Federationの変更
- WorkOrderの最終的な署名

---

## TSK-1 — Task State / Recovery Authority

### 役割

TaskのlifecycleとAttemptを管理します。

### 管理するもの

- Task state
- Attempt ID
- retry countとbudget
- cancellation
- deadline
- UNKNOWNのside-effect outcome
- mass reclaimの後のrecovery queue
- 親と子のbudget

### 重要な境界

Workerが消えたことだけで、retryの権限を作りません。

外部への副作用がUNKNOWNの場合、Task Stateはreconciliationに回し、自動のretryを止めます。

同時に、冪等なTaskでも、retry budgetを越えて、無限に再実行しません。

### 持たせない権限

- Taskのsecurity classificationの変更
- Artifact data grantの発行
- Executor Admission
- Federationの変更

---

## SCH-1 — Scheduler / Placement Planner

### 位置付け

**Security Authorityではありません。**

### 役割

Policyの上で候補になり得るResourceの情報を使って、Placement Proposalを作ります。

### 利用できる情報の例

- Worker class
- 申告された、または検証されたcapabilityの要約
- 現在のcapacity
- localityとcacheのhint
- 予想される転送のbyte数
- deadline
- queueの圧力
- energyやproviderのavailabilityの信号
- 過去の運用上の指標

### 出力

Placement Proposalです。

### 禁止する権限

- Data Classを下げること
- Worker Assuranceを自己生成すること
- Artifact Grantを発行すること
- Executorを承認すること
- WorkOrderに署名すること
- Federationの設定を変更すること
- learned modelのscoreを、Authorizationに変換すること

Schedulerが侵害されると、availabilityや効率には影響し得ます。しかし、他のAuthorityの署名を偽造しない限り、Schedulerだけでは、Security requirementを迂回できない構造を、めざします。

---

## PLC-1 — Placement Authorization Authority

### 役割

Scheduler Proposalを、Security、Safety、Grantの制約に照らし合わせます。そして、そのTask AttemptをそのWorkerに置いてよいかを判断します。

### 主な入力

- Admitted Task
- Attempt Context
- Placement Proposal
- Worker Identity
- Resource Capability Evidence
- AssuranceとAttestation Result
- Compute Grantの制約
- データの局所性とnetwork policy
- Providerの状態

### 出力

Placement Approvalです。

### 重要な境界

Scheduler Proposalは、入力の一つにすぎません。

Schedulerが

> このWorkerは速い

と提案しても、Data ClassやAssuranceを満たさなければ、Placement Authorityは拒否します。

### 持たせない権限

- Artifact Capabilityの直接の発行
- Executor Admission
- Federationの変更
- WorkOrderの最終的な署名

Artifact / Data Authorityは、Placement Approvalだけを唯一のSecurity Evidenceとして、Dataを解放しません。必要なWorker IdentityとAssurance Evidenceを、独立に確認できる構造を選びます。

---

## LSE-1 — Lease Authority

### 役割

特定のTask Attemptを、特定のWorkerに、一定の時間だけ割り当てるLeaseを、発行し、renewし、revokeします。

### Leaseが結び付けるもの

- Task ID
- Attempt ID
- Worker ID
- lease IDとnonce
- 発行した時刻
- expiry
- renewalの制約

### 目的

- 切断されたWorkerの検出
- 古い割り当ての失効
- retryとrescheduleの境界
- replayの抑制

### Policy currentnessの境界

署名済みPlacement Approvalが、あるPolicy Revisionで正しく発行されていても、それだけでは新しいLeaseを発行しません。

LSE-1は、Lease予約の直前に、ADM-1が管理する現在のPolicy Stateを読みます。M0ではPolicy Stateを単調増加する`Policy Epoch`としてState Storeに永続化し、Lease予約も同じ永続化境界でexpected epochを再確認します。

~~~text
read current Policy(epoch=N, revision=P)
  ↓
verify Placement against P
  ↓
possible concurrent revoke / replace
  ↓
ReserveLease(expected policy epoch=N)
  ↓ same durable boundary
current epoch still N and P is active?
  ├─ no  -> fail closed
  └─ yes -> reserve FenceEpoch
~~~

Policy Revisionの文字列を読んだ事実だけを、freshnessの保証にはしません。Policyのrevokeや置換とLease reservationが競合した場合は、古いsnapshotを持つ側を失敗させます。

Policy revision identityは一度currentになったら再利用しません。`P1 -> P2 -> P1`のようにepochだけを進めて古いrevisionを復活させることもrollbackとして拒否します。過去のpolicy内容へ戻す必要がある場合は、その内容を新しいrevision identityとして発行します。これにより、revision identityが「どのpolicy snapshotか」を一意に指し続けます。

Policy Stateの読み取りと更新は、Admissionのwrite surfaceとは分離します。同じSQLite fileを使うReference Profileでも、ADM-2がPolicy Authorityを持つことにはしません。

既存のpre-Policy-Epoch Leaseは、履歴として読めても`PolicyEpoch=0`のunfenced stateです。新しい署名済みLease Authorityへ、そのまま昇格しません。

### SessionとResidentのLease

（Baseline 0.2で追加）

Leaseの対象を、Task Attemptに加えて、Sessionと常駐の期間へ広げます。

- Session: 期限のある枠のLease。枠の中の小さな依頼は、枠のLeaseの範囲で扱う
- 常駐: 期間ごとのLease。期間が終わる前に更新し、更新のたびに、BUD-1の予約、DLG-1の失効、現在のPolicyを確かめ、FenceEpochを進める

更新の判断が得られなければ、Leaseは期限で失効し、常駐も終わります。古い期間のLeaseで動く実行者の出力は、確定しません。

### 非目標

Leaseだけでは、exactly-onceを保証しません。

Side-effectのUNKNOWNやidempotencyは、Task Contract側の、別の概念です。

---

## CAP-1 — Elastic Capacity Authority

（Baseline 0.2で追加）

### 役割

費用を生む容量（クラウドの拡張、有償の外部の資源）の確保と解放を、SCH-1の提案を受けて承認します。

### 主な入力

- SCH-1のCapacity Proposal
- 容量を必要とする依頼者の予算の枠と、組織の容量の予算の枠（BUD-1）
- 資源の提供元の側の上限
- 管理者が定めた、人間の承認を求める閾値

### 持てる権限

- 予算の枠の範囲での、容量の確保と解放
- 依頼の取り消しや需要の減少に応じた、容量の解放

### 持たせない権限

- Taskのadmission
- 予算の配分
- ResultをAcceptedにすること
- 資源の提供元の側の上限の変更

### 重要な境界

確保の費用は、提案の時点で、容量を必要とする依頼者の予算の枠と、組織の容量の予算の枠の両方から予約します。後から精算するだけでは、自律に委ねる依頼を大量に出して組織に費用を負わせる攻撃を止められません。閾値を超える確保は、人間の承認を経るまで行いません。

資源の提供元へ接続する資格情報は、Adapterが持ち、範囲を限ります。CAP-1をADM-2とBUD-1から分けるのは、予算を配分する権限と、費用を使う権限が同じだと、一つの侵害で配分と支出の両方を操作できるからです（RQ-051）。

---

## DAT-1 — Artifact / Data Capability Authority

### 役割

ArtifactやDataを、誰が、どのTask Attemptで、どのWorkerから、どの操作として利用できるかを限定した、Capability / Grantとして発行します。

### 主な入力

- Artifact固有のclassification
- 要求しているPrincipalとTask
- Admitted Task
- Worker Identity
- Placement Approval
- Worker AssuranceとAttestation Evidence
- Lease
- organizationのdata policy

### Grantが結び付けるもの

- Artifact Digest
- Task ID
- Attempt ID
- Worker ID
- 許可する操作
- 任意の、byte、range、objectの範囲
- expiry
- Data Class
- policy revision

### 重要な境界

Workerは、Clientが指定したURLやpathを、Data Authorityとして扱いません。

~~~text
https://internal-service/
file:///etc/passwd
\\host\share
~~~

このような文字列だけでは、Artifactへのアクセスのauthorityは成立しません。

### 侵害されたときの被害の封じ込め

DAT-1が侵害されると、そのAuthorityが管理するData Scopeの漏えいのriskが、重大になります。

そのため、次のものを、Architectureを比べる対象にします。

- TenantとData Class scopeの分離
- 短い有効期間のGrant
- 最小権限
- audit
- keyの分離

一方で、DAT-1だけでは、Executor AdmissionやWorkOrderの署名まで取得できない構造にします。

---

## WOR-1 — WorkOrder Authority

### 役割

すでに独立したAuthorityで確定されているDecisionを、一つの変更できないexecution envelopeに、整合的に束ねます。そして、Workerに渡すWorkOrderに署名します。

### WorkOrderの前提

少なくとも、必要に応じて、次のものを参照または内包します。

- Admitted TaskとTaskIRのidentity
- TaskとAttemptのID
- Placement Approval
- Lease
- Executor ApprovalとExecutor Digest
- ArtifactとData Grant
- Resourceの上限
- Sandbox capability
- network policy
- side-effect profile
- policy revision
- expiryとnonce

### WOR-1がしてはいけないこと

足りないApprovalを、自分で作ってはいけません。

- Data Grantがないから、WOR-1がData accessを足す
- Executor Approvalがないから、WOR-1がDigestを承認する
- Placement Approvalがないから、WOR-1がWorkerを許可する

このような挙動を、禁止します。

### Worker側の再検証

Workerは、WorkOrderのenvelopeの署名だけを確認して、実行してはいけません。

WOR-1だけが侵害された場合のblast radiusを狭めるため、Worker-localのWorkOrder Verifierが、重要な前提のDecisionの、issuer、binding、freshnessを再検証します。

---

## ATV-1 — Attestation Verifier

### 役割

WorkerやExecution Environmentから得たRaw Evidenceを、Verifier Policyで評価して、正規化したAttestation Resultに変換します。

### 出力

- Attester / Workerのbinding
- Trust Domain
- 正規化したEnvironment Class
- Verifier ID
- Policy Revision
- expiry
- Evidenceのprovenance digest

### 非目標

ATV-1がEvidenceを受理しても、

> このTaskを、このWorkerに置いてよい

ということは、決まりません。

Placement Authorityが、Task固有のPolicyを再評価します。

Raw Evidenceを、SchedulerやClientに広く配布することも、標準にはしません。

---

## RVR-1 — Result Verification Authority

### 役割

Workerが返したResultClaimを検証します。

Task Profileに応じて、Adapterとして次のものを利用できます。

- 決定的な処理での、完全な一致
- アプリケーションのvalidator
- 独立した複製の実行
- 管理下のanchorによる再計算
- 暗号学的なproof
- attested executionのevidence

### 出力

正規化したVerification Resultです。

### 重要な境界

Worker Identityが正しいというだけでは、Resultが正しいとは認めません。

Verifier自身も、絶対に信頼できるものとはせず、Verifier Identity、Policy Revision、provenanceを追跡します。

高い保証が必要なTaskでは、複数のVerifierや、独立したanchorを要求する余地を残します。

---

### 信頼の水準と検証の強さ

（Baseline 0.2で追加）

確定に要る検証の強さは、Taskの保証の要求と、DLG-1に登録した委任先の信頼の水準から決めます。委任先の申告からは決めません。抜き取りの検証で不一致が出たら、その事実をDLG-1へ渡します。

---

## RAC-1 — Result Acceptance Authority

### 役割

Verification Resultを、Taskごとの、Result Assurance requirementに照らし合わせます。そして、ResultClaimをAccepted Resultに昇格させる、最終的なRelying Partyです。

### 主な入力

- Admitted Task
- ResultClaim
- Verification Resultの群
- Result Assurance requirement
- policy revision

### 出力

Accepted Result、または、明示的な未確定の状態です。

### Verificationと分ける理由

Proof VerifierがProofをvalidと判定しても、TaskがそのProof Systemを許可していない場合があります。

Attestation VerifierがEnvironmentをvalidと判定しても、Application Resultが意味の上で正しいかどうかは、別のことです。

そのため、

~~~text
Verifier accepted evidence
  !=
Task result accepted
~~~

という関係を、Architectureで維持します。

---

## EFF-1 — External Effect Confirmation Authority

### 役割

外部への副作用が、実際に成立したかを確認できるAuthorityです。

これは、EKI内部のComponentとは限りません。

たとえば、次のものがあります。

- 外部APIのoperation statusとreceipt
- managed effect adapter
- databaseのtransaction record
- brokerのdeduplication record

### 重要な境界

Workerが、

> 成功した
> 失敗した

と自己申告しただけで、外部の世界の事実を確定しません。

冪等でないeffectでReceiptを得られない場合、Task StateはUNKNOWNを維持します。

---

## CON-1 — Contribution Authority

### 役割

Resource Measurement Evidence、Work Context、必要に応じてAccepted Resultを照合して、署名済みのContribution Recordを発行します。

### 出力

価値に換算する前の、Contribution Recordです。

### 持たせない権限

- EKI Currencyの発行
- 法定通貨への換算
- market priceの形成
- Local Governorのoverride

~~~text
Contribution Record
  != Credit value
  != money
~~~

価値への換算は、外部のPolicyに残します。

---

# 6. Worker-localのTrust Boundary

中央のControl Planeが正しくても、Workerはmaliciousであったり、改変されていたりし得ます。

逆に、業務のEndpointでは、中央のComponentが誤動作する可能性もあります。

そのため、Worker側にも、明示的な境界を置きます。

## WEV-1 — WorkOrder Verifier

### 実行の直前に確認するもの

- WorkOrder Authorityの署名
- TaskとAttemptのbinding
- Placement Approval
- Leaseのexpiryと、Workerとのbinding
- Executor Approvalと、実際のExecutor Digest
- Artifact Grantと、実際のArtifact Digest
- ResourceとCapabilityの上限
- network policy
- 関連するPolicy Revisionとexpiry

未知のAuthority、期限切れのDecision、subjectの不一致は、fail closedします。

## GOV-1 — Local Governor

### 役割

次の条件に応じて、中央が許可したcapacityを、さらに狭めます。

- user opt-out
- userの復帰
- CPU / Memory / GPU / Networkの上限
- BatteryかACか
- Thermal
- 時間帯
- device safety

### 権限の方向

Local Governorが持つのは、capacityを減らす拒否権です。

Organization PolicyやHardware Safetyが許可していないcapacityを、増やすAuthorityにはしません。

## SBX-1 — Sandbox / Capability Enforcer

Executorは、Admission済みであっても、untrustedなprocessとして扱います。

Default denyを基本にして、WorkOrderで必要なCapabilityだけを渡します。

次のものを分離します。

- filesystem
- network
- deviceとGPU
- process
- temporary storage
- secret
- artifactのinputとoutput

Executor Admissionが侵害されても、Sandbox capabilityまで自動的に拡大しない構造を、めざします。

---

# 7. Runtime Trust Flow

標準的なTask lifecycleを、Authorityの流れとして表すと、次のようになります。

~~~text
Untrusted Client
    │
    ▼
IDV-1 Identity Verification
    │ Principal Context
    ▼
ADM-2 Task Admission
    │ Admitted Task / TaskIR
    ▼
TSK-1 Task State
    │ Attempt Context
    ├───────────────────────────────┐
    │                               │
    ▼                               ▼
SCH-1 Scheduler                 EXE-1 Executor Approval
    │ Proposal                       │
    ▼                                │
PLC-1 Placement Authorization ◄──────┤
    │ Placement Approval             │
    ▼                                │
LSE-1 Lease                          │
    │                                │
    ├──────────────► DAT-1 Data Capability
    │                     │ Grants
    └──────────────┬──────┘
                   ▼
             WOR-1 WorkOrder
                   │
                   ▼
          Worker WEV-1 Verifier
                   │
             GOV-1 Local Veto
                   │
             SBX-1 Sandbox
                   │
                   ▼
              ResultClaim
                   │
             RVR-1 Verify
                   │
             RAC-1 Accept
                   │
             AcceptedResult
                   │
        optional CON-1 Contribution
~~~

Remote Attestationを使うTaskでは、ATV-1のAttestation Resultが、PLC-1やDAT-1の入力になります。

Baseline 0.2では、ADM-2がDLG-1の利用の許可とBUD-1の予算の枠を入力にします。SCH-1は、容量が足りない場合にCAP-1へCapacity Proposalを出し、CAP-1がBUD-1から予約して容量を確保します。PLC-1とRVR-1は、DLG-1の信頼の水準を入力にします。

External Side Effectを持つTaskでは、EFF-1のReceiptが、TSK-1のRecoveryの判断に戻ります。

---

# 8. Decision Objectに共通する性質

Wire schemaは、後続のTask Contractで決めます。ただし、Security上重要なDecisionには、共通して次のものを持たせる方向を、Baselineとします。

- issuer identity
- decision typeとschema version
- decision ID
- subjectのIDとdigest
- policy revision
- issued at
- expiry、または明示的なlifetime
- 任意のnonceやsequence
- 暗号学的なintegrityとauthenticity

必要なDecisionでは、revocationも扱います。

「署名が正しい」ことだけでなく、次のことも検証します。

- 正しいissuerか
- 正しいsubjectか
- 現在も有効か
- 現在のPolicyで受理できるか

---

# 9. 永続的な状態に対するAuthority

すべてのComponentが、同じDatabase userで、すべてのTableを読み書きできる構成は、標準にはしません。

論理的には、次のように分けます。

| Authority / Component | 主に書ける状態 | 原則として読み取り専用、または不要な状態 |
| --- | --- | --- |
| ADM-2 Task Admission | admitted task, quota reservation | raw artifact bytes, federation admin keys |
| TSK-1 Task State | task / attempt / recovery | executor trust rootの変更 |
| SCH-1 Scheduler | 原則としてproposalとcacheのみ | artifact secret, signing key |
| PLC-1 Placement | placement decision | raw artifact bytes |
| LSE-1 Lease | lease state | executor admission policyの変更 |
| DAT-1 Data Authority | grants / data access audit | scheduler model state |
| WOR-1 WorkOrder | workorder envelope / dispatch audit | federation configurationの変更 |
| EXE-1 Executor Admission | executor approvals / revocation | task scheduling state |
| RVR-1 Verification | verification result | artifact grantの発行 |
| RAC-1 Acceptance | accepted result state | federation / executor admin |
| CON-1 Contribution | contribution record | Task security classificationの変更 |
| FED-1 Federation Admin | federation config | task / result state |
| DLG-1 Delegate Registry | delegates / trust profiles / entitlements | task / result state, budget allocation |
| BUD-1 Budget | budget totals / envelopes / reservations | delegate trust profiles, capacity provider credentials |
| CAP-1 Elastic Capacity | capacity acquisition / release records | budget totals, task admission state |
| ADM-1の委任状 | mandates / mandate usage audit | 委任状を持つ主体のClientとしてのtask state |

物理的なDatabaseを共有する場合も、Repository、transaction、credentialの境界を分けられる設計を、優先します。

---

# 10. Compromise Matrix

この表は、「侵害されても安全」という意味ではありません。

一つのComponentが侵害されても、別のAuthorityまで自動的に獲得できないことを確認するための、設計の目標です。

| 侵害の対象 | 攻撃者が起こし得ること | 単独では起こせないようにすること |
| --- | --- | --- |
| Client | 自分のPrincipalの範囲で、悪意のあるrequestやquotaの消費を試みる | 任意のWorkerの指定、任意のcodeの実行、他のPrincipalのDataの取得 |
| Identity Provider / IDV-1 | 対象のTrust Domain内で、Principalをなりすます | WorkerのWorkOrderの署名、Artifact Grant、Executor Approval |
| ADM-2 Admission | 不適切なTaskをAdmitする、quotaの判断を壊す | Artifact固有のclassの低下、Executor Approval、WorkOrderの単独の発行 |
| TSK-1 Task State | retry、cancel、stateを乱す、availabilityを壊す | Data Grant、Executor Admission、Federationの変更 |
| SCH-1 Scheduler | 悪い候補を提案する、性能を劣化させる、starvationを起こす | Security requirementの低下、Data Grant、WorkOrderの署名 |
| PLC-1 Placement | 不適切なWorkerを承認し得る | Attestationの偽造、Artifact Authorityのkey、Executor Admission |
| LSE-1 Lease | 重複した、または古いlease、availabilityの混乱 | Dataを読む権限、Executorのcodeの承認 |
| DAT-1 Data Authority | 管理するScope内のDataの漏えい、過剰なGrant | Executor Admission、Federationの設定、WorkOrderの前提のDecisionの偽造 |
| EXE-1 Executor Admission | 悪意のあるExecutorを承認する | Data Grant、Placement、Sandbox capabilityの自動的な拡大 |
| WOR-1 WorkOrder | 不正なEnvelope、replayを試みる | 他のAuthorityの署名による、有効な前提のDecisionの生成 |
| ATV-1 Attestation Verifier | 偽のEnvironment Result | Artifact Grant、Task Admission、Resultのsemanticな検証 |
| RVR-1 Result Verifier | 偽のVerification Result | Program Admission、Data Grant、Contributionの価値の決定 |
| RAC-1 Result Acceptance | 不正なResultをAcceptedに昇格する | Workerの実行権限、Data Grant |
| EFF-1 Effect Authority | 外部のeffectのstatusを偽る | EKIのData / Executor / Federation Authority |
| CON-1 Contribution | 偽のContribution Record | EKI通貨の発行、Local Governorのoverride、Taskの実行 |
| FED-1 Federation Admin | 外部のIdentityのtrustを誤って設定する | Resource Authorizationの自動的な付与 |
| GOV-1 / 改変されたAgent | 自分のEndpointの制御と計測を偽る | Control Planeの署名、他のWorker Identity、Accepted Resultの署名 |
| DLG-1 Delegate Registry | 委任先の信頼の水準を誤って高くする、利用の許可を広げる | Data Grant、Executor Admission、予算の配分、Local Governorのoverride |
| BUD-1 Budget | 委任状や管理の範囲の中で、予算の配分を偏らせる | 容量の確保、予算の総額の変更（人間の管理者の操作が要る）、Data Grant |
| CAP-1 Elastic Capacity | 予算の枠の範囲で、不要な容量を確保して費用を生む | 予算の枠を超えた確保、資源の提供元の側の上限の変更、Taskのadmission |
| 委任状を持つ主体（SEIなど） | 委任状の範囲で、偏った方針を出す | 越えさせない線の変更、委任状の自己更新、Trust Rootの変更、閾値を超えた変更の単独での有効化 |

## 10.1 特に影響の大きい侵害

次のものは、大きなblast radiusを持ち得ます。

- Trust / Policy Administration
- Artifact / Data Authority
- Executor Admission Authority
- Result Acceptance Authority
- Federation Administration
- Delegate / Trust Profile Registry（Baseline 0.2）
- Elastic Capacity Authority（Baseline 0.2。費用を生むため）

これらは、SchedulerやTelemetryのように、複雑で頻度の高いComponentと、同じCredentialを共有しないことを優先します。

## 10.2 Rootの侵害

Organizationのroot signing key、host OSとkernel、hardwareのroot、Verifierのrootなどまで、全面的に侵害された場合は、通常の論理的な分離だけでは防げない範囲があります。

これを隠して、「完全に安全」とは表現しません。

代わりに、次の手段によって、blast radiusを狭める設計を、比べます。

- keyの分離
- 短い有効期間の、派生したauthority
- rotationとrevocation
- offlineやHSMなどによる保護
- multi-party approvalの候補
- 独立した検証

---

# 11. 独立した再確認の原則

一つのAuthorityの出力を、後段が無条件には信頼しない箇所を、明示します。

## Data Release

DAT-1は、Placement Approvalだけを見て、Dataを解放しません。

少なくとも、次のものを独立に確認できる情報を持ちます。Artifact classification、Task binding、Worker Identity、必要なAssurance、Lease。

## WorkOrder

WOR-1は、前提のDecisionを束ねます。そして、Worker側のWEV-1が、重要な署名とbindingを再確認します。

## Result Acceptance

RAC-1は、RVR-1が「valid」としたことだけでなく、次のことも確認します。そのVerifierが、Task Policyで受理できるか。Resultが、Task、Attempt、Input、Programなどに結び付けられているか。

## Contribution

CON-1は、Accepted Resultだけから、Resourceの量を作りません。

Measurement Observer、Assurance、Work Contextを、別に確認します。

この再確認は、すべてのComponentが同じPolicy engineを重複して実装する、という意味ではありません。

目的は、「侵害された一つの要約値だけで、後段の価値の高いAuthorityを無条件に動かさない」ことです。

---

# 12. Data Minimization

Authorityを分離することは、Privacyとも関係します。

各Componentに、必要以上の情報を渡さないことを、めざします。

## Scheduler

原則として、次のものを使います。

- Resourceの要約
- capabilityの要約
- Artifactのlocality hint
- 転送のsize
- deadlineとpriority
- policyで許可された、運用上の履歴

Artifactの本文、Clientのsecret、Raw Attestation Evidenceは、標準の入力にしません。

## Attestation / Verification

Raw Evidenceは、必要なVerifierの境界の中に閉じます。SchedulerやClientには、正規化した結果だけを渡せる設計にします。

## Contribution

Contribution Recordに、userのactivityのraw telemetryを直接埋め込むことを、避けます。

必要なのは、Resource Measurementの根拠とWork Contextです。個人の行動の詳細そのものではありません。

## Client

Clientには、Workerのhostname、IP、ログインしているuser、インストールされているsoftware、cacheの一覧などを返しません。

Task statusは、必要最小限の、queued、running、completed、failed、unknownなどに縮約します。

---

# 13. Deployment Profileへの投影

Authority Mapは、すべてのDeploymentで同じ論理構造を保ちます。物理的な構成は、規模に見合ったものにします。

## 13.1 Small Managed Organization

たとえば、次の構成でも成り立たせます。

~~~text
1 Control Plane binary
1 database
several endpoint agents
~~~

ただし、Binaryの内部では、Authorityごとのinterfaceとkey handleを分けます。

専用のKafka、SPIRE、HSM、複数のMicroserviceを、必須にはしません。

## 13.2 Enterprise Mixed Endpoints

次のものを利用できます。

- 企業のIdPとPKIとの統合
- 分離したArtifact Store
- より強いkeyの保護
- 価値の高い管理の経路の分離
- proxyとoutbound-onlyのworker
- 管理されたendpointのpolicy

## 13.3 Browser / Ad-hoc Participation

次のことを基本にします。

- 短い有効期間のidentity
- 信頼の低い、使い捨てのWorker profile
- 公開または低機密のData
- 制限されたExecutor
- 積極的なlease expiry
- ローカルのsecretを持たない
- 明示的な参加

Browser Workerが存在するからといって、managed Worker側のSecurityを弱めません。

## 13.4 Federation / Public-interest Grant

Federation IdentityとResource Authorizationを分離します。そして、Compute Grantの、scope、Beneficiary、Data Class、期間、preemptionを、明示します。

Contribution Recordは、必要に応じて外部の制度に渡せます。ただし、EKI Coreは、価値の制度を持ちません。

---

# 14. Key / Credentialの基本

具体的なKMSやPKIの方式は、後続で比較します。論理的な原則は、次のとおりです。

1. Client、Worker、Control Plane AuthorityのCredentialを分ける。
2. Authorityごとに、issuer identityを識別できる。
3. Productionでは、異なる価値の高いAuthorityが、同じsigning credentialを共有しないことを、標準とする。
4. 短い有効期間のdecisionは、短いexpiryを持てる。
5. keyのrotationとrevocationを扱える。
6. Trust Rootを、通常のrequestで差し替えられない。
7. signing private keyを、Scheduler、parser、untrustedなexecutorに渡さない。
8. Devとtestのprofileで、簡易なkeyを使っても、Authority identity自体は分離してTestする。

---

# 15. Auditの基本

Security上、重要なAuthorityの遷移は、少なくとも、後から関連付けられる必要があります。

たとえば、次のものがあります。

- Task Admission
- quotaとbudgetの予約
- Placement Approvalとrejection
- Leaseのissue、expiry、revoke
- Artifact Grantのissueとdeny
- Executor Approvalとrevoke
- WorkOrderのissue
- Result Verification
- Result Acceptance
- Federation configの変更
- Contribution Recordのissue
- Local Governorがreclaimした理由
- 委任先の信頼の水準と利用の許可の登録、変更、失効（Baseline 0.2）
- 予算の総額の設定、配分、予約（Baseline 0.2）
- 容量の確保と解放、人間の承認（Baseline 0.2）
- 委任状の発行、使用、失効（Baseline 0.2）

Audit eventは、可能な限り、Task、Attempt、DecisionのID、Policy Revisionに結び付けます。

ただし、Auditのために、Raw secret、Raw Attestation Evidence、不要なPersonal Dataを集約しません。

Audit Store自体の、改ざんへの耐性は、後続の設計項目です。

---

# 16. 未決事項

このBaselineでは、次のことを、まだ固定していません。

- Authorityごとの、Processとservice数
- HTTPやgRPCなどの、内部のtransport
- PKI、KMS、HSMの製品
- OPAやCedarなどの、Policy engine
- decisionの正確なwire schema
- QueueとDB
- Artifact Store
- multi-party approval
- audit logの暗号学的なchaining
- keyless signature
- TEEのvendor
- zkVM
- 複数のverifierによるquorum
- 信頼の水準の引き上げに、複数人の承認を求めるか（Baseline 0.2）
- 委任状で委任できる操作の一覧と、人間の承認を求める閾値の既定値（Baseline 0.2）
- 予算の枠の入れ子の深さ（Baseline 0.2）

後続のArchitecture Work Unitで、RequirementとQuality Attributeに照らして比べます。

---

# 17. 次のArchitecture Work Unitへの入力

このAuthority Mapを前提に、次は、External / Internal Contract Boundaryを具体化します。

優先するConceptは、次のとおりです。

- TaskRequest
- Admitted Task / TaskIR
- Attempt
- PlacementProposal
- PlacementApproval
- Lease
- ExecutorApproval
- ArtifactGrant
- WorkOrder
- ResultClaim
- VerificationResult
- AcceptedResult
- ContributionRecord

次の作業では、これらを、単なるGoのstructの集合として先に固定することは、しません。Contractごとに、次のことを定義します。

1. 誰が生成できるか
2. 誰が検証するか
3. 何に結び付けるか
4. どのFieldをClientが制御できるか
5. versionとextensionをどう扱うか
6. replay、古い情報、downgradeを、どう防ぐか
