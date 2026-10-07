# EKI Product Principles

## 役割

この文書は、RequirementやArchitectureの判断を評価するときに使う、長期的な原則を定めます。

## 原則

### 1. 役割名だけで主体を信頼しない（Trust no participant by role name）

Client、Worker、Provider、Beneficiaryといった役割名は、信頼できることを意味しません。認証されていても、所属が確かでも、過去の実績があっても、セキュリティ上の性質を主体の善意に頼って成り立たせません。

### 2. 参加は簡単でも、権限は狭くとどめる（Participation may be easy; authority must remain narrow）

アドホックな参加は受け入れます。ただし、参加しただけでは、機密データ、任意のコード実行、他の主体の情報、内部のトポロジ、無制限の計算予算に対する権限は得られません。

### 3. 検証されるまで、申告は事実ではない（Claims are not facts until verified）

Workerが返す実行結果、資源量、実行時間、キャッシュの状態、削除の完了などは、すべて申告（Claim）です。安全性や価値の配分に使う情報ほど、独立した検証か、明示した信頼の根拠が必要です。

### 4. 人の利用は、バックグラウンドの計算に優先する（Human use outranks background compute）

業務端末では、利用者の復帰、ネットワークの逼迫、温度、電源、ローカルのポリシー、明示的なopt-outを、中央のSchedulerの判断より優先します。

### 5. 大きなデータを動かす前に、計算を動かす（Move computation before moving large data）

分散によって転送の負荷が増えるのを避けます。すでにあるデータやモデルの局所性を配置の判断に含め、計算能力が高いというだけでWorkerを選びません。

### 6. 信頼性はワークロード契約の一部とする（Reliability belongs to the workload contract）

すべてのTaskに同じ可用性は求めません。リトライできるか、中断されてよいか、チェックポイントを取れるか、期限、結果の検証方式などは、Taskの性質として扱います。必要以上の安全のためにコストを払いません。

### 7. 中核が知るのは計算の意味であり、アプリケーションの意味ではない（Core knows compute semantics, not application semantics）

EKI Coreが扱うのは、Task、Resource、Artifact、Policy、Result、Contributionなどです。SUI Sensemakingの関係・仮説・統合、SEI CognitionのObservationとDecision、TEIのCanonical business meaning、個々のLLMや認知手法の意味は、利用する側の責務とします。

利用する側の意味をそのままEKIへ複製することはしません。必要な情報だけを、資源要件、データの局所性、信頼と分離の要件、期限、中断の可否、結果の検証方式といった、計算の意味に置き換えて受け取ります。

### 8. プロトコルは開き、実装は交換できるようにする（Open protocol, replaceable implementation）

基準となる実装がGoであっても、外部との契約をGoのAPIに閉じ込めません。Queue、Identity、Artifact Store、Sandboxなどの候補製品は交換できる状態を保ち、Requirementより先にCoreへ組み込むことはしません。

### 9. 改変されたClientやWorkerがいても、セキュリティ上の性質を保つ（Security properties must survive modified clients and workers）

正規のClientやWorker Agentが改変されても、次のことができない構造を求めます。他の主体の権限を広げること、資源を無制限に使うこと、機密情報を取得すること、検証されていない結果を正式な記録にすること。

### 10. セキュリティと効率は別の軸として扱う（Security and efficiency are separate dimensions）

評判、過去の成功率、自己申告の性能は、効率を上げるために使えます。ただし、それだけでセキュリティの信頼の根拠（root of trust）にはしません。

### 11. 貢献は測れるが、その価値は外部が決める（Contribution is measurable; value is external）

EKIは、計算資源を提供した事実を、検証できる記録として残せます。その記録を寄付、相互利用のクレジット、社内の配賦、評価、無償の貢献のどれとして扱うかは、外部のPolicyが決めます。

### 12. 企業で普通に使える導入経路を選ぶ（Prefer boring enterprise deployment paths）

企業で標準的に使う構成では、一般的な社内システムとして説明しやすい接続、認証、監査、外向きの通信、権限の制御を優先します。P2Pや特殊なネットワーク構成を選ぶのは、明確な利点があり、安全性を示せる場合だけです。
