# EKI Product Value

## 役割

この文書は、EKI Fabricが長期に守る価値の正本です。具体的な方式、プロトコル、製品選定、実装構造は定義しません。

## 中心価値

EKI Fabricは、**本来は失われる一時的・分散的な計算余力を、安全に利用可能な共通資源へ変え、人・組織・社会の認知能力へ還流させること**を中心価値とします。

クラウド利用量の最小化は重要な効果ですが、それ自体を最終目的とはしません。高価な可用性・耐久性・専用設備をすべての計算へ一律に求めず、失敗可能な計算を失敗可能な資源へ適切に割り当てることで、同じ資本からより多くの認知活動を可能にします。

## 守る価値

### Human first

業務端末や個人端末は、まず人が使うための資産です。計算資源の提供は、人間の操作、端末寿命、電力、熱、ネットワーク品質を侵害しない範囲に限定します。提供者は適切な範囲でいつでも資源提供を縮小・停止できなければなりません。

### Distrust without exclusion

参加主体を信頼しなければ協力できない基盤にはしません。ClientもWorkerも悪意を持ち得ることを前提にしつつ、善意のアドホックな参加者が過度な事前審査なしに価値を提供できる余地を残します。

### Heterogeneity as normal

社員PC、GPUワークステーション、専用ノード、ブラウザ、クラウド、他組織の資源、未知の将来デバイスを例外扱いしません。異質性と一時性を通常状態とみなします。

### Compute where it is economically sensible

計算能力だけでなく、データ局所性、転送量、利用者影響、電力、信頼性要求、セキュリティ、外部利用料金を含めて、全体として意味のある場所で処理します。分散すること自体を目的化しません。

### Cognitive abundance

余剰計算資源は、単なるコスト削減ではなく、通常は費用対効果の理由で省略される観察、比較、分類、検証、探索、再索引、候補生成などへ投入できるものと考えます。計算余力が増えるほど、組織が行える認知活動の厚みを増せる構造を志向します。

ここでいう認知処理は、大型LLMだけを意味しません。小型分類器、embedding、異常検知、疎な連想・再帰型ネットワーク、ローカル軽量LLM、より大きな推論モデルなど、計算特性の異なる処理をCPU / GPU / NPUその他の資源へ適切に配置できることを含みます。

ただし、EKIが認知処理の意味や正しさを所有するわけではありません。どの認知を行うべきか、結果をObservation・Hypothesis・Decision等のどの意味として扱うかは、利用側Product / workload contractの責務です。

### Interoperability before ecosystem lock-in

SEI Cognition、TEI、SUI Sensemakingは重要な初期利用者ですが、EKIをそれらの内部実装にはしません。未知のOSS、他社製品、Webアプリ、異なる言語・Runtimeから同じ基盤を利用できることを重視します。

### Verifiable contribution

資源提供は、必要に応じて検証可能なContributionとして記録できるようにします。ただしEKI自身は通貨、市場価格、現金化、交換レートを提供しません。Contributionをどのような社会的・経済的価値へ写像するかは、各主体や外部制度に委ねます。

### Public-interest extensibility

企業、クラウド事業者、個人等が余剰計算資源を公益性の高い活動へ提供できる構造を妨げません。提供者と受益者が互いを全面的に信頼しなくても協力できることを価値とします。

## 隣接Productとの境界

SUI Sensemaking、SEI Cognition、TEIから認知・検証・生成・Simulation等のworkloadを受け取る場合でも、EKIが所有するのは**計算を安全かつ合理的に実行可能な資源へ配置し、その実行を検証可能に扱う境界**です。

```text
SUI / SEI / TEI
  = application / cognition / formal meaning

EKI
  = compute placement / execution fabric
```

そのため、EKI CoreへSUIのHypothesis、SEIのDecision、TEIのCanonical business meaning等を複製しません。EKIが必要とする場合は、それらをapplication semanticsとして理解するのではなく、resource requirement、data locality、trust / isolation requirement、deadline、preemption可否、result verification等のworkload contractへ投影して受け取ります。

逆に、EKIがあるWorkerを選んだこと、計算が成功したこと、複数Workerの結果が一致したことだけで、利用側の意味上の判断が正しかったことにはしません。

この分離により、認知系OSSが進化してworkloadの種類が変わっても、EKIは汎用distributed compute fabricとして独立して利用できます。

## EKIが価値としないもの

- 全計算を分散化すること
- 全資源を最大使用率まで使い切ること
- Worker数や総FLOPSそのものを成功指標にすること
- 高可用クラスタの置換を目的化すること
- 匿名参加者を善意と仮定すること
- 独自経済圏や投機市場を形成すること
- 一つのクラウド、Runtime、言語、GPUベンダーへ利用者を固定すること
- 認知・判断・sensemaking・業務モデルの意味をEKI Coreの正本として所有すること
