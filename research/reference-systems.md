# Reference Systems Research

## 役割

この文書は、EKI Fabricの要求・設計検討に使う外部事例の調査メモです。要求やArchitectureの正本ではありません。

## 1. BOINC / Volunteer Computing

### 参考になる点

BOINCは、匿名かつ信頼できない多数のVolunteer Hostを前提に、host failure、malicious result、heterogeneous hardware、断続的availabilityを通常状態として扱っています。

特に重要なのは次です。

- jobごとにFLOPs、RAM、Disk等の見積・上限を持つ。
- unreliable / malicious hostを前提にresult validationを行う。
- replication-based validationでは異なるhostへ同じjobを送り、結果一致を確認し、不一致なら追加instanceを発行する。
- application-specific validatorを持てる。
- creditは自己申告だけで確定せず、valid resultと結びつける考え方がある。

### EKIで真似る部分

- endpoint / ad-hoc workerを高可用ノードとみなさない。
- Workloadにresource upper boundを持たせる。
- ResultClaimとAcceptedResultを分離する。
- validator / replicationをfirst-class conceptとして検討する。
- contribution accountingをself-reportだけへ依存させない。

### そのまま真似ない部分

- 2台一致だけを強いByzantine耐性とみなさない。Sybil/collusionを別途扱う。
- scientific batch workload中心の前提を一般業務・AI・browserへそのまま持ち込まない。
- CPU time中心のcredit尺度を共通価値尺度にしない。

References:
- https://boinc.berkeley.edu/boinc_a_platform_for_volunteer_computing.pdf
- https://boinc.berkeley.edu/boinc_papers/hicss_08/hicss_08.pdf
- https://boinc.berkeley.edu/boinc_papers/credit/text.php

## 2. HashiCorp Nomad

### 参考になる点

Nomadはserver/clientを分離し、serverがjob受付・client管理・placementを担当する構造を持ちます。Security ModelではmTLS、ACL、quota、policyをdefense in depthとして扱い、client driverやworkload network access自体もattack surfaceとして明示しています。

### EKIで真似る部分

- Control Planeとexecution nodeのrole分離。
- workload/node identityを別概念として持つこと。
- quota、namespace/policy、mTLS等を一つの対策でなく層として考える。
- workload実行driverがhost attack surfaceになることを明示する。

### 改善・差別化する部分

Nomadは管理下clusterを中心にしており、EKIはmalicious worker、browser、ad-hoc external workerをより強い前提に置く。したがってnode identity取得だけで結果をtrustしない。

References:
- https://developer.hashicorp.com/nomad/docs/architecture
- https://developer.hashicorp.com/nomad/docs/architecture/security
- https://developer.hashicorp.com/nomad/docs/architecture/cluster/node-identity

## 3. SPIFFE / SPIRE Federation

### 参考になる点

SPIFFEはworkload identityをtrust domainで名前空間化し、SVIDとbundleでidentityを検証します。Federationでは各trust domainが独立authorityを保ったままforeign bundleを明示的に受け入れます。

特に、foreign bundleを一つのroot poolへ雑に混ぜず、`trust domain ↔ bundle`のbindingを維持する要求はEKIのfederation設計に重要です。

### EKIで真似る部分

- organization / administrative boundaryをtrust domainとして表現できるidentity model。
- federationが包括的trustを意味しないこと。
- short-lived workload identity / X.509-based identityを有力候補とすること。

### 固定しない部分

SPIFFE/SPIREをEKI必須dependencyにはしない。Enterprise PKI、cloud workload identity等を含めRequirementを満たす複数実装を比較する。

References:
- https://spiffe.io/docs/latest/spiffe-specs/spiffe_federation/
- https://spiffe.io/docs/latest/spiffe-specs/x509-svid/

## 4. Sigstore / Cosign / policy-controller

### 参考になる点

Sigstoreはartifact signature、identity-based verification、attestation、digest binding、admission policyを組み合わせています。policy-controllerはtagをdigestへ解決して、admission時に実行対象が署名時と異ならないことを確認します。

### EKIで真似る部分

- Executorの表示名やtagではなくcontent digestと署名対象を結びつける。
- supply-chain metadata / attestationをpolicy evaluationへ利用可能にする。
- code deployment / executor admissionとtask submissionを分離する。

### 固定しない部分

Cosign/Sigstore実装自体を必須にはせず、EKIが必要とするartifact provenance / admission propertyを先に定義する。

References:
- https://docs.sigstore.dev/cosign/verifying/verify/
- https://docs.sigstore.dev/policy-controller/overview/

## 5. TEI repository documentation practice

EKIと同一作者の関連RepositoryであるTEIでは、Product Value / Requirements / Planning / Architecture / Evidenceの正本を分け、設計として決めたことと実装・actual Evidenceで確認したことを区別しています。

### EKIで真似る部分

- Product ValueとRequirementを設計文書から分離する。
- 長期判断をADRへ置き、current statusや作業記録と混ぜない。
- Architecture記述が存在することと、実装・Product Evidenceが成立したことを分離する。
- 調査文書をRequirementの第二正本にしない。

### EKI初期で改善する部分

TEIは成熟に伴い文書数が多くなっているため、EKI初期ではProduct Value、Requirements、Threat Model、Acceptance Scenario、Research、ADRの最小骨格から始める。GateやEvidence体系は必要性が出た時点で拡張する。

## 現時点の横断的示唆

1. **untrusted workerは特殊ケースではない** — BOINCから学ぶ。
2. **node identityとresult correctnessは別物** — Nomad/SPIFFEのidentity概念を使っても、EKI固有のverificationが必要。
3. **federationはroot trustの統合ではない** — trust-domain bindingを維持する。
4. **code admissionとjob admissionを分ける** — supply-chain attack surfaceを縮小する。
5. **resource upper boundをjob contractへ持つ** — DoSとrunaway taskの双方に効く。
6. **既存技術の名前をRequirementにしない** — propertyを正本にして実装候補を比較する。
