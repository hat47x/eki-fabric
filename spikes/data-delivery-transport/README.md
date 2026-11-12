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

## 2026-11-08 参照意味テストと負例（D0-Aの補強）

既存の形式検査`fixture_test.go`は、イベントとExpectedの対応を検査するが、イベントを**順に解釈したときの意味**を確認しない。そこで`reference_semantics_test.go`に、通信方式に依存しないテスト専用の小さな判定器を追加した。これはEKI本番の認可エンジンやTransport Adapterではない。

- `fixture.json`に**既に固定されているExpectedは変更しない**。参照判定器はExpectedを読まず、観測の系列世代ごとの最大番号と、失効したLease epochからイベントを判定する。最後にExpectedと比較する。
- E1〜E6が一つずつ対応することを検証する。観測が新鮮でも、利用側の消費が証明されたとは扱わない。再接続は新しい系列世代であって新しい権限ではない。失効後の旧epochのClaimは受理済みにならない。
- **改変した6負例**を追加する。古い観測を新しい番号にする／新しい系列を旧系列に混ぜる／Claimと失効の順を入れ替える／不明なepochを持つ／世代識別子が無い／未知のイベント種別。どれも固定Expectedに一致してはならない。
- 結果の「未確認」と「未受理」、通信ACKとProvider Commit、選択したTransportと現在のGrantを混同しない。参照判定器は`accepted`や`consumed`を成功として作り出さない。

**ローカルでの限定試験**：Go 1.23.2（linux/amd64）の隔離ディレクトリに、このGoテストとFixtureの同一の6イベント・Expectedを再現し、`GO111MODULE=off go test -count=1 -v`を実行。参照一致テスト1件と改変負例6件がPASSした。これはGitHubのリポジトリ全体を取得した試験でも、既存の`fixture_test.go`を含めた試験でもない。main上のGoバージョン・実経路での試験は引き続き未実施として記録する。

### ここから先で必要な実証

1. GitHubの同一commit上で`go test ./spikes/data-delivery-transport`を実行し、元の形式検査と参照テストの共存を確認する。
2. Local IPCとConnectの**実在するデータ配送Adapter**へ固定イベントを投入し、Adapter独自の観測ログをExpectedへ比較する。参照判定器の出力をAdapter実測の代わりに使わない。
3. 別途、実際のLease/Grantを発行・失効させて旧epochの結果受理を拒否できることを確認する。これは模擬的な`revoked`真偽値で置き換えられない。
4. [#58](https://github.com/hat47x/eki-fabric/issues/58)の分散適性、TEIのLoaded/Consumed/Provider Receipt、SEIのモデル・意味の判断、VRクマのローカル安全停止は**別の証拠**を必要とする。通信の参照テストPASSから横断的なE2E成功を推測しない。

共通データ基盤の独立製品化や、新たなRegistry／Host／Policy EngineをこのSpikeの依存条件にはしない。マルチモーダルなデータの保持・検索・共同編集は、利用側と既存Providerの責務として扱い、EKIは権限と配送の契約を検証する。

## 2026-11-08 観測Traceを独立Expectedに照合する入口

前段の`reference_semantics_test.go`は**シミュレーションされた意味**の検査であり、Adapterによる配送の証拠ではない。次の段階で結果を比較できるように、`trace_compare_test.go`に**観測記録の照合だけを担当するテスト入口**を追加した。参照意味判定器の結果を流用せず、`fixture.json`の独立Expectedに直接比較する。

Adapterが観測結果を取得できた場合にだけ、以下の例と同じ構造のJSONを、**実行したリポジトリの外部出力から**保存する。これは試験入力形式であり、EKIの本番Wire schemaでも権限Receiptでもない。

~~~json
{
  "case_id": "EKI-TX-01",
  "fixture_sha256": "<fixture.jsonの原byte列をSHA-256で計算した64桁の小文字16進数>",
  "transport_profile": "local-ipc-candidate",
  "run_id": "<当該実行を特定できるID>",
  "adapter_revision": "<動かしたAdapterのcommit/版>",
  "capture_method": "<実際に観測した方法>",
  "observations": [
    {"event_id": "E1", "observed_disposition": "fresh-candidate-not-consumption-proof"},
    {"event_id": "E2", "observed_disposition": "stale-rejected"},
    {"event_id": "E3", "observed_disposition": "new-stream-generation-not-new-authority"},
    {"event_id": "E4", "observed_disposition": "fresh-candidate-not-consumption-proof"},
    {"event_id": "E5", "observed_disposition": "lease-revoked"},
    {"event_id": "E6", "observed_disposition": "claim-not-accepted"}
  ]
}
~~~

**このJSONは構造例であり、観測された実測結果ではない。** 実行例：

~~~sh
EKI_TRANSPORT_TRACE_FILE=/path/to/observed-trace.json go test ./spikes/data-delivery-transport -run TestProvidedAdapterObservedTrace -count=1 -v
~~~

環境変数`EKI_TRANSPORT_TRACE_FILE`を指定しないと、実Adapter照合テストは明示的に**SKIP（NOT RUN）**になる。テスト全体がPASSになっても、SKIPした試験が通ったとは記録しない。`TestTraceComparatorRejectsInvalidEvidence`は、欠落・重複・誤判定・未知イベント・異なるFixture digest・未知Profile・来歴欠損・別Case IDの8ケースを拒否できるかの**合成入力による単体テスト**である。正しい形式・期待文字列を報告するだけの偽造Traceを見抜くことはできない。

実AdapterのPASSに進むには、記録に対応する**実際のプロセス、接続方式、入力イベント、捕捉方法、版、ログ、時刻基準**を別途照合する。さらに、EKIのLease/Grantの有効性、利用側によるConsumed、TEIのProvider Commit、SEIの認知判断、VRクマのローカル安全停止は独立の実証が必要である。`fixture_sha256`の一致は同じ入力Fixtureを参照したことしか示さず、現在の権限やSourceの意味受理を保証しない。

### 次の実装候補と限界

まずLocal IPCで**実Data Adapterが存在するか／実際にイベントを処理できるか**を調査し、見つからなければその事実を`NOT SUPPORTED`として記録する。既存のWorker制御用`WatchCommands`をData Planeへ自動的に拡張しない。Connect側も同じ手順とする。局所応答とSEIへの判断委譲の分担も利用側が決め、EKIが新しい判断Hostを持たない。

## 2026-11-08 Unixドメインソケットでの最初の物理配送確認

[試験用コード](unix_fixture_test.go)を追加し、固定Fixtureの6イベントをOSのUnixドメインソケットで送受信する試験を実施した。ローカル隔離環境（Linux／Go 1.23.2）で、実ソケットの配送1件と、欠落・重複・順序変更・内容改変を検出する4負例がPASSし、race付き5回の繰返しもPASSした。実行条件・生ログと一般化できる範囲は[RESULTS_2026-11-08.md](RESULTS_2026-11-08.md)を参照する。

**これはEKIの実Local IPC Adapterの試験ではない。** 送受信を行ったのは同一テストプロセスのgoroutineであり、権限管理を通した別プロセスの送受信やConnectデータ配送は未実施。Lease失効等は単なるJSONのイベント内容として配送しただけで、実際の認可・回収・結果受理は検証していない。

したがって#753は未完了である。次は、[2026-11-09のGo 1.27.0パッケージ実行記録](RESULTS_2026-11-09.md)と**対象commit**を照合したうえで、**別プロセスの実Adapterと実権限の経路**を対象にする。新しい共通基盤や独自Hostを前提条件にはせず、TEIの意味・外部副作用やSEIの判断、VRクマの安全停止は別の検証票で扱う。

## 2026-11-08 別プロセスでの配送確認と実Adapterの棚卸し

[試験用の別プロセス送受信テスト](unix_interprocess_test.go)を追加した。Goテストバイナリから子プロセスを起動し、親子の異なるPIDの間でUnixドメインソケットを介して固定6イベントを配送できた。最後の1件を欠落させた負例も検出できた。Linux／Go 1.23.2の隔離環境で実行し、race付き5回の繰返しもPASSした。環境、採用したソースのSHA、実ログは[RESULTS_2026-11-08.md](RESULTS_2026-11-08.md)に分離して記録した。

**ただし、この結果はEKI本番AdapterのPASSではない。** 子は`eki-agent`ではなく試験用の子プロセスで、送った内容の`lease_revoked`は実際のLease失効ではない。採用済みのData/Delivery Contract、Transport Profile、現在の認可を実装したという主張も含まない。

現行実装の境界はソースから次のように把握できる。

| 領域 | 確認できたこと | 実際のデータ配送との距離 |
| --- | --- | --- |
| Worker Protocol | `spec/worker/v1/worker.proto`の`WatchCommands`はWorker制御用server stream | 任意の観測・画像フレームを配送するData Planeではない |
| Workerの実HTTP入口 | `internal/control/workerplane/`の認証済みClaim、割当、Artifact取得 | 一般用途の連続データ配送Adapterではない |
| 既存SPI | `architecture/EXTENSION_SPI.md` §26のData Access Transport SPIはArtifactGrantのscope/TTLを保つ | 権限制限の原則は参考になるが、メディアストリームの成立証拠ではない |

これらの**確認したファイルの範囲では**一般用途のLocal IPC／Connectデータ配送Adapterの実装・配線を確認できなかった。リポジトリ全体に無いと断定せず、まず現在の実行経路の調査と既存実装の再利用可能性を確認する。

検証工程は、①正本Go 1.27環境で全Spikeテスト、②実際のEKI Producer/ConsumerとPrincipal・Lease/Grantを結ぶAdapterの確認、③Local IPCとConnectの同一Fixture観測、④実測を伴うD1、⑤#754の方式選定の順とする。通信の選択・実行資格・データの利用・外部効果を別票で観測し、既存Control Planeの権限を新たなTransportへ暗黙に広げない。新Hostや第二のRegistryを前提とせず、M0/MVPの優先順位を変えない。


## 2026-11-09 実リポジトリのGo 1.27.0での形式チェック

**commit `972bc57ec64cf607488aec19cee62c1702dfe1f2`時点**のこのパッケージを、`go.mod`が要求するGo 1.27.0（Linux x86_64、WSL）で実行した。`go test -count=1 -v`はPASS 26件・SKIP 2件、`go test -race -count=5 -v`もともに終了コード0だった。SKIPは、実Adapterの観測が無い`TestProvidedAdapterObservedTrace`（`NOT RUN`）と、別プロセス試験の補助プロセスである`TestUnixFixtureChildSender`である。実行条件、生ログ、一般化できる範囲は[RESULTS_2026-11-09.md](RESULTS_2026-11-09.md)を参照する。

これで、2026-11-08の記録で未実行としていた「正本リポジトリのGo 1.27系での全試験」は埋まった。一方、実Adapterへの同一Fixture投入、実Lease・Grantによる認可、性能測定は今回も確認しておらず、#753は未完了のままである。

## 2026-11-09 観測順序の反証と既存実行経路の再利用

**観測Traceの比較で見つかった欠落**：`trace_compare_test.go`はこれまで、各`event_id`に添えられた判定と件数だけを照合していた。この方法だと、最初の二つの観測を入れ替え、各イベントに元の判定文字列を添え直したTraceが通過する。今回、既存`fixture.json`が定義する**単一Task・単一Trackの試験用イベント再生順序**と、観測列の順序を照合する。独立Expectedを変更せず、順序だけを入れ替えた負例を追加した。

この順序検査は**固定された試験ケースの因果順序**であり、複数の独立したトラック間やネットワーク全体に全順序を要求するものではない。新しい一般のData/Delivery仕様に拡張するときは、系列ごとの順序・タイムスタンプの誤差・権限の世代・部分結果の関連を別途設計する。現行のTrace入力はAdapterの自己申告であり、**順序を照合できても、実際の送受信時刻や取得・消費・認可を実証したことにはならない**。

**実装状況の再照合**：2026-11-09のmainでは、`internal/control/service/run.go`の設定`dispatch`による起動経路、`dispatch.go`の配置からLease/WorkOrderまでの部品配線、[`conformance/process/dispatch_loop_test.go`](../../conformance/process/dispatch_loop_test.go)の実Control Planeで署名付き割当を配るケースが存在する。[`execution_agent_test.go`](../../conformance/process/execution_agent_test.go)では試験内で用意した割当によるAgentの固定コマンド実行とClaim送信を確認するケースもある。ただし、VerificationとAccepted Resultはテスト内で明示的に合成した材料を用いて別途呼び出しており、**Client API→配置→実行→検証→受理が一つの本番起動設定で連続完了したことは未証明**。実際に動いたという証拠を増やす場合は、まず[`conformance/process`](../../conformance/process/README.md)の既存の署名・Principal／mTLS／実プロセス試験を再利用する。

**次の#753の仕事**：試験専用Unixソケットの送受信テストを追加し続けない。現行のWorker Control、Artifact取得、データ配送を混同せず、Data Adapterの提供範囲と権限に基づいて、実際の観測・再接続・Lease失効を別票で検証する。Go 1.27.0の正本パッケージ試験は**commit `972bc57`で実施済み**（[実行結果](RESULTS_2026-11-09.md)）。ただし本節のTrace順序負例（PR #810）はこの実行commitより後に追加されたため、**追加差分については再実行待ち**である。既存試験のPASSを、後続revision・実Adapter・認可のPASSへ一般化しない。#754の追加方式選定は引き続き実測後とする。

## 2026-11-09 外部Effectと配送Evidenceの境界を追加

[conformance/processの同一Task証拠計画](../../conformance/process/EXECUTION_EVIDENCE_PLAN.md)のES-07で、Traceが独立Expectedと一致しても、実Data Adapterの送信・受信、現在Grant、利用側Consumed、Provider Effectを実証していない状態を明示した。#753はData/Delivery/Transportの比較と実観測を担当し、期限切れLease時の未知Effect（ES-01〜05）の実装判断は#745、同一Task E2E（ES-06）は#242が担当する。新しい通信方式やEffect Registryをここでは先取りしない。

**M0の外部Effect前提を再照合**：`internal/core/admission/request.go`が現在受理する`reliability.effect_mode`は`none`のみ。一方、古い合成TaskやAdmission本文が欠落したTaskまで`none`と判定できるわけではない。回収経路の`PossibleExternalEffect: false`固定は[#745](https://github.com/hat47x/eki-fabric/issues/745)で、保存済みの受理本文とTaskIR digestの検証を経て見直す。[同一Task証拠計画](../../conformance/process/EXECUTION_EVIDENCE_PLAN.md)で、現行M0の正例と将来・合成用の副作用負例を分離した。

## 2026-11-12 #745完了と実Data Adapterの未実施を切り分ける

[#745](https://github.com/hat47x/eki-fabric/issues/745)のM0 EffectNone出典検証は現行mainの回収経路へ接続済みであり、以前の`PossibleExternalEffect:false`固定は現在のコードにはない。回収時のAdmission確認と、Data Adapterの実接続、Lease/Grantの現在性、Consumerの実受領は独立した証拠である。

[D0-Aの既存実行記録](RESULTS_2026-11-09.md)はcommit `972bc57`のGo 1.27.0で26 PASS・2 SKIP。後続のTrace順序負例や今回の計画更新を同じcommitで再実行したものではない。D0-B/CのLocal IPC／Connect実Data Adapterの生観測は引き続きNOT RUN。[同一Task Evidence計画](../../conformance/process/EXECUTION_EVIDENCE_PLAN.md)のES-07を、実接続・実受信・Consumer受領・現在Grantの判定に利用する。単なるプロファイル宣言、固定選択ID、外部Harnessに渡しただけの情報、Ackを実Effect/業務完了としない。
