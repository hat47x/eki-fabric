# Native Runner feasibility spike

固定コマンドを`executor.Runner`として実行できるかだけを確認するspikeです。

**sandboxではありません。** コマンドと引数は信頼された設定であり、Task入力から組み立てません。CPU・メモリ・network・一時disk・強制停止は適用しません。未信頼のTaskに使わないでください。

## 確認すること

- `context`の取消しでコマンドが停止する。
- 終了コードを返す。
- stdout/stderrを上限byteまで保持し、上限超過を`ErrOutputCeiling`として返す。
- 正常終了時に出力のSHA-256を返す。
- `executor.Supervisor`との接続で、正常終了を`completed`、終了コード7を`failed`として観測する。
- `SandboxPolicy.Profile`が`native-fixed-spike`以外なら拒否する。
- network bytes limitや一時disk limitが非ゼロなら拒否する。実装していない上限を黙って無視しない。
- `ExecutableSHA256`が設定されていれば、実行ファイルのSHA-256を照合し、不一致なら実行しない。
- 子プロセスへ親の環境変数を継承せず、`Runner.Env`で明示した値だけを渡す。
- Supervisorの期限到達を`deadline_exceeded`、呼出し側の取消しを`cancelled`として観測する。

## 実行

```text
go test ./... -count=1
```

## 限界

- sandbox、cgroup、Job Object、CPU/メモリ制限は未実装。
- 実行可能ファイルのdigest照合、引数の許可リスト、環境変数の除去は未実装。
- `runner_stuck`後の強制停止、checkpoint、結果の意味検証は未実装。
- このspikeの成功は、Native Runnerの採用や、未信頼Taskの安全な実行を意味しない。
