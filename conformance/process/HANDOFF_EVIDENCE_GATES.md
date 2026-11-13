# EKIの実行・配送から外部Harnessへ渡す証拠の境界

2026-11-13｜**限定Conformance計画。仕様採用・実装・テストPASSではない。**

## 適用範囲

EKIをAI Harnessから操作する場合、ユーザーが選択した対象、製品側で再解決した対象、許可された情報、Transportで実際に送った内容、Consumerで読み込まれた内容、Tool呼出し、外部Providerの効果を、一つの「渡した／完了した」にまとめない。

EKIが正本として扱うのは**Task／Attempt／Lease／実行資格／配送とResultの受理**に必要な範囲に限る。選択対象や意味、Product Projection、Toolの業務操作、Provider Commitの正本は、それぞれの製品・Consumer・Providerが持つ。HarnessやSkillの名称・接続状態は、現在の実行Grantではない。共通Host、第二Registry、汎用Activity状態機械を新設しない。

既存の[同一TaskのEvidence計画](EXECUTION_EVIDENCE_PLAN.md)のES-06（同じTaskでの一貫した実行）とES-07（実配送／受領）を補足するものであり、その代わりにはならない。

## 証拠を切り離す境界

| 境界 | 実行前に確認するもの | 成立を主張できる範囲 | 判断を委譲する先 |
| --- | --- | --- | --- |
| 対象選択 | 選択ID、固定した範囲、変更時の再解決方針 | ユーザーが選択した範囲 | GUI・Product。誤選択の訂正は利用者 |
| Product再解決 | 対象Identityと現在Revision、曖昧性・削除・権限、解決した要素数 | 現時点で一意に解決された対象 | Product正本 |
| 開示Projection | Data Class、送出先、実際に含めるフィールド・添付、Grantの現在性 | 許可された出力範囲とbyte列 | Productと認可主体 |
| EKI実行資格 | 同じTask/Attempt、Worker、Lease/FenceEpoch、Policy・Grant | 今この実行経路が許可されるか | EKI既存Authority |
| Transport実配送 | 選ばれたProfile、対象Run、送受信の観測点、digest、欠落・再接続 | 観測した範囲の通信 | EKI Data/Delivery Adapter |
| Consumer受領 | 実際のConsumer、受信byte範囲、取込・Load成否、時刻、部分取得の印 | Consumerが受け取った／読み込んだ範囲 | Consumer側 |
| Harness／Tool | 対象の再確認、Tool名称と実際の呼出し、応答の相関・完全性、操作ID | 呼出し／応答を観測した事実 | Harness／Tool実行主体 |
| 業務Effect／意味 | ProviderのOperation IDとIntent digest、Commit Receipt、必要な独立Read、意味受理 | 外部効果・業務意味について権威ある確認済み範囲 | Provider／Product／Owner |

**固定したソースbyte列や同じTarget IDへの復帰（A→B→A）は、失効したGrant・Policyや過去の意味受理を復活させない。** 送信成功だけではConsumerのLoadを証明せず、HarnessのTool応答やEKIのResultClaimだけでは外部Commitを証明しない。

## Conformanceで使う相関・証拠票

新しい本番Wire形式ではなく、テストが別々の正本から照合する**観測票**とする。最低限、実行commit、実行時刻、実行環境、Task ID／Attempt ID／Worker／Lease ID・FenceEpoch、対象のIdentity／Revision、Projectionのdigest・byte長、Data Class・宛先・Grant revision、Transport Profile、実送受信の範囲、Consumer実受領の範囲、HarnessのTool Operation ID、外部Provider Receiptの有無と出典を記録する。取得不能な欄はそのまま「未取得」とし、架空の値で埋めない。

観測票の**項目が揃うこと**と各フィールドの**真正性**は異なる。署名済みEnvelope、現在のGrant、Consumer実ログ、ProviderのReceiptなど、確認した主体と証拠の所在を付ける。外部へ渡す内容に秘密情報を含めず、大きな実データはNAS/S3等に保管してURI・digest・アクセス可能性を参照することを許容する。ただしURIとハッシュだけではConsumerへの実配送を証明できない。

## 実行前に固定する反証ケース

| ID | 独立Expected | 主担当 |
| --- | --- | --- |
| HE-01 | Pinされた対象が変更・削除・複数候補化したら、再解決なしで旧対象を実行しない | Product／Harness |
| HE-02 | 選択後に開示Grantが失効・送出先が変更したら、旧Projectionを外部へ送らない | Product認可／EKI配送 |
| HE-03 | 宣言済みSourceの一部しかDiscovered／Loadedでないとき、「存在しない」「全部受領した」と結論しない | Product／Consumer |
| HE-04 | 送信側Ackだけで受信側の観測がない場合、配送とLoadedは未確認 | #753 |
| HE-05 | 受信byteは揃ってもConsumerのLoadが失敗した場合、Consumedは未確認 | #753／Consumer |
| HE-06 | Toolの応答が到着してもProviderのOperation Receiptが無い場合、業務EffectはUNKNOWN。非冪等操作を無断再試行しない | Harness／Provider |
| HE-07 | Harness再起動・応答再送後に旧Operation IDだけが残る場合、現在の対象・Grantを再確認しないまま操作しない | Harness／Provider |
| HE-08 | Task IDが同じでも別Attempt／Lease／FenceEpochの証拠をつぎ合わせず、Resultを誤受理しない | #242 |
| HE-09 | 本物のread-only Consumer受領が確認できた場合でも、業務意味のOwner受理やWriteの実行を自動的に主張しない | #753／Consumer |
| HE-10 | ローカル基準より分散が遅いか計測値が無ければ、Transport対応だけを理由に分散を選ばない | #58 |

これらは**テストの期待結果候補**であり、実行・採用したケース数を表さない。HE-01〜03・06〜07・09の製品固有部分はEKIの内部契約へ複製せず、対象製品が実ログで確認する。EKIが検査できるのは、供給された情報に対する自らの認可・通信・Taskの境界である。

## 実施と受入の順序

1. **既存の実行系を優先**：[#242](https://github.com/hat47x/eki-fabric/issues/242)の同一Task縦断試験に、相関ID・署名・期限・実Claimと、実行時に存在する権限の観測を追加する。別Taskの成功をつなぎ合わせたE2E PASSは禁止する。
2. **実Data Adapterがある場合だけ配送を測る**：[#753](https://github.com/hat47x/eki-fabric/issues/753)ではLocal IPC／Connectで同じ固定Fixtureを実配送し、送信／受信／Consumer Loadのどこまで確認したかを別票へ記録。データ配送Adapterが無い場合は`NOT SUPPORTED`／`NOT RUN`とする。
3. **回収の範囲を限定**：[#745](https://github.com/hat47x/eki-fabric/issues/745)は完了済みで、保存AdmissionからM0のEffectNone宣言を確認する実装がmainにある。これは外部Providerの実EffectやRecovery Permitではない。期限切れ後のTask状態と再投入は#242で追う。
4. **新プロトコルの採否は再提案時に**：[#754](https://github.com/hat47x/eki-fabric/issues/754)はADR-0041により完了済み。M0/MVPでWebRTC・WebTransport・WebSocketを新たに採用しない。#753のD1測定結果により再評価するなら**新ADR**とし、過去の閉じたIssueを未完に戻さない。
5. **外部Harnessの実受領は利用側で検証**：Pin→許可Projection→送出→Consumer実Load→Tool呼出し／応答を、それぞれの正本と照合する。読み取り専用Handoffの成立と、Tool実行・Write/Effectの実証は別の受入とする。

実行環境が異なる過去のGoログを、現行mainや未実装AdapterのPASSへ読み替えない。現行mainのGo 1.27試験、Harness実Tool、Consumer実受領、Provider実Effectの証拠が揃うまでは該当項目を`NOT RUN`に保つ。M0/MVPの優先順位は変更しない。
