# EKI Fabric 要求文書の読み方

このディレクトリには、EKI Fabricが満たすべき要求、安全上の前提、Workerに求める保証の考え方を置きます。

ここにある文書は単なるアイデア集ではありません。一方で、まだArchitectureを確定する段階でもありません。現在は、「何を守る必要があるか」「どのような失敗や攻撃に耐える必要があるか」「どの範囲まで保証できるか」を、具体的な実装方式から切り離して詰めている段階です。

## まず全体像を知りたい場合

要求文書へ入る前に、[はじめてEKI Fabricを読む方へ](../docs/for-readers.md) を読むことを推奨します。

業務端末の余剰資源をなぜ使うのか、ClientやWorkerをなぜ無条件には信用しないのか、悪意を前提にしながら善意のアドホック参加者をなぜ受け入れられるのか、といった背景から説明しています。

## 文書ごとの役割

### Product Requirements

[PRODUCT_REQUIREMENTS.md](PRODUCT_REQUIREMENTS.md) は、EKIが満たすべき性質の正本です。

現在は、たとえば次を扱っています。

- 特定OSSや特定言語へ依存しないこと
- 業務端末では利用者を最優先すること
- ネットワーク負荷とデータ局所性を考慮すること
- 悪意のあるClientやWorkerを前提にすること
- Browserや一時Workerの離脱を通常状態として扱うこと
- アドホック参加者や複数組織を受け入れられること
- Compute Grantや資源提供実績を扱えること
- 副作用を持つTaskを無条件に再実行しないこと
- 非決定的AI Taskについて、確認できない正しさを誇張しないこと

Requirementには「何を満たすか」を書きます。NATS、SPIFFE、WASMなど「何を使って実現するか」は、原則としてこの段階では固定しません。

### Threat Model

[THREAT_MODEL.md](THREAT_MODEL.md) は、何を攻撃者として想定し、何を安全上の不変条件として守るかの正本です。

匿名の攻撃者だけでなく、正規Credentialを持ったClient、改変された公式Worker、多数Identityを作るSybil operator、侵害されたValidator / Verifier、意図しないBrowser resource useなども想定します。

Threat Modelを読むときは、「あらゆる攻撃を完全に防げるか」ではなく、次を確認してください。

1. どこまでをEKIの保護対象にしているか
2. 何を明示的な信頼基盤として置いているか
3. どの信頼基盤まで壊れた場合は保証外なのか
4. 単一の主体やCredentialが、許可された境界を越えて被害を広げられないか

### Worker Assurance Model

[ASSURANCE_MODEL.md](ASSURANCE_MODEL.md) は、Workerを単純な `trusted / untrusted` で分類しないための要求解釈です。

「企業管理端末だから安全」「匿名だから危険」と単純化すると、何が確認できていて、何が確認できていないかを見誤ります。

現在は少なくとも次の観点を分けています。

- Identity
- Execution Environment
- Confidentiality
- Result Integrity
- Independence / Failure Domain
- Operational Reliability History
- Resource Measurement
- Verification Authority
- Evidence Freshness / Revocation

一つの軸が強いことを、別の軸の保証として使わないことが重要です。

### Acceptance Scenarios

[ACCEPTANCE_SCENARIOS.md](ACCEPTANCE_SCENARIOS.md) は、要求を具体的な利用・攻撃・離脱場面へ当てはめ、矛盾や不足を探す文書です。

Requirementを増やすためのチェックリストではありません。実際の場面を考えたときに、現在の要求だけで必要な判断ができるかを確認します。

### Requirement Gap Review

[REQUIREMENT_GAP_REVIEW.md](REQUIREMENT_GAP_REVIEW.md) は、初版の要求群を横断して不足を洗い出したレビュー記録です。

ここに書かれた提案自体は正本ではありません。必要と判断した内容はProduct RequirementsやThreat Modelへ反映して初めて正式な要求になります。

「なぜこのRequirementが追加されたのか」を追いたい場合の補助資料として利用します。

## 要求レビューで確認したいこと

要求をレビューするときは、専門用語の細部より先に、次を確認してください。

### 守りたい価値とつながっているか

要件が厳しいほど安全とは限りません。

たとえば、すべてのWorkerに企業管理端末と同等のIdentityを要求すれば、アドホック参加という価値を大きく損ないます。逆に、参加しやすさだけを優先すれば、機密データを安全に扱えません。

要求は [Product Value](../product/value.md) と [Product Principles](../product/principles.md) の両方から説明できる必要があります。

### 実装方式を先に決めていないか

「mTLSを使う」「NATSを使う」「WASMを使う」という記述は、それ自体ではProduct Requirementではありません。

本当に必要なのが「相手を認証し、改変を検出できること」であれば、まずその性質をRequirementとして書き、方式はArchitecture比較で決めます。

### 保証を言いすぎていないか

Remote Attestationが成功しても、Applicationの意味上の結果まで必ず正しいとは限りません。

匿名Workerを3台使って多数決をしても、3台すべてが同じ攻撃者に支配されている可能性があります。

Resultが正しくても、Workerが申告したGPU時間まで正しいとは限りません。

「何を確認できたか」と「そこから何を保証できるか」の間に飛躍がないことを確認します。

### 失敗時の振る舞いが分かるか

EKIでは、Workerの離脱やTask失敗を完全にはなくしません。

そのため、「失敗しないこと」だけではなく、失敗したときに再実行できるのか、安全側に止まるのか、実行済みか不明な状態として上位へ返すのか、といった境界がRequirementから読み取れる必要があります。

### 信頼基盤が隠れていないか

「検証済み」「Attestation済み」という言葉だけで終わらず、誰が、どのPolicyで、何を確認したのかを説明できることが重要です。

信頼基盤をなくすことではなく、**どこへ依存している保証なのかを明らかにすること**を重視します。

## 分からない用語がある場合

[用語集](../docs/glossary.md) に、EKIで頻繁に使う用語をまとめています。

用語集は理解を補助する文書であり、Product RequirementやThreat Modelの意味を上書きするものではありません。用語集と正本に矛盾がある場合は、正本を基準にします。
