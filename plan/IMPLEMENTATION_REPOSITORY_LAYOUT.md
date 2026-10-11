# 実装リポジトリの構成

Status: **Baseline 0.1 — 2026-09-21**

## 1. 目的

Architecture Baselineを、GoのReference Implementationに対応づける、物理的なRepositoryの構成を定義します。

目的は、次の4つです。External ContractとGoの内部の型を分けること。Core Stateのsemanticsを、QueueやStorage Adapterに逃がさないこと。Control PlaneとAgentの責務を分けること。Reference Adapterを交換しても、Coreの意味を変えないこと。

関連する文書は、次のとおりです。

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

Directoryは、必要になった時点で追加します。空のdirectoryを、大量に先行して作りません。

## 3. `cmd/`

実行するbinaryのentry pointだけを置きます。

`cmd/eki-control`は、Small Managed ProfileのControl Planeのprocessです。`cmd/eki-agent`は、Native Worker Agentです。`main.go`には、business logicやAuthority logicを書きません。configuration、dependencyの組み立て、lifecycle、serverの起動と停止に限定します。

## 4. `internal/core/`

EKIの意味論を置きます。HTTP frameworkや、具体的なStorage SDKには、依存させません。

### `ids/`

Task ID、Attempt ID、Lease ID、Decision IDなどの型と、validationです。

### `contract/`

Goの内部で扱う、正規化したsemantic modelです。External wire schemaの正本ではありません。

### `admission/`

悪意のあるTaskRequestを、TaskIRに変換するlogicです。HTTPのparse自体は置きません。

### `authority/`

Logical Authorityごとの、Decisionのinterfaceとvalidationです。SchedulerがSignerを自由に使える、汎用のinterfaceは置きません。

P2の実装では、Architecture Authority roleと、署名付きDecision kindの、最小の対応表を、ここに置きます。`decisiontrust`などのSecurity boundaryから、再利用します。

### `taskstate/`

Task、Attempt、Lease、Cancellation、Resultのstate transitionの正本です。QueueやHTTP handlerから、直接stateを書き換えません。

### `placement/`

Scheduler Proposalの後の、hard constraintの評価です。OptimizationとAuthorizationを混ぜません。

### `recovery/`

Workerの喪失、reclaim、retry、Recovery Candidate、上限を設けたreleaseを扱います。

### `budget/`

Compute、Network、Retry、Outputなどの、予約と消費のmodelです。

### `decisiontrust/`

署名付きAuthority Decisionに必要な、次のものを扱います。公開鍵のmetadata、Authority roleとDecision kindのbinding、algorithm、key type、curveの整合、keyのlifecycle、型付きの署名鍵と検証鍵の選択。Client / Workerの認証や、一般的なsemanticなtrustを集約するRegistryにはしません。メモリ上のRegistryは、M0のsemanticsとtrust snapshot用です。永続的なAdministrativeな正本は、後続のAdapterとして分離します。

## 5. `internal/control/`

ProtocolとCoreの境界です。

`clientapi/`は、P0 OpenAPIの、厳格なHTTPとJSONのparse、冪等性、公開用のresponseへの射影を担います。Worker IDやLeaseなどを、Externalなresponseに出さない、最後の境界でもあります。

`workerapi/`は、P1 Worker Planeの実装です。`artifactapi/`は、Artifactのingest、upload、descriptorです。`service/`は、Control Planeの組み立てを扱います。

## 6. `internal/agent/`

`session/`は、outboundのsession、再接続、epochを扱います。`governor/`は、Localでのresourceの拒否権を扱います。`workorder/`は、WorkOrderと前提のDecisionの検証を扱います。`runtime/`は、承認済みのExecutorを扱います。`cache/`は、検証済みのArtifact cacheを扱います。

Cache hitは、Authorityではありません。

## 7. `internal/spi/`

Go Reference Implementationの内部の、Adapter interfaceです。公開されたGo plugin APIではありません。

out-of-processや多言語のAdapter contractは、後続の機械可読なprotocolを、正本にします。

巨大な`Backend` interface一つにまとめず、State Store、Artifact Store、Signer、COSE Decoderなどを分けます。CredentialとAuthorityを、暗黙のうちに共有させないためです。

`cosedecoder/`は、悪意のある生のCOSE_Sign1 bytesと、具体的なCBOR libraryとの境界です。Coreに、libraryへの依存を持ち込みません。厳格なdecodeの結果と、`Signature1`のSig_structure encoderだけを、小さなinterfaceとして公開します。Decoderがrejectした場合は、既存の`decisiontrust.VerificationFailure`に正規化します。library固有のerrorを、Architecture contractにしません。

## 8. `internal/adapters/reference/`

MVPのReference Adapterを置きます。

製品を選定した後は、たとえばSQLiteなら、`internal/adapters/reference/statestore/sqlite/`のように、Architectureのroleと、製品の名前を分けます。

## 9. `spec/`

言語に中立なContractの正本です。

すでに、P0 External OpenAPIが入っています。後続で、`worker/`、`decisions/`、`adapter/`を、必要に応じて追加します。

## 10. `conformance/`

複数のpackageと複数のprocessにまたがる、Architecture InvariantのTest assetを置きます。

`cosedecodertest/`は、厳格な署名付きDecisionのCOSE fixtureを、具体的なcodecに適用する、共通のharnessです。fixture本体は、`conformance/vectors/`に、言語に中立なassetとして保持します。Goのharnessは、同じJSONを、`go:embed`で読み出します。

候補は、次のとおりです。

- protocolの不正な入力
- 誤ったAuthorityのsigner
- 古いLease
- malicious Client
- malicious Worker
- 再起動とreplay
- Adapterの置き換え

通常の、package内のUnit Testは、対象のpackageと同じdirectoryに置きます。

## 11. 依存の方向

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

重要なのは、Coreが、具体的なAdapterをimportしないことです。

禁止する方向は、次のとおりです。

~~~text
core -> adapters/reference/*
core -> cmd/*
core -> HTTP framework
core -> SQLite / NATS / S3 SDK
agent governor -> control scheduler
scheduler -> signer concrete key
~~~

## 12. 公開のGo packageを、急いで作らない

最初のRepositoryでは、`pkg/`を作りません。

Goのpackageを、External APIだと誤認しやすいからです。また、Coreの型が、互換性を保つ負担になり、言語に中立であることを損ないやすいからです。

Go SDKが必要になった時点で、`sdk/go/`を、External Client Contract専用として設計します。

## 13. 生成したcode

生成したcodeを、手書きのCoreと混ぜません。

P1のProtobufなどを導入した場合は、`internal/gen/worker/v1/`や`internal/gen/adapter/v1/`などを候補とします。生成元のschemaを、正本にします。

## 14. Configuration

M0が単一のbinaryの構成でも、Configのnamespaceを、論理的なcomponentごとに分けます。

たとえば、次のとおりです。

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

一つのroot credentialを、すべてのcomponentに渡す形を、避けます。

## 15. Errorの変換

Coreのerrorは、HTTP statusそのものを持たない方向を、基本とします。

~~~text
Core semantic error
   ↓ clientapi mapping
RFC 9457 Problem
~~~

Worker APIでも、同じCoreのerrorを、Worker protocol用のcodeに変換します。

## 16. Logging

Core packageから、globalなloggerに、直接依存させません。

必要なAudit Eventは、構造化したrecordとして、上位に返します。そして、Telemetry Adapterに接続できるようにします。生のcredentialやsecretを、Coreのlogに流しません。

## 17. M0で最初に作るdirectory

最初から、layout全体を作りません。

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

Task APIに進む段階で、`internal/control/clientapi`、`admission`、`identity`を追加します。Agentの登録の段階で、`internal/agent`を追加します。

## 18. Repository layoutのレビューのルール

新しいtop-levelのdirectoryを追加するときは、次の4つを確認します。

1. どのArchitectureの責務か
2. 既存のdirectoryでは、なぜ不十分か
3. 公開の互換性の境界になるか
4. credentialとauthorityの境界を変えるか

Directoryを追加しただけで、新しいAuthorityを作ったことにはしません。

## 19. 非目標

このlayoutは、次のことを意味しません。

- 各directoryが、microserviceであること
- 各SPIが、動的なpluginであること
- `internal/core`が、公開のSDKであること
- GoだけがClientとExecutorを書けること
- Reference Adapterが、Productionで唯一の選択肢であること

## 20. M0への次の一歩

Repository Layoutをmergeした後、最新の`main`から、M0 executable skeletonのbranchを切ります。

最初の実装の内容は、次のとおりです。

1. `go.mod`
2. `cmd/eki-control`
3. `cmd/eki-agent`
4. `internal/core/ids`
5. `internal/core/taskstate`の最小の型
6. `internal/spi/statestore`
7. メモリ上のReference adapterと、永続的なadapterの比較

State Storeは、`ADR-0003`で、M0とDP-1向けのReference Adapterとして、SQLiteを採用しました。実装は、`internal/adapters/reference/statestore/sqlite/`に置きます。Coreは、引き続き、`internal/spi/statestore`だけに依存します。まず、restart、CAS、migrationのsemanticsを実証し、その後でTask APIを載せます。
