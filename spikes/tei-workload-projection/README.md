# TEIの仕事をEKIへ投げてみる（TEI Workload Projection Spike）

## このSpikeの役割

TEIがEKIを実行基盤として使う場面を、文書上の想定ではなく、実際のコマンドと実際の`eki-control`で確かめるSpikeです。目的は、EKIの要件と設計に**利用者側から圧力をかけること**です。EKIを動かすことではありません。

調べる問いは次の四つです。

1. TEIの実際の仕事は、どのくらいの大きさか。EKIへ出す価値があるのはどれか（RQ-014）。
2. 「一つの計算を複数の経路で実行し、同等の結果か確かめる」は、TEIの仕事で成り立つか。成り立つ条件は何か（RQ-021、RQ-022）。
3. TEIの仕事を、現在のTaskRequest（`eki.task-request/v1`）にどこまで書けるか。書けないものは何か（RQ-002、RQ-011、RQ-016）。
4. 実際の`eki-control`に投げたとき、TEI側のAdapterは何を観測し、どこで止まるか。

背景と問い直しの全体は[TEIから使う分散基盤としてのEKI](../../research/tei-usage-requirements-review.md)にあります。ここで見つけた論点の評価は[TEI利用からの圧力の評価](../../plan/TEI_USAGE_STRESS_EVALUATION_2026-10-19.md)で行います。このSpikeだけを根拠に、Product RequirementやArchitectureを変えません。

## 構成

| ファイル | 内容 |
|---|---|
| `measure_tei.sh` | TEIの決定的なCLI操作6種を、置き場所の異なる二つの複製で実行し、時間と出力のsha256を比べる |
| `measure_build.sh` | TEIの実行体を、置き場所の異なる二つの複製でビルドし、成果物のdigestを比べる。`-trimpath`の有無と、ビルドキャッシュの有無を変える |
| `tei_eki_client.py` | TEI側に置くAdapterの試作。TEIの仕事9種をTaskRequestへ投影し、OpenAPIのschemaで検査する（offline）。起動済みの`eki-control`へ実際に投げる（live）。Python標準ライブラリだけを使う |
| `run_live.sh` | 一時ディレクトリで`eki-control`を起動し、`tei_eki_client.py live`を実行する |
| `results-2026-10-19/` | 下記の条件で得た出力 |

`tei_eki_client.py`はEKIのGoのコードをimportしません。EKIが公開する言語中立の契約（`spec/external/openapi.json`）とHTTPだけを使います。依存の向きはTEI → EKIで、`conformance/productboundary`の検査対象（`internal/`、`cmd/`）にも入りません。

## 実行方法

WSL（Ubuntu）上で実行しました。Goは`~/sdk/go/bin/go`（go1.27.0。TEIのビルドでは`go.mod`の指定によりgo1.26.9のtoolchainが選ばれた）です。

```bash
# バイナリを作る
go build -o ~/ekitei/bin/eki-control ./cmd/eki-control        # eki-fabric
go build -o ~/ekitei/bin/tei ./cmd/tei                        # tei-platform

# TEIの実測
bash spikes/tei-workload-projection/measure_tei.sh ~/ekitei/bin/tei <tei-platform> ~/ekitei/measure
bash spikes/tei-workload-projection/measure_build.sh ~/sdk/go/bin/go <tei-platform> ~/ekitei/build

# 投影とEKIへの投入
python3 spikes/tei-workload-projection/tei_eki_client.py offline --openapi spec/external/openapi.json --out ~/ekitei/out
bash spikes/tei-workload-projection/run_live.sh ~/ekitei/bin/eki-control <eki-fabric> ~/ekitei/out
```

## 条件

- tei-platform: `main`、`1f69562b8`
- eki-fabric: `main`、`65cdd63`（このSpikeを置いたbranchの分岐元）
- `eki-control`は、loopback上の平文HTTP、SQLiteの一時ファイル、固定のBearer token一つで起動した。受理の上限は`run_live.sh`に書いたとおり（一Taskあたりメモリ4GiB、wall time 3600秒、未完了Taskの上限10件）。
- 実行環境は一台の開発PC上のWSL。ネットワークの往復はloopbackだけで、実際の分散配置の費用は含まない。

## 観測

### 1. TEIの決定的な操作は、EKIの往復より小さい

`results-2026-10-19/measurements.json`

| 操作 | 時間（二つの複製） | 出力 | 同じ場所での再実行 | 置き場所を変えた複製 |
|---|---|---|---|---|
| `validate` 参照ワークスペース | 8 / 9 ms | 721 B | 一致 | 一致 |
| `validate` 最初のワークスペース | 5 / 5 ms | 67 B | 一致 | 一致 |
| `world` 参照ワークスペース | 11 / 9 ms | 41,704 B | 一致 | 一致 |
| `source-index` | 6 / 6 ms | 701 B | 一致 | 一致 |
| `scenario verify` | 9 / 10 ms | 1,325 B | 一致 | 一致 |
| `scenario run` | 10 / 10 ms | 1,716 B | 一致 | 一致 |

時間はプロセスの起動を含む壁時計の時間です。

一方、`eki-control`へ一件を投げて状態を一度読むだけの往復は、中央値10.2 ms、p90 13.2 msでした（`live-report.json`のL7、30件）。これは受理の永続化だけの費用で、配置、Lease、Workerへの転送、検証、結果の取得はまだ含みません。それでも、TEIの操作一件の実行時間と同じ大きさです。

### 2. 二経路での一致は、実行体の作り方で決まる

`results-2026-10-19/build-measurements.json`

| ビルド | 経路A | 経路B | 一致 |
|---|---|---|---|
| `-trimpath`なし | `97477cb7…` | `f45e165e…` | しない |
| `-trimpath`あり | `b2679e8f…` | `b2679e8f…` | する |

ソースは同じrevisionを`git archive`で二か所に展開したものです。違いは置き場所の名前だけです。`-trimpath`がないと、ソースのパスが実行体に埋め込まれ、digestが一致しません。

時間は、ビルドキャッシュが温まっている場合に0.8秒、空のキャッシュでは12.5〜12.8秒でした。約15倍の差です。

また、手元のgo1.27.0で実行しても、TEIの`go.mod`の指定により、go1.26.9のtoolchainが選ばれました。このtoolchainが手元になければ、`go`コマンドはネットワークから取得しようとします。

### 3. 投影できたもの、できなかったもの

`results-2026-10-19/offline-report.json`

9種の仕事は、すべてschemaの上では書けました。ただし、TEI側が本当に必要とする情報のかなりの部分は、書く場所がありません。仕事ごとに数えた不足は次のとおりです（多い順）。

| 不足 | 該当した仕事の数 |
|---|---|
| ワークスペースを内容digestで渡す経路 | 8 / 9 |
| 結果と来歴を受け取る経路 | 7 / 9 |
| 実行体のdigestをClient側から固定する場所 | 3 |
| 期限内に終わった分だけを集める単位 | 3 |
| 「つなぐ」形態、等級、保証の要求、同等の判定方法、実行環境の区分、キャッシュの局所性 | 各2 |
| toolchainの事前配置、model lineage、必達のための予算、loopback通信、副作用の宣言、資源量の既定値 | 各1 |

TEIが書きたい形（`executor.digest`を加える、`effect_mode`に照合つきの副作用を書く）は、schemaで拒否されます。

### 4. 実際の`eki-control`での振る舞い

`results-2026-10-19/live-report.json`

| 試し | 結果 | 読み取れること |
|---|---|---|
| L1 入力つきで投げる | 9件すべて422 `ARTIFACT_NOT_AVAILABLE` | TEIの意味のある仕事は、現時点では一件も受理されない |
| L1 入力を外して投げる | 8件が202。画面の評価者（メモリ8GiB）だけ422 `RESOURCE_BUDGET_EXCEEDED` | 受理の上限は全Taskで共通の一つだけ。大きい仕事を通すには、全員の上限を上げるしかない |
| L1 配備の操作 | `effect_mode: "none"`と書けば202 | 副作用のある仕事も、Clientが`none`と書けば受理される。Clientの申告は信用しない前提なので、副作用の有無は管理者が登録するExecutorの側で決める必要がある |
| L2 digestを`executor.digest`に書く | 400 `INVALID_TASK_REQUEST` | 安全側への固定も書けない |
| L2 digestを必須の拡張に入れる | 422 `EXTENSION_NOT_SUPPORTED` | 拡張の経路も閉じている（安全側に閉じているのは正しい） |
| L3 冪等キー | 同じ本文の再送は同じTask。本文を変えると409 | TEIの`IdempotencyKey`の意味と合う |
| L4 3秒の期限を付けて5秒待つ | `queued`のまま。`terminal_code`なし | 期限を過ぎても状態が変わらない。TEIの三値では「未確定」のまま待ち続ける |
| L4 結果の取得、Artifactの取り込み | 501 `NOT_IMPLEMENTED` | |
| L5 候補12件を一度に投げる | 10件が202、2件が429 | 未完了の上限10件で止まる。実行されないので枠は空かず、取り消すまで戻らない |
| L6 取消 | `cancelled`、`CANCELLED` | TEIの三値ではrejectedに写せる |
| L7 30件の連続投入 | 中央値10.2 ms、p90 13.2 ms | 上記1を参照 |

## 一般化できる範囲

次の点は、この実験の条件を越えて言えると考えます。

- **小さな決定的な仕事を一件ずつTaskにすると、EKIの往復の費用が仕事より大きくなる**。この傾向は、配置や検証が実装されると、さらに強まります。多数の小さな仕事は、まとめて一件にするか、枠を開いて続けて流す形態（研究メモ§4.1の「つなぐ」）が要ります。
- **複数経路での一致は、EKIの検証の仕組みより先に、実行体の再現性で決まる**。`-trimpath`のような設定を、Clientのparameterで切り替えられるようにすると、Clientは一致しない設定を選べます。再現性に関わる設定は、管理者が登録するExecutorの側で固定する必要があります。
- **ビルドのような仕事では、キャッシュの局所性が計算性能より時間を大きく左右する**。RQ-008が想定する「データの局所性」に、ビルドキャッシュや依存モジュールのキャッシュも含める必要があります。また、二経路で比べると、片方はキャッシュが冷えていることが多く、検証側の費用が本体の十数倍になり得ます。
- **toolchainや依存の取得は、Worker内のネットワーク取得になる**。RQ-029（Workerによる任意の取得を標準にしない）のもとでは、toolchainと依存を事前に配置した実行環境（Runtime Class）を、管理者が用意する形になります。
- **Client側の申告（副作用なし、決定的）は、検証されない**。これは既存の前提（RQ-015）どおりです。副作用の有無や決定性を安全の根拠に使うなら、Executorの登録情報として管理者が持つ必要があります。

## 限界

- EKIのWorker、配置、Lease、検証、結果の取得は未実装です。このSpikeは、受理までの経路と、契約の表現力しか試していません。分散実行の費用（転送、配置、検証）は測っていません。
- 時間の計測は、一台のPC上のloopbackでの値です。実際のネットワーク越しの値ではありません。
- TEIの重い仕事のうち、生成器の候補選定、画面の評価、Gate Cの証拠取得は、実行していません。投影と、コードの読み取りに基づく見積もりだけです。
- ビルドの再現性は、Go、Linux、同一のtoolchainでの一回の観測です。OSやCPUアーキテクチャをまたいだ一致は確かめていません。
- 予実の管理、クラウドへの引き上げ、管理画面については、EKIに対応する実装がないため、試していません。研究メモで論点として扱います。
