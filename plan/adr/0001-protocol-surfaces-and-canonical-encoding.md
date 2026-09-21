# ADR-0001 — Protocol Surfaces and Canonical Security Encoding

- Status: **Accepted for MVP Baseline 0.1**
- Date: 2026-09-21
- Scope: External Client Plane / Worker Plane / out-of-process Adapter Plane / signed Authority Decisions

## Context

EKI Fabricは、未知のApplication、Native Worker、将来のBrowser Worker、企業Network、複数言語Adapterを同時に扱う必要があります。

一方、次の要件は同じProtocol技術へ最適化されません。

- External APIはcurl等から扱いやすく、language-neutralで、企業Proxyと共存しやすいこと。
- Worker Planeはoutbound session、streaming、deadline、typed message、efficient binary encodingが欲しいこと。
- Security-sensitive Decisionは異なる言語・build・schema evolutionを跨いでも署名対象bytesが曖昧にならないこと。
- Bulk ArtifactはRPC messageへbase64埋込せずData Planeでstreamすること。

特にProtocol Buffersの公式文書は、deterministic serializationであってもcanonical serializationではなく、異なる言語やbuild、unknown fieldを跨いだstable fingerprintとして使うべきではないと説明しています。

そのため、Transport IDLとCryptographic canonical formを同一にしません。

## Decision Summary

### External Client Plane

**HTTPS + JSON HTTP API + OpenAPI**をBaselineとします。

- OpenAPI Specification: 3.2.1をArchitecture上のcurrent targetとする。
- P0 schemaではtooling互換性を優先し、3.2-only featureへ依存しないconservative subsetから開始する。
- JSON bodyはstrict parse / schema validationする。
- HTTP error bodyはRFC 9457 Problem Detailsを基本形とする。
- Artifact bulk bytesはJSONへbase64埋込せずData Plane upload/downloadへ分離する。
- Public Task statusは初回MVPではpollingで成立させ、event streamingは後続拡張とする。

### Worker Plane

**Protocol Buffers IDL + Connect protocol over HTTPS**をReference Transportとします。

- Workerはoutbound-first connectionを使用する。
- Connect protocolをMVP referenceとし、HTTP/1.1 / HTTP/2環境との共存を優先する。
- 同一Protobuf service definitionからgRPC / gRPC-Web interoperabilityを将来利用できる構造を保つ。
- Long-lived streamingがenterprise proxyで利用困難な環境向けに、unary / bounded polling型のfallback profileをProtocol semantics上許容する。
- Workerへinternal Queue / Broker protocolを直接公開しない。

### Out-of-process Adapter Plane

**Protocol Buffers IDL + Connect reference protocol**を基本とします。

In-process Go Adapterは同じLogical SPIをGo interfaceで実装できますが、Go interfaceをSPIの唯一の正本にしません。

### Signed Authority Decisions

**Deterministically Encoded CBOR + CDDL + COSE_Sign1**をBaselineとします。

- CBOR: RFC 8949 core deterministic encoding requirementsを基礎にする。
- Schema notation: CDDLを使用する。
- Signature envelope: COSE_Sign1を使用する。
- Signature algorithm profile / key typeは別ADRまたはSecurity Profileで決める。
- security-critical payloadへfloating pointを原則使わない。
- timestamp / revision / byte count / budget等は単位を固定したintegerとして表現する。
- unknown critical field / unsupported decision versionはfail closedする。

### Semantic Fingerprint

Task idempotencyやDecision bindingで、raw JSON bytesやraw Protobuf serializationをfingerprintしません。

External JSONをstrict parse / validate / normalizeした後、用途別のsemantic fingerprint projectionをdeterministic CBORへencodeし、そのbytesをhashします。

Artifact Content DigestだけはArtifact raw bytesそのものを対象にします。

---

## Why this split

### External HTTP / JSON

未知のClientにSDK必須を強制しません。

HTTP APIは:

- curl / generic HTTP clientから利用可能
- Browser / CLI / Python / Go / Rust等から扱いやすい
- enterprise proxy / gateway / observabilityと統合しやすい
- OpenAPIで人間とmachineの両方へ説明しやすい

という利点があります。

OpenAPI公式Specification indexでは2026-09-10公開の3.2.1が掲載されています。3.2系ではstreaming media typeの記述も強化されていますが、MVP P0ではstreaming機能自体を必須にしません。

### Worker Connect / Protobuf

Worker Planeでは、public REST resource modelより:

- typed request / response
- server / client streaming
- deadline
- generated clients
- binary payload efficiency
- message evolution

が重要です。

ConnectはProtobuf schemaから生成され、Connect / gRPC / gRPC-Webの互換Surfaceを持ち、Connect protocol自体はHTTP/1.1 / HTTP/2上で動作します。

EKIのenterprise defaultがoutbound-only / proxy-compatibleであること、将来Browser Workerを持つことと相性がよいと判断します。

ただしConnect library自体をEKI Core semantic contractにはしません。

Transportを別実装へ変更してもWorker Contract semanticsは維持します。

### Deterministic CBOR / COSE

Authority Decisionでは、署名検証側が同一semantic payloadから同じbytesを再構築できる必要があります。

RFC 8949はdeterministic CBORのcore requirementsを定義しています。

COSE_Sign1はsingle signerのsigned data objectを標準化しています。

CBORは64-bit integerやbyte stringを自然に扱えるため、JCSのI-JSON number制約をSecurity payloadへ持ち込みにくい利点があります。

---

## Alternatives Considered

### A. Everything REST / JSON

利点:

- 最も分かりやすい
- curl / Browser / proxy friendly
- OpenAPI一つで統一可能

不採用理由:

- Worker long-lived streaming / typed generated protocolで追加設計が必要
- signed Decisionのcanonicalizationを別途解決する必要がある
- binary worker chatterの効率が下がる

External Planeには採用しますが、全Planeへは広げません。

### B. Everything gRPC / Protobuf

利点:

- mature RPC
- bidirectional streaming
- polyglot code generation
- efficient binary encoding

不採用理由:

- public unknown ClientにgRPC toolchainを要求したくない
- Browser / proxy環境では追加のgRPC-Web / gateway考慮が必要
- Protobuf serializationはcryptographic canonical formではない

Worker / Adapter Planeの候補として利用します。

### C. Connect / Protobuf for everything

利点:

- HTTP friendly
- browser friendly
- gRPC compatibility
- generated clients

不採用理由:

- public resource APIを特定RPC ecosystemへ寄せすぎる
- OpenAPI / plain HTTP integrationを第一級に保ちたい
- cryptographic canonicalization問題は残る

### D. JSON + JCS for signed Decision

JCS (RFC 8785)はJSONのcanonical representationを定義しており、有力候補です。

不採用理由:

- I-JSON / IEEE 754 double precision制約をSecurity-critical numeric fieldへ持ち込む
- byte stringや大きなintegerをstring encodingへ寄せる必要がある
- Internal Authority payloadは人間可読wireである必要性が低い

External requestのlogging / comparison用途には利用可能ですが、Baseline signature encodingには採用しません。

### E. Raw Protobuf deterministic bytesをsign

不採用です。

Protobuf公式文書がdeterministic serializationとcanonical serializationを明確に区別し、異なる言語/build/schema evolutionを跨いだgeneric fingerprintingを推奨していません。

---

## External Client API Profile

### HTTP

- TLS protected HTTPS
- HTTP/1.1を最低互換Surfaceとして許容
- HTTP/2を利用可能
- HTTP/3をMVP必須にしない

### JSON

External JSONは:

- duplicate property reject
- maximum body size
- maximum nesting
- maximum string / array / metadata count
- integer range validation
- unknown core field reject

を行います。

JSON numberへ任意精度integerを暗黙に期待しません。

security / budget上64-bit精度が必要なfieldはschema上のrangeを明示し、必要ならdecimal string等の明示表現を採用します。

### OpenAPI

OpenAPI 3.2.1をcanonical API description targetとします。

ただし2026-09時点で3.2.1は新しいため、P0では:

- ordinary JSON request / response
- standard HTTP methods
- no required 3.2-only streaming construct

のconservative subsetを使います。

Toolchainが3.2.1を処理できない場合、source semantic schemaを弱めずに3.1-compatible derived artifactを生成できる余地を残します。

### Error

HTTP errorsはRFC 9457 `application/problem+json`を基本にします。

EKI固有extensionとして:

- stable error code
- task / attempt correlation where disclosure-safe
- retryable category
- policy reason category

を追加できます。

`detail`文字列をmachine logicに使わせません。

---

## Worker Plane Profile

### Protobuf

ProtobufはWorker PlaneのIDL / transport message formatとして使用します。

用途:

- WorkerHello
- ResourceAdvertisement
- WorkOffer / WorkOrder carrier
- WorkOrderAck
- Heartbeat
- LeaseRenewalRequest / Result
- ResultClaim carrier
- Stop / Reclaim signal

Security-sensitive Authority Decisionの署名対象そのものには使いません。

### Connect

Reference transportはConnectです。

理由:

- standard HTTP infrastructureへ載せやすい
- HTTP/1.1 / HTTP/2を扱える
- gRPC / gRPC-Web ecosystemとのinteroperability pathを持つ
- Browser Workerへ拡張しやすい
- Goのnet/httpとの統合が軽い

### Session modes

MVPでは二つを許容します。

1. streaming session
2. polling / unary fallback

両方で:

- WorkOrder ID
- Attempt ID
- Lease revision
- duplicate tolerance

を同じにし、transport modeでState semanticsを変えません。

---

## Bulk Artifact Profile

Artifact bytesはRPC messageに入れません。

Control Plane APIでUpload Intent / ArtifactGrantを取得し、Data Plane endpointへ:

- `application/octet-stream`
- multipart / resumable adapter-specific transport

等でtransferします。

Content Digestはraw Artifact bytesを対象にします。

compressionを使用する場合、stored identityがcompressed bytesかlogical decompressed bytesかをArtifact Media/Profileで明示します。

archive expansion後内容を元Artifact Digestと混同しません。

---

## Signed Decision Profile

### Payload

Decision payloadはdeterministic CBORでencodeします。

### Schema

CDDLで各versionのallowed map / field / typeを定義します。

### Envelope

COSE_Sign1を使用します。

Protected metadataに最低限:

- algorithm identifier
- key identifier
- content / decision type

を扱える構造とします。

Authority ID、Decision Kind、schema versionはsignature-covered semanticsから外さないようにします。

### Cryptographic algorithm

このADRでは固定しません。

理由:

- enterprise PKI / KMS適合性
- platform crypto support
- algorithm agility
- current standards

を別途Security Profileで比較するためです。

WorkerがalgorithmをClient inputから選ばせる構造にはしません。

---

## Fingerprint Profile

Idempotencyやbinding digestは用途別Projectionを定義します。

例:

```text
TaskRequestFingerprintV1
  principal scope
  logical executor
  normalized inputs
  normalized params
  resource request
  reliability semantics
```

External JSONの:

- whitespace
- object property order
- equivalent textual formatting

でFingerprintが変わらないことを目指します。

一方、意味が異なるunknown / omitted / default fieldを誤って同一視しないよう、Projection versionを固定します。

---

## Security Parser Boundary

External JSON parser、Protobuf decoder、CBOR decoderはいずれもattack surfaceです。

共通制約:

- maximum message bytes
- maximum nesting
- bounded repeated fields
- bounded string / bytes
- decompression limit
- no recursive unbounded extension
- unknown critical semantics reject
- parser panic isolation / fuzzing

を要求します。

---

## Compression

Control message compressionはoptionalです。

Compression bombを避けるため:

- compressed size limit
- decompressed size limit
- supported algorithm allowlist
- no nested transparent compression

を適用します。

Bulk Artifact compressionはArtifact profile / Safe Processing Budgetで別に扱います。

---

## Compatibility Rules

### External API

- additive optional fieldはminor evolution候補
- semantic changeはversionを上げる
- unknown core fieldはMVP strict modeでreject
- namespaced optional extensionだけforward-compatibleに扱う

### Protobuf

- field number reuse禁止
- removed fieldはreserve
- enum unknown値をSecurity Decisionで安全側へ処理
- required security semanticsをunknown fieldに置かない

### Signed CBOR Decision

- Decision schema version必須
- unknown field policyをversionごとに定義
- security-critical unknownはreject
- signed old versionをsilent reinterpretしない

---

## MVP Technology Cut

### P0 External

- HTTPS
- JSON
- OpenAPI 3.2.1 description
- RFC 9457 errors

### P1 Worker

- Protobuf schema
- Connect reference transport
- polling fallback

### P2 Security Decision

- deterministic CBOR
- CDDL
- COSE_Sign1

### Bulk

- Data Plane binary transfer

---

## Consequences

### Positive

- External Clientは特定SDKに依存しない。
- Worker Planeはtyped / streaming-friendly。
- future Browser Workerへ同一Protobuf service modelを再利用しやすい。
- signed DecisionがProtobuf serialization stabilityへ依存しない。
- Control metadataとbulk dataが分離される。
- language-neutralityを維持しつつGo reference implementationを簡潔にできる。

### Cost

- JSON / Protobuf / CBORの3 data representationを扱う必要がある。
- semantic modelとwire modelの変換Testが必要。
- CDDL / COSE toolchainを追加する。
- Schema source-of-truthの管理ルールが必要。

この複雑さは、Security signature bytesとpublic/worker ergonomicsを一つのformatへ無理に押し込むより明示的であると判断します。

---

## Required Conformance Tests

### JSON / OpenAPI

- duplicate property
- unknown core field
- numeric boundary
- oversized body
- invalid UTF-8 / string constraint
- semantic fingerprint stability

### Protobuf / Connect

- unknown enum security behavior
- duplicate delivery
- stream reconnect
- HTTP/1.1 fallback
- message size limit
- deadline / cancellation

### CBOR / COSE

- non-deterministic encoding reject or canonical re-encode policy
- duplicate map key
- wrong Decision Kind
- wrong key ID / Authority
- mutated payload
- stale version
- unknown critical field
- signature algorithm allowlist

---

## Sources

- OpenAPI Specification 3.2.1: https://spec.openapis.org/oas/v3.2.1.html
- RFC 9457 Problem Details: https://www.rfc-editor.org/rfc/rfc9457.html
- gRPC Core Concepts: https://grpc.io/docs/what-is-grpc/core-concepts/
- Connect: https://connectrpc.com/
- Protocol Buffers — Serialization Is Not Canonical: https://protobuf.dev/programming-guides/serialization-not-canonical/
- RFC 8949 CBOR: https://www.rfc-editor.org/rfc/rfc8949.html
- RFC 8610 CDDL: https://www.rfc-editor.org/rfc/rfc8610.html
- RFC 9052 COSE Structures: https://www.rfc-editor.org/rfc/rfc9052.html
- RFC 8785 JCS: https://www.rfc-editor.org/rfc/rfc8785.html

## Follow-up

次はこのADRに基づきP0 machine-readable schemaを作成します。

対象:

- TaskRequest
- Task public status
- RFC 9457 EKI Problem extension
- Artifact ingest intent / descriptor

Worker Protobuf / signed Decision CDDLはP1 / P2として別work unitで追加します。
