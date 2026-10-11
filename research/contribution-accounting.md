# 資源提供実績の集計と価値交換の境界（Contribution Accounting）

## 役割

この文書は、EKI Fabricが計算資源の提供実績をどこまで記録し、どこから先を外部の価値判断へ委ねるべきかを整理する調査・検討メモです。

通貨、決済、交換市場の設計書ではありません。EKI Coreへ経済制度を持ち込みすぎず、それでも外部制度が安全に利用できる情報を残すための境界を検討します。

## 結論

EKIが記録すべき中心は、**価値ではなく、検証可能なContributionの事実と、その保証水準**です。

次の三つを分けます。

```text
資源を提供したという事実
        ↓
その事実をどの程度確認できたか
        ↓
社会・組織がその提供をどう評価するか
```

最後の価値判断はEKI Coreの責務にしません。

## 「GPUを10時間使った」だけではContributionとして弱い

Worker自身が「GPUを10時間使った」と申告しても、改変されたWorkerなら任意の値を返せます。

さらに、本当にGPUを10時間使っていたとしても、その計算が必要なものだったか、Beneficiaryが依頼したものだったか、同じTaskを水増し目的で繰り返していないかは別問題です。

そのため、Contribution Recordには単なる利用時間だけでなく、少なくとも次を関連付けられる余地が必要です。

- Provider
- 実行対象となったWork / Task
- BeneficiaryまたはGrant
- Resource Class
- 測定された量
- 測定方法
- Resultの検証状態
- WorkとResultのprovenance
- どのAssuranceを満たしたか

## BOINCから学べること

BOINCはVolunteer Computingで長年Creditを扱っており、Result ValidationをCredit cheatingへの対策としても利用しています。

一方、BOINCの論文では、結果が予測しやすいApplicationなどでは、実際に計算せず「よく出る答え」を返すだけでvalidationを通過し、Creditを得られる場合があることも指摘されています。

つまり、

> Resultがvalidationを通った

ことと、

> 申告された量の計算を本当に提供した

ことは、必ずしも同じではありません。

参考は次のとおりです。

- BOINC: A Platform for Volunteer Computing  
  https://boinc.berkeley.edu/boinc_a_platform_for_volunteer_computing.pdf
- BOINC papers / adaptive replication  
  https://boinc.berkeley.edu/boinc_papers/

EKIでは、Result AssuranceとResource Measurement Assuranceを別軸にする必要があります。

## Contribution Recordは「債権」ではない

EKIが記録したContributionを、あるコミュニティが相互利用Creditへ変換することはできます。

しかし、EKI自身が

```text
100 compute units = 100円
```

のような交換価値を定義すると、Compute Fabricとは別の問題がCoreへ入ってきます。

同じContributionでも、外部主体によって意味は変わり得ます。

- 社内では部門間の参考配賦に使う
- 企業間では相互利用枠へ変換する
- 公益Poolでは寄付実績として記録する
- OSSコミュニティではRecognitionにだけ使う
- 何のCreditにも変換せず、監査記録としてだけ残す

この違いを許容するため、Contribution Recordは交換価値を持たない中立的な記録として設計する方がよいと考えます。

## 自作自演Workをどう考えるか

ProviderとBeneficiaryが同一主体であること自体を禁止する必要はありません。社内利用では普通に起こります。

問題は、外部制度が「他者への貢献」を評価する場合に、自作自演Workを同じ価値として数えてしまうことです。

これはEKI Coreだけでは判断できません。

したがってContribution Recordには、外部Policyが判断できるだけのprovenanceを残しつつ、EKI自身は

> このContributionは社会的に価値がある

とは判定しない方がよいです。

## 待機能力を評価する制度もあり得る

実際のTaskを処理した量だけでなく、一定期間いつでもTaskを受けられる状態を維持することへ価値を置く制度も考えられます。

たとえば災害時の公益Compute Poolでは、平時にTaskを処理していなくても、非常時に利用可能なcapacityを確保していること自体を評価するかもしれません。

このため、将来のContribution Eventは一種類に固定せず、少なくとも概念上は次を区別できる方がよいと考えます。

- Capacity offered
- Work accepted
- Work executed
- Result accepted
- Availability provided
- Data transferred

ただし、どのEventへCreditを付けるかは外部制度が決めます。

## Contribution quantityを単純な時間にしない

H100を1時間利用した場合と、一般的な個人向けGPUを1時間利用した場合では、提供された能力が大きく異なります。

CPUでも同じ問題があります。

そのため、EKI Coreでは「GPU-hour」を唯一の共通尺度にするのではなく、

```text
Resource Class
      +
Measured Quantity
      +
Measurement Assurance
```

を分けて記録する方向が適切です。

Resource Classの具体的な標準化方法は未決です。ベンダー名や特定GPU型番だけに固定すると将来性を失う一方、抽象化しすぎると性能差を説明できなくなります。

## 不正防止の原則

Contributionを外部の価値交換へ利用する場合でも、EKI Coreで次を完全に解決できるとは限りません。

- ProviderとBeneficiaryの結託
- 外部制度側での多重計上
- 社会的価値の虚偽申告
- 複数Fabric間で同じContributionを重複利用すること

EKIが担うのは、内部で観測・検証した事実について、改変されにくく追跡可能なRecordを出せることです。

外部制度が二重利用防止や交換価値を必要とする場合は、その制度側のSettlement / Registryが責任を持つ構成が自然です。

## SEIのCapability Observationとの対応

同じ作者の関連製品であるSEI Cognitionには、Capabilityやその実現候補について観測された情報を、DecisionやMatchingから切り離して交換するためのCapability Observationという意味契約があります（`hat47x/sei-cognition:contracts/capability/SEI_CAPABILITY_OBSERVATION_v1alpha1.md`）。

SEIがEKIのContribution Recordを受け取るとすれば、「どの資源が、いつ、どの条件で、どれだけ提供されたと観測されたか」を表すCapability Observationとして読むのが自然です。価値の判断ではなく観測の記録として扱う点で、この文書の結論とも一致します。

対応づけるときも、次の点は変わりません。

- **価値の写像はEKIの外に残る。** SEIがContribution Recordを観測として記録しても、SEIが価値を決める主体になるわけではなく、EKIが価値を決めるようになるわけでもありません（RQ-037、RQ-038）。SEIのCapability Observationも、観測から採用やAuthorityを自動では導かないことを原則にしています。
- **測定の保証水準を落とさない。** Workerの自己申告にとどまる量を、検証済みの量として渡しません。SEIの契約も、変換や解釈を経た観測を生の事実として扱わず、その変換の来歴（mapping provenance）を残すことを求めています。
- **「観測されていない」を「提供がない」にしない。** 測定できなかった量を0として記録すると、Providerに不利な誤った記録になります。SEIの契約にある`not observed != unavailable`と同じ考え方です。
- **Eventの種類を一つの得点にまとめない。** 「待機能力を評価する制度もあり得る」で挙げたCapacity offered、Work executed、Result accepted、Availability providedなどは観測の対象が異なるため、別々の観測として渡します。

もう一つ注意が必要なのは、業務端末のContributionを時刻付きで細かく渡すと、端末利用者がいつ席を外していたかを読み取れてしまうことです。外部へ渡す粒度は、RQ-040（不要な開示をしない監査）とRQ-018の範囲で決める必要があります。

この対応はSEIへの依存を作るものではありません。EKIは、Contribution RecordをSEIへ送らなくても成り立ちます。SEIとの対応の全体像は [SEI Cognitionとの対応ノート](sei-cognition-mapping.md) にまとめています。

## Requirementへの反映

この検討は、現在の次のRequirementを支持します。

- RQ-036 — Contribution accounting
- RQ-037 — External value mapping
- RQ-038 — No native currency or cash market
- RQ-039 — End-to-end provenance
- RQ-047 — Security-relevant capability claims require a trust basis

また、Worker Assurance ModelではResource Measurement AssuranceをResult Integrityとは別軸として維持する必要があります。

## 未決事項

- Resource Classをベンダー中立に比較する最小尺度
- Control Plane観測だけで測定できる量と、Worker側Evidenceが必要な量
- Energy contributionを含めるか
- Availability contributionをCore語彙へ含めるか
- 複数Fabric間でContribution Recordを持ち運ぶ場合の重複利用検知
- Privacyを保ちながらProvider / Beneficiary関係を外部Policyへ証明する方法
- Contribution RecordをSEI Cognitionなど外部の記録へ渡すとき、端末利用者の在席の傾向を読み取れない粒度をどう決めるか
