# ADR-0002 — GoをReference Implementation Languageとする

- Status: **Accepted for MVP**
- Date: 2026-09-21

## Context

EKI FabricのMVPでは、Control PlaneとEndpoint Worker Agentを実際に動かし、Task / Attempt / Lease、Local Governor、Artifact transfer、Result verificationを一つのvertical sliceとして検証する必要があります。

一方でRQ-002により、External ContractをReference実装の言語専用にしてはいけません。

したがって、次の二つは別の判断です。

1. EKIのReference Implementationを何語で書くか
2. EKIのProtocol / Contractを何語から利用できるか

## Decision

**Reference ImplementationはGoを採用します。**

ただし、

- External Client Contractの正本は `spec/external/openapi.json`
- Worker / Adapter Contractは後続のlanguage-neutral IDL
- signed Authority DecisionはCDDL / COSE

とし、Go struct / interfaceを外部Contractの正本にしません。

Goの実装packageは原則 `internal/` 配下へ置きます。

将来のGo SDKは、Core実装packageを直接公開するのではなく、External Contract向けの独立した `sdk/go` として扱います。

## 主な理由

### Endpoint Agentを軽く配布しやすい

業務端末や個人端末で常駐する可能性があるため、単一binary化しやすく、OSごとの配布が比較的単純で、標準libraryだけでもHTTP、TLS、process、filesystem等の基盤を作りやすい点を重視します。

### Control PlaneとAgentで共通実装を持てる

Task ID、Lease検証、Digest、bounded I/O、structured error等の共通logicを同じmoduleで再利用できます。

### 実装参加の敷居を過度に上げない

EKIは分散system、security、worker runtime等を同時に扱います。言語側まで高い習熟を前提にせず、Architecture上難しい部分へ集中しやすくします。

### 既存Spikeを活用できる

既存SpikeでGoを使って検証してきたため、上流工程で得た小さな実証を捨てずに済みます。

## Goを選んでも固定しないもの

- External Clientの利用言語
- Executor実装言語
- out-of-process Adapter実装言語
- Browser Worker実装
- Proof / Attestation verifierの実装言語
- Application側の言語

WASMやexternal service Adapter等を通じて、他言語をfirst-classに扱える構造を維持します。

## RustをCore Referenceにしない理由

Rustはmemory safetyや低level制御の点で有力です。

ただしMVPの中心課題はunsafe codeを大量に書くことではなく、Authority separation、durable state、protocol validation、endpoint governance、recovery semanticsを正しく実装することです。

初期Reference全体をRustへ寄せるより、Goで実装参加の負荷を抑え、native sandbox backend等の必要箇所だけ別実装を接続できるSPIを保つ方を選びます。

Rust製AdapterやRuntimeを排除する判断ではありません。

## Consequences

### Positive

- M0を小さく開始できる。
- Control / Agentの共通実装が容易。
- Cross-platform binaryを作りやすい。
- 既存Spikeの知見を移しやすい。
- 初見の開発者がRepositoryを追いやすい。

### Cost / Risk

- Go runtime / GCが一部の厳しいruntime用途に不向きな場合がある。
- OS-specific sandboxやGPU runtimeでは別言語・native componentが必要になり得る。
- Go interfaceを外部SPIだと誤認しやすい。

最後の点を防ぐため、Go packageは `internal/` を基本とし、machine-readable specを正本にします。

## Follow-up

Repository Layoutでは `cmd/eki-control`、`cmd/eki-agent`、`internal/core`、`internal/control`、`internal/agent`、`internal/spi`、`internal/adapters`、`spec/`、`conformance/` を基礎にします。
