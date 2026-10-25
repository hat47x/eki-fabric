# 回収後の再投入（Recovery admission）

Workerや端末の利用者が資源を取り戻すと、実行中のAttemptは権限を失います（ADR-0015）。このpackageは、閉じたAttemptを再投入の待ち行列に積み、時間窓の予算の中で解放し、許可証（Permit）から次のAttemptを作る境界です。判断そのものは`recovery`の純粋な関数が行い、このpackageはState Storeの原子的な操作で、その結果を耐久化します。

```text
閉じたAttempt -> Intake  -> 待ち行列に積む（または保持する）
待っている再投入 -> Release -> Permit（窓の予算の範囲で）
Permit        -> Admit   -> 次のAttempt（CREATEDで作る）
```

## 信頼できる入力

- 再投入してよいか、何回までか、期限、優先度は、受付済みTaskの契約（`ContractSource`）から読みます。Workerの申告や、失敗したAttemptからは読みません。読めないときは、再投入可能とは読まずにエラーにします。
- 回収されたかどうかは、Attemptの閉じ方から導きます。再投入できるのは、中断（`reclaimed`、`worker_lost`、`lease_expired`、`deadline`など）で閉じたAttemptだけです。結果が決まった閉じ方や取消しは対象外です。
- 方針（`PolicySource`）は、窓の長さ、件数・転送量・計算量の予算、Permitの有効期間を決めます。

## 何を守るか

- 同じAttemptを何度積んでも、待っている再投入は一つで、開始時刻（`NotBefore`）は動きません。
- 窓の予算とsequenceはState Storeが持ちます。再起動しても、二つ目の呼び出しがあっても、窓は巻き戻りません。別の処理が先に確定した場合は`ErrStateChanged`を返すので、呼び出し側がやり直します。
- Permitから作ったAttemptは、通常のAttemptと同じく、Placement Approval、Lease、WorkOrderを経て初めて実行できます。Permitは実行の許可ではありません。
- 再試行してはならないTask、attempt budgetを使い切ったTask、期限の過ぎたTaskは、失敗にせず保持（Hold）として返します。人や上位の判断に任せます。
- Permitを確定してからAttemptを作るまでの間に落ちても、Permitは未使用のまま残り、有効期間の間は`Admit`で一度だけ使えます。

## まだないもの

- 保持（Hold）の印の保存。
- 再投入の公平性（aging、fair share）と、複数のControl Plane instanceにまたがる窓の合意。
