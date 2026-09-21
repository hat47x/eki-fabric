# EKI Fabric Architecture

## このディレクトリの役割

このディレクトリは、Architecture Entry Gateを通過した後に採用する設計の正本を置く場所です。

Product ValueやProduct Requirementを置き換えるものではありません。

関係は次のように考えます。

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

RequirementとArchitectureが食い違った場合は、実装を正当化するためにRequirementを読み替えません。

Requirementの意図が変わったのであれば先にrequirements/を更新し、設計だけが誤っているのであればarchitecture/を修正します。

## Architecture Entry Gate

2026-09-21のGate Uレビューで、Architecture baseline作成を開始できる状態と判定しました。

詳細:

- ../plan/ARCHITECTURE_ENTRY_REVIEW_2026-09-21.md
- ../plan/UPSTREAM_PLAN.md

判定は条件付きPASSです。

実zkVM benchmark、learned scheduling、anonymous Sybil耐性、multi-component Confidential Computing、非決定的AI Result等の研究は継続します。

それらを未解決のまま特定実装へ固定しないことがGate通過の条件です。

## 最初に固定するもの

Architectureでは、使用製品やProcess数より先に次を固定します。

1. Authority / Trust Boundary
2. External / Internal Contract Boundary
3. Control Plane / Data Plane
4. Deployment Profile
5. Task / Attempt / Lease / Recovery state model
6. Extension SPI

現在のBaseline文書は次です。

1. `AUTHORITY_BOUNDARIES.md` — 誰が何を確定・許可できるか
2. `CONTRACT_BOUNDARIES.md` — External / Internal Contractの意味変換とbinding
3. `CONTROL_DATA_PLANE.md` — Control metadataとbulk Artifact transferの分離、Data access / cache / transfer境界
4. `DEPLOYMENT_PROFILES.md` — 小規模社内、Enterprise、Browser / Ad-hoc、Federation、Public-interest Grant等の物理構成

後続でState / Lease、Extension SPIを追加します。

## Logical AuthorityとProcessを分ける

EKIでは、Authorityを「Microserviceの数」と同義にしません。

小規模構成では複数Authorityを一つのBinaryへ同居させることができます。

それでも、

- どの鍵を使えるか
- どのpersistent stateを書けるか
- どのCapabilityを発行できるか
- どのPolicyを評価できるか
- どのAuthorityの結果を検証しなければならないか

は論理的に分けます。

一つのProcessへ同居したからといって、Scheduler moduleがExecutor Admission keyやArtifact Grant keyを直接使える構造にはしません。

大規模DeploymentでProcessを分離する場合も、この論理境界を保ったまま配置を変えられることを目指します。

## 設計文書の状態

Architecture文書は、次の状態を明示します。

- Draft: 比較・検討中
- Baseline: 現時点の採用設計
- Superseded: 後続ADR等で置き換えられた

Baselineは永久固定を意味しません。

Requirement、Evidence、運用知見が変われば、理由と移行経路を残して更新します。

## 未解決研究をArchitectureへ混ぜない

ArchitectureにはExtension Pointを用意できますが、未実証技術を必須Dependencyとして先取りしません。

たとえば、

- Remote Attestation
- Verifiable Computation
- Learned Scheduler
- Attested Contribution Metering

は接続可能な設計にしますが、Coreの成立条件にはしません。

標準構成は、それらがなくてもRequirement上の最低保証を説明できる必要があります。

## Branch運用

Architecture作業も一つのレビュー可能なWork UnitごとにBranchを切ります。

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

未マージBranchから次のArchitecture Branchを派生させません。
