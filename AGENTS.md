# AGENTS.md

EKI Fabricの文書構成と上流工程の管理ルールです。

## 文書構成

```text
product/          守る価値・Vision・原則
requirements/     満たすべきProduct RequirementとThreat Model
research/         外部事例・標準・比較調査。要求や設計の正本ではない
spikes/           Architecture採用前の小さな実現可能性検証。仮説・条件・観測・限界を記録する
architecture/     Gate通過後に採用する論理構造・責務境界・Contractの設計正本
plan/             上流工程、Acceptance Scenario、Gate、ADR、作業計画
  adr/            長く残る設計判断（設計段階以降）
docs/             初見の読者、利用者、実装者向けの説明と用語整理
```

## 正本ルール

- 価値判断は`product/`、要求は`requirements/`、外部調査は`research/`、実現可能性検証は`spikes/`、工程・Gate・判断履歴は`plan/`を正本とする。
- `spikes/`の結果は、その実験条件で観測した事実と一般化可能な範囲を分けて記す。Spikeだけを根拠にProduct RequirementやArchitectureを暗黙に変更しない。
- 同じ要求を複数文書へ複製しない。説明文書は要求IDや正本文書を参照する。
- `docs/`は理解を助けるための説明層であり、Product RequirementやSecurity Invariantの第二正本にしない。
- Architecture Entry Gate通過後の採用設計は`architecture/`を正本とする。ただし特定製品・ライブラリ・プロトコルをRequirementから直接固定せず、Requirement・Evidence・Quality Attributeから設計判断を導く。
- 設計へ進む前に、Product Requirements、Threat Model、主要Acceptance Scenario、未決事項をレビュー可能な状態にする。
- 「Client」「Worker」「Provider」「Beneficiary」等の主体名は、善意や信頼性を含意しない。
- 認証済み主体も悪意を持ち得る。security propertyは主体の善意・未改変性・自己申告へ依存させない。
- 実現不可能な絶対保証を要求として書かない。保証対象と信頼基盤、保証不能条件を明示する。
- 未決事項は曖昧なままArchitectureへ押し込まず、Open Questionとして残す。
- 外部事例から優れた仕組みを積極的に採るが、その仕組みの前提条件とEKIとの差を記録する。

## ブランチ運用と履歴の収束

作業ブランチは、並行作業を無制限に増やすためではなく、まとまった変更を安全にレビューして元の履歴へ戻すために使います。

- 新しい作業ブランチは、原則として最新の`main`から作る。
- 一つの目的に対して複数の派生ブランチを連鎖的に作らない。作業途中で別branchへ乗り換えて変更を分散させない。
- 後続作業を未マージの作業ブランチから派生させない。先に現在の作業をPRで`main`へ収束させ、その後の作業は更新後の`main`から始める。
- 作業が一つのレビュー可能な単位になったらPRを作成し、内容を確認して`main`へマージする。
- マージ済みbranchを新しい正本として扱わない。正本は常に`main`側へ戻す。
- 同じテーマを継続する場合でも、前回PRがマージ済みであれば、次の作業は`main`を基点にする。
- branch名やPR名は作業目的が分かるものにし、「lane」「tmp」だけのように目的が追えない命名を避ける。
- PRを跨いで設計判断が変わった場合は、文書上の正本を更新し、未マージbranchにだけ新しい判断を残さない。

このルールの目的は、Git上の履歴と文書上の正本を同じ方向へ収束させることです。ブランチの数そのものを減らすことより、**未マージの派生関係を増やさず、変更を必ず元の`main`へ戻すこと**を重視します。

## 文書品質の原則

EKI Fabricは複数の専門領域にまたがるため、文書側が前提知識を補うことを重視します。

目標は、専門家向けの厳密さを下げることではありません。背景、具体例、用語説明、判断理由を丁寧に書くことで、**一般的なソフトウェア開発経験があれば、分散システムやセキュリティの専門家でなくても設計議論へ参加できる状態**を目指します。

### 日本語を第一にする

- 文書は自然な日本語を基本とする。英単語を並べた方が短くても、日本語として意味が取りにくい場合は文章で説明する。
- Protocol名、標準名、Requirement ID、確立した技術用語は英語を併記してよい。
- 不自然な直訳を避ける。日本語だけでは意味が曖昧になる場合は、最初に日本語で説明したうえで原語を添える。
- 名詞の列挙だけで背景や因果関係が失われる場合は、文章で補う。

### 専門用語を説明なしの前提にしない

専門用語を禁止しない。ただし、初出時には必要に応じて「何が問題なのか」「なぜその概念が必要なのか」を先に説明する。

たとえば`Sybil`という語だけを書くのではなく、「一人の攻撃者が多数のWorker Identityを作ることで、見かけ上の多数決を支配できる問題」と説明してから名称を使う。

`attestation`、`federation`、`quorum`、`provenance`等も、文脈上の意味が自明でない場合は同様に扱う。

### 説明の順序

新しい概念や難しい設計判断は、可能な限り次の順序で説明する。

1. どのような利用場面・問題があるか
2. 単純な方法では何が不足するか
3. EKIとして何を守る、または要求するか
4. 具体例ではどのように振る舞うか
5. 何を保証せず、何がまだ未決か
6. 厳密なRequirement、Threat、ADR等はどこにあるか

すべての節へ機械的に同じテンプレートを強制するものではない。読者が判断の流れを追えることを優先する。

### 抽象概念には具体例を添える

- Security boundaryは、攻撃や誤用の短い例を添える。
- Resource schedulingは、CPU/GPU性能だけでなく、network transferやcache localityを含む例を添える。
- Assuranceは、「何を確認できたか」と「そこから何を保証できるか」を分けた例を添える。
- 非目標は、単に「対象外」と書くだけでなく、必要に応じてなぜEKI Coreへ入れないのかを説明する。

### 説明と正本を混ぜない

読みやすい説明を増やすことと、同じRequirementを複数箇所へ複製することは別です。

`docs/`では背景や具体例を厚くしてよい一方、厳密な条件は`requirements/`を参照します。説明文書の言い換えから新しいRequirementを暗黙に作らないようにします。

## Requirement記法

- Product Requirementは`RQ-###`。
- Security RequirementはProduct Requirement内で`SEC-###`を補助IDとして利用できる。
- Requirementは可能な限り「何を満たすか」を書き、「どう実装するか」は設計判断へ分離する。
- 各Requirementには少なくとも理由、成立条件、非目標または境界を付ける方向で具体化する。
- 短いRequirement本文だけでは背景が読み取れない場合、正本側にも必要な理由説明を追加する。説明のために`docs/`を読まなければRequirementの意図が分からない状態にはしない。

## Architectureへ進む条件

少なくとも次が成立するまで、Core architectureを採用済みとはしない。

1. 主要利用形態がAcceptance Scenarioとして列挙されている。
2. 悪意あるClient / Worker / Sybil / resource exhaustion / result corruption / data exfiltrationがThreat Modelに含まれる。
3. 業務端末のopt-outとhuman-first resource governanceがRequirement化されている。
4. unknown OSS / browser / native / federated / ad-hoc参加がRequirement上で矛盾しない。
5. Public-interest Compute GrantとContribution accountingの境界が定義されている。
6. EKI自身が通貨・市場を提供しない非目標が固定されている。
7. Design候補の比較軸がRequirementとQuality Attributeから導ける。
8. Federation namespace、Artifact safe processing、Control Plane authority separation、mass reclaimが上流要件で扱われている。
9. 初見の開発者が`docs/`から正本文書へ辿れ、主要用語と設計上の難所について背景を追える。

## 読者向け導線

- 文書案内: [`docs/README.md`](docs/README.md)
- 初めて読む方へ: [`docs/for-readers.md`](docs/for-readers.md)
- 用語集: [`docs/glossary.md`](docs/glossary.md)
- Requirementの読み方: [`requirements/README.md`](requirements/README.md)
