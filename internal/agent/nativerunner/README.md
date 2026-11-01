# native runner（production adapter）

`executor.Runner`の固定コマンド実装です。Spike `spikes/native-runner` を本番パッケージへ移し、テストを同じ場所で維持します。

## 実行するもの

- 信頼済み設定の固定コマンドと引数だけを実行する。
- 期限・取消し・出力上限・プロセスツリー停止を扱う。
- CPU・メモリ・一時ディスクを20ms間隔で監視し、上限超過時にプロセスツリーを停止する（Linux/Windows）。
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
