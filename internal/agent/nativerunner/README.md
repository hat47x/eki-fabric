# native runner（production adapter）

`executor.Runner`の固定コマンド実装です。Spike `spikes/native-runner` を本番パッケージへ移し、テストを同じ場所で維持します。

## 実行するもの

- 信頼済み設定の固定コマンドと引数だけを実行する。
- 期限・取消し・出力上限・プロセスツリー停止を扱う。
- CPU・メモリ・一時ディスクを20ms間隔で監視し、上限超過時にプロセスツリーを停止する（Linux/Windows）。
- 実行ファイルのSHA-256、引数allowlist、sandbox profile、network/GPU/digestの不一致をfail-closedで拒否する。

## 実行しないもの

- Task入力からコマンドを組み立てない。
- sandboxを提供しない。
- networkとGPUの上限を強制しない。一時ディスクは実行ごとの一時ディレクトリ配下のみを対象とする。
- 信頼できないTaskに使わない。

## テスト

```text
go test ./internal/agent/nativerunner -count=1
```

Windowsでは`taskkill /T /F`、Unixではprocess groupの`SIGKILL`を使います。
