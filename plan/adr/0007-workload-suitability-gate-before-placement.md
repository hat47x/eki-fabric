# ADR-0007 「EKIへ載せない」判断をSchedulerより前のSuitability Gateで行う

- Status: **Proposed**
- Date: 2026-10-06
- Related: RQ-011, RQ-014, RQ-046, RQ-024, Issue #58

## 1. Context

EKIの価値は、多くのWorkerへTaskを送れることではありません。遊休資源を使う意味があるWorkloadだけを、安全にFabricへ入れることにあります（[value.md](../../product/value.md)の「Compute where it is economically sensible」、[principles.md](../../product/principles.md) §5、§6）。

単純に作るなら、受理したTaskをすべてSchedulerへ渡し、Schedulerに最適なWorkerを選ばせる設計になります。この設計には次の問題があります。

- Schedulerは必ずどこかのWorkerを選ぶので、「どこにも置かない方が良い」という答えを返しにくい構造になる。
- たとえば、2GBの入力を読んで10秒だけ計算するTaskでは、どのWorkerを選んでも転送時間が計算時間を上回る。Schedulerが最速のWorkerを選んでも、ローカルで実行するより遅くなる。
- 外部APIへ書き込むTaskでは、Workerが離脱してretryされると二重に書き込まれる（RQ-046）。これは性能の問題ではない。そもそもFabricへ載せてはいけない性質である。
- #19のlearned schedulingのようにSchedulerの性能を上げても、載せるべきでないTaskが混ざったままでは、導入価値を説明する根拠にならない。

## 2. Decision

1. Admission（ADR-0006、ADM-2）の後、Schedulerの前に **Workload Suitability Gate** を置く。Schedulerが決めるのは「どこへ置くか」だけであり、「EKIへ載せるか」はGateが決める。
2. Gateは次の4つのいずれかを返す。
   - `FABRIC_ELIGIBLE`: TaskをSchedulerへ渡す。
   - `LOCAL_OR_EXISTING_EXECUTION`: EKIで実行しない方が良いと判断した。理由を添えて返し、Taskの実行をClient側の既存手段へ戻す。
   - `NEEDS_MEASUREMENT`: 入力量や実行時間の目安など、判定に必要な事実が足りない。足りない事実を推測で埋めて`FABRIC_ELIGIBLE`にはしない。
   - `NOT_SUPPORTED`: 現在のProfileでは安全に扱えない。retryの扱いが決まっていない外部への書き込みや、Data Class上Workerへ渡せない入力がこれにあたる。
3. 最初の実装（EKI-SUIT-0）は、あらかじめ決めた規則だけで判定し、MLや学習済みモデルを使わない。判定に使った事実と規則のversionを記録し、あとから判定理由を説明できるようにする。
4. Gateの入力には、Clientが申告したworkloadの事実も含まれる。ただし、申告はClaimとして扱う（原則 §3）。Clientが申告を偽っても、security propertyは崩れない。Admission、Lease、Local Governorが別にそれを守っているからである。Gateが守るのは効率と導入判断の正しさであり、Gateはsecurity boundaryではない。
5. `LOCAL_OR_EXISTING_EXECUTION`を返すのは失敗ではなく、製品が正しく動いた結果である。この判定も計測し、表示する。

## 3. Consequences

### Positive

- 「分散しない」という答えを、Schedulerのついでではなく、明示した判断として返せる。
- 導入を検討する人へ、どのWorkloadがEKIに向くかを、規則と記録をもとに説明できる。
- Schedulerを評価する際（#19）、Fabricに向くTaskだけを対象にできる。

### Negative / Limitations

- Gateの規則が粗いと、本来EKIで有利になるTaskまで`NEEDS_MEASUREMENT`に回してしまう。初期はこの保守的な振る舞いを許容する。
- Clientが申告を偽れば効率は下がり得る。資源上限（RQ-009、RQ-017）とContributionの記録でこれを抑えるが、Gate単体では防がない。

## 4. Open Questions

- 転送コストと計算で得られる利点を比べる閾値を、どのAcceptance Scenarioから決めるか。
- `NEEDS_MEASUREMENT`になったTaskを管理された資源で試しに実行し、足りない事実を集める経路を持つか。
