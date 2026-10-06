# ADR-0009 — 管理された資源は既存オーケストレータを置き換えず、Resource Providerとして接続する

- Status: **Proposed**
- Date: 2026-10-06
- Related: Issue #67, RQ-001, RQ-003, RQ-004, RQ-019, RQ-030, [orchestrator-integration-and-layering](../../research/orchestrator-integration-and-layering.md)

## 1. Context

[value.md](../../product/value.md)の「Managed and opportunistic resources on one fabric」は、2種類の資源を同じTask契約、同じ配置判断、同じ検証、同じ監査で扱うことを掲げています。一つは専用サーバや、KubernetesやSlurmなどが管理するクラスタです。もう一つは業務端末やBrowserのように一時的に使える資源です。

単純に作る方法は二つ考えられますが、どちらもこの価値と矛盾します。

- **EKIがクラスタを直接管理する方法**では、Nodeの管理、常駐Service、ネットワーク構成までEKIが持つことになる。これは既存オーケストレータを置き換えることになり、企業が使い慣れた運用経路（原則 §12）とぶつかる。
- **管理された資源を信頼済みとして特別扱いする方法**では、たとえば「Kubernetes上のWorkerは検証しない」と決めることになる。そうすると、改変されたPodや設定ミスが検証を通り抜けてしまう（原則 §1、§9）。

## 2. Decision

1. 既存オーケストレータは、**Resource Provider**としてEKIへ接続する。EKIはProviderへ、制約、期間、量を指定して実行枠を依頼する。Providerはその枠をWorkerとして提供する。Podの配置やNodeの管理は、引き続きオーケストレータが受け持つ。
2. 管理された資源から来たWorkerも、ほかのWorkerと同じく、Lease、WorkOrder、Resultの検証を経る。「管理されている」ことは、Assurance Levelや配置の優先度を上げる根拠の一つにすぎない。この根拠は管理者が設定するpolicyでだけ表現し、Worker自身の申告では表現できない（RQ-047）。
3. Providerの接続では、特定製品のAPIをCoreへ入れない。Extension SPI（[EXTENSION_SPI.md](../../architecture/EXTENSION_SPI.md)）のAdapterとして実装する。MVP後の最初の検証では、Kubernetes系とbatch scheduler系のように性質の異なる2系統を接続できることを確かめる。1系統だけでは、その製品の拡張機能と区別がつかないので、完了としない。
4. 業務Application本体のように終わりのない常駐ServiceはEKIの対象外とし、Providerへの依頼にも含めない。

## 3. Consequences

### Positive

- 遊休資源を活用しにくい組織でも、管理クラスタを統治するだけで導入価値が成り立つ。
- 管理された資源と遊休資源とで、Resultの検証や監査の基準が分かれない。

### Negative / Limitations

- Podの起動やqueueの待ちといったオーケストレータ側の待ち時間が配置判断に入ってくるので、Suitability Gate（ADR-0007）の規則が複雑になる。
- Provider Adapterが持つ資格情報は、EKIの中でも特に強い権限の一つになり得る。そのため、Control Planeの権限分離（RQ-051）の対象に含める必要がある。

## 4. Open Questions

- EKIのLeaseが失効したとき、Providerが確保した枠をどれだけ速く回収できるか（RQ-048、RQ-052）。
- 2系統目に何を選ぶかを、どのAcceptance Scenarioから導くか。
