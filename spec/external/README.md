# EKI Fabric P0 External Protocol

Status: **Baseline 0.1 — 2026-09-21**

このディレクトリは、EKI Fabric External Client Planeの最初のmachine-readable contractを置きます。

正本は次のとおりです。

- `openapi.json` — OpenAPI 3.2.1
- Architecture decision: `../../plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`

## P0の範囲

P0で固定するのは次です。

- Task submit
- public Task status
- Task cancellation request
- Accepted Result retrieval
- Artifact ingest intent
- bounded Artifact upload
- public Artifact descriptor
- RFC 9457 Problem Detailsに基づくError shape

Worker Plane、WorkOrder、Lease、ArtifactGrant token、Placement、internal Policy Revision、raw Verification EvidenceはP0 External Contractへ含めません。

## Hostile input boundary

OpenAPI validationだけで安全になるとは考えません。

Ingressは少なくとも次をschema validationより前または同時に制限します。

- request body bytes
- nesting depth
- array length
- string length
- object property count
- duplicate JSON property
- invalid encoding
- decompression expansion
- request timeout

Core objectは原則 `additionalProperties: false` です。

明示的な拡張点は次だけです。

- `TaskRequest.parameters`
- `ExtensionEnvelope.payload`

`parameters` はそのままWorkerへ渡しません。Task Admissionがapproved Executorのparameter schemaへ照合します。

## Idempotency

Task submit、cancel、Artifact ingest intentは `Idempotency-Key` を要求します。

KeyはPrincipal scopeです。

同一Keyについて:

- 同一normalized semantic fingerprint → 同じlogical operationへ収束可能
- 異なるfingerprint → conflict

とします。

raw JSON byte列はfingerprintに使いません。

## Authentication

P0 OpenAPIはAuthentication mechanism自体をまだ固定しません。

ProductionではAuthenticationが必須です。

OAuth / OIDC / mTLS等の具体profileは別Security work unitで決定します。

各operationの `x-eki-authentication-required: true` は、OpenAPI上の未選定を「anonymous allowed」と誤読しないためのEKI markerです。

## Artifact upload

Control metadataとbulk bytesを分けます。

1. Clientがingest intentを作る。
2. Control Planeがbounded upload sessionを発行する。
3. bytesはData Plane upload endpointへ送る。
4. upload後もContent Identity / Safe Processing / Provenanceを別facetとして評価する。
5. availableになったArtifactだけがTask input候補になる。

Clientがdeclared / expected Digestを送っても、それだけでverified contentにはなりません。

## Artifact facets

`ArtifactDescriptor.facets` は意図的に単一statusへ潰していません。

- `content_identity`
- `safe_processing`
- `provenance`
- `availability`

を別々に公開します。

`trusted: true` のような総合bitは作りません。

## Task public status

Public statusは内部State Modelの縮約です。

External Clientへ返す候補:

- queued
- running
- verifying
- reconciling
- attention-required
- completed
- cancelled
- failed

内部のAttempt、Lease、Worker identity、Policy Revision等は返しません。

特に `running` は「Workerが現在CPU命令を実行していることを証明した」という意味ではなく、Client向けのderived statusです。

## Effect semantics

P0/MVPのTaskRequestでは `effect_mode = none` だけを許可します。

これは将来external effectを扱わないという意味ではありません。

Side-effect UNKNOWN / reconciliationのArchitectureは既に定義済みですが、MVPで不完全なgeneric effect executionを先に公開しないためです。

後続versionで追加する場合も、v1の `none` semanticsを変更しません。

## Error

Error bodyはRFC 9457 Problem DetailsにEKI extensionを追加します。

machine logicは次を利用します。

- HTTP status
- `code`
- `retryable`
- optional `policy_reason_category`

`detail` は人間向けであり、Clientがbranchするstable APIにはしません。

Security上の理由で存在を開示すべきでないTask / Artifactでは、403と404の使い分けをDeployment Policyで情報漏えいしないように扱います。

## Data class

External Clientの `requested_data_class` はSecurity classificationを下げるAuthorityではありません。

Effective Data ClassはAdmission / Data Authorityが入力Artifact、Executor requirement、organization policy等から安全側へ確定します。

## P0で未固定のもの

- Authentication mechanism
- OAuth scope naming
- exact server base URL
- pagination
- event / webhook
- streaming Task status
- Browser-specific CORS policy
- Worker protocol
- signed internal Decision CDDL
- cryptographic signature algorithm
- SDK generation tool
- generic side-effect Task
- Federation

## Conformanceで必ず確認するもの

### Parser

- duplicate JSON property
- oversized body
- excessive nesting
- invalid UTF-8
- unknown core property

### TaskRequest

- `worker_id` 等のClient-selected Worker fieldをreject
- arbitrary executable pathをreject
- arbitrary input URLをreject
- unsupported effect modeをreject
- Idempotency-Key conflict
- resource numeric bound
- extension count / payload bound

### Artifact

- upload size超過
- expected digest mismatch
- expired upload session
- unknown / unauthorized artifact
- facetをClient入力で昇格できないこと

### Public response

- Worker hostname / IPを含めない
- Lease / Attempt topologyを含めない
- raw verification evidenceを含めない

## 次のProtocol work unit

P0 merge後は、別branchでP1 Worker Planeを定義します。

候補schema:

- WorkerHello
- ResourceAdvertisement
- WorkOrder carrier
- WorkOrderAck
- Heartbeat
- LeaseRenewal
- ResultClaim carrier
- Reclaim / Stop signal

P1でもAuthority Decisionのcanonical signed payload自体はP2 CDDL / COSEへ残します。

## M0 Reference Implementationでの扱い

Reference Implementation（`internal/control/clientapi`）は、P0 OpenAPIの`submitTask`、`getTaskStatus`、`cancelTask`を実装しています。応答のschemaが`openapi.json`と一致することは、テストが同じ文書を読んで確認します。

認証には、M0に限り固定のBearer tokenを使います（[ADR-0006](../../plan/adr/0006-m0-client-api-authentication-and-admission.md)）。P0が宣言していない次の応答を返します。契約へ反映する場合は、別の変更として`openapi.json`を更新してください。

- `401` — 認証がない、または認証に失敗した
- `405` — 対象pathに対して許可されないmethod
- `501` — `getTaskResult`と、Artifact関連のoperation。Result取得とArtifact Storeは未実装

Artifactを入力にするTaskは、入力を認可できないため`422`（`ARTIFACT_NOT_AVAILABLE`）で拒否します。
