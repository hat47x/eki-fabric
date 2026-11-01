# ADR-0020 Task State Modelに、期限切れの区別、Task Group、SessionとSandbox、常駐の状態を加える

- 状態: **採用**
- 日付: 2026-10-21
- 関連: Issue [#111](https://github.com/hat47x/eki-fabric/issues/111)、[#126](https://github.com/hat47x/eki-fabric/issues/126)、[#106](https://github.com/hat47x/eki-fabric/issues/106)、[#130](https://github.com/hat47x/eki-fabric/issues/130)、[ADR-0013](0013-product-core-and-faces.md)、[ADR-0014](0014-authorities-for-delegation-budget-capacity.md)、[ADR-0019](0019-sandbox-face-isolation-levels-and-frontends.md)、[Task State Model](../../architecture/TASK_STATE_MODEL.md)、RQ-011、RQ-017、RQ-019、RQ-055、RQ-060、RQ-062

## 1. 背景

RQ-055で、仕事の型を「投げる（TaskとTask Group）」「つなぐ（Session）」「隔離して試す（Sandbox）」「住まわせる（常駐）」の四つと定めました。RQ-011には、期限までに配置または完了しなかったTaskの扱いを、「実行されていないことが確か」と「実行されたかどうか分からない」に分けて返すことを加えました。

Task State Model（Baseline 0.1）は、TaskとAttemptだけを状態として持っています。Task Group、Session、Sandbox、常駐の状態がなく、期限切れの区別も§47で「Task Contractで決める」としたままでした。TEIの投影Spikeでは、3秒の期限を付けたTaskが5秒後も`queued`のまま変わりませんでした。

## 2. 決定

1. 期限切れを、送り出しの記録で区別する。§16（Commit before dispatch）により、WorkOrderを送り出す前に必ずその事実を記録している。期限が来た時点で、そのTaskのどのAttemptにも送り出しの記録がなければ、「実行されていないことが確か」として`EXHAUSTED`で終え、公開の`terminal_code`を`DEADLINE_EXPIRED_NOT_STARTED`とする。送り出しの記録があるAttemptが残っていれば、「実行されたかどうか分からない」とし、副作用のない仕事は`DEADLINE_EXPIRED_AFTER_DISPATCH`で終え、副作用の分からない仕事は既存の`reconciling`へ回す。期限で終えたTaskは、未完了の依頼の上限の予約を解放する。
2. Task Groupを、Taskの集まりの状態として持つ。Groupは予算の枠と期限を持ち、所属するTaskの予算はGroupの枠から予約する。Groupの状態は、所属するTaskの終わり方（完了、失敗、実行されないまま期限切れ、実行されたか分からない）を数えて返す。Groupの取り消しは、所属するすべてのTaskへのCancellation Intentとする。
3. SessionとSandboxを、期限のある枠の状態として持つ。枠には、開く、開いている、閉じる、閉じたの段階を置く。枠の中の小さな依頼（Invocation）は、Taskにはしない。依頼ごとに、入力と出力のdigestと、検証したかどうかを来歴として記録する。枠を閉じた理由（依頼者が閉じた、期限、失効、reclaim）を記録する。Sandboxは、枠を閉じたときに実行環境を破棄し、破棄したという報告はClaimとして記録する。
4. 常駐を、期間の連なりとして持つ。各期間は新しいLeaseであり、FenceEpochを進める。同じ期間の中での延長（§18のLease Renewal）とは区別する。次の期間へ移る判断では、次の期間の予算の予約、委任先と実行体の失効、現在のPolicy、業務端末で常駐に回してよい上限を確かめる。持ち主の拒否権で止められた期間は中断として記録し、次の期間で再開できる。古い期間のLeaseで動く実行者の出力は確定しない。
5. Task State ModelをBaseline 0.2へ上げる。

## 3. 移行

- M0とMVPは、TaskとAttemptだけで進めてよい。新しい状態は、ADR-0013の段階で、それぞれの型を実装するときに加える。
- 期限切れの区別（決定1）は、いまの`eki-control`の公開状態にも当てはまる。期限を過ぎた`queued`のTaskを`EXHAUSTED`で終える処理は、スケジューラの実装と合わせて加える。

## 4. 影響

### 良い点

- 依頼者が、期限切れのTaskを安全に再投入してよいか（実行されていないことが確か）を判断できる。
- Task Group、Session、Sandbox、常駐が、既存のTask、Attempt、Leaseの考え方の延長として表され、fencingと予算の考え方を共有できる。

### 悪い点と制約

- 状態の種類が増え、保存と回復（§55、§56）の対象が広がる。
- Session内の小さな依頼をTaskにしないため、依頼ごとの監査はTaskより粗くなる。粗さの程度は、#106で決める。

## 5. 未決事項

- 期限切れの`terminal_code`を、外部契約（openapi.json）へどう出すか。公開する値の集合と、依頼元が再投入してよいかの区別は`TaskStatus.terminal_code`に定めた。期限切れの2値（`DEADLINE_EXPIRED_NOT_STARTED`・`DEADLINE_EXPIRED_AFTER_DISPATCH`）は、その一覧への互換な追加として後から足す（#281、#111）。
- Session内の依頼の識別子と、監査の粒度（#106）。
- 常駐の期間の長さの既定値と、業務端末で常駐に回してよい上限の既定値（#130）。
