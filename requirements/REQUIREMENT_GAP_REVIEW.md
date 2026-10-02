# EKI Fabric 要求ギャップレビュー — 第三巡目

## 役割

この文書は、Product Requirements、Threat Model、Worker Assurance Model、Acceptance Scenariosを横断し、Architectureへ進む前に残っている要求の抜けを確認するためのレビュー記録です。

ここでの結論はRequirementそのものではありません。追加が必要と判断した項目は、`PRODUCT_REQUIREMENTS.md`や`THREAT_MODEL.md`へ反映して初めて正式な要求になります。

第二巡目では、Browser参加の可視性、悪意あるWorkerに渡したDataの事後削除非保証、Validator / Verifierの信頼境界、非決定的AI Resultの保証上限、副作用Taskのretry境界などを追加しました。

第三巡目では、それらを前提に、複数組織・大量Artifact・Control Plane内部・大規模Resource Reclaim・Endpoint保護という、より運用に近い境界を確認します。

---

## GAP-01 — FederationでIdentity namespaceのbindingが失われる危険

### 問題

RQ-031では複数Trust Domainの限定的なFederationを要求しています。しかし、「外部Identityを検証できること」と「どのAuthorityが発行したIdentityかを取り違えないこと」は、より明示的に分けた方が安全です。

複数Trust Domainの公開鍵やBundleを単純に一つの集合へ混ぜ、どれかの鍵で署名が検証できればよいとすると、異なるDomain間のIdentityを取り違える可能性があります。

SPIFFE Federationでも、Trust Domain名とBundleのbindingを保つこと、Federation設定そのものの改変が大きなSecurity riskになることが明示されています。

### 結論

**新しいProduct RequirementとSecurity Invariantを追加する。**

要求する方向は次のとおりです。

- FederationされたIdentityは発行Trust Domain / Authorityとのbindingを保持する
- 異なるIdentity namespaceを暗黙に併合しない
- Federation設定は通常Client入力から変更できない
- FederationはIdentity validationを可能にするものであり、Authorization Grantとは分離する
- Trust chainを利用する場合、どのauthority chainを根拠にしたかを追跡できる余地を持つ

関連: RQ-031, RQ-039, SEC-010

調査: `research/federation-namespace-safety.md`

---

## GAP-02 — Artifact取込・展開処理そのものがDoS面になる

### 問題

RQ-017はClientによるResource Exhaustion、RQ-027はArtifactのContent Integrityを扱っています。しかし、Artifactを受理した後の展開・解析・変換処理が、独立した攻撃面としてはまだ弱いです。

数MBの圧縮Artifactが巨大な展開後サイズになる、数百万fileを生成する、深い入れ子でParserを消耗させる、Archive entryで展開先外へ書き込もうとする、といった攻撃は、Task本体を実行する前でもFabricを消耗させます。

### 結論

**新しいProduct RequirementとSecurity Invariantを追加する。**

要求する方向は次のとおりです。

- Artifactの受理・検査・展開・変換もresource budgetの対象にする
- compressed sizeだけでなくexpanded size、file count、nesting、処理時間等を制限できる
- Archive entry名やpathだけでHost filesystem authorityが成立しない
- Parser / ConverterのfailureがControl Plane全体へ無制限に波及しない
- Provenance、Content Integrity、Safe Processingを別の保証として扱う

関連: RQ-009, RQ-017, RQ-027, RQ-029

調査: `research/artifact-ingest-safety.md`

---

## GAP-03 — Control Plane内部を一枚岩の完全信頼領域にしない

### 問題

Threat ModelではControl Planeのroot signing authority等まで侵害された場合を通常保証の外に置いています。この境界自体は必要です。

一方、Scheduler、Policy評価、Artifact capability発行、Executor admission、Federation administration、Verificationなど、Control Plane内部には性質の異なる権限が存在します。

一つのComponentが侵害されたとき、それだけで他のすべての権限まで得られる構造を前提にする必要はありません。

### 結論

**新しいProduct RequirementとThreat Actor / Security Invariantを追加する。**

要求する方向は次のとおりです。

- Control Planeの一部componentも、その役割に不要なauthorityを持たない構成を選べる
- Task admission、placement、data authority、executor admission、federation administration、verification authority等を分離可能にする
- 高価値なTrust設定変更を通常Task実行経路から分離できる
- 内部の重要なauthority transitionを監査できる

これは最初から大量Microserviceへ分割する要求ではありません。論理的な権限境界を一つのProcess内で実現するArchitectureも候補に残します。

関連: RQ-016, RQ-039〜RQ-041, RQ-044

調査: `research/control-plane-compromise.md`

---

## GAP-04 — 大規模Resource ReclaimがRetry stormを起こす

### 問題

RQ-006、RQ-035ではUser / Providerが資源提供を停止できることを要求しています。これはEKIの中心原則です。

しかし、大規模Compute Grantや数百台のEndpointが同時にreclaimされた場合、未完了Taskを一斉に再実行すると、残ったWorker、Artifact Store、Network、Control Planeへ負荷が集中し、二次障害を起こす可能性があります。

### 結論

**新しいProduct Requirementを追加する。**

要求する方向は次のとおりです。

- Provider / Userのreclaim権を維持したまま、reclaim後のretry / migrationがresource amplificationを起こさない
- 大量離脱を異常な単一障害ではなく、起こり得るFabric eventとして扱う
- Task priorityやdeadline等に応じて再配置を抑制・遅延できる余地を持つ
- ReclaimのためにProvider側がTask完遂を待ち続けることを要求しない

具体的なbackoff、jitter、queue disciplineはArchitectureで決めます。

関連: RQ-006, RQ-010, RQ-013, RQ-017, RQ-035

調査: `research/reclaim-energy-and-endpoint-safety.md`

---

## GAP-05 — Energy / Thermal / Device wearがResource Policy上弱い

### 問題

RQ-007では温度・電力・Networkをローカル安全判断の例に挙げています。しかし、EKIの思想では社員PCをリッチにし、その余剰GPUを組織の認知資源へ還流させることを重要な利用形態としています。

この場合、単にCPU/GPUがidleであることだけを「利用可能」とみなすと、Battery、消費電力、Fan noise、Thermal throttling、Hardware wearなどを無視してしまいます。

Compute GrantやContributionが普及した場合、Credit獲得などの外部動機がEndpointの安全上限を押し上げようとする可能性もあります。

### 結論

**RQ-007を補強し、新しいResource Policy Requirementを追加する。**

要求する方向は次のとおりです。

- Energy budget、power source、thermal state、device safetyをResource availabilityへ反映できる
- local Safety Governorは中央Schedulerより厳しい上限を適用できる
- Providerは時間帯やEnergy policyによって提供量を制限できる
- Contributionや外部Credit policyがローカルSafety上限を迂回できない

関連: RQ-005〜RQ-007, RQ-036〜RQ-038

調査: `research/reclaim-energy-and-endpoint-safety.md`

---

## GAP-06 — Supply-chain provenanceとArtifact安全性を混同しない

### 問題

RQ-026 / RQ-027ではExecutor provenanceとContent Integrityを扱っています。しかし、署名やAttestationがあるArtifactであっても、脆弱なParserへ与えれば危険な入力になり得ます。

出所の確認と、安全に処理できることは別です。

### 結論

**新Requirement追加ではなく、Artifact safety Requirementの理由として明示する。**

EKIでは少なくとも次を分けます。

```text
Provenance
  誰が・どこから作ったか

Integrity
  byte列が期待したものか

Safe Processing
  現在のParser / Runtimeで許可されたResource内に処理できるか
```

関連: RQ-026, RQ-027

---

## GAP-07 — FederationのTrust chainを無意識に推移させない

### 問題

AがBとFederationし、BがCとFederationしているとき、AがCを暗黙に信頼する構成は、信頼境界を予想以上に広げます。

これはSPIFFE固有の問題ではなく、複数組織のIdentity基盤を連結する一般的な問題です。

### 結論

**Federation Requirementへ非推移性を補強する。**

- Federation関係は明示的に構成する
- 他DomainのFederation関係を継承して包括的Trustを自動生成しない
- chainを許す場合は、どのchainを信頼根拠にしたか追跡できる

関連: RQ-031

---

## GAP-08 — Security上のローカル停止はControl Plane障害時にも成立すべき

### 問題

RQ-006 / RQ-007はすでにlocal opt-outとlocal boundを要求しています。

第三巡目でControl Plane部分侵害を考えると、これは単なるUXではなくSecurity boundaryでもあります。中央Componentが誤動作・侵害された場合でも、Endpoint側のSafety GovernorがCPU / GPU / Memory / Network / Thermal上限を超えないことが重要です。

### 結論

**新Requirement追加ではなく、RQ-006 / RQ-007のSecurity rationaleを補強する。**

Local GovernorはCentral Schedulerの性能最適化機能ではなく、Endpoint側に残る最後のResource Enforcement Boundaryの一つとして扱う。

---

# 第三巡目で正式要求へ反映する項目

Product Requirementsへ次を追加・補強します。

1. Federation Identity namespace binding
2. Bounded Artifact ingestion / expansion
3. Control Plane authority separation
4. Coordinated reclaim without retry amplification
5. Energy / thermal / device-safety aware resource policy

Threat Modelへ次を追加・補強します。

1. malicious Artifact / parser abuse
2. compromised Control Plane component
3. Federation configuration / namespace substitution
4. mass reclaimを利用したresource amplification
5. local resource safety enforcementの無効化

Acceptance Scenarioへ次を追加します。

1. Federation bundle / namespace substitution
2. archive bomb / path escape
3. compromised SchedulerまたはVerifier component
4. Compute Grantのmass reclaim
5. Endpointのthermal / power emergency reduction

## Architectureへ持ち越すもの

次は重要ですが、方式をRequirementへ固定しません。

- SPIFFEを採用するか、別のFederation identity modelを採るか
- Artifact parser isolationにprocess、container、WASM等のどれを使うか
- compressed / expanded resource limitの具体値
- Control Planeを何Process / Serviceへ分けるか
- high-value operationへmulti-party approvalを要求するか
- reclaim backoff / jitter / queue algorithm
- thermal sensorの取得方法
- Energy accountingの精度とOS / hardware依存の扱い
