# ADR-0002 参照実装にGoを採用する

- Status: **Accepted for MVP**
- Date: 2026-09-21

## 背景

EKI FabricのMVPでは、Control PlaneとEndpoint Worker Agentを実際に動かす必要があります。そのうえで、Task、Attempt、Lease、Local Governor、Artifactの転送、Resultの検証を、一つの縦断スライスとして確かめます。

一方でRQ-002により、External契約を参照実装の言語専用にしてはいけません。

したがって、次の二つは別の判断です。

1. EKIの参照実装を何語で書くか
2. EKIのプロトコルと契約を、何語から利用できるか

## 決定

**参照実装にはGoを採用します。**

ただし、外部契約の正本は次のとおりとし、Goのstructやインターフェースを外部契約の正本にはしません。

- External Client契約の正本は`spec/external/openapi.json`とする。
- Workerとアダプタの契約は、後で作る言語非依存のIDLとする。
- 署名付きのAuthority Decisionは、CDDLとCOSEで定める。

Goの実装パッケージは、原則として`internal/`の下に置きます。

将来のGo SDKは、Core実装のパッケージを直接公開しません。External契約向けの独立した`sdk/go`として扱います。

## 主な理由

### Endpoint Agentを軽く配布しやすい

Agentは業務端末や個人端末で常駐する可能性があります。そのため、単一のバイナリにしやすいこと、OSごとの配布が比較的単純なこと、標準ライブラリだけでもHTTP、TLS、プロセス、ファイルシステムなどの基盤を作りやすいことを重視しました。

### Control PlaneとAgentで共通の実装を持てる

Task ID、Leaseの検証、Digest、上限付きのI/O、構造化されたエラーなどの共通の処理を、同じモジュールで使い回せます。

### 実装に参加する敷居を上げすぎない

EKIは、分散システム、セキュリティ、Workerのランタイムなどを同時に扱います。言語にまで高い習熟を求めないことで、Architecture上の難しい部分へ集中しやすくします。

### 既存のSpikeを活かせる

これまでのSpikeではGoを使って検証してきました。Goを選べば、上流工程で得た小さな実証を捨てずに済みます。

## Goを選んでも固定しないもの

- External Clientが使う言語
- Executorの実装言語
- out-of-processのアダプタの実装言語
- ブラウザWorkerの実装
- ProofやAttestationを検証する部品の実装言語
- アプリケーション側の言語

WASMや外部サービスのアダプタなどを通じて、ほかの言語も第一級に扱える構造を保ちます。

## RustをCoreの参照にしない理由

Rustは、メモリ安全性や低水準の制御の点で有力です。

しかし、MVPの中心課題は、unsafeなコードを大量に書くことではありません。権限の分離、永続化された状態、プロトコルの検証、端末の統治、障害からの回復の意味を、正しく実装することです。

初期の参照をすべてRustへ寄せることはしません。Goで実装参加の負担を抑え、ネイティブのサンドボックスのバックエンドのように必要な箇所だけを別の実装で接続できるSPIを保つ方を選びます。

Rustのアダプタやランタイムを排除する判断ではありません。

## 影響

### 良い点

- M0を小さく始められる。
- ControlとAgentの共通実装を作りやすい。
- 複数プラットフォーム向けのバイナリを作りやすい。
- 既存のSpikeで得た知見を移しやすい。
- 初めて読む開発者がリポジトリを追いやすい。

### コストとリスク

- GoのランタイムやGCが、一部の厳しいランタイムの用途に向かない場合がある。
- OS固有のサンドボックスやGPUのランタイムでは、別の言語やネイティブの部品が必要になり得る。
- Goのインターフェースを、外部のSPIだと誤解しやすい。

最後の誤解を防ぐため、Goのパッケージは`internal/`を基本にし、機械可読な仕様を正本にします。

## 今後の作業

リポジトリの構成は、`cmd/eki-control`、`cmd/eki-agent`、`internal/core`、`internal/control`、`internal/agent`、`internal/spi`、`internal/adapters`、`spec/`、`conformance/`を基礎にします。
