# ADR-0009 — 管理された資源は既存オーケストレータを置き換えず、Resource Providerとして接続する

- Status: **Proposed**
- Date: 2026-10-06
- Related: Issue #67, RQ-001, RQ-003, RQ-004, RQ-019, RQ-030, [orchestrator-integration-and-layering](../../research/orchestrator-integration-and-layering.md)

## 1. Context

[value.md](../../product/value.md)「Managed and opportunistic resources on one fabric」は、専用サーバやKubernetes / Slurm等が管理するクラスタと、業務端末やBrowser等の一時的な資源を、同じTask契約・配置判断・検証・監査で扱うことを掲げています。

単純な方法として、次の二つが考えられますが、どちらも価値と矛盾します。

- **EKIがクラスタを直接管理する**: Node管理、常駐Service、ネットワーク構成までEKIが持つと、既存オーケストレータの置換になり、企業の運用経路（原則 §12）と衝突する。
- **管理された資源を「信頼済み」として特別扱いする**: Kubernetes上のWorkerは検証不要、とすると、改変されたPodや設定ミスが検証を素通りする（原則 §1、§9）。

## 2. Decision

1. 既存オーケストレータは、**Resource Provider**としてEKIへ接続する。EKIはProviderへ「この制約で、この期間、この量の実行枠」を依頼し、Providerがその枠をWorkerとして提供する。Pod配置やNode管理はオーケストレータの責務のまま残す。
2. 管理された資源から来るWorkerも、他のWorkerと同じLease、WorkOrder、Result検証の経路を通る。「管理されている」ことは、Assurance Levelや配置の優先度を上げる**根拠の一つ**として、Admin-managed policyでだけ表現する。Worker自身の申告では表現できない（RQ-047）。
3. Providerの接続は、特定製品のAPIをCoreへ入れず、Extension SPI（[EXTENSION_SPI.md](../../architecture/EXTENSION_SPI.md)）のAdapterとして実装する。MVP後の最初の検証で、性質の異なる2系統（例: Kubernetes系とbatch scheduler系）を接続できることを確認する。1系統だけの対応は、その製品の拡張と区別できないため、完了としない。
4. 終わりのない常駐Service（業務Application本体等）はEKIの対象外とし、Providerへの依頼にも含めない。

## 3. Consequences

### Positive

- 遊休資源の活用が難しい組織でも、管理クラスタの統治だけで導入価値が成り立つ。
- 管理資源と遊休資源の間で、Result検証と監査の基準が分かれない。

### Negative / Limitations

- オーケストレータ側の待ち時間（Pod起動、queue待ち）が配置判断へ入り、Suitability Gate（ADR-0007）の規則が複雑になる。
- Provider Adapterの資格情報はEKI内で最も強い権限の一つになり得るため、Control Plane authority separation（RQ-051）の対象に含める必要がある。

## 4. Open Questions

- Providerが確保した枠を、EKIのLease失効時にどの速さで回収できるか（RQ-048、RQ-052）。
- 2系統目に何を選ぶかを、どのAcceptance Scenarioから導くか。
