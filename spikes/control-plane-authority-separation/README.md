# Control Plane Authority Separation Spike

## このSpikeの役割

このSpikeは、EKI FabricのControl Plane内部で一つのComponentが侵害されたとき、そのComponentに不要なAuthorityまで当然に得られない構造を、小さな署名付きモデルで確認するものです。

対象は主に次です。

- RQ-028 Data authority is separate from task authority
- RQ-039 End-to-end provenance
- RQ-044 Verification authority is explicit and bounded
- RQ-047 Security-relevant capability claims require a trust basis
- RQ-048 Freshness and revocation of time-bounded authority
- RQ-051 Control-plane authority separation
- SEC-002 Client cannot directly command a Worker
- SEC-010 Role credentials are non-transitive
- SEC-021 Partial control-plane compromise does not imply total authority
- PAS-25 Schedulerだけが侵害される
- QA-01 Security containment

特定のPKI、KMS、HSM、Service Mesh、Microservice構成を採用するものではありません。

## なぜ必要か

SchedulerはPlacementを決めるため、多くの情報を見ます。

そのため実装を急ぐと、

```text
Scheduler
  ├ Taskを受理する
  ├ Workerを選ぶ
  ├ Artifact credentialを発行する
  ├ Executorを承認する
  └ Worker向け命令を署名する
```

という強いComponentになりがちです。

しかしこの構造では、Schedulerの一つの脆弱性がControl Plane全体の権限へ広がります。

EKIでは、Scheduler侵害を「Control Planeだから保証外」と一括りにしません。

少なくとも、

> Schedulerが侵害されても、Schedulerに本来不要なAuthorityまでは得られない

ことをArchitecture比較の前提にします。

## NIST Zero Trustから得られる示唆

NIST SP 800-207系では、access decisionを行うPolicy Engine / Policy Administratorと、Resource accessを実際に強制するPolicy Enforcement Pointを論理的に区別します。

EKIがその構成をそのまま採用する必要はありません。

参考にするのは、

- 判断する主体
- 権限を発行する主体
- 実際に受け入れる主体

を一つの暗黙Trustへまとめない、という考え方です。

## Spikeの最小構造

このSpikeでは次の4 Authorityを分けています。

```text
Admission Authority
  TaskIRを確定する
  ↓ signed AdmissionApproval

Scheduler
  候補Workerを提案するだけ
  ↓ unsigned SchedulerProposal

Placement Guard
  admitted TaskとWorker capabilityを照合する
  ↓ signed PlacementApproval

Artifact Authority
  admitted input + approved WorkerだけにArtifact Grantを発行する
  ↓ signed ArtifactGrant

WorkOrder Authority
  上記の署名とbindingを検証する
  ↓ signed WorkOrder

Worker
  自分向けのWorkOrder Authority署名だけを受理する
```

Schedulerには、このSpike上の署名鍵を一つも持たせません。

## Admission Approval

Admission Authorityが確定する情報には、少なくとも次を含めます。

- Task ID
- Executor digest
- Data Class
- Input Artifact digest

このApproval以降、SchedulerがTaskを都合よく書き換えることはできません。

たとえばConfidential TaskをPublicへ下げた別Taskを作っても、Admission Authorityの署名を作れません。

## Placement ProposalとPlacement Approvalを分ける

Schedulerが作るのは、

```text
Task XをWorker Yへ置きたい
```

というProposalだけです。

Placement Guardは、Admission ApprovalとWorker Profileを使い、

- WorkerのData Class上限
- ExecutorがそのWorkerで承認済みか
- Task ID / Worker IDのbinding

を確認します。

条件を満たした場合だけPlacement Approvalへ署名します。

これにより、侵害Schedulerが高性能だという理由だけでConfidential TaskをPublic-only Workerへ送ることを、別境界で拒否できます。

## Trust Rootを呼び出し側に選ばせない

初期実装レビューで重要な問題が見つかりました。

もしPlacement Guard APIが、

```text
Approve(admission, admissionPublicKey, proposal, worker)
```

のように「どの公開鍵をAdmission Authorityとして信じるか」まで呼び出し側から受け取ると、侵害Schedulerが自分の鍵を渡せます。

そこで最終Spikeでは、Placement Guard自身がAdmission Authorityの公開鍵を保持します。

```text
caller
  └ trust rootを選べない

Placement Guard
  └ pinned admission public key
```

Artifact AuthorityとWorkOrder Authorityも同様に、依存Authorityの検証鍵を内部に固定します。

この原則は、将来SPIFFEやOIDC等を使う場合でも同じです。

「Credentialを提示する側」が「どのIssuerを信頼すべきか」まで自由に決めてはいけません。

## Artifact Grant

Artifact Authorityは、

- 正しいAdmission Approval
- 正しいPlacement Approval
- admitted TaskのInputに含まれるDigest

を確認して初めてGrantを発行します。

Grantは、

- Task ID
- Worker ID
- Artifact digest
- expiry

へbindingします。

そのため、

```text
Task AのArtifact GrantをTask Bへ使う
Worker A向けGrantをWorker Bへ使う
AdmissionにないArtifactへGrantを広げる
期限切れGrantを使う
```

経路をWorkOrder生成前に拒否できます。

## WorkOrder Authority

WorkOrder Authorityは最終的に、

- Admission Authority署名
- Placement Guard署名
- Artifact Authority署名
- Task / Worker / Artifact binding
- Grant expiry
- 全Input分のGrantが揃っていること
- 重複Grantがないこと

を確認し、Worker向けWorkOrderへ署名します。

WorkerはScheduler Proposalを直接受け取りません。

```text
Client Request
  ≠ Scheduler Proposal
  ≠ Placement Approval
  ≠ WorkOrder
```

という境界を維持します。

## Unit Testで確認したこと

2026-09-18時点で、

```bash
go test ./...
```

を実行し、Testは通過しています。

主なScenarioは次です。

1. 正常経路では4 AuthorityのApprovalを連結してWorkOrderを生成できる
2. 侵害SchedulerがConfidential TaskをPublic-only Workerへ提案しても拒否される
3. Executor未承認WorkerへのPlacementを拒否する
4. SchedulerがData Classを下げた偽Admissionを作っても署名検証で拒否する
5. 偽Placement署名からArtifact Grantを取得できない
6. admitted inputでないArtifactへGrantを広げられない
7. 全Input分のGrantが揃わなければWorkOrderを作れない
8. 期限切れArtifact GrantをWorkOrderへ入れられない
9. Scheduler側の別鍵でWorkOrderを偽造してもWorkerが拒否する
10. Worker A向けWorkOrderをWorker Bが受け入れない
11. 呼び出し側が偽Admission鍵をTrust Rootとして差し替えられない
12. 同一Artifact Grantの重複投入を拒否する

## Authority Matrix

このSpikeで意図する最小Authorityは次です。

| Role | Task内容確定 | Placement提案 | Placement承認 | Artifact Grant | WorkOrder署名 |
| --- | --- | --- | --- | --- | --- |
| Admission | 可 | 不要 | 不可 | 不可 | 不可 |
| Scheduler | 不可 | 可 | 不可 | 不可 | 不可 |
| Placement Guard | 不可 | 不要 | 可 | 不可 | 不可 |
| Artifact Authority | 不可 | 不可 | 不可 | 可 | 不可 |
| WorkOrder Authority | 不可 | 不可 | 不可 | 不可 | 可 |
| Worker | 不可 | 不可 | 不可 | 不可 | 検証のみ |

実際のArchitectureでは、これらを何Processへ分けるかは別問題です。

## 「Authority分離」と「Microservice化」は同じではない

このSpikeは4個以上の常駐Serviceを必須にするものではありません。

小規模Deploymentでは、一つのBinary内に実装しながら、

- 別Credential
- 別key handle
- 別interface
- 別Policy decision
- 別Audit event

として論理的に分ける方式も比較できます。

大規模構成ではprocess / host / trust domainまで分離する可能性があります。

重要なのはProcess数ではなく、

> 一つのRoleが侵害されたとき、何のAuthorityまで行使できるか

です。

## Ed25519の位置付け

このSpikeではAuthority bindingを分かりやすく検証するため、Go標準libraryのEd25519を使っています。

これは、

- EKI内部署名形式をEd25519へ確定する
- 独自PKIを作る
- 全Componentがraw private keyを保持する

というArchitecture判断ではありません。

実装段階では、

- workload identity
- KMS / HSM
- short-lived credential
- mTLS
- signed capability
- local IPC credential

などを比較します。

Spikeで重要なのは、**Authorityの根を分けて検証できること**です。

## Partial compromiseで保証できること

このモデルでは、Schedulerだけが侵害された場合、

- Confidentiality requirementを下げる
- 未承認ExecutorをPlacementへ通す
- 任意Artifact accessを作る
- WorkOrderを単独署名する

ことはできません。

Artifact Authorityだけが侵害された場合も、その鍵だけではPlacement ApprovalやWorkOrder署名は作れません。

Admission AuthorityだけでもPlacement / Artifact / WorkOrderの各署名は作れません。

## 保証できないこと

一方、各Authorityが自分の本来権限を悪用することまでは防げません。

たとえばPlacement Guard自体が侵害されれば、誤ったPlacement Approvalを署名できる可能性があります。

WorkOrder AuthorityはこのSpikeでは最終実行Authorityなので、そのsigning authorityが完全に侵害された場合、Workerへ偽WorkOrderを発行できてしまいます。

これはThreat Modelでいうroot signing authority侵害に近い境界です。

Architectureでは、

- WorkOrder signerのkey保護
- signerへ渡す入力の最小化
- high-value Taskでの複数承認
- policy engineとsignerの分離
- HSM / KMS
- signer audit
- key rotation

などを比較する必要があります。

## Scheduler Proposalを信用しない

SchedulerはPerformanceやLocalityを扱うため、複雑な実装になりやすいComponentです。

そのため、

```text
Schedulerが選んだ
        ↓
正しいPlacementである
```

とはしません。

前のArtifact locality SpikeでSchedulerがLocalityを最適化しても、hard Security constraintを別に置いたのと同じ考え方です。

SchedulerはOptimization Componentであり、Security Authorityそのものにはしません。

## Auditへの示唆

各段階を別Decisionとして持つと、

```text
Admission Authority
  TaskIRを確定した

Scheduler
  Worker Bを提案した

Placement Guard
  Worker Bは条件を満たすと承認した

Artifact Authority
  Digest XをWorker Bへ期限付きで許可した

WorkOrder Authority
  これらを束ねてAttemptを発行した
```

というprovenanceを残せます。

これは「最終的に誰が悪かったか」を探すためだけではなく、侵害時にどのAuthorityまでrevocationすべきかを判断する材料になります。

## 次に確認すること

このSpikeの次に重要なのは、署名の存在そのものより運用境界です。

候補は次です。

1. **Federation trust binding**
   - Trust DomainとIssuer keyのbinding
   - rotation / revocation
   - callerがTrust Rootを差し替えられないこと

2. **Executor provenance / admission**
   - Executor digest
   - signing / attestation
   - approved Executor Registry

3. **WorkOrder signer boundary**
   - signerが持つべき最小情報
   - signer compromise時のblast radius
   - high-assurance Taskでの複数Authority

4. **Single-binary deployment**
   - 一つのprocessでもAuthorityを混ぜずに運用できる最小構成

## 参考資料

- NIST SP 800-207 — Zero Trust Architecture
  - https://csrc.nist.gov/pubs/sp/800/207/final
- NIST SP 800-207A — A Zero Trust Architecture Model for Access Control in Cloud-Native Applications in Multi-Cloud Environments
  - https://csrc.nist.gov/pubs/sp/800/207/a/final
- NIST Implementing a Zero Trust Architecture — Architecture and Builds
  - https://pages.nist.gov/zero-trust-architecture/VolumeB/architecture.html
