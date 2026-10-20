# 実行結果の申告（ResultClaim）

WorkerがAttemptを終えたとき、その終わり方をControl Planeへ報告します。この報告は**申告**であり、結果の確定ではありません（RQ-019）。Control Planeは、申告を記録し、Attemptの状態を進め、`completed`だけをResult Verificationへ送ります。

```text
WorkerのOutcome
  -> ResultClaim（Task / Attempt / Lease / fence / WorkOrder / executor / nonce / status）
  -> Decide（今のAttempt / Leaseの状態と比較）
       new        : 初回。記録し、Attemptを閉じる
       duplicate  : 同じ申告の再送。何も変えない
       stale      : fenceが古い。記録だけ残し、Attemptを閉じない
       late       : Attemptは既に閉じている。記録だけ残し、再開しない
       conflict   : 同じAttemptに異なる申告。拒否する（最初の申告が勝つ）
```

## 状態ごとの扱い

| Status | Attemptを閉じる | Result Verificationへ |
|---|---|---|
| `completed` | いいえ（Verifyingへ） | はい（確定ではない） |
| `failed` | はい | いいえ |
| `reclaimed` | はい | いいえ。回復の境界へ |
| `deadline_exceeded` | はい | いいえ |
| `cancelled` | はい | いいえ。意図的な停止 |
| `output_exceeded` | はい | いいえ |
| `runner_stuck` | **いいえ** | いいえ。先に端末側で止める |

`runner_stuck`でAttemptを閉じないのは、まだプロセスが動いている可能性があるためです。強制停止を確認した後に、改めて申告します。

## 確かめないこと

- 申告の内容が正しいこと。`completed`は、Workerがそう申告したという意味であり、結果の正しさではありません。
- Leaseの復活。期限切れのLeaseに対して遅れて届いた申告でも、Leaseを復活させません。
- Result Verificationの結果。`completed`の申告は、検証の入力になるだけです。
- 実行時間や出力byte数の独立した観測。Workerの自己申告として記録し、必要な場合は別のEvidenceと突き合わせます。

## 決定の入力

`Decide`は、Claimと、Control Planeが同じ操作で読んだ`Existing`（Attemptの存在、開閉、現在のfence、既に記録したClaim）を比べます。Claimだけから状態を推測しません。fenceが現在より古い申告は、記録はしますが、現在のAttemptを閉じたり、新しいAttemptの根拠にしたりしません。
