# 参考システムの調査（Reference Systems Research）

## 役割

この文書は、EKI Fabricの要求と設計を検討するときに使う、外部の事例についての調査メモです。要求やArchitectureの正本ではありません。

## 1. BOINC / Volunteer Computing

### 参考になる点

BOINCは、匿名で、信頼できない多数のVolunteer Hostを前提にしています。host障害、悪意のあるresult、多様なハードウェア、断続的なavailabilityを、通常の状態として扱っています。

特に重要なのは、次の点です。

- jobごとに、FLOPs、RAM、Diskなどの見積もりと上限を持つ。
- 信頼できない、または悪意のあるhostを前提に、resultを検証する。
- replicationによる検証では、同じjobを異なるhostに送り、結果の一致を確認する。不一致なら、追加のinstanceを発行する。
- アプリケーション固有のvalidatorを持てる。
- creditは、自己申告だけでは確定せず、有効なresultと結び付ける考え方がある。

### EKIで取り入れる部分

- endpointやアドホックなworkerを、高可用なnodeとはみなさない。
- Workloadに、resourceの上限を持たせる。
- ResultClaimとAcceptedResultを分離する。
- validatorとreplicationを、第一級の概念として検討する。
- contribution accountingを、自己申告だけに依存させない。

### そのまま取り入れない部分

- 2台の結果が一致したことだけを、強いByzantine耐性とはみなさない。Sybilとcollusionは、別に扱う。
- 科学計算のbatch workloadを中心にした前提を、一般の業務、AI、browserにそのまま持ち込まない。
- CPU timeを中心にしたcreditの尺度を、共通の価値の尺度にしない。

References:
- https://boinc.berkeley.edu/boinc_a_platform_for_volunteer_computing.pdf
- https://boinc.berkeley.edu/boinc_papers/hicss_08/hicss_08.pdf
- https://boinc.berkeley.edu/boinc_papers/credit/text.php

## 2. HashiCorp Nomad

### 参考になる点

Nomadは、serverとclientを分離しています。serverが、jobの受け付け、clientの管理、placementを担当する構造です。Security Modelでは、mTLS、ACL、quota、policyを、defense in depthとして扱っています。また、client driverやworkloadのnetwork accessそのものも、攻撃を受ける面として明示しています。

### EKIで取り入れる部分

- Control Planeと、実行するnodeのroleを分離する。
- workloadとnodeのidentityを、別の概念として持つ。
- quota、namespaceとpolicy、mTLSなどを、一つの対策ではなく、層として考える。
- workloadを実行するdriverが、hostへの攻撃の面になることを、明示する。

### 改善し、差別化する部分

Nomadは、管理されたclusterを中心にしています。EKIは、malicious worker、browser、外部のアドホックなworkerを、より強い前提に置きます。そのため、node identityを取得しただけでは、結果を信頼しません。

References:
- https://developer.hashicorp.com/nomad/docs/architecture
- https://developer.hashicorp.com/nomad/docs/architecture/security
- https://developer.hashicorp.com/nomad/docs/architecture/cluster/node-identity

## 3. SPIFFE / SPIRE Federation

### 参考になる点

SPIFFEは、workload identityをtrust domainで名前空間に分けます。そして、SVIDとbundleでidentityを検証します。Federationでは、各trust domainが独立したauthorityを保ったまま、外部のbundleを、明示的に受け入れます。

特に、外部のbundleを、一つのroot poolに雑に混ぜず、`trust domain ↔ bundle`のbindingを維持するという要求は、EKIのfederationの設計にとって重要です。

### EKIで取り入れる部分

- organizationや管理の境界を、trust domainとして表現できるidentity model。
- federationが、包括的なtrustを意味しないこと。
- 短い有効期間のworkload identityや、X.509に基づくidentityを、有力な候補とすること。

### 固定しない部分

SPIFFEとSPIREを、EKIの必須の依存先にはしません。企業のPKIやcloudのworkload identityなどを含めて、Requirementを満たす複数の実装を比べます。

References:
- https://spiffe.io/docs/latest/spiffe-specs/spiffe_federation/
- https://spiffe.io/docs/latest/spiffe-specs/x509-svid/

## 4. Sigstore / Cosign / policy-controller

### 参考になる点

Sigstoreは、artifactの署名、identityにもとづく検証、attestation、digestのbinding、admission policyを組み合わせています。policy-controllerは、tagをdigestに解決します。そして、admissionのときに、実行する対象が、署名したときと異ならないことを確認します。

### EKIで取り入れる部分

- Executorの表示名やtagではなく、content digestと署名の対象を結び付ける。
- supply-chainのmetadataとattestationを、policyの評価に利用できるようにする。
- codeのdeploymentとexecutor admissionを、task submissionから分離する。

### 固定しない部分

CosignとSigstoreの実装自体を、必須にはしません。EKIが必要とする、artifactのprovenanceとadmissionのpropertyを、先に定義します。

References:
- https://docs.sigstore.dev/cosign/verifying/verify/
- https://docs.sigstore.dev/policy-controller/overview/

## 5. TEIのrepositoryにおける文書の慣行

EKIと同じ作者の関連Repositoryである、TEIでは、Product Value、Requirements、Planning、Architecture、Evidenceの正本を分けています。そして、設計として決めたことと、実装とactual Evidenceで確認したこととを、区別しています。

### EKIで取り入れる部分

- Product ValueとRequirementを、設計の文書から分離する。
- 長期的な判断を、ADRに置き、現在の状況や作業の記録と混ぜない。
- Architectureの記述が存在することと、実装とProduct Evidenceが成り立ったことを、分離する。
- 調査の文書を、Requirementの第二の正本にしない。

### EKIの初期で改善する部分

TEIは、成熟するにつれて、文書の数が多くなっています。EKIの初期では、Product Value、Requirements、Threat Model、Acceptance Scenario、Research、ADRの、最小の骨格から始めます。GateとEvidenceの体系は、必要になった時点で広げます。

## 現時点での、横断的な示唆

1. **untrustedなworkerは、特殊なケースではない** — BOINCから学ぶ。
2. **node identityと、resultが正しいことは別のもの** — NomadとSPIFFEのidentityの概念を使っても、EKI固有のverificationが必要である。
3. **federationは、root trustの統合ではない** — trust-domainのbindingを維持する。
4. **code admissionとjob admissionを分ける** — supply-chainの攻撃の面を縮める。
5. **resourceの上限を、job contractに持たせる** — DoSと、暴走したtaskの両方に効く。
6. **既存の技術の名前を、Requirementにしない** — propertyを正本にして、実装の候補を比べる。
