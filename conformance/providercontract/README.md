# providercontract

Resource Provider（ADR-0009）のアダプタが満たすべき契約を、共通の試験群として定めるパッケージです。対象は `internal/core/resourceprovider` の `ProviderAdapter` と、五つの規則（a）〜（e）、そして期限切れから枠の回収までの時間を求める関数（RQ-048）です。

## 限界

この試験は**契約レベルのシミュレーション**です。ネットワーク通信を行わず、実際のKubernetesやSlurmのクラスタにも接続しません。合格は、インメモリのフィクスチャが契約に従うことを示すだけで、実際のクラスタが同じように振る舞うことは示しません。回収時間の関数は二つの時刻の差を求めるだけで、実クラスタの遅延は測りません。Issue #67 は、アダプタが実クラスタに対して動くことを確かめ、遅延を実測するまで開いたままにします。

## 実行

```
go test -count=1 -v ./internal/core/resourceprovider/
go test -count=1 -v ./conformance/providercontract/...
```

## 構成

| パス | 役割 |
| --- | --- |
| `suite.go` | 共通の試験 `RunContract`。テストごとに `Factory` から新しいフィクスチャを受け取る |
| `suite_test.go` | 二つの既定フィクスチャに共通試験を適用する。誤設定フィクスチャに対して規則（a）が配置を拒否することを確かめる |
| `retry_limit_test.go` | 自動再試行の上限（`RetryLimit`）の意味を確かめる。上限 0 を含む |
| `fixture/memory.go` | インメモリの `ProviderAdapter`。`Simulator` で下層の出来事（完了、失敗、先取り）を起こす |
| `fixture/kubernetes.go` | Kubernetes Job に相当する既定値のフィクスチャ。`backoffLimit` を指定するコンストラクタも持つ |
| `fixture/slurm.go` | Slurm の batch job に相当する既定値のフィクスチャ |
| `fixture/misconfigured.go` | 意図的に誤設定したフィクスチャ。自動再試行を投入ごとに無効化できない |

`internal/core/resourceprovider` の関連ファイル:

| パス | 役割 |
| --- | --- |
| `result.go` | 規則（c）の接続。提供者の成功を `resultaccept` の入力へ変換し、検証結果とともに判定する |
| `reclaim.go` | 期限切れから枠の回収までの時間を、二つの時刻から求める（RQ-048） |

誤設定フィクスチャは、宣言どおりに振る舞うため共通試験には合格します。問題は配置の段階で現れ、規則（a）がそれを拒否します。

## 五つの規則と対応する関数

| 規則 | 内容 | 関数 |
| --- | --- | --- |
| （a） | 副作用があるか、冪等である根拠がないタスクは、投入ごとに自動再試行を無効化できる提供者にだけ置く | `PlaceWithRetryRule` |
| （b） | 提供者による先取り（preemption）は reclaim であり、失敗ではない | `Classify` |
| （c） | 提供者の成功報告は Claim であり、受理された結果ではない。受理には独立した検証結果が必要である | `ClaimFromEvent`、`ResultClaimFromProvider`、`DecideProviderResult` |
| （d） | 提供者が自己申告する管理状態は保証を上げない。管理者のポリシーだけが上げられる | `ManagedForPlacement` |
| （e） | アダプタの資格情報は一つの提供者、一つの partition に限る | `ValidateCredentialScope` |

## フィクスチャの既定値と確認状況

確認日は 2026-10-09 です。ページは WebFetch で取得しました。ここに載せる引用は、WebFetch が返した本文に含まれる文言です。取得した原本との一字一句の照合は行っていません。確認は取得時点の公開ドキュメントに対するものであり、特定の製品の版に対するものではありません。

### Kubernetes Job

出典: https://kubernetes.io/docs/concepts/workloads/controllers/job/

| 項目 | 状態 | 根拠 |
| --- | --- | --- |
| 自動再試行は既定で有効 | 確認済み | `.spec.backoffLimit` は既定で 6 と記載されている（「The `.spec.backoffLimit` is set by default to 6」） |
| backoffLimit は Job ごとに設定でき、投入ごとに指定できる | 確認済み | `.spec.backoffLimit` は Job spec のフィールドである |
| backoffLimit 0 で再試行が止まる | **未確認** | 2026-10-09 に「Pod backoff failure policy」の節を再確認した。節は「If either of the calculations reaches the `.spec.backoffLimit`, the Job is considered failed.」と述べるが、値 0 を明記していない。フィクスチャは 0 を表せるが、実クラスタで 0 がどう振る舞うかは仮定である |
| restartPolicy OnFailure のコンテナ再起動も backoffLimit に数えられる | 確認済み。未モデル | 再起動された試行は `backoffLimit` の計数に入ると記載されている。フィクスチャは Pod 単位の再試行だけを扱う |
| podFailurePolicy の DisruptionTarget を無視するルール | 確認済み。未モデル | 既定では各 Pod の失敗が backoffLimit に数えられ、ルールで先取りなどの中断を除外できると記載されている |

### Slurm

出典: https://slurm.schedmd.com/slurm.conf.html、https://slurm.schedmd.com/sbatch.html

| 項目 | 状態 | 根拠 |
| --- | --- | --- |
| クラスタ全体の JobRequeue の既定値は 1 | 確認済み | slurm.conf に「The default value is 1.」と記載 |
| `sbatch --no-requeue` でジョブごとに再キューを止められる | 確認済み | sbatch に「never be requeued under any circumstances」と記載 |
| PrologFlags の ForceRequeueOnFail は `--no-requeue` を上書きできる | 確認済み。未モデル | sbatch に、ForceRequeueOnFail が `--no-requeue` を上書きし得ると記載 |
| PreemptMode=REQUEUE では、先取りされたジョブを可能な場合に再キューする | 確認済み。未モデル | slurm.conf に「requeuing them (if possible) or canceling them」と記載。`--requeue` か JobRequeue=1 が必要 |
| PreemptMode の既定値は OFF | 確認済み | slurm.conf の PreemptMode の項で、OFF について「Is the default value and disables job preemption and gang scheduling.」と記載。既定の構成では先取りが起きない。フィクスチャの先取りは、PreemptMode を設定したクラスタを想定した出来事である |
| 自動再キューの回数の上限（MaxBatchRequeue）の既定値は 5 | 確認済み。未モデル | slurm.conf の MaxBatchRequeue の項に「Maximum number of times a batch job may be automatically requeued before being marked as JobHeldAdmin.」、既定値について「The default value is 5.」と記載。フィクスチャの再試行が、この上限に数えられる自動再キューに当たるかは未確認である。そのためフィクスチャは上限を設けない（`RetryLimit` は nil） |

slurm.conf と sbatch の版は固定していません。上の既定値は取得時点の公開ページに対するものです（未決事項 4）。

### 誤設定フィクスチャ

上流の製品の既定値ではありません。「自動再試行が有効で、投入ごとに無効化できない」という、規則（a）が拒否すべき構成を表すために意図的に作っています。

## 再試行の上限（RetryLimit）

フィクスチャの `Config.RetryLimit` は `*int` で、三つの値の意味を分けます。

| 値 | 意味 | 用途 |
| --- | --- | --- |
| nil | 上限をモデルしない。再試行は続く | Slurm-like（上限は未モデル） |
| 0 | 自動再試行をしない。最初の失敗が最終 | Kubernetes の backoffLimit 0 |
| n > 0 | 最大 n 回再試行し、その後の失敗が最終 | Kubernetes の既定値は 6 |

以前は 0 を「上限なし」と読んでいたため、backoffLimit 0 を表せませんでした。今は 0 を「再試行しない」と読みます。負の値は `New` が拒否します。

共通試験の規則 `declared-default-retry-matches-observed` は、既定で再試行が有効なら内部の再試行が起きることを期待します。そのため上限 0 のフィクスチャには `RunContract` を適用しません。上限 0 の振る舞いは `retry_limit_test.go` が確かめます。

## 規則（c）の確認

規則（c）は、提供者の Claim が受理された結果になるまでの経路を確かめます。

`ResultClaimFromProvider` は、成功の Claim を `resultaccept.Decide` の入力である ResultClaim に変換します。変換は検証を加えず、判定もしません。`DecideProviderResult` は、変換した入力と呼び出し側が渡した検証結果を `resultaccept.Decide` に渡します。

変換に必要な値（Task、Attempt、Worker、出力の digest、identity の dimension）は、`ResultBinding` として信頼境界から渡します。提供者の Claim には、これらを表す項目がないためです。

`result_test.go` は次を確かめます。

1. 成功の Claim に検証結果がなければ、結果は held になり、accepted にならない。また、変換が検証を作り出していないことを、判定の根拠が空であることで確かめる。
2. 独立した検証者が受理したときだけ accepted になる。提供者本人と同じ principal の検証者は根拠にならず、不一致の検証があれば held になる。
3. 失敗の Claim や、別の lease の Claim は変換しない。

## 回収時間（RQ-048）

`ReclaimLatency(expiredAt, reclaimedAt)` は、期限切れから枠の回収までの時間を、二つの時刻の差として返します。関数は時計を読まず、I/O も行いません。

- 回収が期限より前なら `ErrReclaimBeforeExpiry` を返します。
- 差が `time.Duration` の範囲（約 292 年）を超えるなら `ErrReclaimLatencyOverflow` を返します。`time.Time.Sub` は範囲を超えると最大値を返すため、そのままでは誤った値になるからです。

この関数は、与えられた二つの時刻の差を求めるだけです。実クラスタで期限切れと回収の時刻を観測し、遅延を測ることは未決で、Issue #67 に残します。

## この試験で確かめないこと

- ネットワーク、認証、実際のクラスタの振る舞い、イベントの欠落。
- 回収時間の実測。RQ-048（期限切れ後の枠の回収時間）と RQ-052（大量の先取り時の再試行の集中）は、実クラスタで計測する。
- 資格情報の実際の権限の範囲（RQ-051）。規則（e）は宣言を検査するだけで、実際に何へ届くかは検査しない。
- 結果の受理の実際（RVR-1、RAC-1）。規則（c）の試験は、変換と `resultaccept` への接続、および検証結果がない場合に受理されないことを確かめる。しかし、実際の検証者（アプリケーションの validator や独立した再実行）が何を検証するかは確かめない。

## 未決事項（Open Questions）

1. **既定で再試行が無効だが、投入ごとに無効化できない提供者。** 現在は副作用のあるタスクを拒否する（`PlaceWithRetryRule`）。理由は、管理者の設定で再試行が有効になり得て、EKIは投入の時点でそれを見ることも防ぐこともできないためである。受け入れる場合に必要な実行時の確認は未決である。
2. **冪等性の根拠がないが、副作用もないタスク。** 現在は再試行に敏感として扱う（`TaskProfile.RetrySensitive`）。ADR-0007 は副作用のない純粋計算を予算内で再実行してよいとしている。この差をどう埋めるか（純粋計算を区別する入力を足すか）は未決である。
3. **先取りと下層の再試行。** フィクスチャは先取りを EKI から見た reclaim として報告し、Lease を終える。既定設定の Kubernetes では、中断された Pod の置換が同じ Job 内で起き得る。Slurm でも PreemptMode=REQUEUE の場合は、先取りされたジョブが再キューされ得る。これらを実クラスタでどう観測し、Lease Authority へどう渡すかは未決である（research/orchestrator-integration-and-layering.md 5.1、5.2、RQ-046、RQ-052）。
4. **上流の既定値の版。** 確認はページの公開版に対するもので、版は固定していない。接続するクラスタの実際の版で、既定値を改めて確かめる必要がある。
5. **未確認の値。** 残る未確認は backoffLimit 0 の実際の動作だけである。2026-10-09 に次を確認した。Slurm の PreemptMode の既定値は OFF（「Is the default value and disables job preemption and gang scheduling.」、出典 https://slurm.schedmd.com/slurm.conf.html）。自動再キューの上限 MaxBatchRequeue の既定値は 5（「The default value is 5.」、同じ出典）。ただし、フィクスチャがこの上限を再試行として扱うかは未決である。
6. **管理された状態の宣言（DeclaredManaged）。** `ManagedForPlacement` は管理者のポリシーだけを読み、`Declaration.DeclaredManaged` は読まない。`DeclaredManaged` を読む箇所はリポジトリにない。`ManagedForPlacement` を呼ぶのは本パッケージの試験と共通試験（`suite.go`）だけで、本番コードからの呼び出しはない。今回は実装しない。決めるべきことは、フィールドを監査用の記録として残すか削除するか、管理者ポリシーをどの権限主体（Policy authority）から得るか、である。
7. **backoffLimit 0 と投入ごとの無効化の対応。** フィクスチャでは backoffLimit 0 と RetryDisabled は、どちらも再試行をしない。アダプタが RetryDisabled を backoffLimit 0 に写すかは未決である。また、共通試験は既定の有無だけを見るため、上限 0 を含む宣言をどう検査するかも未決である。
8. **出力 digest の出所。** 提供者の Claim には出力の内容がない。`ResultBinding.OutputDigest` を誰が計算するか（EKI が出力を取得して計算するか、Worker 側の報告を検証するか）は未決である。提供者本人と Worker identity の関係も、同じ問いに含まれる。
9. **回収時間の実測。** `ReclaimLatency` は時刻の差を返すだけである。実クラスタで期限切れと回収の時刻をどう観測するかは、Issue #67 で未決である。

## 関連文書

- [ADR-0009 管理された資源は資源プロバイダとして接続する](../../plan/adr/0009-managed-resources-as-providers.md)
- [ADR-0007 外部副作用の再実行規則（暫定）](../../plan/adr/0007-provisional-side-effect-retry-policy.md)
- [既存オーケストレータとの接続と層構造](../../research/orchestrator-integration-and-layering.md)（5.1 から 5.3、5.8）
- 要求: RQ-018、RQ-019、RQ-046、RQ-047、RQ-048、RQ-051、RQ-052（[requirements/PRODUCT_REQUIREMENTS.md](../../requirements/PRODUCT_REQUIREMENTS.md)）
