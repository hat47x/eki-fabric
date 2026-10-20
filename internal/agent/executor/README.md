# 実行の監督（Executor supervisor）

開始を許された後、Agentは実行を始め、終わるまで見張ります。このパッケージは、その間の「いつ終わるか」「なぜ終わったか」を一か所にまとめます。

```text
startgate.Plan
  -> SpecFromPlan
  -> Runner.Run（上限の適用はRunnerの責務）
       ├ 完了
       ├ 失敗
       ├ Local GovernorのStopRunning -> 停止
       ├ 期限                         -> 停止
       └ 呼出し側のcancel             -> 停止
  -> Outcome
```

## Runnerとの契約

`Runner.Run`は、渡された`Spec`のlimitsとsandboxを自分で適用し、`ctx`が取り消されたら速やかに返ります。Agentは、返らないRunnerを強制的に止める手段をこの層では持ちません。`StopGrace`を過ぎても返らない場合は`StatusRunnerStuck`として記録し、端末側の強制停止（Job Object、cgroup等）は別の境界に任せます。

## Outcomeの意味

| Status | 意味 | 回復への渡し方 |
|---|---|---|
| `completed` | 正常終了 | 結果の検証へ |
| `failed` | 異常終了、または出力上限超過 | Taskの信頼性契約に従う |
| `reclaimed` | Local Governorが停止した | 実行権限の失効。`reclaimed`として閉じ、回復へ（ADR-0015） |
| `deadline_exceeded` | 期限に達した | 再実行可能かはTaskの契約とattempt budgetで決める |
| `cancelled` | 呼出し側が取り消した | 意図的な停止。自動retryの対象にしない |
| `runner_stuck` | 猶予内に停止しなかった | 端末側の強制停止が必要。結果は信用しない |

`StatusReclaimed`は、Runnerがその直前に正常終了していた場合でも、停止の観測を優先します。利用者の復帰や熱の上昇で止めた事実を、完了として消さないためです。

## 認可ではない

`Spec`は、実行してよい根拠ではありません。開始の根拠は`startgate.Plan`であり、その前の`Admission`とWorkOrderの検証があります。このパッケージは、その判断をやり直しません。したがって、`Spec`を独自に組み立てて実行を始めてはいけません。`SpecFromPlan`を使ってください。

## まだ残っていること

- Native / Container / WASM / BrowserのRunner実装。
- 実行中のcheckpointと再開。
- `runner_stuck`の後の強制停止と、Attemptの状態遷移。
- 出力の内容の検証。この層はbyte数だけを見ます。
- `Outcome`をControl Planeへ報告する経路（ResultClaim）。
