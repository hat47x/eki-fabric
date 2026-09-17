# Resource Reclaim、電力、温度と端末保護

## 役割

この文書は、業務端末や一時的な提供資源をEKI Fabricで利用するとき、資源の取り戻し、電力、温度、端末寿命をどのように上流要件へ位置付けるかを検討する調査メモです。

## 背景

EKIでは、Providerや端末利用者が資源提供を止められることを重要な原則にしています。

しかし、資源提供の停止を単純に「Workerを一斉に切断する」と実装すると、別の問題が生じます。

たとえば大規模なCompute Grantを提供している企業が通常需要の増加によって一斉に資源を取り戻した場合、多数のTaskが同時にretryされ、残ったWorkerやNetworkへ負荷が集中する可能性があります。業務端末でも、利用者の復帰時に複数Taskが同時に別端末へ移動すれば、LAN負荷が急増することがあります。

また、遊休GPUは無料の資源ではありません。計算を行えば電力を消費し、熱を発生させ、冷却系やhardwareへ負荷を与えます。

## KubernetesのNode Pressureから得られる示唆

Kubernetesのkubeletは、MemoryやDiskなどのNode resourceが閾値を下回った場合、Podを退避してNodeの安定性を守ります。重要なのは、Schedulerが配置を決めた後でも、Node側が現在のresource pressureを見て実行を止められることです。

EKIの業務Endpointでも同様に、中央Schedulerの判断よりローカルのSafety Governorを優先する考え方が有用です。

ただしEKIでは、MemoryやDiskだけでなく、利用者操作、GPU温度、Battery、AC接続、Network、管理Policyなども考慮対象になります。

## Reclaimの基本原則

### 1. Providerの停止権は最終的に優先する

Providerや端末利用者が資源を必要とした場合、EKI Taskの継続を理由に資源返却を拒んではなりません。

Graceful checkpointを試みることはできますが、Providerの安全上限や利用者復帰よりTask完遂を優先しません。

### 2. Reclaimを異常ではなく通常状態として扱う

Spot / preemptible resourceと同様、Task側は一定のResource Classでは途中回収があり得ることを理解できる必要があります。

高い継続性が必要なTaskは、そうしたWorkerだけへ配置しない、replicationを増やす、checkpoint可能なExecutorを選ぶなど、別の保証が必要です。

### 3. Mass reclaim時の二次障害を避ける

大量Workerが同時に離脱した場合、すべてのTaskを即時retryすると、残存Node、Artifact Store、Network、Control Planeへ負荷が集中する可能性があります。

Architectureではbackoff、jitter、admission control、priorityなどが候補になりますが、上流要件としては「reclaimによるTask再配置が別のresource exhaustionを無制限に引き起こしてはならない」と整理できます。

## 電力・温度・端末寿命

### 1. 「idle」と「利用してよい」は同じではない

CPU/GPU usageが低くても、Battery駆動中、端末温度が高い、Fan noiseを抑えたい、電力Policy上利用したくない、といった状況があります。

Resource availabilityはCPU/GPUの空き率だけで決めません。

### 2. EnergyはProvider costの一部

Compute Grantや社内共有では、利用料が発生しなくても電力費用は発生します。

ProviderがEnergy budget、利用時間帯、Battery禁止、AC接続必須などのPolicyを設定できる余地が必要です。

### 3. Thermal safetyはローカルが最終判断する

温度センサーの正確な利用方法はOSやhardwareに依存します。そのためEKI共通Protocolで温度制御方式を固定する必要はありません。

ただし、ローカルAgent / Governorが現在の状態を理由にcapacityを縮小または0へできることは、RQ-007の重要な具体例です。

### 4. ContributionをHardware酷使の動機にしない

将来Contribution Recordが外部Credit制度へ接続された場合、Credit獲得のために端末を常時高負荷で動かす動機が生まれる可能性があります。

EKI Coreが価値を決めないことに加え、Resource Governorによる安全上限がContribution incentiveより常に優先される必要があります。

## Requirementへの反映候補

- Provider / User reclaimはTask完遂より優先されること。
- 大規模reclaim後のretry / migrationがFabric全体の負荷を無制限に増幅させないこと。
- Energy、power source、thermal状態、device safetyをResource Policyへ反映できること。
- ローカルSafety Governorは中央Schedulerより厳しい上限を常に適用できること。
- Contribution incentiveによってローカルSafety上限を迂回できないこと。

## 参考

- Kubernetes Node-pressure Eviction: https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/
