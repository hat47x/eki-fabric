# Control Plane部分侵害と権限分離

## 役割

この文書は、EKI FabricのControl Plane内部に複数の役割が存在するとき、その一部が侵害されても直ちに全権限へ拡大しないための要求を検討する調査メモです。具体的なService分割や実装方式はまだ定めません。

## 背景

これまでのThreat Modelでは、ClientとWorkerを信頼しない一方、Control Planeのroot signing authorityやAdministrator Policy authorityまで同時に侵害された場合は通常保証の外としています。

この境界は必要ですが、だからといってControl Plane内部を「一つの完全信頼Process」と考える必要はありません。

たとえば、Schedulerが侵害された場合でもSecret管理やFederation設定まで変更できる必要はありません。Telemetry Collectorが侵害されても新しいExecutorを承認できる必要はありません。

Control Plane内部でも、役割ごとに必要最小限の権限へ分けることでblast radiusを小さくできます。

## NIST Zero Trustから得られる示唆

NIST SP 800-207では、Policy Engine、Policy Administrator、Policy Enforcement Pointといった役割を分け、アクセス判断と実際のEnforcementを明示的な境界として扱います。また、least privilege、separation of duties、継続的な評価を重視します。

EKIがこの構成をそのまま採る必要はありませんが、「判断する主体」「権限を発行する主体」「実際に強制する主体」を一つの無制限な権限へまとめないという考え方は参考になります。

## EKIで考えるべき権限の例

Control Planeには将来的に、少なくとも次のような異なる権限が現れる可能性があります。

- TaskをAdmissionする
- Placementを決める
- WorkOrderを発行・署名する
- Artifact read capabilityを発行する
- Executorを承認する
- Federation設定を変更する
- Grant / quotaを変更する
- Attestation / Result verificationを評価する
- Contribution Recordを確定する
- Audit logを読む

これらすべてを一つのCredentialで操作できることをProduct Requirementにする理由はありません。

## 基本原則

### 1. Control Plane componentも自動的に全権ではない

内部Serviceであることだけを理由に、すべてのArtifact、Credential、Federation設定、Signing Keyへアクセスできる構造を前提にしません。

### 2. 判断とEnforcementを分けられる余地を持つ

Schedulerが「このWorkerへ配置する」と判断しても、その判断だけで任意のData access権が生成される必要はありません。

逆に、Capability発行Serviceが任意のPlacementを選べる必要もありません。

具体的なService構成はArchitectureで決めますが、少なくとも権限の種類を分離可能なモデルを要求する価値があります。

### 3. 高価値変更は通常のTask実行経路から分ける

Federation設定、Executor admission、Root key、Grant policyなどは、日常的なTask schedulingより影響範囲が大きい操作です。

これらを通常のClient APIやWorker APIから到達できないようにし、必要に応じて別の管理権限や複数者承認へ接続できる余地を持たせます。

### 4. 一部侵害を検知・追跡できる

内部component間の判断についても、重要なauthority transitionを追跡できることが望まれます。

「Schedulerが選んだ」「Policyが許可した」「SignerがWorkOrderを発行した」といった事実を、必要な範囲で区別できれば、事後分析と権限縮小が容易になります。

## Requirementへの反映候補

- Control Planeの一部componentが侵害されても、その役割に不要な権限まで自動的に得られない構造を選べること。
- Task admission、placement、data authority、executor admission、federation administration、verification authority等を分離可能にすること。
- 高価値なPolicy変更やTrust設定変更を通常Task経路から分離できること。
- Control Plane内部の重要なauthority transitionを監査可能にすること。

## 参考

- NIST SP 800-207: https://csrc.nist.gov/pubs/sp/800/207/final
- NIST Zero Trust implementation architecture: https://pages.nist.gov/zero-trust-architecture/VolumeB/architecture.html
- NIST SP 800-207A: https://csrc.nist.gov/pubs/sp/800/207/a/final
