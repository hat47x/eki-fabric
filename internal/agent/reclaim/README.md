# 実行結果を回復へ渡す（Outcome to interruption）

実行の終わり方は、すべてが「再実行してよい失敗」ではありません。このパッケージは、`executor.Outcome`を、回復の境界（`recovery.Classify`）へ渡してよい中断だけに変換します。

| Outcome | 扱い |
|---|---|
| `reclaimed` | 中断。Local Governorによる実行権限の失効として回復へ渡す |
| `failed` | 中断。Taskの契約とattempt budgetの範囲で再実行を検討する |
| `deadline_exceeded` | 中断。期限と予算の範囲で再実行を検討する |
| `cancelled` | 自動retryしない（意図的な停止） |
| `runner_stuck` | 自動retryしない（先に端末側で停止する必要がある） |
| `completed` | 回復へ渡さない |
| `output_exceeded` | 自動retryしない（同じ上限では再び超える） |

```text
executor.Outcome + Attempt（Taskの契約）
  -> FromOutcome
  -> recovery.Interruption
  -> recovery.Classify（attempt budget / deadline / 時間窓の予算）
```

`FromOutcome`は、新しいAttemptを許可しません。許可するかどうかは`recovery.Classify`が決めます。`reclaimed`であっても、Taskがretryableでなければ保留になり、attempt budgetを使い切っていれば再実行しません。

## 意図的な停止をretryにしない理由

`cancelled`は、利用者や上位の判断で止めたものです。これを自動retryすると、止めた意味が失われます。`runner_stuck`も同じで、まだ動いている実行が残っている可能性があるため、強制停止と状態の確認より先に新しいAttemptを始めません。

## 契約の出どころ

`Attempt.Contract`は、WorkOrderやOutcomeから作らず、耐久化されたTaskの状態から読みます。WorkOrderの上限や、Runnerの自己申告から、retryableやmax attemptsを決めません。
