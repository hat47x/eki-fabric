# 最小実装計画（MVP Implementation Plan）— Architecture Baseline 0.1

Status: **Planning Baseline — 2026-09-21**

## 1. 目的

この文書では、Architecture Baseline 0.1を参照実装へ移すため、最初のMVPに含める範囲を定めます。

MVPの目的は、機能の数を増やすことではありません。

最小の一連の処理を通し、EKI Fabricが守る中心的な不変条件（Invariant）が実際に成り立つかを確かめます。

中心となるE2E Goalは、次のとおりです。

```text
untrusted Client
  ↓
strict TaskRequest / Admission
  ↓
durable Task / Attempt
  ↓
policy-constrained Placement
  ↓
signed WorkOrder
  ↓
2+ outbound Workers
  ↓
Artifact Grant / Digest verification
  ↓
approved Executor
  ↓
ResultClaim
  ↓
Verification / Acceptance
  ↓
Worker reclaim / loss
  ↓
bounded retry to another Worker
  ↓
AcceptedResult
```

この経路を通すことで、個々の技術のデモではなく、Fabricとしての意味を検証します。

---

# 2. MVPで必ず守るもの

## 2.1 Security

- 認証済みのClientも、untrustedである。
- Client Requestを、Workerに直接渡さない。
- Clientが、具体的なWorkerを指定できない。
- 任意のcode、任意のURL、任意のfilesystem pathを、標準のTask APIから実行できない。
- 承認済みのExecutor Digestに結び付ける。
- ArtifactGrantと、Task submissionのauthorityを分離する。
- WorkOrderの署名者だけで、前提となるAuthorityを捏造できない。
- Worker側で、ローカルな検証を行う。
- Local Governorの拒否権。
- ResultClaimを、AcceptedResultに直接昇格しない。

## 2.2 Reliability

- Task != Attempt
- retry = 新しいAttempt
- at-least-onceの配送
- 重複したWorkOrderの配送への耐性
- Leaseのexpiryとfencing
- Control Planeの再起動後の回復
- Workerの喪失を、通常の状態として扱う
- 上限を設けたretry

## 2.3 Network / Data

- 大きなArtifactを、Controlのmetadataの経路に通さない。
- Artifact Digestの検証
- cache localityは、optimizationのhintにとどめる。
- inputとoutputの転送のbudget
- Workerに、長い有効期間のstorage credentialを渡さない。

## 2.4 Operability

- Task、Attempt、Lease、Worker、Decisionの相関
- placementをrejectした理由
- reclaimとretryの理由
- 構造化されたlogとmetrics
- operatorが、内部の状態を追える。

---

# 3. 最初のMVPに含める機能

## 3.1 External Client API

最低限、次のものを含みます。

- Task submit
- Task getとstatus
- Task cancel
- Resultの取得
- Artifact ingest intentとupload

Task APIは、言語に中立なcontractとします。

具体的なTransportは、別の比較で決めます。ただし、MVPでは、Browser以外のClientからも扱いやすく、企業のproxyと共存しやすいSurfaceを、優先します。

## 3.2 Task Admission

実装するものは、次のとおりです。

- 厳格なschemaの検証
- Principalのbinding
- Idempotency Keyとrequest digest
- Executorの論理的な要求の解決
- input Artifactのauthorization
- resource budget
- retry budget
- Data Class
- side-effect mode = noneを、最初の標準にする

最初のMVPでは、external side-effectの実行は、必須にしません。

State Modelの上のUNKNOWNは、保持します。ただし、実際のside-effect Adapterは、後続のmilestoneに送ります。

## 3.3 永続的なTask / Attempt / Lease State

必要なPropertyは、次のとおりです。

- 一意なTask ID
- TaskIR Digest
- Attempt ID
- state revisionと条件付きの更新
- Lease ID、revision、expiry
- cancellation intent
- retry budget
- 再起動後の回復
- 永続的なWorkOrder intent

Queueだけを、正本にしません。

### RQ-014 Workload Suitability Gate

TaskがSecurity、Data、Resourceのfeasibilityを満たしても、それだけで、Fabricに送る根拠にはしません。RQ-014をMVPの実行の経路に反映するため、Baseline Schedulerの前に、決定的なWorkload Suitability Gateを置きます。

```text
Task Admission
  ↓
Security / Data / Resource feasibility
  ↓
Workload Suitability
  ├─ FABRIC_NOT_JUSTIFIED
  ├─ NEEDS_MEASUREMENT
  └─ FABRIC_JUSTIFIED
           ↓
       Scheduler
```

このGateは、次のAuthorityを持ちません。

- 具体的なWorkerを選ばない。
- Security、Data、Resourceのfeasibilityを緩和しない。
- Clientの申告だけを、高い保証のbaselineの事実に昇格しない。
- `FABRIC_NOT_JUSTIFIED`を理由に、EKI Coreが、local executionを勝手に開始しない。
- Placement、Lease、WorkOrderを発行しない。

Reference Implementationの、最初のpureな境界は、`internal/core/suitability`とします。Fabric pathの費用の要素は、値がないものを0とみなさず、**estimate済み、明示的に対象外、unresolved**を区別します。baselineまたはFabric pathのcomponentが不足する場合は、`NEEDS_MEASUREMENT`で止めます。

最初に比較する要素は、次のとおりです。

- suitability decision
- admissionとplacementのdecision
- dispatch
- queue
- inputの転送
- compute
- retryのpenalty
- outputの転送
- verification

すべてを、常に測定することを、要求するものではありません。ただし、測っていないものを、暗黙に0として畳みません。fixtureや特定のworkloadで対象外とする場合は、その理由を明示します。

Decision receiptは、少なくとも、次のものを保持できることを、Referenceの方向とします。workload digest、policy revision、evaluatorのversion、decision、reason code、Evidenceの参照、unresolvedなinput、比較した時間、decision basis digest。同じworkload名でも、Evidenceのbasisが変われば、過去の判断を、現在のdecisionとして再利用しません。

最初のAcceptanceは、`spikes/artifact-locality-cost`で、すでに確認した数値の例を、再利用します。

- 8 GiB input / 100 MiB/s / remote compute 20s / baseline 60sでは、input transferの約81.92sを含めて、`FABRIC_NOT_JUSTIFIED`になる。
- 10 MiB input / 100 MiB/s / remote compute 20s / baseline 60sでは、単純なfixtureの上で、約20.1sとなり、`FABRIC_JUSTIFIED`になる。
- baselineがない場合は、`NEEDS_MEASUREMENT`になる。
- hard feasibilityの違反は、Suitability decisionに変換せず、既存のAdmissionとPolicyの境界で止める。
- output transferと、Suitability / Scheduler自身のdecisionのoverheadも、測定された場合は、Fabric path costに含める。

このpureな境界を、M0とM1の実行の経路に接続するのは、永続的なTask / Attempt / Leaseと、署名付きWorkOrderという、既存の優先作業を崩さないためです。remote placementを通常の経路にする前に、行います。

### SuitabilityからSchedulerへの、型付きhandoff

Suitability decisionを、単なるenumとして、Schedulerに渡しません。Reference Implementationでは、現在のEvidenceを使ってSuitabilityを評価した、同じ呼び出しだけが、後段に渡せる`FabricEntry`を生成します。

```text
Suitability Input
      ↓ EvaluateForFabricEntry
DecisionReceipt ── audit / explanation
      │
      └─ FABRIC_JUSTIFIED only
             ↓
         FabricEntry
             ↓
          Scheduler
```

`DecisionReceipt`は、監査と説明のためのEvidenceであり、routing authorityではありません。保存済みのreceiptや、Clientが渡した`FABRIC_JUSTIFIED`というenumから、`FabricEntry`に変換するAPIは、持たせません。Control Planeを再起動した後などで、再度routingする場合は、現在のEvidenceから、Suitabilityを再評価します。

`FabricEntry`は、次のものに結び付けます。Admitted TaskのTask IDとTaskIR digest、Suitabilityで比較したworkload digest、Suitability decision basis digest、policy revision、evaluatorのversion。別のTask向けのEntryや、同じTask IDでもTaskIRが変わったEntryを、再利用しません。Worker、Attempt、Lease、ArtifactGrant、WorkOrder、Executorのauthorityは含めません。したがって、`FabricEntry`が存在することは、Placement Approvalではありません。「この正確なTaskとTaskIRを、Fabric placementの候補に進める根拠が、現在の比較で成り立った」という、前提のEvidenceに限ります。

Baseline Schedulerのproductionのentry pointは、生のTaskRecordだけでなく、有効な`FabricEntry`、または同等の型付きの前提を要求する方向とします。これによって、`FABRIC_NOT_JUSTIFIED`と`NEEDS_MEASUREMENT`の経路では、Scheduler Proposal自体を作りません。後段のPlacement、Lease、ArtifactGrant、WorkOrderのauthorityが、発行されない構造に接続します。

## 3.4 Baseline Scheduler

最初は、決定的なheuristicとします。

最初のReference実装は、`internal/core/scheduler`で、**権限を持たないPlacement Proposal**だけを作ります。

入力には、生のTaskだけでなく、同じTask IDとTaskIR digestに結び付いた、有効な`FabricEntry`を必須とします。`FABRIC_NOT_JUSTIFIED`または`NEEDS_MEASUREMENT`では、`FabricEntry`が生成されません。そのため、Scheduler Proposalも生成できません。

最初のrankingは、万能のscoreにまとめず、次の辞書式の順序に限定します。

1. planningの上で適格な候補だけを残す。
2. 検証し、正規化したplanning viewの上で、cache hitを優先する。
3. 転送のbyte数の見積もりが少ない候補を優先する。
4. 利用できるCPUのmillisが大きい候補を優先する。
5. 同値のときは、Worker identityの辞書順で、決定的にtie-breakする。

`PlanningEligible`や、cacheとcapacityは、Schedulerが使うplanningの入力です。Security Authorityではありません。Clientのrequest fieldを、そのまま候補に流しません。候補のData Class、Assurance、Provider state、Executorの対応などは、PLC-1が独立に再確認します。

Proposalは、少なくとも、次のものに結び付けます。

- Task ID
- TaskIR digest
- Attempt ID
- 提案するWorker identity
- Suitabilityのworkload digest
- Suitabilityのdecision basis digest
- Suitabilityのpolicy revision
- 選択に使ったrank basis

Proposalには、Authority role、署名、Lease、ArtifactGrant、WorkOrderのauthorityを持たせません。


Hard filter:

- Workerがonlineで、適格である
- Executorに対応している
- Data ClassとWorker profile
- resourceのfeasibility
- Artifactとnetworkのbudget

Soft ranking:

- cache locality
- 転送のbyte数の見積もり
- 単純なcompute capacity

MLやlearned estimatorは、MVPの必須にしません。

## 3.5 Placement Authorization

Scheduler Proposalを、別の境界で再評価します。

少なくとも、次のものを確認します。

- TaskとAttemptのbinding
- Worker Identity
- Executorへの対応
- Data Class
- 有効なProviderとWorkerの状態
- resourceのfeasibility

Reference coreでは、`internal/core/placementauth`が、現在のEvidenceを再確認します。そして、すべてのhard conditionを満たした場合だけ、**署名前のPLC-1 ApprovalDraft**を生成します。Schedulerのrankや`PlanningEligible`だけでは、ApprovalDraftを作れません。Policy revisionのずれ、Task、Attempt、Workerのbindingの不一致、Data Class、Assurance、Provider state、resource、転送budgetの失敗は、いずれもfail closedとします。

この境界の最初の、コンポーネントをまたぐconformanceを、`conformance/coreflow`に置きます。そして、次のことを固定します。

```text
FABRIC_NOT_JUSTIFIED / NEEDS_MEASUREMENT
  -> no FabricEntry
  -> no Scheduler Proposal
  -> no PLC-1 ApprovalDraft

FABRIC_JUSTIFIED
  -> Scheduler Proposal
  -> any hard Placement failure
  -> no PLC-1 ApprovalDraft
```

すべての条件を満たした場合も、現段階で生成するのは、PLC-1が署名する前のDraftまでです。Suitability、Scheduler、Placement Authorizationが、Lease、ArtifactGrant、WorkOrderを自己生成しないことを、境界とします。

### P2 Placement Approvalのpayloadの境界

PLC-1の`ApprovalDraft`を、そのまま、Lease Authorityに渡しません。P2の`eki.placement-approval/v1`を、決定的なCBORとして読み、Decision固有の意味に変換する境界を、独立させます。

Reference実装の候補として、次のものを追加します。

- `internal/core/decisionpayload`: P2の意味を表す、型付きの`PlacementApproval`
- `internal/spi/decisiondecoder`: 生のpayload bytesを、型付きのDecisionに変換するSPI
- `internal/adapters/reference/decisiondecoder/cbor`: RFC 8949のCore Deterministic Encodingを要求する、厳格なCBOR adapter

Decoderは、少なくとも、次のものをfail closedにします。

- 重複したmap key
- 長さを定めないitem
- 未知のfield
- 想定外のCBOR tag
- 決定的でないencoding
- P2 v1以外のschema version
- Decision kindとAuthority roleの不一致
- 不正なTask、Attempt、Workerのbinding
- SHA-256以外のDigest、または32 byteでないDigest
- 不正なissuedとexpiryの期間
- Evidenceの件数の上限を超えたもの

ただし、Decoderが成功したことは、Authorizationではありません。

```text
PLC-1 ApprovalDraft
  -> canonical Placement Approval payload
  -> PLC-1 signing
  -> strict COSE envelope decode
  -> strict P2 payload decode
  -> key / role / principal / signature verification
  -> current Task / Attempt / policy revalidation
  -> LSE-1 prerequisite
  -> Lease Decision
```

`decisionpayload.PlacementApproval.Valid()`が表すのは、「閉じたP2のpayloadとして、意味が成り立っている」ことだけです。署名の検証済み、現在のpolicyで有効、Leaseを発行できる、という意味にはしません。`verified=true`のような、呼び出し側が渡すflagを、この間の境界に追加しません。

このReference adapterは、外部のCBOR libraryに依存します。そのため、依存moduleを実際に取得して、focused testを実行できるまで、採用済みとは扱いません。依存を取得できない環境で、GoのAPIの形だけを確認した結果を、Security Evidenceに読み替えません。

### LSE-1 — Policy EpochとLease reservationのlinearization

P2 Placement Approvalを暗号学的に検証し、current Task / Attempt / Workerを再確認しても、PolicyのcurrentnessをLease予約と別transactionで確認するだけでは不十分です。

次のraceをfail closedにします。

```text
T1  LSE-1 reads policy P42
T2  Placement(P42) passes revalidation
T3  ADM-1 revokes P42
T4  stale LSE-1 tries to reserve Lease
```

M0では、State Storeへ次を追加します。

- 単調増加する`Policy Epoch`
- current policy revisionと`active / revoked`
- append-onlyなPolicy history
- Policy Stateのcompare-and-swap
- Lease reservationへ渡す`expectedPolicyEpoch`
- Lease recordへ残す`PolicyEpoch`

Reference `leaseissue.Service`は、独立した`CurrentPolicyRevision() string`を使いません。Task / Attempt / Leaseと同じdurable State ownerからcurrent Policy Stateを読み、そのepochをLease reservationの前提にします。

Memory adapterでは同じmutex、SQLite adapterでは同じtransactionの内側で、少なくとも次を再確認します。

- current policy epochがexpected epochと一致する
- current revisionがPlacement Approvalのrevisionと一致する
- current stateがactiveである
- Task / Attempt / Worker bindingがcurrentである
- active Leaseがない
- FenceEpochを単調に採番できる

一つでも変わっていれば、新しいLease reservationもFenceEpochも作りません。同じPlacement Decisionのreplayも、Policyがrevokeまたは置換された後は返しません。

SQLiteのReferenceでは、Adapterを迂回した直接INSERTでもstale policy Leaseを作れないtriggerを置きます。Policy historyもappend-onlyとし、epoch飛び、revoked revisionの再活性化、時刻の逆転を拒否します。

この段階で作る`LeaseRecord`は、依然として内部のdurable reservation stateです。Workerが依拠できる署名済みP2 Leaseは後続のsigning boundaryで生成します。そのboundaryでも、`PolicyEpoch=0`のlegacy recordや、current policyから外れたreservationを署名対象にしません。

### LSE-1 — 署名済みLeaseの発行を耐久化する

Lease reservationと署名処理の間には、もう一つ境界を置きます。署名関数が成功したことだけでは、Worker向けAuthorityを発行済みとみなしません。

```text
durable Lease reservation
  -> canonical Lease payload
  -> LSE-1 signing
       # signed bytesはまだ外へ出さない
  -> current Policy / Task / Attempt / signing premiseを再確認
  -> durable emission commit
  -> EMITTED
  -> Worker delivery
```

Reference State Storeでは、Lease reservationに対して次の状態を区別します。

- `RESERVED`: Lease reservationはあるが、発行記録はない。
- `EMITTED`: exact signed envelopeを耐久的に確定した。
- `ABORTED`: Worker向けAuthorityを発行しないことを耐久的に確定した。

`EMITTED`のcommitを、新しいLease Authority発行の線形化点とします。commit前にPolicy、Task、Attemptなどの前提が変われば、署名済みbytesを外へ出さず、発行を止めます。

一度`EMITTED`になったLeaseを再送するときは、再署名しません。耐久記録にある同じsigned bytesを返します。これによって、再送やControl Plane再起動が新しいAuthorityを増やすことを防ぎます。

`ABORTED`では、対応するAttemptを同じ保存境界で`closed`へ進めます。新しいPlacementへ進むことはできますが、消費済みのFenceEpochは戻しません。同じPlacement DecisionからLeaseを作り直すことも許しません。

このState Store境界は、署名済みbytesとmetadataの暗号学的な対応を、それ自体で証明するものではありません。後続のLSE-1 compositionでは、canonical Lease payloadの生成、LSE-1 keyでの署名、signed envelopeの再decodeと検証、Trust Bundleとkeyのcurrentness確認を行った型付き結果だけを、emission commitへ渡します。呼出側の自己申告fieldをAuthority証明として扱いません。

## 3.6 WorkOrder

最初のWorkOrderは、少なくとも、次のものに結び付けます。

- WorkOrder ID
- Task IDとTaskIR Digest
- Attempt ID
- Worker ID
- Placement Approvalの参照
- Leaseの参照
- Executor Approvalの参照とDigest
- ArtifactGrantの参照
- resource limit
- network policy
- output binding
- expiry

署名の形式の具体的な方式は、ProtocolとSignerの実装で決めます。

## 3.7 Worker Agent

2台以上のNative Workerで、E2E Testします。

Worker Agentの責務は、次のとおりです。

- outboundのsession
- Worker Identity
- Resource Advertisement
- WorkOrderの検証
- Local Governor
- Artifactのfetchと、Digestの確認
- Executor Runtime
- ResultClaim
- Leaseのheartbeatとrenewalの要求
- ローカルでの停止とreclaim

## 3.8 Local Governor

最初の対象は、次のとおりです。

- 手動のopt-out
- workerの有効化と無効化
- CPUのavailability
- memoryの上限
- userがactiveであることのsimulationとhook
- WorkOrderのwall-timeでの停止

Platform固有のhard enforcementは、Reference Runtimeのcapabilityとして、段階的に導入します。

実証されていないResourceを、HARDと広告しません。

## 3.9 Executor Runtime

最初は、**承認済みのcommand executor**を、Reference Runtimeとします。

安全の条件は、次のとおりです。

- Adminが登録した、commandとbinaryだけ
- Clientが指定する、command lineのexecutable pathは禁止
- 正確なExecutor Digestとversion
- parameterのschema
- 範囲を限った作業ディレクトリ
- 既定ではnetworkなし
- outputの上限

WASMは、早い段階での任意の候補として、次のmilestoneの候補にします。

## 3.10 Artifact Store

Reference implementationは、小規模なProfileで動く、単純なbackendから開始します。

必要なPropertyは、次のとおりです。

- 変更できないcontent
- digest
- size
- 範囲を限ったreadとwrite
- uploadの確定
- outputの保存

Filesystemを使ったreference implementationを候補とします。S3互換のAdapterは、早い段階での任意の候補とします。

## 3.11 ArtifactGrant / Data Access Resolver

最低限、次のものを強制します。

- Task、Attempt、Workerのbinding
- Artifact Digest
- readとwriteのoperation
- expiry
- outputのsizeの上限

最初のReferenceでは、同じHostの、単純なobject serviceでもかまいません。ただし、Workerに、rootのfilesystem pathや、store全体のcredentialを渡しません。

## 3.12 Result Verification / Acceptance

最初のVerification methodは、次のとおりです。

- Result ArtifactのDigestと、構造の妥当性
- Executor固有の、決定的なvalidator

少なくとも一つのdemo workloadでは、同じInputに対する、期待される決定的なresultを、validatorで確認します。

Workerが成功のexit codeを返したことだけを、Acceptanceの根拠にしません。

## 3.13 Telemetry / Audit

最初から、少なくとも次のものを追跡します。

- Task ID
- Attempt ID
- Worker ID
- Lease ID
- WorkOrder ID
- placementの理由
- rejectの理由
- retryの理由
- reclaimの理由
- Artifactの転送のbyte数
- Result acceptance

OpenTelemetryのsemantic attributesを、Referenceの方向とします。

---

# 4. 最初のMVPに含めないもの

Architectureの上のExtension Pointは保持します。ただし、最初のMVPの完成の条件には含めません。

- Browser Worker
- Federation
- 組織をまたぐPublic-interest Grantの実運用
- Remote Attestation
- Confidential Computingのkey release
- 実際のzkVMのproof
- learned scheduler
- GPU固有のhard enforcement
- attestされたContribution Metering
- 外部のCreditとValueのmapping
- multi-party approval
- site cache
- 任意のP2P
- cloud固有のresource provisioning
- 汎用のexternal side effect
- Sagaとcompensation
- secret managerとの完全な統合

これらをあとから入れても、Core ContractとState Modelを変更しないことが、Architectureを検証する点です。

---

# 5. MVP workload

一つのdemoだけに最適化しないため、性質の異なる3種類を、dogfoodします。

## W1 — Deterministic text / manifest transform

目的は、次のとおりです。

- 小さなArtifact
- 決定的
- validatorを使える
- CPUのみ

確認することは、次のとおりです。

- TaskRequest
- Executor parameter
- ArtifactGrant
- Result verification

## W2 — Large cached Artifact compute

目的は、次のとおりです。

- 数百MiBから、GiB相当のArtifact
- localityと転送のcost
- cacheのhitとmiss

実際の巨大なdataを必須にはしません。Test fixtureや、疎な、または生成したdataで、Network pathを測定できます。

確認することは、次のとおりです。

- cache
- 転送のbudget
- Schedulerのranking
- retryの転送

## W3 — Interruptible long-running compute

目的は、次のとおりです。

- user reclaim
- Leaseのexpiry
- Workerの喪失
- retry
- 後から追加する、任意のcheckpoint

確認することは、次のとおりです。

- A1 -> Worker 1
- reclaimまたは喪失
- A1をclose
- 上限を設けたRecovery
- A2 -> Worker 2
- AcceptedResult

SEI、SUI、TEIのworkloadを、後続のdogfoodに利用できます。ただし、MVP Coreに、そのDomain semanticsを埋め込みません。

---

# 6. End-to-End Acceptance Scenario

最低限のAcceptanceは、次のとおりです。

1. Client Aが、Artifactをingestする。
2. Client Aが、Idempotency Key付きのTaskRequestをsubmitする。
3. Task Admissionが、TaskIRとBudgetを確定する。
4. Schedulerが、Worker 1を提案する。
5. Placement Authorityが、Worker 1を承認する。
6. Lease、ArtifactGrant、WorkOrderを発行する。
7. Worker 1が、WorkOrderの前提を再検証する。
8. Worker 1が、Artifactを取得して、Digestを検証する。
9. Executorが、上限のある環境で、実行を開始する。
10. User reclaimを発生させる。
11. Local Governorが、Worker 1を停止する。
12. Leaseのrenewを止める。
13. Task Stateが、A1をreclaimとしてcloseする。
14. retryと転送のbudgetを評価する。
15. 新しいAttempt A2を作成する。
16. Worker 2に、新しいWorkOrderを発行する。
17. Worker 2が、ResultClaimを返す。
18. Validatorが、VerificationResultを生成する。
19. Result Acceptanceが、AcceptedResultを生成する。
20. Task Stateが、COMPLETEDに遷移する。
21. Clientは、Workerのtopologyを知らずに、Resultを取得する。

この一本を、自動のIntegration Testとして成立させます。

---

# 7. Malicious Client Acceptance

最低限の、negativeなE2Eは、次のとおりです。

- 具体的なWorker IDの指定を拒否する。
- 任意のexecutable pathを拒否する。
- 任意のURLのinputを拒否する。
- Data Classのdowngradeを拒否する。
- 大きすぎるrequestを、Ingressで拒否する。
- retry budgetの増幅を拒否する。
- 同じIdempotency Keyで、request digestが異なるものを拒否する。
- outputのbudgetを超えたものを停止する。

---

# 8. Malicious Worker Acceptance

- 誤ったWorkOrderのWorker IDを拒否する。
- 古いLeaseを拒否する。
- 期限切れのArtifactGrantを拒否する。
- 誤ったArtifactのbytesを、Digestの不一致で拒否する。
- 偽のResultClaimを、validatorでrejectする。
- 重複したResultClaimを、冪等に処理する。
- 古いAttemptから遅れて届いたResultで、Leaseを復活させない。
- 偽のContributionやResourceの自己申告を、高い保証の事実にしない。

---

# 9. Restart / Failure Acceptance

- Control Planeの再起動後に、Task、Attempt、Leaseを復元する。
- outboxの配送をreplayしても、新しいAttemptを増やさない。
- Worker sessionの再接続。
- 古いLeaseのheartbeatをrejectする。
- Artifact Storeの一時的な障害を、Taskのsemanticな失敗にしない。
- Worker 1の喪失の後に、上限を設けたretryを行う。

---

# 10. 最初のComponentの切り出し

論理的なcomponentは、次のとおりです。

```text
cmd/
  eki-control
  eki-agent

core/
  contracts
  admission
  authority
  taskstate
  placement
  recovery

worker/
  session
  governor
  workorder
  runtime
  cache

spi/
  statestore
  delivery
  artifact
  identity
  signer
  executor
  scheduler
  verification
  telemetry

adapters/
  reference implementations

protocol/
  external
  worker

conformance/
  contract
  malicious-client
  malicious-worker
  restart
```

これは、MVPの計画の上での、論理的な切り出しです。

実際のRepositoryのdirectoryを、このまま凍結するものではありません。

Baseline reviewの後の、Repository Layoutの作業で確定します。

---

# 11. Reference Adapterの優先順位

## M0で必須

- State Store: ローカルで永続的な、relationalまたは組み込みの候補
- Delivery: 永続的なoutboxと、単純な配送
- Artifact Store: filesystemとobjectの抽象
- Identity: 開発用で、管理されたローカルのreference
- Signer: ローカルで保護された、開発用のreference key
- Executor: 承認済みのcommand executor
- Scheduler: 決定的なheuristic
- Verification: 決定的なアプリケーションのvalidator
- Telemetry: 構造化されたlogと、OTLPに対応できる経路

## M1の早い段階

- OIDC Identity
- S3互換のArtifact Store
- WASM Runtime
- OSのhard-limit backend

## 研究、または後続

- TEE Attestation
- zkVM
- learned estimator
- Cloud providerのmeter
- federationのadapter

---

# 12. State Storeを選ぶための要件

製品名よりも先に、必要なPropertyを固定します。

- transaction
- 一意性の制約
- compare-and-swapとrevision
- 永続的な、再起動への対応
- recoveryのための、indexを使ったscan
- outbox pattern
- 小規模なdeploymentで、無理なく運用できる
- 将来の、複数instanceへの道筋

MVPでは、分散consensus databaseを必須にしません。

組み込み、または単一nodeのrelationalな実装から始めても、SPI Contractによって、あとから置き換えられるようにします。

---

# 13. Deliveryを選ぶための要件

- at-least-once
- 重複した配送
- ackとretry
- 上限のあるbacklog
- poisonの扱い
- backpressure
- messageのidentity

Queueの製品を、Worker Protocolに露出しません。

MVPでは、State StoreのOutboxとWorker Gatewayによる配送で開始します。NATSなどは、負荷の要件が出た後に、Adapterとして比べられます。

---

# 14. Protocolを固定する順序

いきなり、すべてのschemaを固定しません。

## P0

- 外部のTaskRequest
- 公開のTaskStatusとError
- Artifactのingestとdescriptor

## P1

- Worker helloとResourceAdvertisement
- LeaseとWorkOrderの配送のenvelope
- heartbeatとrenewal
- ResultClaim

## P2

- 内部の、署名付きDecisionのschema
- Adapter protocol
- VerificationResult

内部の実装のstructを、公開のschemaに流用しません。

---

# 15. Security Conformance Suite

実装と同時に、Testのcontractを作ります。

分類は、次のとおりです。

## C1 Parser / Protocol

- fuzz
- 大きすぎるもの
- 重複したfield
- 不正なencoding
- 未知のcritical field

## C2 Authority Binding

- 誤ったissuer
- 誤ったsubject
- 期限切れ
- 古いrevision
- 誤ったWorker
- 誤ったTaskとAttempt

## C3 State / Lease

- 重複した配送
- 古いwriter
- 古いepoch
- 再起動
- 並行したretry

## C4 Artifact

- digestの不一致
- 期限切れのGrant
- pathやURLのinjection
- uploadの上限

## C5 Worker

- WorkOrderの改変
- capabilityの拡大
- ローカルでのreclaim

## C6 Result

- 偽のresult
- 重複したresult
- 遅れて届いたresult
- 矛盾するresult

---

# 16. Milestones

## M0 — Executable Skeleton

成果は、次のとおりです。

- repositoryの実装のlayout
- coreのIDとcanonical contract
- State Store adapter
- Task submitとstatus
- Workerの登録
- 一つのWorkerでのローカルな実行

Gate:

- 再起動しても、Task identityを保持する
- Client RequestとWorkOrderが、別のtypeとschemaである

## M1 — Secure Two-Worker Fabric

成果は、次のとおりです。

- 2台以上のWorker
- schedulerとplacementの分離
- Leaseとfencing
- 署名付きWorkOrder
- ArtifactGrant
- command executor

Gate:

- ClientがWorkerを選べない
- 古いLeaseをrejectする
- 誤ったArtifactをrejectする

## M2 — Reclaim / Recovery

成果は、次のとおりです。

- Local Governorのopt-out
- Workerの喪失
- Recovery Candidate
- retry budget
- 転送のbudget

Gate:

- A1の喪失 -> 上限のあるA2
- 配送のretryで、重複したAttemptを作らない

## M3 — Result Assurance

成果は、次のとおりです。

- ResultClaim
- VerificationResult
- AcceptedResult
- 決定的なvalidator

Gate:

- Workerの成功のclaimだけでは、Taskは完了しない

## M4 — Operability / Conformance

成果は、次のとおりです。

- 構造化されたaudit
- OTLPのmetrics
- malicious Clientのsuite
- malicious Workerのsuite
- restartとchaosのsuite

Gate:

- 主要なE2E scenarioが、自動化されている
- invariantのregressionが、mergeを止める

## M5 — Early Adapters

候補は、次のとおりです。

- OIDC
- S3互換のstore
- WASM
- platform固有のhard enforcement

Gate:

- Adapterを置き換えても、Coreのsemanticsが変わらない

---

# 17. MVP Definition of Done

MVPの完成は、単に、Taskが別のPCで動くことではありません。

次のことを満たすことを、要求します。

1. malicious Clientが、Workerを直接命令できない。
2. 2台以上のWorkerに、安全にPlacementできる。
3. Workerの喪失やopt-outがあっても、Taskを安全に再配置できる。
4. retryが、新しいAttemptとして、監査できる。
5. Artifactのaccessが範囲を限られ、Digestが検証される。
6. 承認されていないExecutorを、実行しない。
7. ResultClaimが、VerificationとAcceptanceを通る。
8. Task、Attempt、Leaseが、Control Planeの再起動の後も、復元できる。
9. queueやdeliveryの重複が、実行のAuthorityを増やさない。
10. Networkとretryのbudgetに、上限がある。
11. Local Governorが、中央より強く停止できる。
12. Clientに、Workerのtopologyを、不要に公開しない。
13. Auditで、主要なDecisionの連鎖を追える。
14. Reference Adapterを交換しても、Coreのsemanticsが変わらない。

---

# 18. CIについて

現状、GitHub Actionsの残量によるCIのerrorは、Project側で既知の条件として扱います。

実装を開始するときは、localでのtestと、再現できるcommandを、必ず文書化します。CIが使えることだけを、Evidenceにしません。

CIが利用できるようになった段階で、conformance suiteを、段階的にworkflowに接続します。

---

# 19. Branch / milestoneのルール

これまでと同じく、milestoneの中でも、レビューできるwork unitごとに、次の流れを維持します。

```text
latest main
  -> one purpose branch
  -> test / docs
  -> PR
  -> squash merge
  -> branch realign to main
```

複数のmilestoneの、マージされていないbranchを、連鎖させません。

---

# 20. 次の作業

Baseline Reviewをmergeした後の、次の作業は、Implementation Entry Planningとして、次の順序で進めます。

1. Protocol technologyの比較
2. P0の機械可読なschema
3. Repositoryの実装のlayout
4. M0 executable skeleton

Protocolの選定とRepository layoutは、別のwork unitにします。マージされていないbranchから、実装のbranchを派生させません。

## 20.1 2026-09-27 current mainlineのcheckpoint

Section 20の最初の列挙は、すでに一部が完了しています。そのため、現在の実装の入口を、ここで更新します。

完了済みのものは、次のとおりです。

- Protocol technologyの比較
- P0の機械可読な外部schema
- Repositoryの実装のlayout
- M0 executable skeletonの基礎（Go module、binary、型付きID、TaskRecord、State Store SPI、メモリ上のadapter）
- P1 Worker Planeのschema
- P2 Signed Decisionのschemaと暗号のprofile
- 厳格なCOSE_Sign1のwire profile
- COSE decoderのSPI、共通のconformance harness、negative vector

未達で、優先するものは、次のとおりです。

1. **M0の永続的なState Store** — SQLiteのreference adapterを実装し、Task、Attempt、Lease reservation、FenceEpoch、Policy Epochまで耐久化しています。再起動後のidentityとrevision、Leaseのfencing、Placement replay、Policy revokeとの競合をconformanceで固定しています。未実装の中心は、outboxとWorker deliveryです。
2. ~~**P2の具体的なCOSE decoder**~~ — 厳格なCOSE_Sign1 decoderと、Signature1 encoderを、実装済みです（2026-10-06、ADR-0005 §18）。
   - Placement Approvalのpayloadについては、型付きのCore contract、decoder SPI、厳格なCBORのReference adapterを、`feature/placement-approval-payload-decoder`で実装中です。
   - RFC 8949の決定的な再エンコードの比較、閉じたschema、重複したkey、長さを定めないitem、未知のfield、P2のbindingについての、negativeなtestを含みます。
   - 外部のmoduleを取得したfocused testは、2026-10-06に実施済みです（Go 1.27.0、WSL2）。
   - COSE envelopeのdecode -> payloadのdecode -> 暗号の検証 -> 現在のstateでの再検証という組み合わせは、`internal/core/placementverify`に、実装済みです（2026-10-06）。LSE-1自体は、未実装です。
3. 永続的なstateを前提に、M0のE2Eを、Task submitとstatus -> Workerの登録 -> 一つのWorkerでのローカルな実行まで接続する。
   - Task submit、status、cancelは実装済みです。再起動後も、Task、冪等性、未完了Task数の上限を保持します。
   - Attempt、Lease reservation、FenceEpoch、Policy Epoch、検証済みPlacementからLease reservationへ進むLSE-1の前半は実装済みです。
   - 次の中心作業は、canonical Lease payloadの署名、署名済みbytesの耐久的な発行確定、Worker deliveryとWorker側検証、一つのWorkerでのローカル実行です。

`learned scheduler`は、Section 4と11で定義したとおり、MVPの必須ではありません。Researchの実装がmainに存在しても、上記のM0とM1のgateの優先順位を変更しません。

外部のGo moduleを取得し、versionをlockし、testできない環境では、依存の追加だけを先にcommitしません。検証できる環境が得られるまで、既存のcontractとconformanceを、正本として保持します。
