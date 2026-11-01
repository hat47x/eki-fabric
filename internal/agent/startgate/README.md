# 開始前のLocal Governorとの突き合わせ（Start gate）

Control PlaneがWorkOrderで許した上限と、この端末の利用者が許した上限は、別のものです。このパッケージは、実行を始める直前に両者を比べ、**小さい方**で実行を開始してよいかを決めます。

```text
admitted WorkOrder（Control Planeの上限）
  + Local Governorの現在の判断（端末の上限、％）
  + MachineProfile（端末の実測容量）
  -> 実行を始めてよいか
  -> 実効上限と期限（Plan）
```

## 実効上限の決め方

- CPUとMemoryは、`機械の容量 × Governorの上限%` と、WorkOrderの上限の小さい方。
- GPUは、`機械のGPU数 × Governorの上限%`（切り捨て）と、WorkOrderの要求数の小さい方。0台になる場合は開始しません。
- 期限は、WorkOrderの`WallTimeMillis`とAdmissionの期限の早い方。
- Temporary Disk、Network、出力byteは、この版では**使用の可否だけ**を見ます。0の上限は「使えない」という意味であり、不明ではありません。量の按分には、資源ごとの単位の定義が別に必要です。
- **他の実行が使っている分は、この版の判定に入りません。** 1件ずつ実行している間は結果が同じになりますが、同時実行を入れると、2件の割当がそれぞれ全容量を上限に通ります。合計で判定する形と、実行slot・予約の扱いは[ADR-0031](../../../plan/adr/0031-local-execution-slots-and-resource-reservation.md)で決めています（#354）。

## 拒否する条件

- `StopRunning`（利用者の復帰、熱、バッテリーなど）。理由の種類は問わず、開始しません。
- `AcceptNewLeases`がfalse。
- WorkOrderが使う資源の上限が0。CPUとMemoryは常に必要です。Diskは`TemporaryDiskBytes > 0`、Networkは`NetworkBytesMax > 0`、GPUはGPU要求がある場合に必要とみなします。
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
