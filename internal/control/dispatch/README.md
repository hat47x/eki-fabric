# 配置ループ（placement loop）

`admitted`のTaskを`ready`に進め、`ready`のTaskを、配置の判断（suitability → Scheduler → Placement Approval → Lease → WorkOrder）まで一歩進めます。Control Planeの実行系の入口です。

## 1回のtickで行うこと

```text
1. admittedのTaskをreadyへ進める（ADR-0032 §2.2）
2. readyのTaskを一覧し、1件ずつ配置を進める
3. 進まなかったTaskは、理由付きでDeferredに積む（tickは失敗させない）
```

`admitted`と`ready`を分けるのは、受理（要求を受け付けるか）と配置（いま始めてよいか）の判断が別だからです。policyの現在性、予算、容量、開始時刻の指定は、受理の時点では確定しません。

## `admitted` → `ready`

条件（すべて満たすときだけ進める）:

- 取消の意図（`CancellationRequested`）が無いこと。取消が来たTaskは開始しない。
- policyが読めて`active`であること。読めない・`active`でない場合は、進めずに理由付きで`Deferred`へ積む。
- 書き込みはcompare-and-swap（期待revision付き）。競合したら何も書かず、次のtickで読み直す。

開始時刻の指定（`NotBefore`など）を見る条件は、Taskの内容の保存（#265）と合わせて実装します。

## `ready` → `active`

最初のAttemptを作った直後に、Taskを`active`にします（ADR-0032 §2.3）。Attemptの作成とTaskのphaseを別のtickに分けると、「AttemptはあるがTaskは`ready`」という、`TASK_STATE_MODEL` §5の意味と合わない状態が外から見えます。

`active`への書き込みが失敗しても、Attemptは残ります。次のtickが`PLACING`のAttemptを見つけ、配置の続きを進めます。

## `Deferred`の理由

| 理由 | 意味 |
| --- | --- |
| `no usable worker is registered` | 受け入れ可能なWorkerが居ない。Workerの登録（enrollment）を待つ。 |
| `policy is not available` / `policy <rev> is <status>, not active` | policyが読めない、または停止している。管理者の操作を待つ。 |
| `cancellation was requested` | 取消の意図がある。このTaskは開始しない。 |
| `attempt already holds a lease` | 前のtickがLeaseまで進めている。Lease側の続きで扱う。 |

Deferredは失敗ではありません。次のtickが同じTaskを読み直し、同じ判断をやり直します。

## 何を保証しないか

- **1件も進まないtick**がありえます（Workerが居ない、policyが止まっている）。その間、Taskは`queued`のままです。
- **公平性**は、一覧の件数上限（既定8）に依存します。上限で切られる場合に古い順へ進めるかは未決です（#267）。
- **実行の完了**は、このループの仕事ではありません。`active`→`verifying`と`active`→`ready`は、結果の受理と回復の経路が担当します（#267）。
- 書き込みはState StoreのCASが守ります。表に無い遷移は`taskstate.ErrInvalidTaskTransition`、revisionの競合は`statestore.ErrConflict`です（#280、ADR-0032 §2.6）。

## 関連

- 上の判断の正本: [ADR-0032](../../../plan/adr/0032-task-phase-transition-subjects.md)、`architecture/TASK_STATE_MODEL.md` §5。
- 状態の検証: `internal/core/taskstate/transition.go`。
- 実行系の配線: `internal/control/service`（配置ループを有効にするかは設定`dispatch`で決める）。
