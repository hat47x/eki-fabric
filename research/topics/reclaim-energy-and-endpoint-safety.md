# Resource Reclaim、電力、温度と端末保護

## この文書の役割

この文書は、業務端末や一時的な提供資源をEKI Fabricで利用するとき、資源の取り戻し、電力、温度、端末寿命、Provider側のEnergy Policyをどのように上流要件へ位置付けるかを検討する調査メモです。

Product RequirementやArchitectureの正本ではありません。特定のCarbon API、電力市場、OS APIを必須採用することも、この文書では決めません。

## 背景

EKIでは、Providerや端末利用者が資源提供を止められることを重要な原則にしています。

しかし、資源提供の停止を単純に「Workerを一斉に切断する」と実装すると、別の問題が生じます。

たとえば大規模なCompute Grantを提供している企業が通常需要の増加によって一斉に資源を取り戻した場合、多数のTaskが同時にretryされ、残ったWorkerやNetworkへ負荷が集中する可能性があります。

業務端末でも、勤務開始時に多数の社員が同時にPCへ戻れば、複数Taskが一斉に別端末へ移動し、LANやArtifact Storeへ負荷が集中することがあります。

また、遊休GPUは無料の資源ではありません。計算を行えば電力を消費し、熱を発生させ、冷却系やhardwareへ負荷を与えます。

## 1. 「idle」と「利用してよい」は同じではない

CPU / GPU usageが低くても、次の理由で資源提供を止めたい場合があります。

- Battery駆動中
- 端末温度が高い
- Fan noiseを抑えたい
- 電力Policy上利用したくない
- Networkが混雑している
- 本来業務の開始が近い
- Hardware保護のため高負荷時間を抑えたい

したがって、Resource availabilityはCPU / GPUの空き率だけで決めません。

## 2. Local Safety Governorは最終的に中央より強い

中央Schedulerが「この端末は60%まで利用可能」と判断していても、ローカル側が現在のBattery、Thermal、Network、User activity等を理由に0%へ縮小できる必要があります。

この原則は、単なるUX上の配慮ではなくSecurity / Safety boundaryでもあります。

中央Componentが誤動作・侵害された場合でも、Endpoint側の上限を勝手に引き上げられないことが重要です。

RQ-007とSEC-022は、この考え方を上流Requirementとして固定します。

## 3. EnergyはProvider costの一部

Compute Grantや社内共有では、利用料が発生しなくても電力費用は発生します。

そのためProviderは、必要に応じて次のようなPolicyを持てる方が自然です。

```text
夜間だけ提供する
GPU powerを一定範囲までにする
月間電力量の上限を設ける
Battery駆動中は提供しない
AC接続時だけ提供する
Network transferを一定量までにする
業務繁忙時間は提供しない
```

具体的なPolicy schemaはArchitectureで検討します。

## 4. Carbon-aware schedulingは任意の拡張要因にできる

Green Software Foundationでは、TrainingやBatchのように期限に余裕があるWorkloadを、電力のCarbon intensityが低い時間帯やRegionへ移す考え方が整理されています。

EKIでも、Providerが希望する場合に、外部のEnergy / Carbon signalをPlacement要因へ加えられる余地は有用です。

ただし、これはSecurity Assuranceとは別のPolicy軸です。

```text
Security上は実行可能
        +
Provider Energy Policyを満たさない
        ↓
実行しない
```

また、Carbon-aware schedulingや特定のEnergy data providerをEKI Coreの必須機能にはしません。

## 5. Providerの停止権はTask完遂より優先する

Providerや端末利用者が資源を必要とした場合、EKI Taskの継続を理由に資源返却を拒んではなりません。

Graceful checkpointを試みることはできますが、ProviderのSafety上限や利用者復帰よりTask完遂を優先しません。

## 6. Reclaimを異常ではなくCapacity Changeとして扱う

Spot / preemptible resourceと同様、Task側は一定のResource Classでは途中回収があり得ることを理解できる必要があります。

特にCloud Providerや大企業が公益向けに大量の余剰資源を提供する場合、通常需要が増えれば、数千〜数万Worker相当のCapacityを短時間に回収する可能性があります。

このような計画的Reclaimは、単なる「大量のWorker crash」ではなく、Fabricで起こり得るCapacity Changeとして扱える方が自然です。

Providerが事前通知できる場合には、たとえば次のような情報を使える余地があります。

```text
5分後からcapacityを80%縮小する
新しいLeaseは受けない
preemptible Taskはgrace period内に終了できる
```

ただし、すべてのProviderが事前通知できるとは限りません。突然の大量離脱も安全に扱う必要があります。

## 7. Mass reclaim時の二次障害を避ける

大量Workerが同時に離脱した場合、すべてのTaskを即時retryすると、次の問題が起こり得ます。

- Queueへの一斉再投入
- Artifactの大量再転送
- 残存Workerへの負荷集中
- 別Providerへの急激な負荷集中
- Cloud fallback費用の急増
- Result verificationの重複
- Network congestion

Architectureではbackoff、jitter、admission control、priorityなどが候補になります。

上流要件としては、**reclaimによるTask再配置が別のResource Exhaustionを無制限に引き起こしてはならない**と整理します。

また、Artifactが大きいTaskでは、Compute capacityだけを見て遠隔地へ移すとNetwork側で二次障害を起こす可能性があります。再配置でもData localityとTransfer Costを維持する必要があります。

## 8. ContributionをHardware酷使の動機にしない

将来Contribution Recordが外部Credit制度へ接続された場合、Credit獲得のために端末を常時高負荷で動かす動機が生まれる可能性があります。

EKI Coreが価値を決めないことに加え、Resource Governorによる安全上限がContribution incentiveより常に優先される必要があります。

## Requirementへの反映

第三巡目では、次をProduct Requirementsへ反映しています。

### RQ-052 — Coordinated reclaim without retry amplification

- Provider / Userのreclaim権を維持する
- 大量Workerの同時離脱を通常起こり得るFabric Eventとして扱う
- retry / migrationが別のResource Exhaustionへ無制限に増幅しない
- Priority、Deadline、Budget、Data locality等を再配置判断へ利用できる余地を持つ

### RQ-053 — Energy / thermal / device-safety aware resource policy

- Resource availabilityをidle率だけで決めない
- Energy、Battery / AC、利用時間帯、Thermal、端末保護等でcapacityを縮小・停止できる
- Contribution incentiveがlocal Safety Governorを迂回しない

Carbon-aware schedulingはArchitecture上の任意拡張候補として残します。

## Architectureへ持ち越すもの

- Reclaim通知Protocol
- backoff / jitter / queue discipline
- grace periodの表現
- Energy / Power telemetryの取得方法
- OS / hardware差の吸収方法
- Carbon intensity取得元
- Energy accountingの精度

## 参考資料

- Kubernetes Node-pressure Eviction
  - https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/
- Green Software Foundation — Use carbon-aware scheduling and region selection for AI workloads
  - https://patterns.greensoftware.foundation/operations/carbon-aware-ai-scheduling/
- Green Software Foundation — Carbon Awareness
  - https://learn.greensoftware.foundation/carbon-awareness/
- Green Software Foundation — Workload Dynamic Power and Cooling
  - https://greensoftware.foundation/standards/wdpc/
