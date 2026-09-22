# Implementation Repository Layout

Status: **Baseline 0.1 — 2026-09-21**

## 1. 目的

Architecture BaselineをGo Reference Implementationへ写像する物理Repository構成を定義します。

目的は、External ContractとGo内部型を分け、Core State semanticsをQueueやStorage Adapterへ逃がさず、Control PlaneとAgentの責務を分け、Reference Adapterを交換してもCoreの意味を変えないことです。

関連:

- `../architecture/MVP_IMPLEMENTATION_PLAN.md`
- `../architecture/EXTENSION_SPI.md`
- `../plan/adr/0002-reference-implementation-language.md`

## 2. Baseline layout

~~~text
eki-fabric/
├── cmd/
│   ├── eki-control/
│   └── eki-agent/
├── internal/
│   ├── core/
│   │   ├── ids/
│   │   ├── contract/
│   │   ├── admission/
│   │   ├── authority/
│   │   ├── taskstate/
│   │   ├── placement/
│   │   ├── recovery/
│   │   └── budget/
│   ├── control/
│   │   ├── clientapi/
│   │   ├── workerapi/
│   │   ├── artifactapi/
│   │   └── service/
│   ├── agent/
│   │   ├── session/
│   │   ├── governor/
│   │   ├── workorder/
│   │   ├── runtime/
│   │   └── cache/
│   ├── spi/
│   │   ├── statestore/
│   │   ├── delivery/
│   │   ├── artifact/
│   │   ├── identity/
│   │   ├── signer/
│   │   ├── executor/
│   │   ├── scheduler/
│   │   ├── verification/
│   │   └── telemetry/
│   └── adapters/
│       └── reference/
├── spec/
├── conformance/
├── docs/
├── architecture/
├── requirements/
├── research/
├── spikes/
└── plan/
~~~

Directoryは必要になった時点で追加します。空directoryを大量に先行作成しません。

## 3. `cmd/`

実行binaryのentry pointだけを置きます。

`cmd/eki-control` はSmall Managed ProfileのControl Plane process、`cmd/eki-agent` はNative Worker Agentです。`main.go`にはbusiness logicやAuthority logicを書かず、configuration、dependency wiring、lifecycle、server start/stopに限定します。

## 4. `internal/core/`

EKIの意味論を置き、HTTP frameworkやconcrete Storage SDKへ依存させません。

### `ids/`

Task ID、Attempt ID、Lease ID、Decision ID等の型とvalidation。

### `contract/`

Go内部で扱うnormalized semantic modelです。External wire schemaの正本ではありません。

### `admission/`

Hostile TaskRequestをTaskIRへ変換するlogic。HTTP parsing自体は置きません。

### `authority/`

Logical AuthorityごとのDecision interface / validation。SchedulerがSignerを自由に使えるgeneric interfaceを置きません。

P2実装ではArchitecture Authority roleとsigned Decision kindの最小対応表をここへ置き、`decisiontrust`等のSecurity boundaryから再利用します。

### `taskstate/`

Task / Attempt / Lease / Cancellation / Result state transitionの正本。QueueやHTTP handlerから直接stateを書き換えません。

### `placement/`

Scheduler Proposal後のhard constraint評価。OptimizationとAuthorizationを混ぜません。

### `recovery/`

Worker loss、reclaim、retry、Recovery Candidate、bounded release。

### `budget/`

Compute / Network / Retry / Output等のreservation / consumption model。

### `decisiontrust/`

signed Authority Decisionに必要なpublic-key metadata、Authority role/Decision kind binding、algorithm/key type/curve整合、key lifecycle、typed signing/verification key selectionを扱います。Client/Worker authenticationや一般的なsemantic trustを集約するRegistryにはしません。in-memory RegistryはM0 semantics / trust snapshot用であり、durable Administrative source-of-truthは後続Adapterとして分離します。

## 5. `internal/control/`

ProtocolとCoreの境界です。

`clientapi/` はP0 OpenAPIのstrict HTTP/JSON parsing、idempotency、public response projectionを担います。Worker IDやLease等をExternal responseへ出さない最後の境界でもあります。

`workerapi/` はP1 Worker Plane実装、`artifactapi/` はArtifact ingest/upload/descriptor、`service/` はControl Plane compositionを扱います。

## 6. `internal/agent/`

`session/` はoutbound session / reconnect / epoch、`governor/` はLocal Resource veto、`workorder/` はWorkOrderと前提Decisionの検証、`runtime/` はapproved Executor、`cache/` はverified Artifact cacheを扱います。

Cache hitはAuthorityではありません。

## 7. `internal/spi/`

Go Reference Implementation内部のAdapter interfaceです。public Go plugin APIではありません。

out-of-process / polyglot Adapter contractは後続machine-readable protocolを正本にします。

巨大な `Backend` interface一つへまとめず、State Store、Artifact Store、Signer、COSE Decoder等を分けます。CredentialとAuthorityを暗黙共有させないためです。

`cosedecoder/`はhostile raw COSE_Sign1 bytesとConcrete CBOR libraryの境界です。Coreへlibrary依存を持ち込まず、strict decode結果と`Signature1` Sig_structure encoderだけを小さなinterfaceとして公開します。Decoder rejectは既存`decisiontrust.VerificationFailure`へ正規化し、library固有errorをArchitecture contractへしません。

## 8. `internal/adapters/reference/`

MVPのReference Adapterを置きます。

製品選定後は、たとえばSQLiteなら `internal/adapters/reference/statestore/sqlite/` のように、Architecture roleと製品名を分けます。

## 9. `spec/`

言語中立Contractの正本です。

既にP0 External OpenAPIが入っています。後続で `worker/`、`decisions/`、`adapter/` を必要に応じて追加します。

## 10. `conformance/`

複数package・複数processを跨ぐArchitecture InvariantのTest assetを置きます。

`cosedecodertest/`はstrict signed-decision COSE fixtureをConcrete codecへ適用する共通harnessです。fixture本体は`conformance/vectors/`を言語中立assetとして保持し、Go harnessは同じJSONを`go:embed`で読み出します。

候補:

- protocol malformed input
- wrong Authority signer
- stale Lease
- malicious Client
- malicious Worker
- restart / replay
- Adapter replacement

通常のpackage-local Unit Testは対象packageと同じdirectoryへ置きます。

## 11. Dependency direction

~~~text
cmd
 ↓
control / agent
 ↓
core
 ↓
SPI abstractions

adapters
  └─ SPIを実装
~~~

重要なのはCoreがconcrete Adapterをimportしないことです。

禁止する方向:

~~~text
core -> adapters/reference/*
core -> cmd/*
core -> HTTP framework
core -> SQLite / NATS / S3 SDK
agent governor -> control scheduler
scheduler -> signer concrete key
~~~

## 12. Public Go packageを急いで作らない

初期Repositoryでは `pkg/` を作りません。

Go packageをExternal APIだと誤認しやすく、Core型を互換性負債にし、language-neutralityを損ないやすいためです。

Go SDKが必要になった時点で `sdk/go/` をExternal Client Contract専用として設計します。

## 13. Generated code

Generated codeを手書きCoreと混ぜません。

P1 Protobuf等を導入した場合は `internal/gen/worker/v1/`、`internal/gen/adapter/v1/` 等を候補とし、生成元schemaを正本にします。

## 14. Configuration

M0が単一binary構成でも、Config namespaceをLogical component別にします。

例:

~~~text
control.client_api
control.worker_api
state_store
artifact_store
identity
signer.workorder
signer.artifact_grant
agent.governor
agent.runtime
~~~

一つのroot credentialを全componentへ渡す形を避けます。

## 15. Error translation

Core errorはHTTP statusそのものを持たない方向を基本にします。

~~~text
Core semantic error
   ↓ clientapi mapping
RFC 9457 Problem
~~~

Worker APIでも同じCore errorをWorker protocol用codeへ変換します。

## 16. Logging

Core packageからglobal loggerへ直接依存させません。

必要なAudit Eventはstructured recordとして上位へ返し、Telemetry Adapterへ接続できるようにします。raw credentialやsecretをCore logへ流しません。

## 17. M0で最初に作るdirectory

最初から全layoutを作りません。

~~~text
cmd/eki-control/
cmd/eki-agent/

internal/core/
  ids/
  contract/
  taskstate/

internal/spi/
  statestore/

internal/adapters/reference/
  statestore/

spec/
~~~

Task APIへ進む段階で `internal/control/clientapi`、`admission`、`identity` を追加し、Agent registrationで `internal/agent` を追加します。

## 18. Repository layout review rule

新しいtop-level directoryを追加するときは、

1. どのArchitecture responsibilityか
2. 既存directoryではなぜ不十分か
3. public compatibility boundaryになるか
4. credential / authority boundaryを変えるか

を確認します。

Directory追加だけで新しいAuthorityを作ったことにはしません。

## 19. Non-goals

このlayoutは、各directory=microservice、各SPI=dynamic plugin、`internal/core`=public SDK、GoのみがClient/Executorを書ける、Reference AdapterがProduction唯一の選択肢、という意味ではありません。

## 20. M0への次の一歩

Repository Layout merge後、最新 `main` からM0 executable skeleton branchを切ります。

最初の実装内容:

1. `go.mod`
2. `cmd/eki-control`
3. `cmd/eki-agent`
4. `internal/core/ids`
5. `internal/core/taskstate` の最小型
6. `internal/spi/statestore`
7. Reference in-memory / durable adapterの比較

State Storeは`ADR-0003`で、M0 / DP-1向けReference AdapterとしてSQLiteを採用しました。実装は`internal/adapters/reference/statestore/sqlite/`へ置き、Coreは引き続き`internal/spi/statestore`だけへ依存します。まずrestart / CAS / migration semanticsを実証し、その後Task APIを載せます。
