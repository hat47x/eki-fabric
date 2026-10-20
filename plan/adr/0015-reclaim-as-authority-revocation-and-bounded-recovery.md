# ADR-0015 回収を実行権限の失効として扱い、回復を新しいAttemptの予算づき受理にする

- 状態: **採用**
- 日付: 2026-10-20
- 関連: Issue [#57](https://github.com/hat47x/eki-fabric/issues/57)、[#66](https://github.com/hat47x/eki-fabric/issues/66)、[#78](https://github.com/hat47x/eki-fabric/issues/78)、RQ-011、RQ-012、RQ-013、RQ-035、RQ-052、RQ-053、SEC-007、SEC-023、[大量回収後の復旧Spike](../../spikes/mass-reclaim-recovery/README.md)、[Task State Model](../../architecture/TASK_STATE_MODEL.md)、ADR-0013

## 1. 背景

業務端末では、利用者が戻った時点で計算を止める必要があります。ProviderがGrantを縮小したときも同じです。EKIは、この回収をTaskの失敗として扱いません。回収は通常の出来事であり、Task完遂より優先されます（RQ-052）。

一方で、回収の直後にすべてのTaskを再投入すると、残ったWorker、Artifact Store、Network、Control Planeへ負荷が集中します。1,000のTaskが一斉に回収され、それぞれ64 MiBの入力を必要とする場合、素朴な即時retryは約62.5 GiBの転送要求を一度に作ります。これはWorkerの障害ではなく、回復そのものが起こす障害です。

Spikeでは、backoffとjitterだけでは「その時間窓に何件まで、何byteまで戻すか」を保証できないことを確認しました（[Spikeの結果](../../spikes/mass-reclaim-recovery/README.md)）。この判断を、実装の正本として定めます。

## 2. 決定

1. 回収は、実行権限の失効として扱う。 Local GovernorやProviderが資源を取り戻した時点で、そのAttemptの実行権限は失われます。端末側の停止は、中央の承認を待ちません。Control Planeは、遅れて届く停止の申告やLeaseの期限を、権限の失効を確認する材料として使いますが、失効そのものを承認する権限者にはなりません。

2. 回復は、新しいAttemptの受理として扱う。 回復は、失効したLeaseを延長せず、同じAttemptを再開もしません。前のAttemptをcloseし、次の番号のAttemptを、新しいPlacement、新しいLease、新しいfencingの番号で作ります。Attempt番号は飛ばさず、一つずつ進めます。

3. 三つの予算を、別々に判定する。
   - attempt budget: 一つのTaskが持てるAttemptの総数。Taskの信頼性契約（RQ-011）の一部であり、回復が広げることはできません。
   - release count: 一つの時間窓に再投入できる件数。
   - transfer / compute: 一つの時間窓に再投入できる転送byte数と計算ミリ秒数。件数が少なくても、一つが巨大ならNetworkを守る必要があります。

   予算は独立に判定し、どれか一つでも超える候補は次の窓へ回します。priorityは窓の中の順序を決めるだけで、予算を越える権限にはなりません。

4. 回復の証拠を、Permitとして発行する。 Permitは、対象のTask、TaskIRのdigest、前のAttempt、次のAttempt番号、policy revision、releaseの通し番号、発行時刻、使用可能時刻、期限を持ちます。PermitはLeaseでもPlacement Approvalでもなく、attempt budgetを広げる権限でもありません。Scheduler、Client、Workerが自分で作れないよう、packageの外からfieldを設定できません。

5. 判定を、入力から決まる純粋な処理にする。 回復の判定は、時計もファイルもgoroutineも読みません。現在時刻、policy、読んだ状態を呼出側が渡します。backoffのjitterはTask IDとAttempt番号から決まるため、Control Planeを再起動しても同じPendingは同じ時刻になります。

6. 重複した配送を通常の出来事として扱い、食い違いはfail closedにする。 同じ回収イベントや同じPendingが二度届いた場合は一件に畳みます。同じAttempt IDやTask IDで内容が食い違う場合は、どちらかを選ばずにエラーにします。上流の状態が壊れているときに、都合のよい方へ倒さないためです。

7. deadlineを過ぎたPendingは、別の扱いにする。 待っている間にTaskのdeadlineを過ぎたものは、次の窓へ回さずにqueueから外します。いつまでも再投入待ちのままにすると、実行できないTaskが予算を占め続けます。

8. M0では、この境界を純粋なCoreとして実装し、durableな状態への接続を次の作業にする。 実装は`internal/core/recovery`に置きます。Pending、予算、Permitの耐久化と、Attempt / Leaseの更新と同じtransaction境界への接続は、次の作業単位で行います。接続前のPermitは、Productionの実行権限として使いません。

## 3. 影響

### 良い点

- 回収が連続しても、再投入の件数、転送量、計算量を窓ごとに抑えられる。Spikeの1,000 Task / 64 MiBの条件では、2 GiBの窓予算で32件までしか戻らない。
- なぜ再投入しなかったかを、予算の種類ごとに説明できる。監査では「priorityが低かった」と「予算が尽きた」を区別できる。
- 再起動しても、同じPendingから同じ時刻、同じPermitのdigestが得られる。再起動を理由にbackoffがやり直しにならない。
- permitを新しいAttemptと一緒に保存すれば、どの予算判定でそのAttemptが生まれたかを後から再現できる。
- Attempt budgetを回収の経路から広げられないため、失敗のたびに試行が増え続ける状態を型で作れない。

### 悪い点と制約

- 耐久化する状態が増える。Pending queue、窓の消費量、Permitを、Attempt / Leaseの更新と同じ原子性で保存する必要がある。
- priorityだけの順序では、urgentなTaskが続くとbest-effortのTaskが飢える可能性がある。
- 一つのTaskの転送量が窓の予算を超える場合、そのTaskはどの窓でも再投入できない。予算を暗黙に上げずにdeferするため、Taskの分割や予算の見直しが必要になる。
- policyの値を誤ると、回復が遅すぎるか、逆に緩すぎる。値の根拠を実測で示す必要がある。
- 回収イベントをWorkerの自己申告から作ると、偽の回収で予算を消費させられる。Leaseの失効とAttemptのcloseから導く必要がある。

## 4. 未決事項

- Pending、予算、Permitを、どのState Storeの表へ、どのtransaction境界で保存するか。
- 回収イベントを確定する権限と、Workerの申告をどこまで材料にするか。
- 窓をまたいだ公平性（aging、fair share、Principal / Beneficiaryごとのquota、reserved recovery capacity）。
- 一つのTaskが窓の予算を超える場合の扱い（Taskの分割、予算の見直し、明示的な拒否の返し方）。
- planned reclaim noticeを使った事前の再配置を、回復の境界を弱めずにどう滑らかにするか。
- 回復のPermitを、Worker側の受入やArtifact Grantの再発行とどう結び付けるか。
- 窓の境界を、単一のControl Planeの時計に置いてよい範囲と、複数instanceへ広げるときの合意の取り方。
