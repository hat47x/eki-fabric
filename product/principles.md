# EKI Product Principles

## 役割

この文書は、RequirementやArchitecture判断を評価するときの長期的な原則を定義します。

## 原則

### 1. Trust no participant by role name

Client、Worker、Provider、Beneficiaryという役割名は信頼性を意味しません。認証、所属、過去の実績があっても、security propertyを主体の善意へ依存させません。

### 2. Participation may be easy; authority must remain narrow

アドホック参加を受け入れますが、参加しただけで機密データ、任意コード実行、他主体の情報、内部トポロジ、無制限の計算予算へのauthorityは得られません。

### 3. Claims are not facts until verified

Workerが返す実行結果、資源量、実行時間、キャッシュ状態、削除完了等はClaimです。安全性や価値配分に使う情報ほど、独立した検証または明示的な信頼根拠を必要とします。

### 4. Human use outranks background compute

業務端末では、ユーザー復帰、ネットワーク逼迫、温度、電源、ローカルポリシー、明示的opt-outを中央Schedulerより優先します。

### 5. Move computation before moving large data

分散化による転送負荷を避けます。既存データやモデルの局所性を配置判断に含め、計算能力の高さだけでWorkerを選びません。

### 6. Reliability belongs to the workload contract

すべてのTaskへ同じ可用性を要求しません。retryable、preemptible、checkpointable、deadline、結果検証方式等をTaskの性質として扱い、必要以上の安全コストを買いません。

### 7. Core knows compute semantics, not application semantics

EKI CoreはTask、Resource、Artifact、Policy、Result、Contribution等を扱います。SUI Sensemakingの関係・仮説・統合、SEI CognitionのObservation・Decision、TEIのCanonical business meaning、個別LLMや認知手法の意味論は利用側の責務とします。

利用側の意味をEKIへそのまま複製するのではなく、必要な情報だけをresource requirement、data locality、trust / isolation requirement、deadline、preemption、result verification等のcompute semanticsへ投影します。

### 8. Open protocol, replaceable implementation

基準実装がGoであっても、外部契約をGo APIへ閉じません。Queue、Identity、Artifact Store、Sandbox等の候補製品は交換可能性を保ち、Requirementより先にCoreへ焼き込みません。

### 9. Security properties must survive modified clients and workers

正規クライアントやWorker Agentが改変されても、他主体の権限拡張、無制限資源消費、機密情報取得、未検証結果の正本化ができない構造を要求します。

### 10. Security and efficiency are separate dimensions

Reputation、過去の成功率、自己申告性能は効率改善に利用できても、単独でsecurity root of trustにはしません。

### 11. Contribution is measurable; value is external

EKIは計算資源提供を検証可能な記録へできますが、その記録が寄付、相互利用Credit、社内配賦、評価、無償貢献等のどれになるかは外部Policyに委ねます。

### 12. Prefer boring enterprise deployment paths

企業利用の標準形では、一般的な社内システムとして説明しやすい接続、認証、監査、outbound通信、権限制御を優先します。P2Pや特殊なネットワーク構成は、明確な利点と安全性が示せる場合だけ選択肢とします。
