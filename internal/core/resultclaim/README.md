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

## 出力のダイジェスト

`completed`の申告は、Workerが確定した出力のダイジェスト（`OutputDigest`）を必ず持ちます。Result Verificationは、検証者が確認した出力をこのダイジェストと突き合わせるため、これがないと、何を検証すればよいのかを申告から決められないためです。

- `completed`以外のstatusでは、`OutputDigest`は空でなければなりません。途中までの出力を、結果として扱わないためです。
- ダイジェストは申告の同一性（`Digest`）に含めます。ただし、ダイジェストがない申告の同一性は、この項目を足す前と同じ値のままです。保存済みの古い行を検証し直せます。
- 出力が複数のArtifactに分かれる場合の一覧（名前、個別のダイジェスト、サイズ）は、Artifactの確定の責務です。Coreの申告が束縛するのは、確定した出力を表す一つのダイジェストだけです。通信仕様の`outputs`との往復は、#198で確定します。
- 現在のnative Runnerでは、出力は標準出力と標準エラーを合わせたbyte列で、そのSHA-256を報告します。

## 申告時刻の範囲

開始・終了時刻は符号付き64bit整数のUnixナノ秒で表現できる範囲に限ります。境界は`time.Unix(0, math.MinInt64)`と`time.Unix(0, math.MaxInt64)`で、両端を含みます。範囲外の時刻は`NewClaim`が`ErrInvalidClaim`で拒否します。

`Claim.Digest`は時刻をこの整数のbytesにして計算します。表現範囲を超える時刻を受け付けると、異なる世紀の申告が同じダイジェストになる場合があるため、構築時に検査します。範囲内の申告のダイジェストは従来と同じです。保存済みの範囲外の申告も復元時に拒否し、時刻やダイジェストを自動で書き換えません。

## 決定の入力

`Decide`は、Claimと、Control Planeが同じ操作で読んだ`Existing`（Attemptの存在、開閉、現在のfence、既に記録したClaim）を比べます。Claimだけから状態を推測しません。fenceが現在より古い申告は、記録はしますが、現在のAttemptを閉じたり、新しいAttemptの根拠にしたりしません。
