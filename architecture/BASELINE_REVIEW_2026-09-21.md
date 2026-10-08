# Architecture Baseline 0.1 横断レビュー — 2026-09-21

Status: **Reviewed / Implementation Planning Ready**

## 1. 目的

この文書は、EKI FabricのArchitecture Baseline 0.1を構成する6つの文書を横断して、実装の計画に進む前に確認した記録です。確認したのは、責務、用語、状態、Authorityに矛盾がないかどうかです。

対象は、次のとおりです。

- `AUTHORITY_BOUNDARIES.md`
- `CONTRACT_BOUNDARIES.md`
- `CONTROL_DATA_PLANE.md`
- `DEPLOYMENT_PROFILES.md`
- `TASK_STATE_MODEL.md`
- `EXTENSION_SPI.md`

RequirementやArchitectureの、第二の正本を作ることが目的ではありません。各正本の文書の関係を確認し、曖昧な箇所は、実装の前の解釈として固定します。

## 2. 結論

**Architecture Baseline 0.1には、MVPの実装の計画に進めるだけの整合性があります。**

重大な責務の矛盾、Security Invariantの反転、State ModelとContractの不整合は、確認されませんでした。

残る論点は、主に次の2つです。具体的なtransport、schema、database、queue、KMS、runtimeなどの技術の選択。高度なOptional Assuranceの実測。

Issue #19のlearned schedulingと、Issue #23のreal zkVM benchmarkは、継続する研究とします。MVPの必須の依存先にはしません。

---

# 3. 横断Consistency Matrix

| 観点 | 正本の間の関係 | 判定 |
| --- | --- | --- |
| Clientの悪意のある入力 | Contract → Admission Authority | 整合 |
| Schedulerのauthority | Authority → Deployment → SPI | Proposalのみで一貫 |
| Workerの実行のauthority | WorkOrder + 前提となるDecision + ローカルでの検証 | 整合 |
| Dataのauthority | ArtifactGrantとData Authorityを、Taskのauthorityから分離 | 整合 |
| 大きなデータの経路 | Contract → Control/Data Plane → Deployment | 整合 |
| Task / Attempt | Contract → State Model | Retryは新しいAttemptとして整合 |
| Lease | Authority → Contract → State | 時間を区切った割り当てとして整合 |
| Queue | Deployment → State → SPI | 配送の仕組みのみとして整合 |
| Result | ResultClaim → VerificationResult → AcceptedResult | 整合 |
| 外部への副作用 | Contract → State → Effect SPI | UNKNOWNを保持する点で整合 |
| Contribution | Measurement → Contribution Authority → Record | Resultや価値とは分離 |
| Federation | Identityのnamespace + ローカルなadmission + Dataのexportの分離 | 整合 |
| Browser / アドホック | 保証の低いprofile + 短いLease + 明示的な参加 | 整合 |
| Local Governor | Authority → Deployment → State | 中央より強い拒否権として整合 |
| Extensions | 管理者が管理し、意味を上書きできない | 整合 |

---

# 4. 用語の整理

## 4.1 ArtifactGrantとData Grant

両者を、同じ語に無理に統合しません。

**ArtifactGrant**:

- 特定のArtifactやobjectに対する、具体的な実行時のcapabilityです。
- Task、Attempt、Worker、operation、有効期限に結び付けます。
- WorkOrderとData Access Resolverで使います。

**Data Grant**:

- より上位の、Dataの認可の概念です。
- Federationのexport、アプリケーションが所有するData、Compute GrantとDataへのアクセスの分離などを含みます。
- 必要に応じて、一つ以上のArtifactGrantやquery capabilityに具体化します。

そのため、MVPの実行時の契約で使う具体的なobjectの名前には、`ArtifactGrant`を優先します。`Data Grant`は、policyやfederationの層の、上位の概念として扱います。

## 4.2 Task Admission Authority

`ADM-2 Task Admission Authority`を、正式な名称とします。

文脈によっては、`Admission Authority`と短縮してかまいません。ただし、Executor AdmissionやExtension Administrationと混同しそうな箇所では、`Task Admission`と明記します。

## 4.3 WorkOrder Authority

WorkOrder Authorityは、最終的なexecution envelopeを組み立てて、署名します。

ただし、独立したroot policy authorityではありません。足りないPlacement、Lease、Executor Approval、ArtifactGrantを、自分で生成してはいけません。

Worker側は、WorkOrder Authorityの署名だけでなく、Securityの面で重要な前提について、bindingとissuerとfreshnessを確認し直します。

## 4.4 Result VerificationとResult Acceptance

**Result Verification Authority**:

- ResultClaimに対するEvidenceを評価します。
- VerificationResultを生成します。

**Result Acceptance Authority**:

- VerificationResultを、Task固有のResult Assurance Policyに照らして確認します。
- AcceptedResultを生成します。

**Task State Authority**:

- 永続化されたAcceptedResultを確認して、Task LifecycleをCOMPLETEDに遷移させます。

この3段階を維持します。

## 4.5 Data ClassificationとDeclassification

OutputのData Classは、安全な側に伝播します。

Baseline 0.1には、汎用のDeclassification Authorityを置きません。

将来、匿名化や集計などによってClassを下げる必要が生じた場合は、承認されたExecutor、専用のPolicy、Validation、Auditを伴う、明示的なArchitectureの追加として扱います。

WorkerやExecutorの自己申告だけでは、Classを下げません。

## 4.6 Extension Administration

Extensionの登録、有効化、取り消しは、Administrative Planeの責務です。

Baseline 0.1では、新しい実行時のAuthorityを増やしません。Trust / Policy Administrationと、対象の種類のAdmissionの責務のもとで管理します。

特権の高いExtensionを、将来、別のAdmin Authorityに分ける余地は残します。

---

# 5. AuthorityとContractの整合

## 5.1 Client → TaskRequest

Clientは、Intentを表現できます。しかし、Worker、Trust Root、内部のPolicy Revision、ArtifactGrant、Executor ArtifactのDigestなどを、確定することはできません。

Task Admission Authorityが、TaskRequestを厳格にparseし、認証し、認可し、予算を確認します。そのうえで、AdmittedTaskとTaskIRを確定します。

## 5.2 Scheduler → PlacementProposal

Schedulerの出力は、Proposalです。

Placement Authorization Authorityが、security、assurance、grant、データの局所性の、厳しい制約を再評価します。

将来、learned estimatorを導入しても、この関係は変えません。

## 5.3 WorkOrder

WorkOrderは、TaskIR、Attempt、PlacementApproval、Lease、ExecutorApproval、ArtifactGrant、resourceとcapabilityの上限を束ねます。

Worker側にあるWorkOrder Verifierが、重要な前提のDecisionを、再検証します。

この二重の境界によって、WorkOrderの署名者だけが侵害された場合の、blast radiusを抑えます。

---

# 6. ContractとState Modelの整合

## 6.1 TaskRequest / TaskIR

Admissionに成功した後に、Task identityを生成します。そして、TaskIRのDigestに、後続のDecisionを結び付けます。

Task Stateは、Clientのrequestを、変更できる正本にしません。

## 6.2 Attempt

Retry、migration、speculative実行、検証のための複製は、すべて別のAttemptです。

Queueが重複して配送したことは、別のAttemptではありません。

## 6.3 Lease

Leaseは、AttemptとWorkerに与える、時間を区切ったauthorityです。実行の結果ではありません。

Leaseの期限が切れたあとに、遅れて届いたheartbeatによって、Authorityを復活させません。

## 6.4 Result

ResultClaimを受信したことは、Attemptの結果についてのClaimです。

VerificationとAcceptanceを経て、AcceptedResultが成立します。そして、Task StateがCOMPLETEDに収束します。

---

# 7. Control/Data PlaneとDeploymentの整合

Small Managedでは、Control PlaneとData Access Resolverを、同じバイナリに同居させられます。

Enterpriseでは、Worker Gateway、Data Authority、Store、Site Cacheなどを、物理的に分離できます。

どちらの場合でも、次の論理的な境界は同じです。

- ArtifactGrantの範囲
- Content Digestの検証
- 短い有効期間のアクセス
- bucket全体にわたるWorkerのcredentialを持たせない
- 大きなArtifactを、Control Planeのmetadataの経路に、標準では集約しない

---

# 8. DeploymentとState Modelの整合

## DP-1 Small

Control Planeが単一でも、永続化された状態、revision、再起動後のreconciliationを、要求します。

## DP-2 Enterprise

複数のinstanceでも、Task、Attempt、Leaseのfencingを維持します。

## DP-3 Browser

短いLease、高いchurn、古いsessionの拒否を、State Modelで吸収します。

## DP-4 Federation

Provider domainが、ローカルのAttemptとLeaseのauthorityを持ちます。遠隔との接続が切れても、すぐにTaskの失敗とはしません。

## DP-5 Compute Grant

mass reclaimを、Recovery Eventとして扱います。そして、上限を設けて再度のadmissionを行います。

---

# 9. State ModelとExtension SPIの整合

Queue AdapterとState Store Adapterが、State semanticsを所有しないことを、あらためて確認します。

**Queue SPI**:

- at-least-onceでの配送
- 重複、遅延、backpressureの処理
- Attemptやretryのauthorityは生成しない

**State Store SPI**:

- 永続性
- 条件付きの更新とrevision
- 一意なidentity
- transactionまたはそれと同等の原子性
- 回復のためのscan

State Store SPIは、これらを提供します。ただし、Leaseの期限切れからTaskの失敗を決めるのは、Coreの State Modelです。

---

# 10. Securityの面での横断的な確認

## 10.1 Trust Rootの注入

通常のrequestやadapterの呼び出し側が、Trust Rootを選べないという原則は、全領域で一致しています。

## 10.2 Credentialのroleの分離

Client、Worker、Admin、Authorityのcredentialを分け、roleのcredentialを推移させません。

## 10.3 分類の引き下げ

Client、Worker、Adapter、Schedulerのいずれも、単独では分類を下げられません。

## 10.4 任意のfetch

Workerは、Clientが指定したURLやpathを、標準のData authorityとして利用しません。

## 10.5 任意のplugin

Clientは、Control PlaneのExtension packageを指定することも、installすることもできません。

---

# 11. Application Planeとの境界

EKI Coreが担当するのは、compute placementとexecution fabricです。

アプリケーション固有の業務の意味、認知処理、画面や業務の構造を、Coreに持ち込みません。

未知のApplicationは、External Client Contractと、承認されたExecutorによって統合します。

Application Planeで、TEIなどを利用することはできます。ただし、EKIのscheduler、worker、protocol、lease/retry、sandbox、verification kernelを、TEIに移すことは、Baselineの前提にしません。

---

# 12. 残る研究との整合

## Issue #19 — Learned scheduling

接続先は、SchedulerとLearned EstimatorのSPIです。

Security、Authorization、Data residencyの厳しいfilteringを行ったあとの段階で、Proposalの順位付けに使います。

MVPの正しさは、learned modelがなくても成り立たせます。

## Issue #23 — Real zkVM benchmark

接続先は、Cryptographic Proof VerifierのSPIです。

Proofは、任意のResult Assurance methodです。

MVPの正しさを、real zkVMに依存させません。

---

# 13. 実装の前に固定すべき最小のConcept

次のものを、機械可読なschemaに進める、優先的なContractとします。

1. TaskRequest、公開のTask status、error
2. Workerのidentity、registration、resource advertisement
3. TaskIRとAttemptのidentity
4. PlacementApprovalとLease
5. ExecutorApprovalの参照
6. ArtifactDescriptorとArtifactGrantの参照
7. WorkOrder
8. ResultClaim
9. VerificationResultとAcceptedResultの参照

Contribution、Federation、Attestation、Proofなどは、Coreのextension pointを保ったまま、MVPの最初のschemaから分離できます。

---

# 14. MVPの切り出しの原則

MVPは、「Architectureのすべての機能を縮小したもの」ではありません。重要なInvariantを、実際に通す、最小のvertical sliceとします。

必須とするものは、次のとおりです。

- 悪意のあるClientからWorkerまでの、authorityを分離した経路
- 永続化されたTask、Attempt、Lease
- 2台以上のWorkerでの、placement、損失、retry
- ローカルでのopt-outとreclaim
- 承認されたExecutor
- ArtifactGrantとDigestの検証
- ResultClaim、Verification、Acceptance
- 上限のあるretryと転送
- 構造化されたauditとtelemetry

最初のMVPでは、次のものを必須にしません。

- Federation
- Browser Worker
- TEE
- real zkVM
- learned scheduler
- Contributionと外部のCreditとの連携
- multi-party approval
- site cache
- 特定のcloudに依存した統合

詳細は、`MVP_IMPLEMENTATION_PLAN.md`で定義します。

---

# 15. Protocolの技術の選定は、別のWork Unit

Baselineは、HTTP、gRPC、JSON、Protobufなどを、まだ固定していません。

ただし、外部のClient Contractが言語に中立であること、内部のQueueの製品を直接公開しないことは、固定済みです。

Protocolの技術を比べるときは、少なくとも次の観点を比べます。

- 厳格なparseと正規化
- streaming
- schemaの進化
- 多言語でのコード生成
- browserとの互換性
- 企業のproxyとの互換性
- fuzzingのしやすさ
- 署名やdigestの対象の明確さ

---

# 16. ADRの候補

長く残る判断として、少なくとも次のものを、ADRにする候補とします。

1. 論理的なAuthorityの分離と、物理的なserviceの数
2. 外部のTaskRequestと、内部のWorkOrderの分離
3. Task、Attempt、Leaseの状態モデル
4. Control PlaneとData Planeの分離
5. Workerがoutbound-firstであることと、任意のP2Pを既定にしないこと
6. Extension SPIが、Coreのsemanticsを上書きできないこと
7. Contribution Recordは通貨でも価値でもないこと

ADRは、Baselineのレビューのあと、実装技術の比較で、選択肢とTrade-offが明確になったものから作成します。

---

# 17. 判定

Architecture Baseline 0.1は、**複数の文書の間で整合している**と判定します。

次の工程は、MVPの実装の計画です。

ただし、実装の都合によって、SecurityやState semanticsを弱める必要が生じた場合は、実装に合わせて正本の文書を黙って変えません。RequirementとArchitectureに戻って、再評価します。
