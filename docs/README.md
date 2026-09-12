# EKI Fabric ドキュメント案内

このディレクトリは、EKI Fabricを初めて読む方や、特定分野の専門家ではない開発者が、背景から順に理解するための説明文書を置く場所です。

EKI Fabricでは、分散システム、セキュリティ、暗号技術、ボランティアコンピューティング、企業ネットワークなど、複数分野の知識が交差します。すべての参加者に最初からそれらの専門知識を求めると、設計上の重要な判断が一部の専門家だけに閉じてしまいます。

そのため、正本となるRequirementやThreat Modelの厳密さは保ちながら、説明文書側で背景、具体例、用語、判断理由を補い、一般的なソフトウェア開発経験があれば議論へ入れる状態を目指します。

## 最初に読む文書

初めてEKI Fabricを読む場合は、次の順序を推奨します。

1. [はじめてEKI Fabricを読む方へ](for-readers.md)
2. [用語集](glossary.md)
3. [Product Value](../product/value.md)
4. [Product Principles](../product/principles.md)
5. [Product Requirements](../requirements/PRODUCT_REQUIREMENTS.md)
6. [Threat Model](../requirements/THREAT_MODEL.md)
7. [Worker Assurance Model](../requirements/ASSURANCE_MODEL.md)
8. [Acceptance Scenarios](../requirements/ACCEPTANCE_SCENARIOS.md)

最初からすべてを読む必要はありません。全体像を知りたい場合は `for-readers.md` まで、要件レビューへ参加する場合は Product Requirements と Threat Model まで、悪意あるWorkerやアドホック参加者の保証を検討する場合は Assurance Model まで進む、という読み方で構いません。

## 正本と説明文書の関係

`docs/`は理解を助けるための場所であり、Product RequirementやSecurity Invariantの第二正本ではありません。

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
```

説明を分かりやすくするために言い換えることはありますが、Requirementの意味を独自に強めたり弱めたりしません。説明と正本が食い違った場合は、正本側を基準とし、説明文書を修正します。

## 文書を書くときの基本姿勢

EKI Fabricでは、専門用語を避けること自体を目的にはしません。必要な用語は使います。ただし、用語を知っていることを説明の前提にしません。

新しい概念を導入するときは、できるだけ次の順序で書きます。

1. 何が困るのか
2. なぜ単純な方法では足りないのか
3. EKIでは何を要求するのか
4. 具体例ではどう見えるか
5. どこまでは保証せず、何が未決なのか
6. 厳密な定義やRequirement IDはどこにあるか

たとえば「Sybil耐性」という言葉だけを書くのではなく、「一人の攻撃者が多数のWorkerを作り、見かけ上の多数決を支配できるため、Worker数だけでは独立性を証明できない」と先に説明します。その後で必要に応じてSybilという名称を添えます。

## 文書品質の目標

目標とする読者像は、「分散システムやRemote Attestationの専門家ではないが、一般的なアプリケーション開発、インフラ構築、業務システム開発のいずれかを経験している人」です。

専門家向けの厳密さを落とすのではなく、説明を厚くすることで参画に必要な技術レベルを一段階下げます。結果として、専門家は正本を短時間で確認でき、専門外の参加者は背景から追える構成を目指します。
