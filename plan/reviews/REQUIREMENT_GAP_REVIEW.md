# EKI Fabric 要求ギャップレビュー — 第三巡目

## 役割

この文書は、Architectureに進む前に、まだ残っている要求の抜けを確認するための、レビューの記録です。Product Requirements、Threat Model、Worker Assurance Model、Acceptance Scenariosを横断して確認しました。

ここでの結論自体はRequirementではありません。追加が必要と判断した項目は、`requirements/PRODUCT_REQUIREMENTS.md`や`requirements/THREAT_MODEL.md`へ反映した時点で、正式な要求になります。

第二巡目では、Browser参加を利用者に見える状態にすること、悪意あるWorkerに渡したDataの削除を保証できないこと、Validator / Verifierの信頼境界、非決定的なAIのResultに対する保証の上限、副作用を持つTaskの再試行の境界を扱いました。

第三巡目では、これらを踏まえ、複数組織間の連携、大量のArtifact、Control Plane内部の権限、大規模なResource reclaim、Endpointの保護など、運用に近い境界を確認します。

---

## GAP-01 — FederationでIdentity namespaceのbindingが失われる危険

### 問題

RQ-031では、複数のTrust Domainの限定的なFederationを要求しています。しかし、「外部のIdentityを検証できること」と、「どのAuthorityが発行したIdentityかを取り違えないこと」とは、さらに明示的に分けたほうが安全です。

複数のTrust Domainの公開鍵やBundleを一つの集合に混ぜ、「どれかの鍵で署名を検証できればよい」とすると、異なるDomainのIdentityを取り違えるおそれがあります。

SPIFFE Federationでも、Trust Domain名とBundleの対応を保つよう求めています。また、Federation設定そのものの改変が大きなSecurity riskになることも明示しています。

### 結論

**新しいProduct Requirementと、Security Invariantを追加する。**

要求する方向は、次のとおりです。

- Federationされたidentityは、発行したTrust DomainやAuthorityとのbindingを保持する。
- 異なるIdentity namespaceを、暗黙のうちに併合しない。
- Federationの設定は、通常のClientの入力から変更できない。
- Federationは、Identity validationを可能にするものであり、Authorization Grantとは分離する。
- Trust chainを利用する場合は、どのauthority chainを根拠にしたかを、追跡できる余地を持つ。

関連: RQ-031, RQ-039, SEC-010

調査: `research/topics/federation-namespace-safety.md`

---

## GAP-02 — Artifactの取り込みと展開の処理そのものが、DoSの面になる

### 問題

RQ-017は、ClientによるResource Exhaustionを扱っています。RQ-027は、ArtifactのContent Integrityを扱っています。しかし、Artifactを受理した後の、展開、解析、変換の処理が、独立した攻撃の面になる点は、まだ弱いままです。

たとえば、次のような攻撃は、Task本体を実行する前でも、Fabricを消耗させます。数MBの圧縮されたArtifactが、巨大なサイズに展開される。数百万のfileを生成する。深い入れ子でParserを消耗させる。Archive entryを使って、展開先の外に書き込もうとする。

### 結論

**新しいProduct Requirementと、Security Invariantを追加する。**

要求する方向は、次のとおりです。

- Artifactの受理、検査、展開、変換も、resource budgetの対象にする。
- 圧縮されたsizeだけでなく、展開後のsize、fileの数、入れ子の深さ、処理時間などを制限できる。
- Archive entryの名前やpathだけで、Host filesystemに対するauthorityが成立しない。
- ParserやConverterの障害が、Control Plane全体に無制限に波及しない。
- Provenance、Content Integrity、Safe Processingを、別の保証として扱う。

関連: RQ-009, RQ-017, RQ-027, RQ-029

調査: `research/topics/artifact-ingest-safety.md`

---

## GAP-03 — Control Plane内部を、一枚岩の、完全に信頼された領域にしない

### 問題

Threat Modelでは、Control Planeのroot signing authorityなどまで侵害された場合を、通常の保証の外に置いています。この境界そのものは必要です。

一方で、Control Plane内部には、性質の異なる権限があります。Scheduler、Policyの評価、Artifact capabilityの発行、Executor admission、Federationの管理、Verificationなどです。

一つのComponentが侵害されたときに、それだけで他のすべての権限まで得られる構造を、前提にする必要はありません。

### 結論

**新しいProduct Requirementと、Threat ActorおよびSecurity Invariantを追加する。**

要求する方向は、次のとおりです。

- Control Planeの一部のcomponentも、その役割に不要なauthorityを持たない構成を、選べる。
- Task admission、placement、data authority、executor admission、federation administration、verification authorityなどを、分離できるようにする。
- 価値の高いTrustの設定の変更を、通常のTaskの実行の経路から分離できる。
- 内部の重要なauthorityの移り変わりを、監査できる。

これは、最初から大量のMicroserviceに分割することを求めるものではありません。論理的な権限の境界を、一つのProcessの中で実現するArchitectureも、候補に残します。

関連: RQ-016, RQ-039〜RQ-041, RQ-044

調査: `research/topics/control-plane-compromise.md`

---

## GAP-04 — 大規模なResource Reclaimが、Retry stormを起こす

### 問題

RQ-006とRQ-035では、利用者とProviderが資源の提供を停止できることを要求しています。これは、EKIの中心となる原則です。

ところが、大規模なCompute Grantや、数百台のEndpointが、同時にreclaimされることがあります。このとき、未完了のTaskを一斉に再実行すると、残ったWorker、Artifact Store、Network、Control Planeに負荷が集中します。そして、二次的な障害を起こす可能性があります。

### 結論

**新しいProduct Requirementを追加する。**

要求する方向は、次のとおりです。

- Provider、利用者のreclaimの権利を維持したまま、reclaimの後のretryやmigrationが、resourceの増幅を起こさないようにする。
- 大量の離脱を、異常な単一の障害ではなく、起こり得るFabric eventとして扱う。
- Taskのpriorityやdeadlineなどに応じて、再配置を抑えたり遅らせたりできる余地を持つ。
- reclaimのために、Provider側がTaskの完了を待ち続けることを、要求しない。

backoff、jitter、queueの規律の具体的な内容は、Architectureで決めます。

関連: RQ-006, RQ-010, RQ-013, RQ-017, RQ-035

調査: `research/topics/reclaim-energy-and-endpoint-safety.md`

---

## GAP-05 — Energy / Thermal / Deviceの摩耗が、Resource Policyの上で弱い

### 問題

RQ-007では、温度、電力、Networkを、ローカルでの安全の判断の例に挙げています。しかし、EKIの考え方では、社員のPCを高性能にしておき、その余剰のGPUを、組織の認知の資源に還元することを、重要な利用形態としています。

この場合、CPUやGPUがidleであることだけを「利用できる」とみなすと、次のものを無視してしまいます。Battery、消費電力、ファンの騒音、Thermal throttling、Hardwareの摩耗。

Compute GrantやContributionが普及すると、Creditの獲得などの外部の動機が、Endpointの安全の上限を押し上げようとする可能性もあります。

### 結論

**RQ-007を補強し、新しいResource Policy Requirementを追加する。**

要求する方向は、次のとおりです。

- Energy budget、電源の種類、thermal state、device safetyを、Resourceが使えるかどうかに反映できる。
- ローカルのSafety Governorは、中央のSchedulerより厳しい上限を適用できる。
- Providerは、時間帯やEnergy policyによって、提供する量を制限できる。
- Contributionや外部のCredit policyが、ローカルのSafetyの上限を迂回できない。

関連: RQ-005〜RQ-007, RQ-036〜RQ-038

調査: `research/topics/reclaim-energy-and-endpoint-safety.md`

---

## GAP-06 — Supply-chain provenanceと、Artifactの安全性を混同しない

### 問題

RQ-026とRQ-027では、Executorのprovenanceと、Content Integrityを扱っています。しかし、署名やAttestationが付いているArtifactであっても、脆弱なParserに与えれば、危険な入力になり得ます。

出所を確認できることと、安全に処理できることとは、別のことです。

### 結論

**新しいRequirementを追加するのではなく、Artifactの安全に関するRequirementの理由として、明示する。**

EKIでは、少なくとも次の3つを分けます。

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

## GAP-07 — FederationのTrust chainを、無意識に推移させない

### 問題

AがBとFederationしていて、BがCとFederationしているとします。このとき、AがCを暗黙のうちに信頼する構成にすると、信頼の境界が、想定以上に広がります。

これはSPIFFEに固有の問題ではありません。複数の組織のIdentity基盤をつなぐときに、一般に起こる問題です。

### 結論

**Federation Requirementに、非推移性を補強する。**

- Federationの関係は、明示的に構成する。
- 他のDomainのFederationの関係を継承して、包括的なTrustを自動的に生成しない。
- chainを許す場合は、どのchainを信頼の根拠にしたかを、追跡できる。

関連: RQ-031

---

## GAP-08 — Securityのためのローカルでの停止は、Control Planeの障害時にも成り立つべき

### 問題

RQ-006とRQ-007は、すでに、local opt-outとlocalな上限を要求しています。

第三巡目で、Control Planeの一部が侵害される場合を考えると、これは単なるUXではなく、Security boundaryでもあります。中央のComponentが誤動作したり侵害されたりした場合でも、Endpoint側のSafety Governorが、CPU、GPU、Memory、Network、Thermalの上限を超えないことが、重要です。

### 結論

**新しいRequirementを追加するのではなく、RQ-006とRQ-007のSecurityの面での理由を、補強する。**

Local Governorは、Central Schedulerの性能を最適化するための機能ではありません。Endpoint側に残る、最後のResource Enforcement Boundaryの一つとして扱います。

---

# 第三巡目で、正式な要求に反映する項目

Product Requirementsに、次の項目を追加または補強します。

1. Federation Identity namespace binding
2. Bounded Artifact ingestion / expansion
3. Control Plane authority separation
4. Coordinated reclaim without retry amplification
5. Energy / thermal / device-safety aware resource policy

Threat Modelに、次の項目を追加または補強します。

1. 悪意のあるArtifactと、parserの悪用
2. 侵害されたControl Plane component
3. Federationの設定とnamespaceのすり替え
4. mass reclaimを利用した、resourceの増幅
5. ローカルのresource safetyの強制を、無効にすること

Acceptance Scenarioに、次の項目を追加します。

1. Federationのbundleとnamespaceのすり替え
2. archive bombとpath escape
3. 侵害されたSchedulerまたはVerifierのcomponent
4. Compute Grantのmass reclaim
5. Endpointのthermalまたはpowerの緊急の縮小

## Architectureに持ち越すもの

次の項目は重要ですが、方式をRequirementに固定しません。

- SPIFFEを採用するか、別のFederation identity modelを採るか。
- Artifact parserの隔離に、process、container、WASMなどのどれを使うか。
- 圧縮後と展開後のresourceの上限の、具体的な値。
- Control Planeを、いくつのProcessやServiceに分けるか。
- 価値の高い操作に、multi-party approvalを要求するか。
- reclaimのbackoff、jitter、queueのアルゴリズム。
- thermal sensorの取得方法。
- Energy accountingの精度と、OSやhardwareへの依存の扱い。
