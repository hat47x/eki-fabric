# EKI Fabric Architecture

## このディレクトリの役割

このディレクトリは、Architecture Entry Gateを通過したあとに採用する設計の正本を置く場所です。

Product ValueやProduct Requirementを置き換えるものではありません。

関係は次のとおりです。

~~~text
product/
  守る価値・原則
    ↓
requirements/
  満たすべき条件・脅威・保証
    ↓
research/ / spikes/
  比較材料・実現可能性Evidence
    ↓
plan/
  Gate・工程・判断履歴
    ↓
architecture/
  採用する論理構造・責務境界
    ↓
implementation
~~~

RequirementとArchitectureが食い違ったとき、実装を正当化するためにRequirementを読み替えることはしません。

Requirementの意図が変わったのなら、先にrequirements/を更新します。設計だけが誤っているのなら、architecture/を直します。

## Architecture Entry Gate

2026-09-21のGate Uレビューで、Architecture baselineの作成を始められる状態だと判定しました。

詳細は次の文書にあります。

- ../plan/ARCHITECTURE_ENTRY_REVIEW_2026-09-21.md
- ../plan/UPSTREAM_PLAN.md

判定は条件付きのPASSです。

実際のzkVMのbenchmark、learned scheduling、匿名のSybilへの耐性、複数componentにまたがるConfidential Computing、非決定的なAIのResultなどの研究は、続けます。

これらが未解決のまま、特定の実装に固定しないことが、Gateを通過する条件です。

## 最初に固定するもの

Architectureでは、使う製品やプロセスの数よりも先に、次のものを固定します。

1. Authority / Trust Boundary
2. External / Internal Contract Boundary
3. Control Plane / Data Plane
4. Deployment Profile
5. Task / Attempt / Lease / Recoveryの状態モデル
6. Extension SPI

現在のBaseline文書は、次の6つです。

1. `AUTHORITY_BOUNDARIES.md`: 誰が何を確定し、許可できるか
2. `CONTRACT_BOUNDARIES.md`: External / Internal Contractの意味の変換と結び付け
3. `CONTROL_DATA_PLANE.md`: Controlのmetadataと大きなArtifactの転送の分離、Dataのアクセス、cache、転送の境界
4. `DEPLOYMENT_PROFILES.md`: 小規模な社内、Enterprise、Browserとアドホック、Federation、公益向けGrantなどの物理的な構成
5. `TASK_STATE_MODEL.md`: Task、Attempt、Lease、Result、外部への副作用、回復の状態機械
6. `EXTENSION_SPI.md`: Queue、State、Storage、Identity、Runtime、Verificationなどを差し替える境界

Architecture Baseline 0.1の主要な6つの領域を横断してレビューし、`BASELINE_REVIEW_2026-09-21.md`で、重大な責務の矛盾がないことを確認しました。`MVP_IMPLEMENTATION_PLAN.md`には、BaselineのSecurityと状態の意味を保った最小のvertical slice、M0からM5、Definition of Doneを定義しています。

`plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`で、Protocolの面ごとの技術を決めました。External Client PlaneにはHTTPS、JSON、OpenAPIを使います。Worker Planeと、プロセス外のAdapter Planeには、Protobuf IDLとConnectの参照transportを使います。署名付きのAuthority Decisionには、deterministic CBOR、CDDL、COSE_Sign1を使います。Protobufをserializeしたbytesは、署名や汎用のフィンガープリントの正規のbytesには使いません。

P1のWorker Planeは`spec/worker/v1/worker.proto`に、P2の署名付きAuthority Decisionのpayloadは`spec/decisions/v1/decisions.cddl`に、それぞれ機械可読な形でまとめました。Worker Identityは認証の文脈に置き、HeartbeatとLeaseの更新を分けています。WorkOrder、Lease、Artifact Grantなど、署名の対象になるものは、P2のdeterministic CBORに固定しています。

`plan/adr/0004-signed-decision-cryptographic-profile.md`では、次のものを定めています。Authority roleごとに鍵を分けること。Ed25519によるsoftwareの参照実装。ES256による企業との相互運用。Key Registry、鍵の交換、失効。COSEのprotected headerの検証。署名が正しいというだけではAuthorizationが成り立たず、Authority role、Decisionの種類、対象、現在のpolicyまで検証し直します。

参照するGo Coreでは、`internal/core/authority`と`internal/core/decisiontrust`で、この境界の最小限の実装を始めています。Decisionの種類を含まない生の鍵解決APIは、外へ公開していません。有効化と引退の時刻を含む鍵のlifecycleを、検証の条件にしています。

`spec/external/openapi.json`に、P0のExternal Client Planeの機械可読なschemaを追加しました。固定したのは、Taskのsubmit、status、cancel、resultと、Artifactのingest、upload、descriptorだけです。Worker、Lease、WorkOrder、ArtifactGrantのtokenなど、内部の概念は露出していません。`spec/external/README.md`と、否定的なfixtureで、悪意ある入力に対する境界も明文化しています。

`plan/adr/0002-reference-implementation-language.md`で、Reference Implementationの言語にGoを採用しました。`plan/IMPLEMENTATION_REPOSITORY_LAYOUT.md`では、`cmd/`、`internal/core`、`internal/control`、`internal/agent`、`internal/spi`、`internal/adapters`、`spec`、`conformance`のそれぞれの責務を定めました。Goのpackageは外部Contractの正本にしません。最初のCore実装は、原則として`internal/`の中にとどめます。

次は、このRepository Layoutを`main`にまとめたうえで、M0のexecutable skeletonへ進みます。

## 論理上のAuthorityと、プロセスを分ける

EKIでは、Authorityの数とMicroserviceの数を同じものとして扱いません。

小規模な構成では、複数のAuthorityを一つのバイナリに同居させられます。

それでも、次のことは論理的に分けておきます。

- どの鍵を使えるか
- どの永続的な状態を書けるか
- どのCapabilityを発行できるか
- どのPolicyを評価できるか
- どのAuthorityの結果を、検証しなければならないか

一つのプロセスに同居していても、Schedulerのmoduleが、Executor Admissionの鍵やArtifact Grantの鍵を直接使える構造にはしません。

大規模なDeploymentでプロセスを分ける場合も、この論理上の境界を保ったまま配置を変えられることをめざします。

## 設計文書の状態

Architectureの文書には、次のいずれかの状態を明示します。

- Draft: 比較や検討の途中
- Baseline: 現時点で採用している設計
- Superseded: 後のADRなどで置き換えられたもの

Baselineは、永久に固定されることを意味しません。

Requirement、Evidence、運用で得た知見が変われば、理由と移行の経路を残したうえで更新します。

## 未解決の研究をArchitectureに混ぜない

Architectureには、Extension Pointを用意できます。ただし、まだ実証されていない技術を、必須の依存先として先取りすることはしません。

たとえば、次のものは接続できる設計にしますが、Coreが成り立つための条件にはしません。

- Remote Attestation
- Verifiable Computation
- Learned Scheduler
- Attested Contribution Metering

標準の構成は、これらがなくても、Requirementが求める最低限の保証を説明できなければなりません。

## Branchの運用

Architectureの作業も、レビューできる単位（Work Unit）ごとにBranchを切ります。

~~~text
latest main
  ↓
architecture/authority-map
  ↓ PR / merge
main
  ↓
architecture/task-contract
  ↓ PR / merge
main
~~~

マージされていないBranchから、次のArchitectureのBranchを派生させません。
