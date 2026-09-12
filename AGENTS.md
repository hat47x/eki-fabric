# AGENTS.md

EKI Fabricの文書構成と上流工程の管理ルールです。

## 文書構成

```text
product/          守る価値・Vision・原則
requirements/     満たすべきProduct RequirementとThreat Model
research/         外部事例・標準・比較調査。要求や設計の正本ではない
plan/             上流工程、Acceptance Scenario、Gate、ADR、作業計画
  adr/            長く残る設計判断（設計段階以降）
docs/             利用者・実装者向け説明（設計段階以降に拡充）
```

## 正本ルール

- 価値判断は`product/`、要求は`requirements/`、外部調査は`research/`、工程・Gate・判断履歴は`plan/`を正本とする。
- 同じ要求を複数文書へ複製しない。説明文書は要求IDを参照する。
- 現段階ではArchitectureを確定しない。特定製品・ライブラリ・プロトコルを要求から直接固定しない。
- 設計へ進む前に、Product Requirements、Threat Model、主要Acceptance Scenario、未決事項をレビュー可能な状態にする。
- 「Client」「Worker」「Provider」「Beneficiary」等の主体名は、善意や信頼性を含意しない。
- 認証済み主体も悪意を持ち得る。security propertyは主体の善意・未改変性・自己申告へ依存させない。
- 実現不可能な絶対保証を要求として書かない。保証対象と信頼基盤、保証不能条件を明示する。
- 未決事項は曖昧なままArchitectureへ押し込まず、Open Questionとして残す。
- 外部事例から優れた仕組みを積極的に採るが、その仕組みの前提条件とEKIとの差を記録する。
- 文書は日本語を基本とし、Protocol名・標準名・Requirement ID等は英語を併記してよい。

## Requirement記法

- Product Requirementは`RQ-###`。
- Security RequirementはProduct Requirement内で`SEC-###`を補助IDとして利用できる。
- Requirementは可能な限り「何を満たすか」を書き、「どう実装するか」は設計判断へ分離する。
- 各Requirementには少なくとも理由、成立条件、非目標または境界を付ける。

## Architectureへ進む条件

少なくとも次が成立するまで、Core architectureを採用済みとはしない。

1. 主要利用形態がAcceptance Scenarioとして列挙されている。
2. 悪意あるClient / Worker / Sybil / resource exhaustion / result corruption / data exfiltrationがThreat Modelに含まれる。
3. 業務端末のopt-outとhuman-first resource governanceがRequirement化されている。
4. unknown OSS / browser / native / federated / ad-hoc参加がRequirement上で矛盾しない。
5. Public-interest Compute GrantとContribution accountingの境界が定義されている。
6. EKI自身が通貨・市場を提供しない非目標が固定されている。
7. Design候補の比較軸がRequirementから導ける。
