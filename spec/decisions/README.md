# P2 Signed Authority Decisions

Status: **Baseline 0.1 — 2026-09-21**

正本:

- `v1/decisions.cddl` — signed payload CDDL
- P1 carrier: `../worker/v1/worker.proto`
- Authority model: `../../architecture/AUTHORITY_BOUNDARIES.md`
- Contract model: `../../architecture/CONTRACT_BOUNDARIES.md`
- Protocol ADR: `../../plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`

## 目的

P2は、Workerが実行Authorityとして検証するDecisionの**署名対象payload**を固定します。

P1 Protobufはtransport carrierです。P2 CDDLはSecurity semanticsです。

~~~text
P1 SignedDecisionCarrier
  bytes cose_sign1
        │
        ▼
COSE_Sign1
  protected headers
  deterministic CBOR payload  ← P2 CDDL
  signature
~~~

raw Protobuf serializationを署名対象にはしません。

## v1で定義するDecision

- Placement Approval — PLC-1
- Lease — LSE-1
- Executor Approval — EXE-1
- Artifact Read Grant — DAT-1
- Artifact Write Grant — DAT-1
- WorkOrder — WOR-1

Task Admission / Result Acceptance / Contribution等は、Worker実行経路で必要になる順に後続追加します。

## COSE Profile

EnvelopeはCOSE_Sign1を使用します。

v1では:

- payloadはdetachedにせずCOSE_Sign1内へ含める
- `alg`をprotected headerへ置く
- `kid`をprotected headerへ置く
- algorithmはTrust / Policy Administrationのallowlistから選ぶ
- Client / Worker supplied valueでalgorithmを選ばない
- external AADはMVPではempty bstrとする

をBaselineとします。

具体signature algorithm / key typeは別Security Profileで固定します。

RFC 9052はCOSE_Sign1等のstructureと署名処理を定義し、algorithm自体はRFC 9053等へ分離しています。その分離をEKIでも維持します。

## Deterministic CBOR

署名payloadはRFC 8949 Core Deterministic Encoding Requirementsへ従います。

特に:

- preferred serialization
- definite-length item
- deterministic map key order
- duplicate map key禁止

を要求します。

P2 v1はSecurity-critical numeric fieldへfloating pointを使いません。timestamp、revision、budget、byte countはintegerです。

Signerはdeterministic bytesだけを生成し、Verifierは非deterministic encodingを受理しない方針とします。

## Closed schema

Decision mapはCDDLに列挙したfieldだけを許します。

Security-critical unknown fieldをforward-compatibleとしてsilent ignoreしません。

Fieldを追加・意味変更する場合は新しいDecision schema versionを発行します。

これはP1 Protobufの「unknown fieldをtransport上保持できる」性質とは別です。署名Decisionの意味はより厳しく閉じます。

## Common header fields

各Decision payloadのtop-level mapは共通して:

- schema version
- decision kind
- decision ID
- issuing Authority role / principal
- issued time
- expiry
- policy revision
- decision-specific body

を署名対象に含めます。

Authority roleはArchitectureのLogical Authorityです。COSE `kid`だけからAuthority roleを推測してpayloadから省略しません。

Verifierは:

1. COSE signature
2. `kid`のTrust configuration
3. payload Authority role
4. Decision kind
5. subject binding

を合わせて確認します。

正しいkeyで署名されていても、そのkeyが当該Authority roleへ許可されていなければrejectします。

## Decision reference

WorkOrderは前提Decisionをinlineで意味的にコピーせず、`decision-ref`で:

- kind
- decision ID
- deterministic payload digest

へbindingします。

P1 `WorkOrderDelivery.prerequisite_decisions`で運ばれたCOSE objectについて、Workerはpayload digestを再計算し、WorkOrder内refと一致するものだけを利用します。

これにより、同じDecision IDの別payload差し替えを防ぎます。

## TaskIR binding

Placement、Lease、Artifact Grant、WorkOrderはTask IDだけでなくTaskIR Digestへbindingします。

Task IDを維持したまま実行意味を差し替えることを許しません。

## Placement Approval

Placement Approvalは:

- Task
- TaskIR Digest
- Attempt
- Worker

をbindingします。

optional Evidence refはPlacement Authorityが利用したassurance等の監査bindingに使えます。

Scheduler scoreやProposalそのものはAuthorization tokenではありません。

## Lease

LeaseはTask / TaskIR / Attempt / Workerに加え:

- Lease ID
- revision / fencing epoch
- renewal sequence
- expiry

を署名します。

古いLease payloadを再送してcurrent Authorityを復活させません。

Heartbeatはこのpayloadを生成できません。RenewalはLSE-1が新しいLease Decisionを発行した場合だけ成立します。

## Executor Approval

Executor Approvalはmutable tagではなくexact Artifact Digestを承認します。

さらに:

- logical Executor ID
- media type / size
- runtime class
- capability profile digest
- optional provenance evidence refs

をbindingします。

Workerは実際に取得したExecutor bytesをDigest検証してから実行します。

## Artifact Grant

Read GrantとWrite Grantを別Decision kindにしています。

Read Grantはverified Content Digestを必須とし、optional rangeを持てます。

Write Grantは事前に確保したoutput Artifact identity / slotとmaximum bytesをbindingします。出力前にはContent Digestが存在しないため、Client申告digestを無理に入れません。

どちらも:

- Task / TaskIR
- Attempt
- Worker
- Data Class
- expiry

へbindingします。

raw URL、filesystem path、bucket credentialはP2 GrantのAuthority表現にしません。

## WorkOrder

WorkOrderはWorkerが実行できる最終execution envelopeです。

v1 bodyは:

- WorkOrder / Task / Attempt / Worker
- TaskIR Digest
- Placement / Lease / Executor Approval refs
- exact Executor Digest
- input / output Artifact Grant refs
- Resource limits
- immutable Sandbox profile ref
- optional registered Network profile ref
- optional GPU limit
- effect mode
- nonce

へbindingします。

MVP v1のeffect modeは`0 = none`だけです。

WorkOrder Authorityがmissing prerequisiteを自己生成できることを意味しません。WorkerはP1 carrierで受け取ったprerequisite Decisionsを独立検証します。

## Expiry

すべてのv1 Decisionはexpiryを持ちます。

WorkOrder verifierは少なくとも:

~~~text
workorder expiry
  <= placement expiry
  <=/and lease validity
  <= executor approval validity
  <= artifact grant validity
~~~

の実効上限を安全側に評価します。

単純な数式の大小だけでなく、各prerequisiteが現在時点で有効かを個別に確認します。

## Default deny

WorkOrderにないCapabilityは利用できません。

v1ではSandbox Capability自体をarbitrary mapにせず、Admin承認済みimmutable profileのID + Digestへbindingします。

Network accessもoptional registered Network Profile refが存在する場合だけ候補になります。

Client supplied destinationはP2へ入りません。

## Size bounds

CDDLでは文字列・Digest等の基本boundsを定義しています。

配列件数もInput Grant最大64、Output Grant最大16、Evidence ref最大16としています。

それとは別に、VerifierはCOSE object全体・CBOR nesting・総bytesへhard parser limitを適用します。

## Replay / stale

署名が正しくても、次をrejectします。

- expired Decision
- revoked issuer/key
- unsupported schema version
- wrong Authority role
- wrong Worker
- wrong Task / Attempt
- stale Lease revision
- mismatched TaskIR Digest
- WorkOrder refとprerequisite payload digestの不一致
- already superseded / terminal Taskに対するDecision

Signature verificationはAuthorization判定の一部にすぎません。

## P1との整合

`SignedDecisionCarrier.kind`はrouting hintです。

Carrier kindとsigned payloadのDecision kindが一致しなければrejectします。

Carrier自体に署名済みpayloadと矛盾するTask / Worker / expiry情報を追加しません。

## Algorithm agility

CDDL payloadへsignature algorithmを入れません。

AlgorithmはCOSE protected headerに置き、Trust / Policy Administrationのallowlistで制御します。

これによりpayload schemaを変えずにkey / algorithm rotationができます。

ただしunknown / deprecated algorithmを自動fallbackしません。

## Conformance候補

- non-deterministic CBOR
- duplicate map key
- indefinite-length encoding
- wrong schema version
- wrong Decision kind
- wrong Authority role
- wrong `kid` / issuer mapping
- expired Decision
- TaskIR digest mismatch
- WorkOrder wrong Worker
- stale Lease revision
- prerequisite digest mismatch
- input Read Grant without Content Digest
- output Write Grant above maximum bytes
- arbitrary Network destination injection
- unsupported effect mode
- nonce length violation

## Standards

- RFC 8949 — CBOR / Core Deterministic Encoding
- RFC 8610 — CDDL
- RFC 9052 — COSE Structures and Process
- RFC 9053 — COSE Algorithms

## 次

P2 merge後は、Reference Go implementationで使うDecision decoder / verifierの境界を設計します。ただしcryptographic algorithm選定とkey managementはSecurity Profileとして別work unitにし、SignerをScheduler等へgeneric `sign(bytes)`として公開しません。
