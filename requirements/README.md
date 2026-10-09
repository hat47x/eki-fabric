# EKI Fabric 要求文書の読み方

このディレクトリには、EKI Fabricが満たすべき要求、安全上の前提、実行端末（Worker）に求める保証の考え方、全体構成の候補を比べるための品質特性を置いています。

ここにある文書は、アイデアを集めただけのものではありません。現在は最初の全体構成を設計基準として定めていますが、この文書に記す要求は、具体的な実装方式とは切り離して扱います。特に、次の点を引き続き確かめます。

- 何を守る必要があるか
- どのような失敗や攻撃に耐える必要があるか
- どの範囲まで保証できるか
- 成り立つ複数の案を、何で比べるか

## まず全体像を知りたい場合

要求文書に入る前に、[はじめてEKI Fabricを読む方へ](../docs/for-readers.md)を読むことをおすすめします。

業務端末の余剰資源をなぜ使うのか、ClientやWorkerをなぜ無条件には信用しないのか、悪意を前提にしながら善意のアドホック参加者をなぜ受け入れられるのか、といった背景から説明しています。

## 文書ごとの役割

### 製品要求

[PRODUCT_REQUIREMENTS.md](PRODUCT_REQUIREMENTS.md)は、EKIが満たすべき性質の正本です。

現在は、たとえば次の内容を扱っています。

- 特定のOSSや言語に依存しないこと
- 業務端末では、利用者を最優先すること
- ネットワークの負荷とデータの局所性を考慮すること
- 悪意のあるClientやWorkerを前提にすること
- Browserや一時的なWorkerの離脱を、通常の状態として扱うこと
- アドホック参加者や複数の組織を受け入れられること
- 組織間連携（Federation）で、識別情報（Identity）の名前空間を取り違えないこと
- 成果物（Artifact）の取り込みと展開の処理にも、資源の上限を設けること
- 制御系（Control Plane）の内部でも、不要な全権限を一か所に集めないこと
- 多数の計算資源を回収する処理（reclaim）が、大量の再試行（retry）を引き起こさないようにすること
- 電力、発熱、機器の安全を、Resource Policyに含められること
- Compute Grantや資源提供実績を扱えること
- 副作用を持つTaskを、無条件には再実行しないこと
- 実行ごとに結果が変わり得るAI処理について、確認できない正しさを誇張しないこと

製品要求に書くのは「何を満たすか」です。NATS、SPIFFE、WASMのように「何を使って実現するか」は、原則として、この段階では決めません。

### 脅威の想定

[THREAT_MODEL.md](THREAT_MODEL.md)は、どのような攻撃者を想定し、どの性質を安全上の不変条件として守るかの正本です。

匿名の攻撃者だけでなく、次のような存在も想定します。

- 正規のCredentialを持つClient
- 改変された公式Worker
- 多数のIdentityを作るSybil operator
- 侵害されたValidatorやVerifier
- 悪意のあるArtifact
- 侵害されたControl Planeのcomponent
- Federationの設定のすり替え

Threat Modelを読むときは、「あらゆる攻撃を完全に防げるか」を問うのではなく、次の点を確認してください。

1. どこまでを、EKIの保護の対象にしているか
2. 何を、明示的な信頼基盤として置いているか
3. どの信頼基盤まで壊れた場合は、保証の対象外なのか
4. 単一の主体、Credential、componentが、許された境界を越えて被害を広げられないか

### 実行端末に求める保証

[ASSURANCE_MODEL.md](ASSURANCE_MODEL.md)は、Workerを単純に`trusted / untrusted`へ二分しないための、要求の解釈をまとめた文書です。

「企業が管理する端末だから安全」「匿名だから危険」と単純にすると、何が確認できていて、何が確認できていないのかを見誤ります。

現在は、少なくとも次の観点を区別しています。

- Identity
- Execution Environment
- Confidentiality
- Result Integrity
- Independence / Failure Domain
- Operational Reliability History
- Resource Measurement
- Verification Authority
- Evidence Freshness / Revocation

大切なのは、ある軸が強いことを、別の軸の保証として使わないことです。

### 受け入れ場面

[ACCEPTANCE_SCENARIOS.md](ACCEPTANCE_SCENARIOS.md)は、要求を具体的な利用、攻撃、離脱の場面に当てはめて、矛盾や不足を探すための文書です。

要求を増やすための単なる確認項目ではありません。実際の場面を考えたとき、現在の要求だけで必要な判断ができるかを確かめます。

現在は、次のような場面を扱っています。社内のEndpoint pool、悪意のあるClientやWorker、Sybil、Browser Worker、Federation、Compute Grant、非決定的なAI、Artifactの展開、Control Planeの部分的な侵害、大規模なreclaim。

### 品質特性

[QUALITY_ATTRIBUTES.md](QUALITY_ATTRIBUTES.md)は、Requirementを満たす複数のArchitecture候補を比べるための、品質特性をまとめた文書です。

セキュリティや処理能力のどちらかだけを最大にした案は選びません。Endpointの利用者への影響、ネットワークの効率、回復のしやすさ、相互運用性、運用のしやすさ、電力と機器の安全などを並べて、両立しにくい条件の関係を確かめます。

ここに挙げる測定項目は、本番運用のサービス水準目標（SLO）ではありません。Architectureの候補を比べられる状態にするための、基準です。

### 要求の不足に関する点検

[REQUIREMENT_GAP_REVIEW.md](REQUIREMENT_GAP_REVIEW.md)は、要求全体を横断して不足を洗い出した、レビューの記録です。

現在は第三巡まで進み、Federationの名前空間、Artifactのparserと展開、Control Planeの権限分離、大規模なResourceのreclaim、電力と発熱の境界まで確認しています。

ここに書かれた提案そのものは、正本ではありません。必要と判断した内容がProduct RequirementsやThreat Modelに反映されて、はじめて正式な要求になります。

## 要求のレビューで確認したいこと

要求をレビューするときは、専門用語の細部よりも先に、次の点を確認してください。

### 守りたい価値とつながっているか

要件が厳しいほど安全になるとは限りません。

たとえば、すべてのWorkerに企業管理端末と同等のIdentityを求めると、アドホック参加という価値を大きく損ないます。逆に、参加しやすさだけを優先すると、機密データを安全に扱えません。

要求は、[Product Value](../product/value.md)と[Product Principles](../product/principles.md)の両方から説明できなければなりません。

### 実装方式を先に決めていないか

「mTLSを使う」「NATSを使う」「WASMを使う」といった記述は、それだけではProduct Requirementではありません。

本当に必要なのが「相手を認証し、改変を検出できること」なら、まずその性質をRequirementとして書きます。方式は、Architectureの比較で決めます。

### 保証を言いすぎていないか

次のような例があります。

- Remote Attestationに成功しても、Applicationの意味の上で結果が正しいとは限りません。
- 匿名のWorkerを3台使って多数決をしても、3台すべてが同じ攻撃者に支配されているかもしれません。
- Resultが正しくても、Workerが申告したGPU時間まで正しいとは限りません。
- 署名済みのArtifactであっても、脆弱なparserにとって安全な入力とは限りません。

「何を確認できたか」と、「そこから何を保証できるか」の間に、飛躍がないかを確かめます。

### 失敗したときの振る舞いが分かるか

EKIでは、Workerの離脱やTaskの失敗を、完全にはなくせません。

そのため、「失敗しないこと」だけでなく、失敗したときの扱いがRequirementから読み取れる必要があります。たとえば次のような境界です。

- 再実行できるのか
- 安全な側に止まるのか
- 実行されたかどうか分からない状態として、上位へ返すのか
- 大量に離脱したとき、retryを抑えるのか

### 信頼基盤が隠れていないか

「検証済み」「Attestation済み」という言葉だけで終わらせず、誰が、どのPolicyで、何を確認したのかを説明できることが大切です。

信頼基盤をなくすことではなく、**その保証がどこに依存しているのかを明らかにすること**を重視します。

## 分からない用語がある場合

[用語集](../docs/glossary.md)に、EKIで頻繁に使う用語をまとめています。

用語集は理解を助けるための文書であり、Product RequirementやThreat Modelの意味を上書きするものではありません。用語集と正本が食い違っている場合は、正本を基準にします。
