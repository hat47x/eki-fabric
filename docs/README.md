# EKI Fabric ドキュメント案内

このディレクトリには、EKI Fabricの背景や用語、設計判断を説明する文書を置いています。初めて読む方や、専門分野の異なる開発者が対象です。

EKI Fabricの設計には、分散システム、セキュリティ、暗号技術、計算資源の自発的な提供、企業ネットワークなど、幅広い知識が関わります。すべての専門知識を参加者に求めると、設計上の重要な判断に加われる人が限られます。

正本である要求文書や脅威モデルの厳密さは保ちます。このディレクトリでは、背景、具体例、用語、判断理由を補い、一般的なソフトウェア開発の経験があれば議論に加われる説明を目指します。

## 最初に読む文書

EKI Fabricを初めて読む場合は、次の順序を勧めます。

1. [はじめてEKI Fabricを読む方へ](for-readers.md)
2. [用語集](glossary.md)
3. [Product Value](../product/value.md)
4. [Product Principles](../product/principles.md)
5. [Product Requirements](../requirements/PRODUCT_REQUIREMENTS.md)
6. [Threat Model](../requirements/THREAT_MODEL.md)
7. [Worker Assurance Model](../requirements/ASSURANCE_MODEL.md)
8. [Acceptance Scenarios](../requirements/ACCEPTANCE_SCENARIOS.md)
9. [Architecture](../architecture/README.md) — 実装・設計へ参加する場合
10. [開発を始める方へ](development.md) — M0 Reference Implementationをローカルで確認する場合

最初からすべてを読む必要はありません。全体像を知る場合は「はじめてEKI Fabricを読む方へ」まで、要求のレビューに参加する場合は製品要求と脅威モデルまでを、読む範囲の目安として使えます。Workerや一時参加者に求める保証を検討する場合は、保証モデルも確認できます。

## 正本と説明文書の関係

`docs/`は理解を助ける説明の場です。製品要求（Product Requirement）や安全上の不変条件（Security Invariant）の正本は、ここには置きません。

```text
説明・背景
  docs/
    ↓ 参照
価値・原則
  product/
    ↓
要求・脅威・保証
  requirements/
    ↓
工程・判断
  plan/
    ↓ Gate通過後
採用設計
  architecture/
```

説明では、分かりやすさのために表現を変えることがあります。ただし、要求の意味を独自に強めたり弱めたりしません。説明と正本が食い違った場合は、正本に合わせて説明文書を直します。

## 文書を書くときの基本姿勢

EKI Fabricでは、専門用語を使わないこと自体は目的にしません。必要な用語は使います。ただし、用語を知っていることを説明の前提にしません。

新しい概念は、できるだけ次の順序で説明します。

1. 何が困るのか
2. なぜ単純な方法では足りないのか
3. EKIでは何を要求するのか
4. 具体例ではどう見えるか
5. どこまでは保証せず、何が未決なのか
6. 厳密な定義やRequirement IDはどこにあるか

たとえば「Sybil耐性」とだけ書くのではなく、「一人の攻撃者が多数の実行端末（Worker）を装い、見かけ上の多数決を支配できるため、Workerの数だけでは独立性を証明できない」と説明します。その後で必要に応じてSybilという名称を添えます。

## 文書品質の目標

目標とする読者像は、「分散システムやRemote Attestationの専門家ではないが、一般的なアプリケーション開発、インフラ構築、業務システム開発のいずれかを経験している人」です。

専門家向けの厳密さを落とさず、説明を補うことで参加に必要な専門知識の水準を下げます。結果として、専門家は正本を短時間で確認でき、専門外の参加者は背景から追える構成を目指します。
