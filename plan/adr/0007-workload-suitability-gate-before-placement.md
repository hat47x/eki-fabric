# ADR-0007 — 「EKIへ載せない」判断をSchedulerより前のSuitability Gateで行う

- Status: **Proposed**
- Date: 2026-10-06
- Related: RQ-011, RQ-014, RQ-046, RQ-024, Issue #58

## 1. Context

EKIの価値は、多くのWorkerへTaskを送れることではなく、遊休資源を使うことに意味があるWorkloadだけを安全にFabricへ入れることにあります（[value.md](../../product/value.md)「Compute where it is economically sensible」、[principles.md](../../product/principles.md) §5、§6）。

単純な方法として、受理したTaskをすべてSchedulerへ渡し、Schedulerが「最適なWorker」を選ぶ設計が考えられます。この設計では次の問題が起きます。

- Schedulerは必ず何かのWorkerを選ぶため、「どこにも置かない方が良い」という答えを構造的に返しにくい。
- 例: 2GBの入力を読んで10秒だけ計算するTaskは、どのWorkerを選んでも転送時間が計算時間を上回る。Schedulerが最速のWorkerを選んでも、ローカル実行より遅い。
- 例: 外部APIへ書き込むTaskは、Worker離脱でretryされると二重書き込みになる（RQ-046）。性能の問題ではなく、Fabricへ載せてはいけない性質である。
- Scheduler性能の改善（#19のlearned scheduling等）を進めても、そもそも載せるべきでないTaskが混ざっていれば、導入価値の説明にならない。

## 2. Decision

1. Admission（ADR-0006、ADM-2）の後、Schedulerの前に **Workload Suitability Gate** を置く。Schedulerは「どこへ置くか」だけを決め、「EKIへ載せるか」は決めない。
2. Gateの判定結果は次の4値とする。
   - `FABRIC_ELIGIBLE` — Schedulerへ渡す。
   - `LOCAL_OR_EXISTING_EXECUTION` — EKIで実行しない方が良い。理由を返し、Client側の既存手段へ戻す。
   - `NEEDS_MEASUREMENT` — 判定に必要な事実（入力量、実行時間の目安等）が足りない。推測で`FABRIC_ELIGIBLE`にしない。
   - `NOT_SUPPORTED` — 現在のProfileでは安全に扱えない（例: retry semanticsのない外部side effect、Data Class上Workerへ渡せない入力）。
3. 最初の実装（EKI-SUIT-0）はdeterministicなルールだけで構成し、MLや学習済みモデルを使わない。判定に使った事実と規則のversionを記録し、後から説明できるようにする。
4. 判定の入力はClientが申告したworkload factsを含むが、申告はClaimである（原則 §3）。申告を偽ってもsecurity propertyは崩れない（Admission、Lease、Local Governorが別に守る）。Gateが守るのは**効率と導入判断の正しさ**であり、security boundaryではない。
5. `LOCAL_OR_EXISTING_EXECUTION`を返すことは失敗ではなく、正常な製品動作として計測・表示する。

## 3. Consequences

### Positive

- 「分散しない」答えを、Schedulerの副作用ではなく明示的な判断として返せる。
- 導入検討者へ、どのWorkloadがEKI向きかを規則と記録で説明できる。
- Schedulerの評価（#19）を、Fabric向きのTaskに限定した母集団で行える。

### Negative / Limitations

- Gateの規則が粗いと、本来有利なTaskを`NEEDS_MEASUREMENT`へ落とす。初期は保守的側へ倒すことを許容する。
- Clientが申告を偽れば効率は悪化し得る。これは資源上限（RQ-009、RQ-017）とContribution記録で抑えるが、Gate単体では防がない。

## 4. Open Questions

- 転送コストとcompute benefitの比較に使う閾値をどのAcceptance Scenarioで決めるか。
- `NEEDS_MEASUREMENT`のTaskを、管理された資源で試験実行して事実を集める経路を持つか。
