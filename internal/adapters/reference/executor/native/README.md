# 登録済み実行体を動かすRunner（native Runner）

WorkOrderは実行体のパスも引数も指定しません。指定するのは、承認された実行体のdigestです。このRunnerは、運用者がローカルに登録した「digest → パス・引数・環境変数」の対応を使い、起動のたびにファイルを再ハッシュしてから実行します。

```text
WorkOrder.ExecutorDigest
  -> ローカル登録の照合（未登録なら起動しない）
  -> ファイルを再ハッシュ（一致しなければ起動しない）
  -> 起動（引数と環境変数はローカル設定）
  -> 期限はcontext、出力はbyte上限で止める
  -> Result（終了コード、出力byte数）
```

## 何を守るか

- ClientやWorkOrderが、実行するパス・引数を選べない。
- 登録後にファイルが差し替わった場合、起動しない。
- 期限を過ぎた実行は、contextの取消しで止める。
- 出力がWorkOrderの上限を超えたら止め、`ExitCode=-1`と実際に観測したbyte数（上限を超えた値）を返す。Agentはこれを`output_exceeded`として扱う。
- 非ゼロ終了は、Runnerの故障ではなく「失敗した実行」として`Result`で返す。Agentは`failed`として記録する。

## 何を守らないか

- OSレベルのsandbox（CPU、メモリ、ファイルシステム、ネットワークの分離）。`Sandbox.Profile`の解釈と強制は、プラットフォームProfileの責務です。
- 子プロセスの停止。`exec.CommandContext`は直接の子を止めますが、その子がさらに起動したプロセスは残り得ます。プロセスグループやJob Objectでの強制は別の境界です。
- 環境変数の最小化。`Executor.Env`がnilの場合は親の環境を継承します。運用者は、必要な変数だけを明示してください。
- 出力の内容の検証。byte数だけを見ます。

## 登録の例

```text
digest(sha256) -> {Path: /opt/eki/executors/transform, Args: ["--json"], Env: [...], WorkDir: /var/lib/eki/work}
```

登録は管理者の操作であり、WorkOrderやClientから変更できません。

## テスト

`TestHelperProcess`が、テストバイナリ自身を子プロセスとして起動します。正常終了、非ゼロ終了、出力上限超過、未登録digest、起動前の改ざん検出、context取消しを確認します。
