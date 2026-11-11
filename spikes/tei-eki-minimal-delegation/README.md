# TEIの一件の検証をEKIへ委任する最小経路（TEI→EKI Minimal Delegation Spike）

## このSpikeの役割

TEIの副作用のない一件の処理を、EKIの公開Client APIだけを使って委任できるかを確かめます。TEI側で実行要求を構成し、入力Artifactを渡し、EKIが返す公開状態と結果を読み、TEI側のReceiptとして記録するところまでを、実際の`eki-control`とモックの両方で試します。

目的はEKIをTEIの代替実行基盤にすることではありません。分散実行、配置、Attempt、Lease、Fencing、回復、スケジューリングはEKIの責務のままにし、TEIは「何を実行したいか」と「返ってきた結果をどう判断するか」だけを持ちます。

既存の[TEI Workload Projection Spike](../tei-workload-projection/README.md)は、受理までの経路と契約の表現力を調べました。このSpikeはその続きで、**入力Artifactの受け渡し、結果の取得、負例の判定**までを対象にします。契約は`spec/external/openapi.json`（EKI）だけを読み、EKIのGoパッケージはimportしません。

## 調べる問い

1. TEIの一件の検証処理を、現在の`eki.task-request/v1`とArtifact経路に、意味を落とさず投影できるか。
2. 入力Artifactのdigest、実行体のdigest、Toolchain、実行条件、Attempt、結果Artifact、Receiptを、どの範囲まで対応づけられるか。
3. EKIが返さない情報（Attemptの識別子、Lease、Fence、独立した検証）を、TEIはどう扱うべきか。
4. 重複実行、期限切れ、結果未到着、Lease失効、古いAttempt、部分的なArtifact受領、キャッシュ差異を、TEIはどう判別できるか。
5. EKIへ委任することがローカル実行より不利になるのは、どの条件か。

## 構成

| ファイル | 内容 |
|---|---|
| `model.go` | EKIの外部契約に対応する型と、このSpikeの記録の型 |
| `adapter.go` | TEI側の入口。`API` interfaceと`Adapter.Execute`、および委任の本体 |
| `project.go` | TEIの操作をEKIの`TaskRequest`と`IngestIntent`へ投影する。対応表（Binding）を持つ |
| `delegate.go` | 委任のオプションと記録の型、補助関数 |
| `evaluate.go` | 公開APIで観測した事実から、TEI側のReceiptを組み立てる |
| `simulate.go` | Lease・Fence・Attemptを含む**模擬**の負例を判定する（実EKIの観測ではない） |
| `bundle.go` | 入力を決定的なtar.gzにする。置き場所や時刻でdigestが変わらない |
| `client.go` | EKIのClient APIだけを使うHTTPクライアント |
| `trace.go` | 入力・実行体・条件・Task・Attempt数・結果・Receiptを一つの記録へ結ぶ |
| `cmd/tei-eki-delegation/` | `offline`・`bundle`・`local`・`live`・`probe`のCLI |
| `run_live.ps1` | 使い捨ての`eki-control`を起動し、ローカル実行とEKI委任を実行して証拠を残す |
| `examples/receipts.json` | 三値それぞれのReceiptの形。後続の実装が読む例 |
| `IMPLEMENTATION_PLAN.md` | 後続の実装者向けの作業計画、受入条件、判断記録、チェックリスト |
| `*_test.go` | モックのEKIに対する試験と、契約・負例の試験 |
| `RESULTS_2026-11-11.md` | 実行条件、観測、一般化できる範囲、未検証の範囲 |
| `evidence/2026-11-11/` | 実行の生の出力（projection、trace、probe、local run） |

`README.md`と`RESULTS`は日本語です。GoのコメントとReceiptの`basis`・`not_checked`は英語で、`reason`は日本語です。Receiptは機械可読な記録であり、日本語の説明は`RESULTS`側にあります。

## 何をEKIへ渡すか

対象は`tei validate -workspace <dir> -json`という、読み取りだけで完結する一件の検証です。TEIの実行体（`tei`）のdigest、Toolchain、コマンド、参照したソースのrevision、入力ワークスペースのtar.gzを一つの操作として扱います。

| TEIが表現したいもの | EKIでの置き場所 | EKIが実際に保証すること |
|---|---|---|
| 入力Artifactの内容同一性 | `task_request.inputs[].artifact_id`（SHA-256 hex） | 受理時にそのdigestの本文が保管されていること（[#763](https://github.com/hat47x/eki-fabric/pull/763)）。ただし`expected_digest`の宣言は強制されない（下記） |
| 実行体のdigest | `task_request.parameters.tei.executable_sha256` | **保証しない**。`ExecutorRequirement`にdigestの欄がなく、`parameters`はWorkerへ渡らない |
| executorの版 | `task_request.executor.version_constraint` | **保証しない**。指紋に記録されるだけで評価されない |
| executorの選択 | `task_request.executor.id` | 管理者が登録した`approved_executors`に含まれること |
| Toolchain | `task_request.parameters.tei.toolchain` | **保証しない**。Toolchainを固定する欄がない |
| 実行条件（CPU・メモリ・disk・出力・network・wall time） | `task_request.resources` | 受理時の上限検査と、WorkerのCPU・メモリ・一時ディスクの監視。networkとGPUは対象外 |
| 期限 | `task_request.deadline` | **保証しない**。期限を過ぎてもTaskは終端へ動かない（[#111](https://github.com/hat47x/eki-fabric/issues/111)） |
| 副作用の不在 | `task_request.reliability.effect_mode` | `none`だけを受理する。重複実行を安全にする根拠はここ |
| 再実行の予算 | `task_request.reliability.max_attempts` | 実効値はポリシーが決める |
| 実際のAttempt | `task_public_status.attempt_count` | 件数だけ。Attempt・Lease・Fenceの識別子は公開されない |
| 結果Artifact | `task_result.artifacts[]`と`GET /v1/artifacts/{id}` | 取得可否と、取得した本文の再測定 |
| 結果のdigest | `task_result.output_digest` | 任意項目。記録値であり、Clientは再計算を強制できない |
| キャッシュの局所性・配置の希望 | （欄がない） | 保証しない。配置はEKIが決める |
| 独立した検証を含むReceipt | （欄がない） | EKIはTEI向けのReceiptを発行しない。TEIが自分で組み立てる |

この対応表は`project.go`の`bindings()`にあり、`Projection.bindings`として記録に残ります。「記録しただけ」と「EKIが保証する」を混ぜないことが、このSpikeの中心です。

## Receiptの意味

Receiptは`accepted`・`rejected`・`undetermined`の三値です。二値にしません。

- `accepted`：EKIが結果を返し、TEIのローカル実行のdigestと一致し、取得したArtifactを再測定しても名前と一致した。**それでも**、独立した検証者による確認やToolchainの一致は`not_checked`に残る。
- `rejected`：受理の拒否、終端したTaskに結果がない、digestの不一致など、採用できないと分かる場合。
- `undetermined`：結果がまだない、期限を過ぎても終端していない、結果にdigestもArtifactもない、Artifactが部分的にしか取れない、TEI側に期待値がない。**待つか、新しいTaskで再試行するか、ローカルで実行するかは呼び出し側が決める。**

Receiptは`basis`（確認したこと）と`not_checked`（確認できないこと）を必ず持ちます。`not_checked`が空のReceiptは作りません。

## Mock試験と実EKI試験の区別

| 種別 | 何を確かめるか | どこ |
|---|---|---|
| モック | 投影、入力の持ち込み、状態の読み方、Receiptの判定、負例（重複、期限、結果未到着、部分受領、digest差異） | `go test ./spikes/tei-eki-minimal-delegation/...` |
| 模擬（Lease・Fence） | Lease失効・古いAttempt・Fence不一致を、TEIがどう扱うべきか | `simulate.go`と`simulate_test.go`。**実EKIの観測ではない** |
| 実EKI | 実際の`eki-control`に対する入力の持ち込み、受理、状態、結果の拒否、重複の収束、負例の応答 | `run_live.ps1`と`evidence/2026-11-11/` |

`simulate.go`の入力はEKIの公開契約に存在しません。モックでしか埋められません。実EKI試験では、Attemptの識別子もLeaseもFenceも観測できないため、`attempt_count`だけを記録し、それ以外は`not_checked`に残します。

## 実装が依拠している契約上の事実

次の事実は`contract_test.go`が`spec/external/openapi.json`に対して固定しています。EKIが契約を変えたら、このSpikeの主張を読み直す必要があることが試験で分かります。

- `ExecutorRequirement`は`id`と`version_constraint`だけを持ち、実行体のdigestを置く欄がない。
- `TaskRequest.parameters`はWorkerへ渡らない。
- `effect_mode`は`none`だけを受理する。
- `TaskPublicStatus`と`TaskResult`はLease・Attempt・Worker・Fenceの識別子を公開しない。`attempt_count`は公開する。
- `TaskResult.output_digest`は任意項目である。

## 実行方法

### モック試験

```bash
go test ./spikes/tei-eki-minimal-delegation/...
```

### 実EKI試験（Windows PowerShell）

`eki-control`と`tei`の実行体、TEIのcheckout、検証対象のワークスペースを指定します。`eki-control`は一時ディレクトリのSQLiteとArtifactディレクトリで起動し、本番DBや外部サービスへは触れません。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\run_live.ps1 `
  -EkiControl <eki-control.exe> `
  -Tei <tei.exe> `
  -TeiRepo <tei-platform checkout> `
  -WorkspaceDir <tei-platform checkout>\reference\workspace
```

`-WorkspaceDir`は省略できます。checkoutの作業状態に依存させたくない場合は、`git archive`で展開した複製を指定します。

スクリプトは次を順に行います。

1. 使い捨ての`eki-control`をloopbackで起動する（クライアント用tokenとWorker用の試験profileを含む設定を一時ファイルに書く）。
2. ワークスペースを決定的なtar.gzにし、ローカルで`tei validate`を3回実行して基準を取る。
3. 投影を作り、拒否経路のprobeを実行する。
4. 同じ投影で2回委任し、2回目が同じTaskへ収束することを確かめる。
5. 証拠を`evidence/YYYY-MM-DD/`へ写し、一時環境を削除する。

WSLで動かす場合は、`run_live.ps1`の代わりに同じCLIをbashから呼びます。ただし証拠はこのリポジトリの`evidence/`に置き、実行条件を`RESULTS`へ記録してください。

### CLI

```text
tei-eki-delegation offline  投影だけを作る（ネットワーク不要）
tei-eki-delegation bundle   ディレクトリを決定的なtar.gzにする
tei-eki-delegation local    同じ操作をローカルで実行し、基準を記録する
tei-eki-delegation live     実EKIへ委任し、traceを書く
tei-eki-delegation probe    拒否経路を実EKIに対して確かめる
```

## 設計上の判断

- 成功を仮実装しない。 結果が得られない場合、Receiptは`undetermined`になる。`accepted`にするのは、結果のdigestがローカル基準と一致し、取得した本文を再測定した場合だけ。
- EKIの内部状態を読まない。 SQLiteもWorkerの状態も読まない。公開APIの応答だけで判断する。
- 再試行を自動化しない。 同じ冪等キーの再送は同じTaskへ収束する。別のTaskを作る再試行は、副作用の有無を知っている呼び出し側の判断にする。
- 入力の宣言と実体を分ける。 `expected_digest`は宣言であり、実装は強制しない（`RESULTS`参照）。TEIはアップロード応答の`artifact_id`を、自分が送るつもりのdigestと比較する。一致しなければ、そのdigestを入力に指定したTaskは受理されない。
- 実行体とToolchainは「記録」である。 EKIが保証しない以上、Receiptも保証しない。Receiptの`not_checked`に残す。

## 限界

- 実EKI試験は**Taskの受理まで**で止まる。`dispatch`を有効にしていないためWorkerへ配送されず、`attempt_count`は0のままである。実際のAttemptを作るにはEKIの試験用profileと署名鍵・Worker登録が必要で、このSpikeの範囲外にした。
- 結果の受理（`completed`）には独立した検証が必要で、現行の`eki-control`の起動経路ではその入力を作れない（[#242](https://github.com/hat47x/eki-fabric/issues/242)）。したがって実EKIで`accepted`のReceiptは得られない。**得られないことを成功として扱っていない。**
- 実行したのは`validate`だけで、ビルドやコード生成は実行していない。ビルドの再現性とキャッシュの影響は、既存の[Workload Projection Spikeの観測](../tei-workload-projection/RESULTS_2026-10-19.md)を参照し、このSpikeでは測っていない。
- 時間は一台の開発PC上での値である。ローカル実行の時間は同じホストでも0.2秒から9秒まで変動した（`RESULTS`参照）。一件の比較から一般的な費用を結論にしない。
- EKIの契約と実装の不一致（`expected_digest`の不強制、`digest`の型）は見つけたが、契約を変更していない。判断はIssueで行う。

## 次の実装

後続の作業は[IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md)に、完了の定義（T1のD1〜D6）、作業パッケージ、Owner判断の記録、実行順序、チェックリストとして整理しています。WP-3（入力の不一致でTaskを送らない）は実装済みです。製品コードへ移す前に、少なくともWP-4（実際のAttemptをEKIの試験用profileで作る）を済ませてください。Receiptの形は[examples/receipts.json](examples/receipts.json)にあります。

## このSpikeが変更しないもの
- EKIの外部契約（`spec/external/openapi.json`）と実行保証。
- TEIのProduct Contract。
- 本番DB、外部サービス、実Provider。

見つけた論点は既存Issueへ記録し、Ownerの判断を待ちます。このSpikeの成功をRequirementやArchitectureの暗黙の変更として扱いません。
