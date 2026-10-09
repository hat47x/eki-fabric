# 署名付き権限判断の仕様（P2）

状態: **設計基準0.1（2026年9月21日）**

正本は次のとおりです。

- `v1/decisions.cddl` — 署名するデータ本体のCDDL
- P1の配送形式: `../worker/v1/worker.proto`
- 権限のモデル: `../../architecture/AUTHORITY_BOUNDARIES.md`
- 契約のモデル: `../../architecture/CONTRACT_BOUNDARIES.md`
- 通信仕様に関するADR: `../../plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`

## 目的

P2では、実行端末（Worker）が実行権限の根拠として検証する判断（Decision）について、**署名するデータの内容**を定めます。

P1のProtobufは署名付きデータの配送を担い、P2のCDDLは安全上の意味を定義します。

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

Protobufで符号化したバイト列を、そのまま署名対象にはしません。

## 初版で定義する権限判断

- Placement Approval — PLC-1
- Lease — LSE-1
- Executor Approval — EXE-1
- Artifact Read Grant — DAT-1
- Artifact Write Grant — DAT-1
- WorkOrder — WOR-1

Task Admission / Result Acceptance / Contribution等は、Worker実行経路で必要になる順に後続追加します。

## COSEの形式

署名付きデータの外側の形式にはCOSE_Sign1を使います。その形式を機械可読に定義する正本は`spec/decisions/v1/cose-profile.cddl`です。

初版の設計基準では、次の形式に固定します。

- CBOR tag 18付きCOSE_Sign1だけを受理する
- protected headerは`alg`、`crit`、`kid`、EKI profileの4項目だけ
- EKI profile header labelはPrivate Use integer `-70000`
- `crit`は`[-70000]`へ固定する
- profile valueは`eki.signed-decision/v1`
- `alg`は`-8` (EdDSA) または`-7` (ES256)
- `kid`は1..128 byteのbstrで、EKI Key RegistryのKeyIDへ対応する
- unprotected headerはempty map
- payloadはdetachedにせずCOSE_Sign1内のbstrへ含める
- signatureはBaseline algorithmsの64-byte bstr
- protected header mapとP2 payloadはRFC 8949 Core Deterministic Encodingを要求する
- algorithmはTrust / Policy Administrationのallowlistから選び、Client / Worker supplied valueでallowlistを拡張しない
- external AADはMVPではempty bstrとする

RFC 9052自体はtagged/untagged COSE_Sign1の両方を許容しますが、P1 `SignedDecisionCarrier`は別media-type識別を持たないため、EKIではtag 18を必須にします。unprotected extensionもMVPでは必要がないため空mapへ閉じます。

`plan/adr/0004-signed-decision-cryptographic-profile.md`がalgorithm/key lifecycleを、`plan/adr/0005-strict-cose-sign1-decoder-boundary.md`がwire envelopeとdecoder境界を定義します。

RFC 9052はCOSE_Sign1等のstructureと署名処理を定義し、algorithm自体はRFC 9053等へ分離しています。その分離をEKIでも維持します。

## 決定論的なCBOR符号化

署名するデータの本体は、RFC 8949のCore Deterministic Encoding Requirementsに従います。

具体的には、次を要求します。

- preferred serialization
- definite-length item
- deterministic map key order
- duplicate map key禁止

を要求します。

P2の初版では、安全性に関わる数値項目に浮動小数点数を使いません。時刻、改訂番号、予算、バイト数は整数で表します。

Signerはdeterministic bytesだけを生成し、Verifierは非deterministic encodingを受理しない方針とします。

## 許可する項目を限定したスキーマ

判断のデータには、CDDLに列挙した項目だけを許可します。

安全性に関わる未知の項目を、将来との互換性のためとして無条件に無視しません。

Fieldを追加・意味変更する場合は新しいDecision schema versionを発行します。

これはP1 Protobufの「unknown fieldをtransport上保持できる」性質とは別です。署名Decisionの意味はより厳しく閉じます。

## 共通のヘッダー項目

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

Authority roleはArchitectureのLogical Authority identifier（`EXE-1` / `PLC-1` / `LSE-1` / `DAT-1` / `WOR-1`）そのものを署名payloadへ含めます。localな数値aliasにはしません。COSE `kid`だけからAuthority roleを推測してpayloadから省略しません。

Verifierは:

1. COSE signature
2. `kid`のTrust configuration
3. payload Authority role
4. Decision kind
5. subject binding

を合わせて確認します。

正しいkeyで署名されていても、そのkeyが当該Authority roleへ許可されていなければrejectします。

## 判断間の参照

WorkOrderは前提Decisionをinlineで意味的にコピーせず、kind-specificなDecision refで:

- expected kind
- decision ID
- deterministic payload digest

へbindingします。

Placement欄にLease ref、Lease欄にExecutor Approval refのような別kindを入れられないよう、CDDL自体でReference型を分けます。

P1 `WorkOrderDelivery.prerequisite_decisions`で運ばれたCOSE objectについて、Workerはpayload digestを再計算し、WorkOrder内refと一致するものだけを利用します。

これにより、同じDecision IDの別payload差し替えを防ぎます。

## 処理内容への結び付け

Placement、Lease、Artifact Grant、WorkOrderはTask IDだけでなくTaskIR Digestへbindingします。

Task IDを維持したまま実行意味を差し替えることを許しません。

## 実行先の承認

Placement Approvalは:

- Task
- TaskIR Digest
- Attempt
- Worker

をbindingします。

optional Evidence refはPlacement Authorityが利用したassurance等の監査bindingに使えます。

Scheduler scoreやProposalそのものはAuthorization tokenではありません。

## 期限付き実行権

LeaseはTask / TaskIR / Attempt / Workerに加え:

- Lease ID
- revision / fencing epoch
- renewal sequence
- expiry

を署名します。

古いLease payloadを再送してcurrent Authorityを復活させません。

Heartbeatはこのpayloadを生成できません。RenewalはLSE-1が新しいLease Decisionを発行した場合だけ成立します。

## 実行体の承認

Executor Approvalはmutable tagではなくexact Artifact Digestを承認します。

さらには次のとおりです。

- logical Executor ID
- media type / size
- runtime class
- capability profile digest
- optional provenance evidence refs

をbindingします。

Workerは実際に取得したExecutor bytesをDigest検証してから実行します。

## 成果物の利用許可

Read GrantとWrite Grantを別Decision kindにしています。

Read Grantはverified Content Digestを必須とし、optional rangeを持てます。

Write Grantは事前に確保したoutput Artifact identity / slotとmaximum bytesをbindingします。出力前にはContent Digestが存在しないため、Client申告digestを無理に入れません。

どちらもは次のとおりです。

- Task / TaskIR
- Attempt
- Worker
- Data Class
- expiry

へbindingします。

raw URL、filesystem path、bucket credentialはP2 GrantのAuthority表現にしません。

## 作業指示書

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

## 有効期限

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

## 許可されない操作を拒否する

WorkOrderにないCapabilityは利用できません。

v1ではSandbox Capability自体をarbitrary mapにせず、Admin承認済みimmutable profileのID + Digestへbindingします。

Network accessもoptional registered Network Profile refが存在する場合だけ候補になります。

Client supplied destinationはP2へ入りません。

## データ量の上限

CDDLでは文字列・Digest等の基本boundsを定義しています。

配列件数もInput Grant最大64、Output Grant最大16、Evidence ref最大16としています。

それとは別に、VerifierはCOSE object全体・CBOR nesting・総bytesへhard parser limitを適用します。

## 再送と古い情報の扱い

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

## P1の通信仕様との整合

`SignedDecisionCarrier.kind`はrouting hintです。

Carrier kindとsigned payloadのDecision kindが一致しなければrejectします。

Carrier自体に署名済みpayloadと矛盾するTask / Worker / expiry情報を追加しません。

## 署名アルゴリズムの変更

CDDL payloadへsignature algorithmを入れません。

AlgorithmはCOSE protected headerに置き、Trust / Policy Administrationのallowlistで制御します。

これによりpayload schemaを変えずにkey / algorithm rotationができます。

ただしunknown / deprecated algorithmを自動fallbackしません。

## 適合性の検証候補

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

## 参照する標準

- RFC 8949 — CBOR / Core Deterministic Encoding
- RFC 8610 — CDDL
- RFC 9052 — COSE Structures and Process
- RFC 9053 — COSE Algorithms

## 今後の対応

P2 merge後は、Reference Go implementationで使うDecision decoder / verifierの境界を設計します。ただしcryptographic algorithm選定とkey managementはSecurity Profileとして別work unitにし、SignerをScheduler等へgeneric `sign(bytes)`として公開しません。
