# 開始前のLocal Governorとの突き合わせ（Start gate）

Control PlaneがWorkOrderで許した上限と、この端末の利用者が許した上限は、別のものです。このパッケージは、実行を始める直前に両者を比べ、**小さい方**で実行を開始してよいかを決めます。

```text
admitted WorkOrder（Control Planeの上限）
  + Local Governorの現在の判断（端末の上限、％）
  + MachineProfile（端末の実測容量）
  + InUse（実行中のAttemptが予約している分の合計）
  -> 実行を始めてよいか
  -> 実効上限と期限（Plan）
```

## 実効上限の決め方

- CPUとMemoryは、`機械の容量 × Governorの上限%` から**実行中の予約の合計**を引いた残りと、WorkOrderの上限の小さい方。予約が0なら、従来どおり`機械の容量 × Governorの上限%`とWorkOrderの上限の小さい方。
- GPUは、`機械のGPU数 × Governorの上限%`（切り捨て）から実行中の予約を引いた残りと、WorkOrderの要求数の小さい方。残りが0台になる場合は開始しません。
- 期限は、WorkOrderの`WallTimeMillis`とAdmissionの期限の早い方。
- Temporary Disk、Network、出力byteは、この版では**使用の可否だけ**を見ます。0の上限は「使えない」という意味であり、不明ではありません。量の按分には、資源ごとの単位の定義が別に必要です。実行中の予約にも入れていません。
- 予約は、実行に許した上限（`Plan.Limits()`）で数えます。実際に使った量ではありません（ADR-0031 §2.3）。実行中の実測と、上限を超えた実行を止める境界は[`nativerunner`](../nativerunner/README.md)の仕事です。

## 実行slot

- 同時に実行してよい件数（`Config.MaxConcurrent`）を、実行中の件数（`InUse.Running`）が満たすと、開始しません（`ErrNoSlot`）。`Config.MaxConcurrent`が0のときは1として扱います。参照実装のAgentは1件ずつ実行するため、既定は1です（ADR-0031 §2.1）。
- **Agentはまだ実行slotと予約を数えていません。** いまは`InUse`に0を渡しているので、合計の判定は効いていません。予約を数える配線は#354で行います。

## 拒否する条件

- `StopRunning`（利用者の復帰、熱、バッテリーなど）。理由の種類は問わず、開始しません。
- `AcceptNewLeases`がfalse。
- WorkOrderが使う資源の上限が0。CPUとMemoryは常に必要です。Diskは`TemporaryDiskBytes > 0`、Networkは`NetworkBytesMax > 0`、GPUはGPU要求がある場合に必要とみなします。
- 実行中の予約が、Governorの上限まで使い切っている（`ErrNoCapacity`）。
- 実行slotが埋まっている（`ErrNoSlot`）。
- 端末の容量が報告されていない。0として扱わず、fail closedにします。
- Admissionの期限を過ぎている。
- Admissionがzero。

## Planが表すもの

`Plan`は、ある時刻に、ある上限で開始してよいという**その場の証拠**です。実行そのものではありません。

```text
Plan != 実行の開始
Plan != 以後のLocal Governorの判断の固定
Plan != Leaseの更新
```

実行を始めた後も、Local Governorが`StopRunning`を返した時点で止めます。`Plan`は、その判断を上書きしません。このパッケージはExecutorを起動しません。

## 実行側の責務

呼出し側は、`Plan.Limits()`と`Plan.Sandbox()`を実行環境へ適用し、`Plan.Deadline()`で打ち切ります。以後のGovernorの判断を取り直し、`StopRunning`なら停止します。`Plan.Reasons()`と`Plan.NotEnforceable()`は、なぜその上限になったかを説明するために残します。
