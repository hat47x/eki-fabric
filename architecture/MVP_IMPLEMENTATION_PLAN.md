# MVP Implementation Plan — Architecture Baseline 0.1

Status: **Planning Baseline — 2026-09-21**

## 1. 目的

本書は、Architecture Baseline 0.1を実装へ移す最初のMVP cutを定義します。

MVPの目的は機能数を増やすことではありません。

最小のvertical sliceで、EKI Fabricの中心Invariantが実際に成立することを確認します。

中心的なE2E Goal:

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

この経路を通すことで、個別技術のデモではなくFabricとしての意味を検証します。

---

# 2. MVPで必ず守るもの

## 2.1 Security

- authenticated Clientもuntrusted
- Client RequestをWorkerへ直接渡さない
- Clientがconcrete Workerを指定できない
- arbitrary code / arbitrary URL / arbitrary filesystem pathを標準Task APIから実行できない
- approved Executor Digestへbinding
- ArtifactGrantとTask submission authorityを分離
- WorkOrder signerだけで前提Authorityを捏造できない
- Worker local verification
- Local Governor veto
- ResultClaimをAcceptedResultへ直接昇格しない

## 2.2 Reliability

- Task != Attempt
- retry = new Attempt
- at-least-once delivery
- duplicate WorkOrder delivery tolerance
- Lease expiry / fencing
- Control Plane restart recovery
- Worker lossを通常状態として扱う
- bounded retry

## 2.3 Network / Data

- bulk ArtifactをControl metadata pathへ通さない
- Artifact Digest verification
- cache localityはoptimization hintのみ
- input / output transfer budget
- Workerへlong-lived storage credentialを渡さない

## 2.4 Operability

- Task / Attempt / Lease / Worker / Decision correlation
- placement rejection reason
- reclaim / retry reason
- structured logs / metrics
- operatorが内部状態を追える

---

# 3. 初回MVPに含める機能

## 3.1 External Client API

最低限は次のとおりです。

- Task submit
- Task get/status
- Task cancel
- Result retrieval
- Artifact ingest intent / upload

Task APIはlanguage-neutral contractとします。

具体Transportは別比較で決めますが、MVPではBrowser以外のClientからも扱いやすく、企業proxyと共存しやすいSurfaceを優先します。

## 3.2 Task Admission

実装するもの。

- strict schema validation
- Principal binding
- Idempotency Key + request digest
- Executor logical requirement resolution
- input Artifact authorization
- resource budget
- retry budget
- Data Class
- side-effect mode = noneを初期標準

初回MVPではexternal side-effect executionは必須にしません。

State Model上のUNKNOWNは保持しますが、実side-effect Adapterは後続milestoneへ送ります。

## 3.3 Durable Task / Attempt / Lease State

必要Property:

- unique Task ID
- TaskIR Digest
- Attempt ID
- state revision / conditional update
- Lease ID / revision / expiry
- cancellation intent
- retry budget
- restart recovery
- durable WorkOrder intent

Queueだけを正本にしません。

### RQ-014 Workload Suitability Gate

TaskがSecurity / Data / Resource feasibilityを満たしても、それだけでFabricへ送る根拠にはしません。RQ-014をMVPの実行経路へ反映するため、Baseline Schedulerの前にdeterministicなWorkload Suitability Gateを置きます。

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

このGateは次のAuthorityを持ちません。

- concrete Workerを選ばない。
- Security / Data / Resource feasibilityを緩和しない。
- Client申告だけを高保証のbaseline事実へ昇格しない。
- `FABRIC_NOT_JUSTIFIED`を理由にEKI Coreがlocal executionを勝手に開始しない。
- Placement、Lease、WorkOrderを発行しない。

Reference Implementationの最初のpure boundaryは`internal/core/suitability`とします。Fabric pathの費用要素は、値が無いものを0とみなさず、**estimate済み / 明示的に対象外 / unresolved**を区別します。baselineまたはFabric path componentが不足する場合は`NEEDS_MEASUREMENT`へ止めます。

最初の比較要素は次です。

- suitability decision
- admission / placement decision
- dispatch
- queue
- input transfer
- compute
- retry penalty
- output transfer
- verification

全部を常に測定することを要求するものではありません。ただし、測っていないものを暗黙の0へ畳みません。fixtureや特定workloadで対象外とする場合は、その理由を明示します。

Decision receiptは少なくとも、workload digest、policy revision、evaluator version、decision、reason codes、Evidence refs、unresolved inputs、比較した時間、decision basis digestを保持できることをReference方向とします。同じworkload名でもEvidence basisが変われば、過去の判断をcurrent decisionとして再利用しません。

最初のAcceptanceは`spikes/artifact-locality-cost`で既に確認した数値例を再利用します。

- 8 GiB input / 100 MiB/s / remote compute 20s / baseline 60sでは、input transfer約81.92sを含み`FABRIC_NOT_JUSTIFIED`。
- 10 MiB input / 100 MiB/s / remote compute 20s / baseline 60sでは、単純fixture上は約20.1sとなり`FABRIC_JUSTIFIED`。
- baselineが無い場合は`NEEDS_MEASUREMENT`。
- hard feasibility違反はSuitability decisionへ変換せず、既存のAdmission / Policy境界で止める。
- output transferとSuitability/Scheduler自身のdecision overheadも、測定された場合はFabric path costへ含める。

このpure boundaryをM0/M1実行経路へ接続するのは、durable Task/Attempt/Leaseとsigned WorkOrderの既存優先作業を崩さず、remote placementを通常経路へする前に行います。

### SuitabilityからSchedulerへの型付きhandoff

Suitability decisionを単なるenumとしてSchedulerへ渡しません。Reference Implementationでは、現在のEvidenceを用いてSuitabilityを評価した同じ呼び出しだけが、後段へ渡せる`FabricEntry`を生成します。

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

`DecisionReceipt`は監査・説明用Evidenceであり、routing authorityではありません。保存済みreceiptやClient supplied `FABRIC_JUSTIFIED` enumから`FabricEntry`へ変換するAPIを持たせません。Control Plane restart後などで再度routingする場合は、current EvidenceからSuitabilityを再評価します。

`FabricEntry`は、Admitted TaskのTask ID / TaskIR digest、Suitabilityで比較したworkload digest、Suitability decision basis digest、policy revision、evaluator versionへbindingします。別Task向けのEntryや、同じTask IDでもTaskIRが変わったEntryを再利用しません。Worker、Attempt、Lease、ArtifactGrant、WorkOrder、Executor authorityは含めません。したがって`FabricEntry`の存在はPlacement Approvalではなく、「このexact Task / TaskIRをFabric placement候補へ進める根拠が現在の比較で成立した」という前提Evidenceに限ります。

Baseline Schedulerのproduction entry pointは、raw TaskRecordだけではなく、有効な`FabricEntry`または同等の型付き前提を要求する方向とします。これにより、`FABRIC_NOT_JUSTIFIED`と`NEEDS_MEASUREMENT`の経路ではScheduler Proposal自体を作らず、後段のPlacement / Lease / ArtifactGrant / WorkOrder authorityが発行されない構造へ接続します。

## 3.4 Baseline Scheduler

初回はdeterministic heuristicとします。

最初のReference実装は`internal/core/scheduler`で、**non-authoritative Placement Proposal**だけを作ります。

入力にはraw Taskだけでなく、同じTask ID / TaskIR digestへbindingされた有効な`FabricEntry`を必須とします。`FABRIC_NOT_JUSTIFIED`または`NEEDS_MEASUREMENT`では`FabricEntry`が生成されないため、Scheduler Proposalも生成できません。

最初のrankingは万能scoreへまとめず、次のlexicographicな順序に限定します。

1. planning上eligibleな候補だけを残す
2. verified/normalized planning view上のcache hitを優先する
3. estimated transfer bytesが少ない候補を優先する
4. available CPU millisが大きい候補を優先する
5. 同値時はWorker identityのlexicographic順で決定論的にtie-breakする

`PlanningEligible`やcache / capacityはSchedulerが使うplanning inputであり、Security Authorityではありません。Client request fieldをそのまま候補へ流しません。候補のData Class、Assurance、Provider state、Executor support等はPLC-1が独立に再確認します。

Proposalは少なくとも次へbindingします。

- Task ID
- TaskIR digest
- Attempt ID
- proposed Worker identity
- Suitability workload digest
- Suitability decision basis digest
- Suitability policy revision
- 選択に使ったrank basis

ProposalにはAuthority role、署名、Lease、ArtifactGrant、WorkOrder authorityを持たせません。


Hard filter:

- Worker online / eligible
- Executor support
- Data Class / worker profile
- resource feasibility
- Artifact / network budget

Soft ranking:

- cache locality
- estimated transfer bytes
- simple compute capacity

ML / learned estimatorはMVP必須にしません。

## 3.5 Placement Authorization

Scheduler Proposalを別境界で再評価します。

少なくともは次のとおりです。

- Task / Attempt binding
- Worker Identity
- Executor support
- Data Class
- active Provider / Worker state
- resource feasibility

を確認します。

Reference coreでは`internal/core/placementauth`がcurrent Evidenceを再確認し、全hard conditionを満たした場合だけ**unsigned PLC-1 ApprovalDraft**を生成します。Schedulerのrankや`PlanningEligible`だけではApprovalDraftを作れません。Policy revision drift、Task / Attempt / Worker binding mismatch、Data Class / Assurance / Provider state / resource / transfer budget failureはいずれもfail closedとします。

この境界の最初のcross-component conformanceを`conformance/coreflow`に置き、次を固定します。

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

全条件を満たした場合も、現段階で生成するのはPLC-1署名前のDraftまでです。Suitability / Scheduler / Placement AuthorizationがLease、ArtifactGrant、WorkOrderを自己生成しないことを境界とします。

### P2 Placement Approval payload境界

PLC-1の`ApprovalDraft`を、そのままLease Authorityへ渡しません。P2の`eki.placement-approval/v1`をdeterministic CBORとして読み、Decision固有の意味へ変換する境界を独立させます。

Reference実装候補として次を追加します。

- `internal/core/decisionpayload` — P2の意味を表す型付き`PlacementApproval`
- `internal/spi/decisiondecoder` — raw payload bytesを型付きDecisionへ変換するSPI
- `internal/adapters/reference/decisiondecoder/cbor` — RFC 8949 Core Deterministic Encodingを要求するstrict CBOR adapter

Decoderは少なくとも次をfail closedにします。

- duplicate map key
- indefinite-length item
- unknown field
- unexpected CBOR tag
- 非deterministic encoding
- P2 v1以外のschema version
- Decision kind / Authority role mismatch
- invalid Task / Attempt / Worker binding
- SHA-256以外、または32 byteでないDigest
- invalid issued / expiry window
- Evidence件数上限超過

ただし、Decoder成功はAuthorizationではありません。

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

`decisionpayload.PlacementApproval.Valid()`は「閉じたP2 payloadとして意味が成立している」ことだけを表します。署名検証済み、current policyで有効、Lease発行可能という意味にはしません。`verified=true`のようなcaller supplied flagをこの間の境界へ追加しません。

このReference adapterは外部CBOR libraryへ依存するため、依存moduleを実取得してfocused testを実行できるまで採用済みとは扱いません。依存取得不能な環境でGo API shapeだけを確認した結果を、Security Evidenceへ読み替えません。

## 3.6 WorkOrder

初回WorkOrderは少なくとも:

- WorkOrder ID
- Task ID / TaskIR Digest
- Attempt ID
- Worker ID
- Placement Approval ref
- Lease ref
- Executor Approval ref / Digest
- ArtifactGrant refs
- resource limits
- network policy
- output binding
- expiry

へbindingします。

署名形式の具体方式はProtocol / Signer実装で決めます。

## 3.7 Worker Agent

2台以上のNative WorkerでE2E Testします。

Worker Agent責務:

- outbound session
- Worker Identity
- Resource Advertisement
- WorkOrder verification
- Local Governor
- Artifact fetch / Digest check
- Executor Runtime
- ResultClaim
- Lease heartbeat / renewal request
- local stop / reclaim

## 3.8 Local Governor

初期対象は次のとおりです。

- manual opt-out
- worker enabled / disabled
- CPU availability
- memory ceiling
- user-active simulation / hook
- WorkOrder wall-time stop

Platform-specific hard enforcementはReference Runtime capabilityとして段階導入します。

未実証ResourceをHARDと広告しません。

## 3.9 Executor Runtime

最初は**approved command executor**をReference Runtimeとします。

安全条件は次のとおりです。

- Admin登録済みcommand / binaryだけ
- Client supplied command line executable pathは禁止
- exact Executor Digest / version
- parameter schema
- bounded working directory
- no network by default
- output bound

WASMはEarly Optionalとして次milestone候補にします。

## 3.10 Artifact Store

Reference implementationは小規模Profileで動く単純なbackendから開始します。

必要Property:

- immutable content
- digest
- size
- scoped read/write
- upload finalization
- output storage

Filesystem-backed reference implementationを候補とし、S3-compatible AdapterをEarly Optionalとします。

## 3.11 ArtifactGrant / Data Access Resolver

最低限は次のとおりです。

- Task / Attempt / Worker binding
- Artifact Digest
- read / write operation
- expiry
- output size limit

を強制します。

初回Referenceでは同一Hostのsimple object serviceでも構いませんが、Workerへroot filesystem pathやstore-wide credentialを渡しません。

## 3.12 Result Verification / Acceptance

初回Verification method:

- Result Artifact Digest / structural validity
- Executor-specific deterministic validator

少なくとも一つのdemo workloadでは、同一Inputに対するexpected deterministic resultをvalidatorで確認します。

Worker success exit codeだけをAcceptance根拠にしません。

## 3.13 Telemetry / Audit

初回から最低限は次のとおりです。

- Task ID
- Attempt ID
- Worker ID
- Lease ID
- WorkOrder ID
- placement reason
- rejection reason
- retry reason
- reclaim reason
- Artifact transfer bytes
- Result acceptance

を追跡します。

OpenTelemetry semantic attributesをReference方向とします。

---

# 4. 初回MVPに含めないもの

Architecture上のExtension Pointは保持しますが、初回MVP完成条件には含めません。

- Browser Worker
- Federation
- Public-interest cross-organization Grant実運用
- Remote Attestation
- Confidential Computing key release
- real zkVM proof
- learned scheduler
- GPU-specific hard enforcement
- attested Contribution Metering
- external Credit / Value mapping
- multi-party approval
- site cache
- arbitrary P2P
- cloud-specific resource provisioning
- generic external side effects
- Saga / compensation
- full secret manager integration

これらを後から入れてもCore Contract / State Modelを変更しないことがArchitectureの検証点です。

---

# 5. MVP workload

一つのdemoだけに最適化しないため、性質の異なる3種類をdogfoodします。

## W1 — Deterministic text / manifest transform

目的は次のとおりです。

- small Artifact
- deterministic
- validator可能
- CPU only

確認は次のとおりです。

- TaskRequest
- Executor parameter
- ArtifactGrant
- Result verification

## W2 — Large cached Artifact compute

目的は次のとおりです。

- 数百MiB〜GiB相当のArtifact
- locality / transfer cost
- cache hit / miss

実際の巨大dataを必須にせず、Test fixture / sparse or generated dataでNetwork pathを測定できます。

確認は次のとおりです。

- cache
- transfer budget
- Scheduler ranking
- retry transfer

## W3 — Interruptible long-running compute

目的は次のとおりです。

- user reclaim
- Lease expiry
- Worker loss
- retry
- optional checkpoint later

確認は次のとおりです。

- A1 -> Worker 1
- reclaim / loss
- A1 close
- bounded Recovery
- A2 -> Worker 2
- AcceptedResult

SEI / SUI / TEI workloadを後続dogfoodに利用できますが、MVP CoreへそのDomain semanticsを埋め込みません。

---

# 6. End-to-End Acceptance Scenario

最低Acceptance:

1. Client AがArtifactをingestする。
2. Client AがIdempotency Key付きTaskRequestをsubmitする。
3. Task AdmissionがTaskIRとBudgetを確定する。
4. SchedulerがWorker 1を提案する。
5. Placement AuthorityがWorker 1を承認する。
6. Lease / ArtifactGrant / WorkOrderを発行する。
7. Worker 1がWorkOrder prerequisitesを再検証する。
8. Worker 1がArtifactを取得しDigestを検証する。
9. Executorがbounded environmentで実行開始する。
10. User reclaimを発生させる。
11. Local GovernorがWorker 1を停止する。
12. Lease renewを止める。
13. Task StateがA1をreclaimとしてcloseする。
14. retry / transfer budgetを評価する。
15. 新Attempt A2を作成する。
16. Worker 2へ新WorkOrderを発行する。
17. Worker 2がResultClaimを返す。
18. ValidatorがVerificationResultを生成する。
19. Result AcceptanceがAcceptedResultを生成する。
20. Task StateがCOMPLETEDへ遷移する。
21. ClientはWorker topologyを知らずResultを取得する。

この一本を自動Integration Testとして成立させます。

---

# 7. Malicious Client Acceptance

最低限のnegative E2E:

- concrete Worker ID指定を拒否
- arbitrary executable pathを拒否
- arbitrary URL inputを拒否
- Data Class downgradeを拒否
- oversized requestをIngressで拒否
- retry budget増幅を拒否
- Idempotency Key + different request digestを拒否
- output budget超過を停止

---

# 8. Malicious Worker Acceptance

- wrong WorkOrder Worker IDを拒否
- stale Leaseを拒否
- expired ArtifactGrantを拒否
- wrong Artifact bytesをDigest mismatchで拒否
- fake ResultClaimをvalidatorでreject
- duplicate ResultClaimをidempotent処理
- stale Attemptからのlate ResultでLeaseを復活させない
- fake Contribution / Resource self-reportを高保証事実にしない

---

# 9. Restart / Failure Acceptance

- Control Plane restart後にTask / Attempt / Leaseを復元
- outbox delivery replayで新Attemptを増やさない
- Worker session reconnect
- old Lease heartbeat reject
- Artifact Store一時failureをTask semantic failureにしない
- Worker 1 loss後にbounded retry

---

# 10. Initial Component Cut

Logical component:

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

これはMVP planning上のLogical Cutです。

実Repository directoryをこのまま凍結するものではありません。

Baseline review後のRepository Layout workで確定します。

---

# 11. Reference Adapter優先順位

## M0必須

- State Store: local durable relational / embedded candidate
- Delivery: durable outbox + simple delivery
- Artifact Store: filesystem/object abstraction
- Identity: development / managed local reference
- Signer: local protected dev/reference key
- Executor: approved command executor
- Scheduler: deterministic heuristic
- Verification: deterministic application validator
- Telemetry: structured log + OTLP-capable path

## M1 early

- OIDC Identity
- S3-compatible Artifact Store
- WASM Runtime
- OS hard-limit backends

## Research / later

- TEE Attestation
- zkVM
- learned estimator
- Cloud provider meter
- federation adapters

---

# 12. State Store selection requirements

製品名より先に必要Propertyを固定します。

- transaction
- unique constraints
- compare-and-swap / revision
- durable restart
- indexed scan for recovery
- outbox pattern
- reasonable small deployment operation
- future multi-instance path

MVPではdistributed consensus databaseを必須にしません。

Embedded / single-node relational implementationから始めても、SPI Contractで後からreplace可能にします。

---

# 13. Delivery selection requirements

- at-least-once
- duplicate delivery
- ack / retry
- bounded backlog
- poison handling
- backpressure
- message identity

Queue製品をWorker Protocolへ露出しません。

MVPではState Store Outbox + Worker Gateway deliveryで開始し、NATS等は負荷要件が出た後にAdapterとして比較できます。

---

# 14. Protocol freeze order

いきなり全schemaを固定しません。

## P0

- external TaskRequest
- public TaskStatus / Error
- Artifact ingest / descriptor

## P1

- Worker hello / ResourceAdvertisement
- Lease / WorkOrder delivery envelope
- heartbeat / renewal
- ResultClaim

## P2

- internal signed Decision schema
- Adapter protocol
- VerificationResult

内部実装structを公開schemaへ流用しません。

---

# 15. Security Conformance Suite

実装と同時にTest contractを作ります。

分類は次のとおりです。

## C1 Parser / Protocol

- fuzz
- oversized
- duplicate field
- malformed encoding
- unknown critical field

## C2 Authority Binding

- wrong issuer
- wrong subject
- expired
- stale revision
- wrong Worker
- wrong Task / Attempt

## C3 State / Lease

- duplicate delivery
- stale writer
- old epoch
- restart
- concurrent retry

## C4 Artifact

- digest mismatch
- expired Grant
- path / URL injection
- upload limit

## C5 Worker

- WorkOrder mutation
- capability widening
- local reclaim

## C6 Result

- fake result
- duplicate result
- late result
- conflicting result

---

# 16. Milestones

## M0 — Executable Skeleton

成果は次のとおりです。

- repository implementation layout
- core IDs / canonical contracts
- State Store adapter
- Task submit/status
- Worker registration
- one Worker local execution

Gate:

- restartしてTask identityを保持
- Client RequestとWorkOrderが別type / schema

## M1 — Secure Two-Worker Fabric

成果は次のとおりです。

- 2+ Worker
- scheduler / placement separation
- Lease / fencing
- signed WorkOrder
- ArtifactGrant
- command executor

Gate:

- Client cannot choose Worker
- stale Lease rejected
- wrong Artifact rejected

## M2 — Reclaim / Recovery

成果は次のとおりです。

- Local Governor opt-out
- Worker loss
- Recovery Candidate
- retry budget
- transfer budget

Gate:

- A1 loss -> bounded A2
- no duplicate attempt from delivery retry

## M3 — Result Assurance

成果は次のとおりです。

- ResultClaim
- VerificationResult
- AcceptedResult
- deterministic validator

Gate:

- Worker success claim alone does not complete Task

## M4 — Operability / Conformance

成果は次のとおりです。

- structured audit
- OTLP metrics
- malicious Client suite
- malicious Worker suite
- restart / chaos suite

Gate:

- main E2E scenario automated
- invariant regressions block merge

## M5 — Early Adapters

候補は次のとおりです。

- OIDC
- S3-compatible store
- WASM
- platform hard enforcement

Gate:

- Adapter replacement does not change Core semantics

---

# 17. MVP Definition of Done

MVP完成は、単にTaskが別PCで動くことではありません。

次を満たすことを要求します。

1. malicious ClientがWorkerを直接命令できない。
2. 2台以上のWorkerへ安全にPlacementできる。
3. Worker loss / opt-outでTaskを安全に再配置できる。
4. retryが新Attemptとして監査可能である。
5. Artifact accessがscopeされ、Digest検証される。
6. unapproved Executorを実行しない。
7. ResultClaimがVerification / Acceptanceを通る。
8. Task / Attempt / LeaseがControl Plane restart後も復元できる。
9. queue / delivery duplicateが実行Authorityを増やさない。
10. Network / retry budgetがboundedである。
11. Local Governorが中央より強く停止できる。
12. ClientへWorker topologyを不要に公開しない。
13. Auditで主要Decision chainを追える。
14. Reference Adapterを交換してもCore semanticsが変わらない。

---

# 18. CIについて

現状GitHub Actions残量によるCI errorはProject側既知条件として扱います。

実装開始時はlocal test / reproducible commandを必ず文書化し、CI availabilityだけをEvidenceにしません。

CIが利用可能になった段階でconformance suiteを段階的にworkflowへ接続します。

---

# 19. Branch / milestone rule

これまでと同じく、milestone内でもレビュー可能なwork unitごとに:

```text
latest main
  -> one purpose branch
  -> test / docs
  -> PR
  -> squash merge
  -> branch realign to main
```

を維持します。

複数milestoneの未マージbranchを連鎖させません。

---

# 20. 次の作業

Baseline Reviewをmergeした後、次はImplementation Entry Planningとして:

1. Protocol technology comparison
2. P0 machine-readable schema
3. Repository implementation layout
4. M0 executable skeleton

の順に進めます。

Protocol選定とRepository layoutは別work unitにし、未マージbranchから実装branchを派生させません。
## 20.1 2026-09-27 current mainline checkpoint

Section 20の初期列挙はすでに一部完了しているため、現在の実装入口をここで更新する。

完了済みは次のとおりです。

- Protocol technology comparison
- P0 machine-readable external schema
- Repository implementation layout
- M0 executable skeletonの基礎（Go module / binaries / typed IDs / TaskRecord / State Store SPI / in-memory adapter）
- P1 Worker Plane schema
- P2 Signed Decision schema / cryptographic profile
- strict COSE_Sign1 wire profile
- COSE decoder SPI / shared conformance harness / negative vectors

未達かつ優先は次のとおりです。

1. ~~**M0 durable State Store**~~ — SQLite reference adapterを実装済み（2026-10-06、ADR-0003 §14）。restart後のTask identity / revision保持を確認した。Task以外のtable（Attempt / Lease、outbox）は未実装。
2. ~~**P2 concrete COSE decoder**~~ — strict COSE_Sign1 decoderとSignature1 encoderを実装済み（2026-10-06、ADR-0005 §18）。
   - Placement Approval payloadについては、型付きCore contract / decoder SPI / strict CBOR Reference adapterを`feature/placement-approval-payload-decoder`で実装中。
   - RFC 8949 deterministic re-encode比較、closed schema、duplicate key / indefinite length / unknown field / P2 bindingの負例を含む。
   - 外部moduleを取得したfocused testは2026-10-06に実施済み（Go 1.27.0、WSL2）。
   - COSE envelope decode → payload decode → cryptographic verification → current-state revalidationのcompositionは後続。
3. durable stateを前提にM0 E2EをTask submit/status → Worker registration → one Worker local executionまで接続。

`learned scheduler`はSection 4 / 11で定義したとおりMVP必須ではない。Research実装がmainに存在しても、上記M0/M1 gateの優先順位を変更しない。

外部Go moduleを取得・version lock・testできない環境では、依存追加だけを先にcommitしない。検証可能な環境が得られるまで、既存contract / conformanceを正本として保持する。

