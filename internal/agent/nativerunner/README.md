# native runner（production adapter）

`executor.Runner`の固定コマンド実装です。Spike `spikes/native-runner` を本番パッケージへ移し、テストを同じ場所で維持します。

## 実行するもの

- 信頼済み設定の固定コマンドと引数だけを実行する。
- 期限・取消し・出力上限・プロセスツリー停止を扱う。
- CPU・メモリ・一時ディスクを20ms間隔で監視し、上限を超えたらプロセスツリーを停止する（Linux/Windows）。
- Linuxは起動したプロセスの**プロセスグループ全体**のRSSとCPU時間を合計して監視する。bwrapの子プロセスも同じグループに属するため、sandbox内の消費を含む。
- Windowsのポーリングは起動したプロセスの値を見る。子プロセスのメモリはJob Objectの上限で封じ込める。
- `spec.Deadline`と`WallTimeMillis`は実行開始時にcontextの期限として設定し、超過時にプロセスグループを停止する。
- この監視は**実行中の実測**であり、開始前の資源の予約（[ADR-0031](../../../plan/adr/0031-local-execution-slots-and-resource-reservation.md)）とは別の境界である。予約は「始める前に、合計が上限を超えないこと」を扱い、監視は「始めた後に、実測が上限を超えたら止めること」を扱う。片方で他方を代用しない。
- Windowsでは`sandbox/native-job-object`を選ぶと、Job ObjectでプロセスメモリとCPU時間の上限、プロセスツリーの封じ込めを行う。
- Linuxでは`sandbox/linux-bwrap`を選ぶと、bubblewrapで読み取り専用root、private `/tmp`、networkなし、別PID名前空間で実行する（`bwrap`が必要）。networkを完全に切るため、数値のnetwork上限はこのprofileでは「通信ゼロ」として満たす。
- 実行ファイルのSHA-256、引数allowlist、sandbox profile、network/GPU/digestの不一致をfail-closedで拒否する。

## 実行しないもの

- Task入力からコマンドを組み立てない。
- `native-fixed-spike`はsandboxではない。
- filesystemとnetworkの分離は提供しない。
- networkとGPUの上限を強制しない。一時ディスクは実行ごとの一時ディレクトリ配下のみを対象とする。
- 信頼できないTaskに使わない。

## テスト

```text
go test ./internal/agent/nativerunner -count=1
```

Windowsでは`taskkill /T /F`、Unixではprocess groupの`SIGKILL`を使います。
