# データ様式・配送条件・通信方式の最小比較Spike（#753）

## なぜ行うか

カメラ等の観測は古くなると捨ててもよい一方、Leaseの失効や結果の確定は、通信が速いことだけでは代替できない。別の通信方式を採っても、同じ入力に対する「古い」「新しい世代」「失効した出力」という意味が変わらないことを先に確認する。

このSpikeは、[RQ-064](../../requirements/PRODUCT_REQUIREMENTS.md)と[QA-13](../../requirements/QUALITY_ATTRIBUTES.md)、[PAS-CAND-31〜37](../../requirements/ACCEPTANCE_SCENARIOS.md)の検証に使う。採否は[#754](https://github.com/hat47x/eki-fabric/issues/754)で、測定後に判断する。

**現状は契約Fixtureを作成した段階で、実際のLocal IPC／Connectによるデータ配送の実装・動作・性能は確認していない。** `WorkerPlaneService.WatchCommands` はWorkerへの制御命令の片方向streamであり、汎用メディア／センサー配送の実装証拠とはならない。

## 固定した最小契約

[fixture.json](fixture.json)は、将来どのAdapterでも同じ入力・独立Expectedを使うための固定例である。Wire schema・製品API・正式な拡張SPIの採用は意味しない。

| 層 | この例で固定した内容 | 所有する境界 |
| --- | --- | --- |
| Data Contract | `sensor-1`の構造化観測。系列の識別はTrack＋世代＋番号 | データの業務・認知的な意味は利用側 |
| Delivery Contract | 同世代では古い番号を採用しない。部分出力はClaimであってAccepted Resultではない | 配送Adapterが観測し、EKIの受理権限と分離 |
| Authority | 単発TaskのLease epoch `e7`。失効後の旧epochのClaimは確定不可 | 既存Admission／Lease／Fencing／Result Acceptance |
| Transport Profile | Local IPCとConnectの「データ配送用の比較候補」を示すだけ | Adapter／配備。双方未実装・NOT RUN |

本例では、一つのTaskに関する6つの出来事を順に与える。旧系列`gA`の40番は新鮮な候補、39番は古い。通信が再接続して`gB`の1番に戻っても、それは`gA`の1番との重複でもLease更新でもない。最後に`e7`が失効した後のResult Claimは、接続が続いていても受理しない。**新鮮な候補の配送は、利用側が消費した証拠ではない。**

## 独立Expectedと試験の区別

`fixture.json`の`independent_expected`は、Transport実装の出力から逆算せず先に固定した合否の根拠である。新しいAdapterには、実測の出来事と対応づく`event_id`、`observed_disposition`、`authority_basis`、観測時刻・測定位置を出力させ、Expectedとの比較を可能にする。Adapterの内部変数や特定のネットワークAPIをExpectedにしない。

| Gate | 合否に必要なもの | 現状 |
| --- | --- | --- |
| D0-A | Fixtureの形式・系列世代・epoch・Expectedの組が成立する | 定義済み。別途静的テストで確認 |
| D0-B | Local IPC Adapterが同じ6出来事の観測結果を出す | NOT RUN |
| D0-C | Connect**データ配送**Adapterが同じ6出来事の観測結果を出す | NOT RUN |
| D1 | 同条件でp50/p95/p99、期限超過、古い応答の採用、Backpressure、転送・CPU負荷を測る | NOT RUN |
| D2 | D1結果を根拠に方式の採否をADRへまとめる | NOT RUN |

D0-AのPASSはD0-B/CやD1のPASSを意味しない。存在しないAdapter、未配線の起動経路、試験できなかったTransportを、`PASS`や「比較した結果、同じ」と表示しない。既存のActor認可・契約版・Data Class・予算・現在のLease確認を通過していないMockも、実行資格の検証には数えない。

## 測定前に必要なAdapterの最小責務

- `fixture.json`のData/Deliveryを解釈し、**実効Delivery条件**と対応していない必須条件を拒否する。利用側のRequestedと、Supported・Permitted・Selectedを混同しない。
- 送信、受信確認、利用側での消費、EKI側でのClaim記録、Accepted Resultをそれぞれ別に観測する。観測不能な段階は不明とする。
- 送信者と受信者が異なる場合、時刻同期が未確認なら一方向遅延の絶対値を主張しない。同一時計で計測可能な区間または往復時間を記録する。
- Lease失効・再接続・同じ系列番号の再出現に対応する。ただしTransportから新しい実行資格を発行しない。
- 有限のキュー、転送時間、Data Class、許可された送信先を維持し、未対応であれば拒否する。旧Workerに直接接続する能力を勝手に外へ公開しない。

**初回比較では新しいSession Registry／独自Host／AIゲートウェイを導入しない。** Workerの制御Connectを使い回して汎用Data Planeの存在を装わず、必要な最小の試験用Data Adapterの有無から調べる。

## 隣接製品との分担

- **VRクマ**：身体運動・危険時のローカル停止と、Godot側の再生可能な認知・運動テストは利用側の責務。EKIの配送適合PASSに含めない。#58のSuitabilityでローカル優先もあり得る。
- **TEI**：宣言したSourceと実際にLoaded/Consumedとなった範囲、ProviderのOperation Identity、Commit Receipt、独立Readは別証拠。EKIのAckを副作用の成功へ昇格させない（PAS-CAND-37）。
- **SEI／AIゲートウェイ**：推論の意味・受理主体・Data Classの方針は利用側／管理者の権限に属する。Transportの能力宣言だけで外部推論先への送信を許さない（#133）。

## 実施順序と結果の保存

1. Fixtureを固定した状態で、形式整合・Expectedの一意性を静的に検証する。
2. 実在するLocal IPCとConnectデータ配送Adapterの構成を記録する。片方が無ければ`NOT SUPPORTED`／`NOT RUN`として止め、架空の数値を作らない。
3. 同じFixtureと独立Expectedを両経路へ適用し、個々の観測・不明・拒否を保存する。外部副作用を伴うテストは別Fixtureとし、安易に再送しない。
4. 実測した日付で`RESULTS_YYYY-MM-DD.md`を作り、OS・CPU・Go/Adapter版・Git SHA・プロファイル・測定点・件数・生の出力を`evidence/YYYY-MM-DD/`へ記録する。
5. 条件付きの成否と未確認を[#753](https://github.com/hat47x/eki-fabric/issues/753)へ戻し、方式選定が必要な場合だけ[#754](https://github.com/hat47x/eki-fabric/issues/754)へ渡す。

M0/MVPの実装順は変更しない。