# providercontract

Resource Provider（ADR-0009）のアダプタが満たすべき契約を、共通の試験群として定めるパッケージです。対象は `internal/core/resourceprovider` の `ProviderAdapter` と、五つの規則（a）〜（e）です。

## 限界

この試験は**契約レベルのシミュレーション**です。ネットワーク通信を行わず、実際のKubernetesやSlurmのクラスタにも接続しません。合格は、インメモリのフィクスチャが契約に従うことを示すだけで、実際のクラスタが同じように振る舞うことは示しません。Issue #67 は、アダプタが実クラスタに対して動くことを確かめるまで開いたままにします。

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
| `fixture/memory.go` | インメモリの `ProviderAdapter`。`Simulator` で下層の出来事（完了、失敗、先取り）を起こす |
| `fixture/kubernetes.go` | Kubernetes Job に相当する既定値のフィクスチャ |
| `fixture/slurm.go` | Slurm の batch job に相当する既定値のフィクスチャ |
| `fixture/misconfigured.go` | 意図的に誤設定したフィクスチャ。自動再試行を投入ごとに無効化できない |

誤設定フィクスチャは、宣言どおりに振る舞うため共通試験には合格します。問題は配置の段階で現れ、規則（a）がそれを拒否します。

## 五つの規則と対応する関数

| 規則 | 内容 | 関数 |
| --- | --- | --- |
| （a） | 副作用があるか、冪等である根拠がないタスクは、投入ごとに自動再試行を無効化できる提供者にだけ置く | `PlaceWithRetryRule` |
| （b） | 提供者による先取り（preemption）は reclaim であり、失敗ではない | `Classify` |
| （c） | 提供者の成功報告は Claim であり、受理された結果ではない | `ClaimFromEvent` |
| （d） | 提供者が自己申告する管理状態は保証を上げない。管理者のポリシーだけが上げられる | `ManagedForPlacement` |
| （e） | アダプタの資格情報は一つの提供者、一つの partition に限る | `ValidateCredentialScope` |

## フィクスチャの既定値と確認状況

確認日は 2026-10-09 です。ページは WebFetch で取得し、引用は取得した本文に基づきます。ここでの確認は、取得時点の公開ドキュメントに対するものであり、特定の製品の版に対するものではありません。

### Kubernetes Job

出典: https://kubernetes.io/docs/concepts/workloads/controllers/job/

| 項目 | 状態 | 根拠 |
| --- | --- | --- |
| 自動再試行は既定で有効 | 確認済み | `.spec.backoffLimit` は既定で 6 と記載されている |
| backoffLimit は Job ごとに設定でき、投入ごとに指定できる | 確認済み | `.spec.backoffLimit` は Job spec のフィールドである |
| backoffLimit 0 で再試行が止まる | **未確認** | ページは「再試行の回数」と定義するだけで、0 の値を明記していない。フィクスチャでは使っていない |
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
| PreemptMode の既定値 | **未確認** | 今回取得した範囲に含まれていない。フィクスチャはこの既定値を主張しない |
| 再キューの回数の上限 | **未確認** | フィクスチャは回数の上限を設けない |

### 誤設定フィクスチャ

上流の製品の既定値ではありません。「自動再試行が有効で、投入ごとに無効化できない」という、規則（a）が拒否すべき構成を表すために意図的に作っています。

## 規則（a）の確認

`suite_test.go` の `TestRetryNotDisableableFixtureIsRejectedByRuleA` は次を確かめます。

1. 副作用のあるタスクは `PlaceWithRetryRule` で `ErrRetryNotDisableable` となり、配置されない。
2. 規則を迂回して既定の設定で割り当てると、誤設定フィクスチャは失敗を内部の再実行（`requeued`）として報告する。つまり、拒否には実際の根拠がある。

## 未決事項（Open Questions）

1. **既定で再試行が無効だが、投入ごとに無効化できない提供者。** 現在は副作用のあるタスクを拒否する（`PlaceWithRetryRule`）。理由は、管理者の設定で再試行が有効になり得て、EKIは投入の時点でそれを見ることも防ぐこともできないためである。受け入れる場合に必要な実行時の確認は未決である。
2. **冪等性の根拠がないが、副作用もないタスク。** 現在は再試行に敏感として扱う（`TaskProfile.RetrySensitive`）。ADR-0007 は副作用のない純粋計算を予算内で再実行してよいとしている。この差をどう埋めるか（純粋計算を区別する入力を足すか）は未決である。
3. **先取りと下層の再試行。** フィクスチャは先取りを EKI から見た reclaim として報告し、Lease を終える。既定設定の Kubernetes では、中断された Pod の置換が同じ Job 内で起き得る。Slurm でも PreemptMode=REQUEUE の場合は、先取りされたジョブが再キューされ得る。これらを実クラスタでどう観測し、Lease Authority へどう渡すかは未決である（research/orchestrator-integration-and-layering.md 5.1、5.2、RQ-046、RQ-052）。
4. **上流の既定値の版。** 確認はページの公開版に対するもので、版は固定していない。接続するクラスタの実際の版で、既定値を改めて確かめる必要がある。
5. **未確認の値。** backoffLimit 0 の動作、Slurm の PreemptMode の既定値、Slurm の再キュー回数の上限。

## この試験で確かめないこと

- ネットワーク、認証、実際のクラスタの振る舞い、遅延、イベントの欠落。
- RQ-048（期限切れ後の枠の回収時間）と RQ-052（大量の先取り時の再試行の集中）。これらは実クラスタで計測する。
- 資格情報の実際の権限の範囲（RQ-051）。規則（e）は宣言を検査するだけで、実際に何へ届くかは検査しない。
- 結果の受理（RVR-1、RAC-1）。規則（c）は Claim を作るところまでを扱い、受理は扱わない。

## 関連文書

- [ADR-0009 管理された資源は資源プロバイダとして接続する](../../plan/adr/0009-managed-resources-as-providers.md)
- [ADR-0007 外部副作用の再実行規則（暫定）](../../plan/adr/0007-provisional-side-effect-retry-policy.md)
- [既存オーケストレータとの接続と層構造](../../research/orchestrator-integration-and-layering.md)（5.1 から 5.3、5.8）
- 要求: RQ-018、RQ-019、RQ-046、RQ-047、RQ-048、RQ-051、RQ-052（[requirements/PRODUCT_REQUIREMENTS.md](../../requirements/PRODUCT_REQUIREMENTS.md)）
