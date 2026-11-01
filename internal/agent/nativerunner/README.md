# native runner（production adapter）

`executor.Runner`の固定コマンド実装です。Spike `spikes/native-runner` を本番パッケージへ移し、テストを同じ場所で維持します。

## 実行するもの

- 信頼済み設定の固定コマンドと引数だけを実行する。
- 期限・取消し・出力上限・プロセスツリー停止を扱う。
- CPU・メモリ・一時ディスクを20ms間隔で監視し、上限を超えたらプロセスツリーを停止する（Linux/Windows）。
- 監視は**起動したプロセスのPID**（LinuxはPID名前空間の先頭のプロセス）について行う。子プロセスが使ったCPU・メモリは、この監視の値に入らない（#351）。実行後のCPU時間の検査も、`os.ProcessState`が返す起動したプロセスの値である。
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
