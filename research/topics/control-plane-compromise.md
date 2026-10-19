# Control Plane部分侵害と権限分離

## この文書の役割

この文書は、EKI FabricのControl Plane内部に複数の役割が存在するとき、その一部が侵害・誤動作しても直ちに全権限へ拡大しないための要求を検討する調査メモです。

Product RequirementやArchitectureの正本ではありません。Control Planeを必ずMicroservice化することや、特定のZero Trust製品を採用することを決めるものでもありません。

## 背景

これまでのThreat Modelでは、ClientとWorkerを信頼しない一方、Control Planeのroot signing authorityやAdministrator Policy authorityまで同時に侵害された場合は通常保証の外としています。

この境界は必要ですが、その間には大きな領域があります。

たとえば次の一つだけが侵害される場合です。

- Scheduler
- Admission component
- Result Verifier
- Artifact capability発行component
- Federation管理component
- Contribution accounting component
- Telemetry / Audit component

一つのComponentが侵害されたとき、それだけでSecret管理、Executor admission、Grant変更、Federation設定、Result acceptanceなどの全権限まで得られる必要はありません。

## Zero Trustから得られる示唆

NIST SP 800-207では、ネットワーク位置や「社内資産であること」だけを暗黙のTrust根拠にせず、ResourceごとにAuthentication / Authorizationを行う考え方を示しています。

NISTのZero Trust実装ガイドでも、Policy Engine、Policy Administrator、Policy Enforcement Pointなどの役割を分け、Least Privilegeによってlateral movementとblast radiusを抑える考え方が示されています。

EKIがこの構成をそのまま採る必要はありません。

重要なのは、**「内部Componentだから何でもできる」という前提を置かないこと**です。

## EKIで考えるべきAuthority

Control Planeには将来的に、少なくとも次のような異なるAuthorityが現れる可能性があります。

- TaskをAdmissionする
- Placementを決める
- WorkOrderを発行・署名する
- Artifact read capabilityを発行する
- Executorを承認する
- Federation設定を変更する
- Grant / quotaを変更する
- Attestation / Result verificationを評価する
- Accepted Resultへ昇格する
- Contribution Recordを確定する
- Audit logを読む

これらすべてを一つのCredentialで操作できることをProduct Requirementにする理由はありません。

## 1. Schedulerは配置を決めても、権限を作らない

Schedulerが「Worker Aで実行する」と判断できても、その判断だけで次のAuthorityまで自由に得る必要はありません。

- 任意Artifactの復号鍵を取得する
- 任意Executorを承認する
- Data Classを下げる
- ProviderのGrant上限を変更する

Placement DecisionとAuthority Issuanceを分離できれば、Schedulerが侵害されたときの被害を小さくできます。

## 2. Verifierも万能な管理者ではない

Result Verifierが一つのResultを評価するAuthorityを持つことと、任意Taskを作成したりFederation設定を変更したりできることは別です。

同様に、Contribution accounting componentが計算提供実績を記録できても、Compute Grantの対象者や価値交換Policyまで自由に変更できる必要はありません。

## 3. 高価値変更は通常Task経路から分ける

Federation設定、Executor admission、Root key、Grant Policyなどは、日常的なTask schedulingより影響範囲が大きい操作です。

これらを通常のClient APIやWorker APIから到達できないようにし、必要に応じて別の管理権限や独立承認へ接続できる余地を持たせます。

具体的な承認方式はArchitectureで検討します。

## 4. 一部侵害とRoot authority侵害を区別する

EKIで「Control Plane Componentの侵害へ耐える」と言っても、Root signing key、Policy root、すべてのAdministrator credentialまで同時に侵害された状態で完全な保証を行うことは要求しません。

重要なのは、次の中間状態です。

> 一部Componentの脆弱性やCredential漏えいがあっても、そこから無関係なAuthorityへ当然には横移動できない。

これは完全なByzantine Control Planeを要求するものではなく、Least Privilegeとblast-radius reductionの要求です。

## 5. Process数ではなくAuthority boundaryを見る

一つのProcessへ複数Roleを実装すること自体を禁止する必要はありません。

小規模な中小企業向けDeploymentでは、一つのBinaryとして提供した方が運用しやすい可能性があります。

その場合でも、内部のAuthorityを論理的に区別し、将来必要に応じて分離できる構造を保つことが重要です。

したがって、RQ-051は「Microserviceへ分割せよ」ではなく「不要なAuthorityを一つへ集めるな」という要求です。

## 6. Auditもblast radiusの把握に使う

侵害を完全に防げない場合、後から「どのAuthorityを使って何が変更されたか」を追えることも重要です。

たとえば、次のような判断を必要な範囲で区別できると、事後分析と権限縮小が容易になります。

```text
SchedulerがWorkerを選んだ
Policyがその配置を許可した
SignerがWorkOrderを発行した
VerifierがResultを受理した
Grant Administratorがcapacityを変更した
```

ただし、Audit component自体の完全な耐タンパ性をこの調査だけで要求するものではありません。

## Requirementへの反映

第三巡目では、次を`RQ-051 — Control-plane authority separation`としてProduct Requirementsへ反映しています。

- Control Planeの一部Componentが侵害されても、その役割に不要なAuthorityまで自動的に得られない構造を選べること
- Task admission、placement、data authority、Executor admission、Federation administration、Verification authority等を論理的に分離できること
- 高価値なTrust設定変更を通常Task経路から分離できること
- Root authority全体の侵害と、一部Componentの侵害を区別すること

Threat Modelでは、compromised Control Plane componentを独立した攻撃面として扱います。

## Architectureへ持ち越すもの

- Control Planeを何Process / Serviceへ分けるか
- WorkOrder signerをどこへ置くか
- Secret / key管理境界
- 高価値操作へmulti-party approvalを要求するか
- Audit logの耐タンパ性をどの水準まで求めるか
- 小規模単一Binary構成と大規模分離構成をどう両立するか

## 参考資料

- NIST SP 800-207 — Zero Trust Architecture
  - https://csrc.nist.gov/pubs/sp/800/207/final
- NIST SP 1800-35 — Implementing a Zero Trust Architecture
  - https://csrc.nist.gov/pubs/sp/1800/35/final
- NIST Zero Trust Architecture Project — Risks Addressed
  - https://pages.nist.gov/zero-trust-architecture/VolumeE/RisksAddressed.html
- NIST SP 800-207A
  - https://csrc.nist.gov/pubs/sp/800/207/a/final
