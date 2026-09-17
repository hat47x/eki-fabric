# Resource ReclaimとEnergy Governance

## この文書の役割

この文書は、Providerが大量のWorkerやCompute Grantを一斉に回収するときの安定性と、遊休資源利用に伴う電力・温度・設備負荷をどう扱うべきかを整理する調査メモです。

Product RequirementやArchitectureの正本ではありません。特定のCarbon APIや電力市場との連携を必須にするものでもありません。

## 1. 遊休資源は「無料の資源」ではない

EKIの出発点は、業務端末や既存設備に残っている計算余力を、安全に再利用することです。

しかし、CPUやGPUが現在使われていないからといって、その計算に追加コストがないわけではありません。

追加計算には、少なくとも次の負荷があります。

- 電力消費
- 冷却負荷
- 温度上昇
- fanや電源などへの負荷
- network transfer
- battery消費
- 利用者の体感性能への影響

したがって、EKIが「余っているなら使う」をそのままSchedulerの目的にすると、本来守りたい人間の利用や設備コストを侵害する可能性があります。

## 2. Providerが決める上限を第一級にする

既存Requirementでは、Endpoint側の温度、電力、network safety判断が中央上限より厳しい制約を適用できることを要求しています。

第三巡目では、これを個人端末だけでなくProvider全体のResource Policyへ広げる価値があります。

たとえばProviderは、次のようなPolicyを持てます。

```text
夜間だけ提供する
GPU powerを一定範囲までにする
月間電力量の上限を設ける
network transferを一定量までにする
業務繁忙時間は提供しない
再生可能電力が多い時間帯だけ低優先度Taskを受ける
```

最後のようなCarbon-aware運用は必須機能ではありませんが、Energy Policyを外部信号から与えられる余地は有用です。

Green Software Foundationでも、期限に余裕のあるAIやBatch workloadを、低Carbon時間帯やRegionへ移す考え方が整理されています。

## 3. Energy / CarbonはSecurityとは別のPolicy軸

Energy制約はSecurity requirementとは性質が異なります。

たとえば、あるWorkerが機密Taskを安全に扱えても、Providerが電力上限に達しているなら、そのWorkerへTaskを置かない判断があり得ます。

```text
Security上は実行可能
        +
Provider energy budgetを超える
        ↓
実行しない
```

そのため、Security Assuranceとは別のPlacement Constraintとして扱う方が自然です。

## 4. 大規模なReclaimは通常事象として起こり得る

Cloud Providerや大企業が公益向けに大量の余剰資源を提供する場合、通常需要が増えれば、その資源を一斉に本来用途へ戻す必要があります。

たとえば、10,000 Worker相当のGrantが短時間に回収される可能性があります。

このとき、単純にすべてのTaskを即座に別Workerへretryすると、次の問題が起こり得ます。

- Queueへの一斉再投入
- Artifactの大量再転送
- 別Providerへの急激な負荷集中
- Cloud fallback費用の急増
- retry storm
- Result verificationの重複

Providerが資源を回収できること自体は既存Requirementで要求済みですが、**回収がFabric全体の増幅障害へ変わらないこと**も別に考える必要があります。

## 5. ReclaimはFailureではなくCapacity Change

大規模Providerによる計画的なReclaimは、Worker crashと同じ「異常」として扱うより、Capacity Changeとして表現できる方がよい可能性があります。

たとえば、可能な場合には次のような情報を使えます。

```text
reclaim starts after 5 min
capacity reduces by 80%
no new leases
running preemptible tasks may finish within grace period
```

ただし、すべてのProviderが事前通知できるとは限りません。

したがってProduct Requirementでは、具体的な通知Protocolを固定するのではなく、突然の大量離脱と、予告された縮小の両方を扱えることを要求するのが適切です。

## 6. Retry stormを防ぐ

大量離脱後のTaskを再配置するときも、既存のbudget原則を維持する必要があります。

```text
10,000 tasks lost
        ↓
10,000 tasksを無条件に一斉retry
```

ではなく、Admission、Priority、残りBudget、Artifact locality、Deadlineなどを考慮して再配置する必要があります。

また、Artifactが大きいTaskでは、Compute capacityだけを見て遠隔地へ移すとNetwork側で二次障害を起こす可能性があります。

## 7. EKIへの要求候補

第三巡目では、少なくとも次を要求へ反映する価値があります。

1. ProviderがEnergy、Thermal、Network、利用時間などのResource Policyを安全側へ設定できること
2. 「遊休中」を「追加コストゼロ」と仮定しないこと
3. 大規模なWorker / GrantのReclaimを正常なCapacity Changeとして扱えること
4. 大量離脱がretry stormやArtifact再転送の無制限な増幅へつながらないこと
5. 期限に余裕があるTaskでは、Energy / Carbon signal等を任意のPlacement要因として利用できる余地を残すこと

## 参考資料

- Green Software Foundation — Use carbon-aware scheduling and region selection for AI workloads
  - https://patterns.greensoftware.foundation/operations/carbon-aware-ai-scheduling/
- Green Software Foundation — Carbon Awareness
  - https://learn.greensoftware.foundation/carbon-awareness/
- Green Software Foundation — Workload Dynamic Power and Cooling
  - https://greensoftware.foundation/standards/wdpc/
