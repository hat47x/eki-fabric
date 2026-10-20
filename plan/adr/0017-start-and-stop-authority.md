# ADR-0017 実行の開始と停止を分け、開始はWorkOrderとLocal Governorの厳しい方で決める

- 状態: **採用**
- 日付: 2026-10-20
- 関連: Issue [#57](https://github.com/hat47x/eki-fabric/issues/57)、[#160](https://github.com/hat47x/eki-fabric/issues/160)、[#165](https://github.com/hat47x/eki-fabric/pull/165)、ADR-0015、RQ-005、RQ-006、RQ-007、RQ-047、RQ-052、RQ-053、[Task State Model](../../architecture/TASK_STATE_MODEL.md)

## 1. 背景

EKIでは、Control PlaneがWorkOrderで「このAttemptを、この上限で実行してよい」と伝えます。一方で、その端末の利用者が許す量は、Control Planeには分かりません。利用者が戻ってきた、熱が上がった、バッテリーが減った、といった事情は、端末の側でしか観測できません。

この二つを一つの判断に混ぜると、どちらかが失われます。

- Control Planeの許可だけで実行を始めると、利用者の復帰を無視できる。
- 端末の空きだけで実行を始めると、Leaseの期限や範囲を無視できる。
- 「開始してよい」という一度の判断を、実行中の停止の判断と混同すると、`StopRunning`が届いても止まらない。

さらに、署名済みWorkOrderを受理したこと（`workorderaccept.Admission`）と、今この瞬間に実行を始めてよいこと（`startgate.Plan`）は、別の問いです。受理は「指示がcurrentで、nonceが未使用だった」ことを示し、開始は「今の端末の上限でも足りる」ことを示します。

## 2. 決定

1. 実行の開始は、WorkOrderの受理と、Local Governorの現在の判断の**両方**を要する。 受理だけでは開始しない。`StopRunning`や`AcceptNewLeases=false`のときは開始しない。

2. 実効上限は、Control Planeの上限と端末の上限の厳しい方（小さい方）とする。 WorkOrderは物理量（CPU millis、byte、ミリ秒）で、Local Governorは端末容量に対する割合（0〜100）で表す。按分の基準になる端末の容量（`MachineProfile`）を明示的な入力にし、報告がない場合は0や無制限とみなさずfail closedにする。

3. 開始の判断を、`Plan`として残す。 `Plan`は、ある時刻に、ある上限で開始してよいという証拠である。実行の開始そのものではなく、以後のLocal Governorの判断を固定もしない。

4. 停止の判断は、常に端末側にある。 利用者の復帰、opt-out、熱、電力、スケジュール、Networkの従量を理由に、Local Governorは中央の承認を待たずに止める。Control Planeは、停止の申告を観測として受け取るが、停止を許可する権限者にはならない。

5. 停止は、実行権限の失効として扱う（ADR-0015）。 停止したAttemptは`reclaimed`として閉じ、回復の境界へ渡す。回復は、Taskの信頼性契約とattempt budget、時間窓の予算で、新しいAttemptを受理してよいかを決める。非retryableのTaskや予算切れのTaskは、自動で再実行しない。

6. 受理・開始・実行・停止を、別の型で表す。 `Accept`の結果は`Admission`、開始の結果は`Plan`、実行はExecutorのRuntime、停止はGovernorの`Decision`である。`Admission`や`Plan`からExecutorを直接起動しない。Runtimeは、`Plan`の上限を適用し、以後のGovernorの判断を取り直す。

7. 実行の上限は、一度決めたら固定しない。 Runtimeは、実行中もLocal Governorの判断を再評価し、`StopRunning`なら停止する。Leaseの更新は別の判断であり、更新のたびに、AdmissionとPlanを取り直す。

8. nonceは、開始のたびに一度だけ使う。 同じWorkOrderで二度開始しない。nonceの記録は、実行の証跡として耐久化する（現在のメモリ実装は試験用）。

9. M0では、実行のRuntimeを最小にする。 最初のvertical sliceでは、決定論的で再実行可能な仕事を対象に、上限の適用と停止の伝播を確かめる。GPU、Network、Temporary Diskの量の按分は、資源ごとの単位の定義が決まるまで、使用可否の判定に留める。

## 3. 影響

### 良い点

- 利用者の意思とControl Planeの許可の両方が、実行の開始条件に入る。どちらか一方だけでは始まらない。
- 開始と停止を分けることで、`StopRunning`が「開始しなかった」と混同されない。
- `Admission`と`Plan`を別に残すことで、拒否の理由を「指示が古い」「nonceが使用済み」「端末の上限が足りない」に分けて説明できる。
- 割合と物理量の変換を明示的な入力にしたため、端末の容量が不明なときに、暗黙に無制限へ倒れない。

### 悪い点と制約

- 端末の容量を検出して保つ必要がある。検出できない資源は使えないものとして扱うため、対応プラットフォームが限られる。
- Disk、Network、出力byteの量の按分は未定義で、現状は使用可否だけである。大きな転送を伴う仕事では、別の制御が要る。
- Leaseの更新ごとに受理と開始を取り直すため、短いLeaseでは往復が増える。
- 実行中の停止には、Runtime側の強制機構（Job Object、cgroup等）が必要で、これは別の実装境界である。

## 4. 未決事項

- 実行中のGovernor再評価の間隔と、停止の猶予。
- Leaseの`RenewalSequence`が進んだ場合に、`Admission`をどこまで再利用できるか。
- Disk、Network、出力byteの量の按分に使う単位と、観測方法。
- 端末容量の検出方法と、その値を誰が更新するか（起動時、変化時、定期）。
- 複数のExecutorを同時に走らせるときの、Governorの上限の配分。
- 実行のRuntimeを、Native / Container / WASM / Browserのどれから実装するか（ADR-0013の外面ごとの判断）。
- 実行中のcheckpointと、停止後の再開可否。
