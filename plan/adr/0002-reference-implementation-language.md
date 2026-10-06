# ADR-0002 Reference ImplementationにGoを採用する

- Status: **Accepted for MVP**
- Date: 2026-09-21

## Context

EKI FabricのMVPでは、Control PlaneとEndpoint Worker Agentを実際に動かす必要があります。そのうえで、Task、Attempt、Lease、Local Governor、Artifactの転送、Resultの検証を、一つのvertical sliceとして確かめます。

一方でRQ-002により、External ContractをReference実装の言語専用にしてはいけません。

したがって、次の二つは別の判断です。

1. EKIのReference Implementationを何語で書くか
2. EKIのProtocolとContractを、何語から利用できるか

## Decision

**Reference ImplementationにはGoを採用します。**

ただし、外部Contractの正本は次のとおりとし、Goのstructやinterfaceを外部Contractの正本にはしません。

- External Client Contractの正本は`spec/external/openapi.json`とする。
- WorkerとAdapterのContractは、後で作る言語非依存のIDLとする。
- 署名付きのAuthority Decisionは、CDDLとCOSEで定める。

Goの実装packageは、原則として`internal/`の下に置きます。

将来のGo SDKは、Core実装のpackageを直接公開しません。External Contract向けの独立した`sdk/go`として扱います。

## 主な理由

### Endpoint Agentを軽く配布しやすい

Agentは業務端末や個人端末で常駐する可能性があります。そのため、単一のbinaryにしやすいこと、OSごとの配布が比較的単純なこと、標準libraryだけでもHTTP、TLS、process、filesystemなどの基盤を作りやすいことを重視しました。

### Control PlaneとAgentで共通の実装を持てる

Task ID、Leaseの検証、Digest、上限付きのI/O、構造化されたerrorなどの共通の処理を、同じmoduleで使い回せます。

### 実装に参加する敷居を上げすぎない

EKIは、分散system、security、Workerのruntimeなどを同時に扱います。言語にまで高い習熟を求めないことで、Architecture上の難しい部分へ集中しやすくします。

### 既存のSpikeを活かせる

これまでのSpikeではGoを使って検証してきました。Goを選べば、上流工程で得た小さな実証を捨てずに済みます。

## Goを選んでも固定しないもの

- External Clientが使う言語
- Executorの実装言語
- out-of-processのAdapterの実装言語
- Browser Workerの実装
- ProofやAttestationを検証する部品の実装言語
- Application側の言語

WASMや外部serviceのAdapterなどを通じて、ほかの言語も第一級に扱える構造を保ちます。

## RustをCoreのReferenceにしない理由

Rustは、memory安全性や低水準の制御の点で有力です。

しかし、MVPの中心課題は、unsafeなcodeを大量に書くことではありません。権限の分離、永続化された状態、protocolの検証、端末の統治、障害からの回復の意味を、正しく実装することです。

初期のReferenceをすべてRustへ寄せることはしません。Goで実装参加の負担を抑え、nativeのsandbox backendのように必要な箇所だけを別の実装で接続できるSPIを保つ方を選びます。

RustのAdapterやRuntimeを排除する判断ではありません。

## Consequences

### Positive

- M0を小さく始められる。
- ControlとAgentの共通実装を作りやすい。
- 複数platform向けのbinaryを作りやすい。
- 既存のSpikeで得た知見を移しやすい。
- 初めて読む開発者がRepositoryを追いやすい。

### Cost / Risk

- GoのruntimeやGCが、一部の厳しいruntimeの用途に向かない場合がある。
- OS固有のsandboxやGPUのruntimeでは、別の言語やnativeの部品が必要になり得る。
- Goのinterfaceを、外部のSPIだと誤解しやすい。

最後の誤解を防ぐため、Goのpackageは`internal/`を基本にし、機械可読なspecを正本にします。

## Follow-up

Repository Layoutは、`cmd/eki-control`、`cmd/eki-agent`、`internal/core`、`internal/control`、`internal/agent`、`internal/spi`、`internal/adapters`、`spec/`、`conformance/`を基礎にします。
