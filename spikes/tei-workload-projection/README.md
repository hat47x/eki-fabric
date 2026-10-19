# TEIの仕事をEKIへ投げてみる（TEI Workload Projection Spike）

## このSpikeの役割

TEIがEKIを実行基盤として使う場面を、文書上の想定ではなく、実際のコマンドと実際の`eki-control`で確かめるSpikeです。目的は、EKIの要件と設計に**利用者側から圧力をかけること**です。EKIを動かすことではありません。

調べる問いは次の四つです。

1. TEIの実際の仕事は、どのくらいの大きさか。EKIへ出す価値があるのはどれか（RQ-014）。
2. 「一つの計算を複数の経路で実行し、同等の結果か確かめる」は、TEIの仕事で成り立つか。成り立つ条件は何か（RQ-021、RQ-022）。
3. TEIの仕事を、現在のTaskRequest（`eki.task-request/v1`）にどこまで書けるか。書けないものは何か（RQ-002、RQ-011、RQ-016）。
4. 実際の`eki-control`に投げたとき、TEI側のAdapterは何を観測し、どこで止まるか。

背景と問い直しの全体は[TEIから使う分散基盤としてのEKI](../../research/integration/tei-usage-requirements-review.md)にあります。ここで見つけた論点の評価は、GitHub Issue [#113](https://github.com/hat47x/eki-fabric/issues/113)で行います。このSpikeだけを根拠に、Product RequirementやArchitectureを変えません。

## 構成

| ファイル | 内容 |
|---|---|
| `measure_tei.sh` | TEIの決定的なCLI操作6種を、置き場所の異なる二つの複製で実行し、時間と出力のsha256を比べる |
| `measure_build.sh` | TEIの実行体を、置き場所の異なる二つの複製でビルドし、成果物のdigestを比べる。`-trimpath`の有無と、ビルドキャッシュの有無を変える |
| `tei_eki_client.py` | TEI側に置くAdapterの試作。TEIの仕事9種をTaskRequestへ投影し、OpenAPIのschemaで検査する（offline）。起動済みの`eki-control`へ実際に投げる（live）。Python標準ライブラリだけを使う |
| `run_live.sh` | 一時ディレクトリで`eki-control`を起動し、`tei_eki_client.py live`を実行する |
| `RESULTS_YYYY-MM-DD.md` | 実行日ごとの条件、観測、一般化できる範囲 |
| `evidence/YYYY-MM-DD/` | 実行日ごとの生の出力（測定値とレポートのJSON） |

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

## 結果

| 実行日 | 記録 | 生の出力 |
|---|---|---|
| 2026-10-19 | [RESULTS_2026-10-19.md](RESULTS_2026-10-19.md) | `evidence/2026-10-19/` |

## 限界

- EKIのWorker、配置、Lease、検証、結果の取得は未実装です。このSpikeは、受理までの経路と、契約の表現力しか試していません。分散実行の費用（転送、配置、検証）は測っていません。
- 時間の計測は、一台のPC上のloopbackでの値です。実際のネットワーク越しの値ではありません。
- TEIの重い仕事のうち、生成器の候補選定、画面の評価、Gate Cの証拠取得は、実行していません。投影と、コードの読み取りに基づく見積もりだけです。
- ビルドの再現性は、Go、Linux、同一のtoolchainでの一回の観測です。OSやCPUアーキテクチャをまたいだ一致は確かめていません。
- 予実の管理、クラウドへの引き上げ、管理画面については、EKIに対応する実装がないため、試していません。研究メモで論点として扱います。
