# EKI Fabric 要求ギャップレビュー — 第三巡目

## この文書の役割

この文書は、第二巡目までに整理したProduct Requirements、Threat Model、Worker Assurance Model、Acceptance Scenariosをもう一度横断し、Architecture Entry Gateへ進む前に残っている暗黙の前提を洗い出すためのレビュー記録です。

新しいRequirementの正本ではありません。ここで追加・補強が必要と判断した内容は、`PRODUCT_REQUIREMENTS.md`、`THREAT_MODEL.md`、`ACCEPTANCE_SCENARIOS.md`へ反映して初めて正式な要求になります。

第三巡目では、個別の機能追加よりも、**これまで安全だと考えていた境界の内側に、まだ無意識の信頼が残っていないか**を重点的に確認します。

---

## GAP3-01 — FederationでIdentity namespaceと検証根拠を混同すると、別主体へのなりすましが起こり得る

### 問題

RQ-031では、複数の管理主体がそれぞれのIdentity authorityを保ったまま限定的に協力できることを要求しています。

しかし、「外部Trust DomainのIdentityを検証できること」だけでは不十分です。

たとえば、複数のTrust Domainの検証鍵を一つの共通poolへ混ぜ、どのDomainのIdentityをどの鍵で検証すべきかという対応関係を失うと、ある主体が別のTrust DomainのIdentityを装える可能性があります。

SPIFFE Federationでも、異なるTrust DomainのBundleを混ぜて扱わず、`Trust Domain name ↔ Trust Bundle` の対応を保つことがSecurity上重要であると明示されています。

### 結論

**新しいProduct Requirementを追加する。**

要求する方向:

- Federationで利用するIdentity namespaceと検証根拠の対応を保持する
- 異なるTrust Domainの検証根拠を無差別に統合しない
- 外部Identityを認証できることと、そのIdentityへAuthorityを付与することを分離する
- Federation設定そのものをSecurity-sensitiveなPolicyとして扱う
- 推移的な包括信頼を暗黙に成立させない

関連: RQ-030, RQ-031, RQ-040, SEC-010, SEC-018

調査: `research/federation-identity-safety.md`

---

## GAP3-02 — Artifactの完全性と、Artifactを安全に処理できることは別である

### 問題

RQ-027ではContent Integrityを要求していますが、Digestが正しいArtifactであっても、そのArtifactを展開・解析したときに安全とは限りません。

代表例は次です。

- ZIP / decompression bomb
- Archive path traversal
- Archive内symbolic link
- file数やdirectory深度の異常増加
- PDF / image / document parserへの悪意入力

また、replicationやretryによって同じ悪意Artifactを多数のWorkerへ配布すると、前処理コストまで増幅される可能性があります。

### 結論

**新しいProduct RequirementとSecurity Invariantを追加する。**

要求する方向:

- Artifactの展開・解析・変換をresource-boundedにする
- 展開後size、file数、directory深度、CPU、Memory、temporary Disk等を制約できる
- Artifact内path / symlinkからWorker hostの許可範囲外へ到達させない
- Artifact前処理コストもPrincipal / Task budgetへ含められる
- retry / replicationで前処理コストを無制限に増幅させない

関連: RQ-009, RQ-017, RQ-027, RQ-029

調査: `research/artifact-ingestion-safety.md`

---

## GAP3-03 — Control Plane全体を一つの完全信頼境界として扱うとblast radiusが大きい

### 問題

現在のThreat Modelでは、Control Plane root signing authorityやAdministrator Policy authorityまで完全に侵害された場合を通常保証の外に置いています。

一方、実際には次のような一部Componentだけが侵害される可能性があります。

- Scheduler
- Admission component
- Result Verifier
- Artifact metadata service
- Contribution accounting component
- Telemetry / Audit component

一つのComponentが、Task配置、Artifact復号、Executor admission、Result acceptance、Grant変更、Policy変更のすべてを自由に行えるなら、一つの脆弱性がFabric全体へ波及します。

### 結論

**新しいProduct RequirementとThreat Actorを追加する。**

要求する方向:

- Security-sensitiveなAuthorityを役割に必要な範囲へ限定できる
- Placement、Artifact access、Executor admission、Result acceptance、Grant / Policy変更を必要以上に一主体へ集約しない
- 一部Componentの侵害だけで、無関係なAuthorityへ当然には横移動できない
- Root authority全体の侵害と、一部Componentの侵害を区別する

これはControl Planeを必ずMicroservice化する要求ではない。Process数ではなくAuthority boundaryを要求する。

関連: RQ-016, RQ-028, RQ-039, RQ-044

調査: `research/control-plane-blast-radius.md`

---

## GAP3-04 — 大規模Resource Reclaimがretry stormへ変わる可能性がある

### 問題

RQ-035ではProviderがCompute Grantを停止・縮小できることを要求しています。

しかし、Cloud Providerや大手企業が大量のWorkerを公益用途へ提供する場合、通常需要の増加によって数千〜数万Worker相当のCapacityを短時間で回収する可能性があります。

このとき、失われたTaskを一斉にretryすると次の問題が起こり得ます。

- Queueへの一斉再投入
- Artifactの大量再転送
- 別Providerへの負荷集中
- Cloud fallback費用の急増
- verification処理の重複
- Network congestion

### 結論

**新しいProduct RequirementとAcceptance Scenarioを追加する。**

要求する方向:

- 大規模なWorker / Grantの縮小を正常なCapacity Changeとして扱える
- 突然の大量離脱と、予告されたReclaimの両方を扱える
- retry / reschedule / Artifact transferをboundedにする
- 優先度、残Budget、Deadline、Data localityを考慮して段階的に再配置できる余地を持つ
- Providerが事前通知できる場合は、その情報を利用できる余地を持つが、事前通知を必須とはしない

関連: RQ-008, RQ-010, RQ-013, RQ-017, RQ-035

調査: `research/resource-reclaim-and-energy-governance.md`

---

## GAP3-05 — 「遊休資源」は追加コストゼロではない

### 問題

EKIは遊休CPU / GPUの活用を中心価値の一つとしていますが、遊休中のHardwareを実際に計算へ使えば、電力、熱、冷却、fan、network、battery等の追加負荷が生じます。

RQ-007では端末側の温度・電力・network safety判断を扱っていますが、Provider全体としてのEnergy / Cost Policyは独立していません。

公益用途でも、「社会貢献だから常に最大限GPUを回す」という設計は持続的ではありません。

### 結論

**新しいProduct Requirementを追加する。**

要求する方向:

- ProviderがEnergy、Thermal、Network、利用時間等のResource Policyを安全側へ設定できる
- Idleであることを追加コストゼロとみなさない
- Energy / Carbon signal等を任意のPlacement要因として利用できる余地を持つ
- Carbon-aware schedulingを標準必須機能にはしない
- Security Assuranceとは別のPolicy軸として扱う

関連: RQ-005〜RQ-009, RQ-034, RQ-035

調査: `research/resource-reclaim-and-energy-governance.md`

---

## GAP3-06 — Federation設定変更のProvenanceが重要である

### 問題

Federationでは、外部Trust Domain名、検証根拠、取得先、Policy等の対応関係がIdentityの意味を決めます。

そのため、この設定が改ざんされた場合、Credential自体が正しくても誤った主体を認証する可能性があります。

### 結論

**RQ-039 / RQ-041と新しいFederation Requirementを結び付けて補強する。**

少なくとも、Federation関係の追加・変更・削除、検証根拠の更新、失効について、Security eventとProvenanceを追跡できる必要がある。

---

## GAP3-07 — Control Plane ComponentへのLeast Privilegeは「善意」を仮定しないための延長である

### 問題

ClientとWorkerについては、認証済みでも善意を仮定しない方針がすでに確立しています。

同じ考え方をControl Plane内部へ適用しないと、「外側はZero Trustだが、内側は一度入れば全権」という非対称な構造になります。

### 結論

GAP3-03のRequirementを、単なるHardening推奨ではなくProduct Requirementとして扱う。

ただし、すべての内部Componentを相互にByzantine tolerantにすることは要求しない。**一つのComponentに不要なAuthorityを持たせないこと**を基本線とする。

---

# 第三巡目で正式要求へ反映する項目

Product Requirementsへ次を追加する。

1. Federation namespace / trust-binding isolation
2. Bounded Artifact expansion and parsing
3. Compartmentalized Control Plane authority
4. Bounded coordinated Resource Reclaim
5. Provider energy / thermal / cost governance

Threat Modelへ次を追加または補強する。

1. Federation configuration substitution / namespace confusion
2. malicious Artifact expansion / parser attack
3. compromised Control Plane component
4. large-scale reclaim amplification
5. energy / thermal policy bypass

Acceptance Scenariosへ次を追加する。

1. Federation bundle / namespace substitution
2. ZIP bomb / Archive traversal / parser exhaustion
3. compromised Scheduler or other limited Control Plane component
4. large public-interest Grant reclaim
5. Energy / Thermal constrained Provider

## Architectureへ持ち越すもの

次は重要ですが、Product Requirement段階では方式を固定しません。

- SPIFFE / SPIREを採用するか
- Trust Bundle配布Protocol
- Artifact Sandboxの具体方式
- Archive展開libraryやParser process isolation
- Control PlaneをProcess / Service単位でどう分割するか
- WorkOrder署名主体を単一にするかThreshold化するか
- Reclaim通知Protocol
- Carbon intensity取得元
- Energy / Power telemetryの取得方法

第三巡目の目的は、これらの方式を決めることではなく、Architecture比較で落としてはいけない判断軸を正本へ引き上げることにある。
