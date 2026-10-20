# 回収後の再投入を予算で区切る（Recovery Admission）

Workerや端末の利用者が資源を取り戻すと、実行中のAttemptは権限を失います。EKIでは、この回収をTaskの失敗として扱いません（RQ-052）。同時に、回収されたTaskをそのまま一斉に再投入しません。再投入そのものが、残ったWorker、Artifact Store、Network、Control Planeへ新しい負荷を集中させるからです。

このpackageは、回収されたAttemptを「新しいAttemptとして再び受理してよいか」と「どの時間窓に何件・何byte・何ミリ秒まで戻してよいか」に分けて判定します。

```text
interrupted Attempt
  -> Classify
       retryable? / attempt budget? / deadline?
       -> Held        # 自動では再実行しない
       -> Pending     # backoff + jitter 後の再投入候補
  -> Release
       window ごとの count / transfer / compute budget
       -> Permit      # 新しいAttemptを受理してよいという証拠
       -> Deferred    # 次のwindowへ
       -> Expired     # deadlineを過ぎた。queueから外す
```

## Permitが表すもの

`Permit`は、回復の境界が「このTaskの、この前Attemptの次として、このpolicy revisionの下で、この時間窓に限って、新しいAttemptを受理した」という証拠です。

次のものではありません。

```text
recovery.Permit != Lease
recovery.Permit != Placement Approval
recovery.Permit != attempt budgetを広げる権限
```

そのため、`Permit`のfieldは非公開にしています。Scheduler、Client、Workerが自分で作ることはできません。上位の実装は、このpackageが返した`Permit`だけを、新しい`Attempt`の記録と一緒に保存します。

`Permit.Validate`は、次を順に確かめます。

- 対象のTask、TaskIR、前のAttemptが一致するか。
- 現在のpolicy revisionと、発行時のrevisionが一致するか。
- 使用可能な時刻に達しているか。
- 期限を過ぎていないか。

policyが変わった後に古い`Permit`を使おうとすると、期限より先にrevisionの不一致として拒否されます。何が変わったのかを記録しやすくするためです。

## 三つの予算を分ける理由

- **attempt budget**: 一つのTaskが何回までAttemptを持てるか。Taskの信頼性契約（RQ-011）の一部であり、回復が広げることはできません。
- **release count**: 一つの時間窓に何件まで再投入するか。
- **transfer / compute**: 一つの時間窓に何byte、何ミリ秒まで戻すか。件数が少なくても、一つが巨大ならNetworkを守る必要があります。

優先度は、窓の中で順番を決めるだけです。urgentなTaskでも、三つの予算を越える権限にはなりません。

## 同じ入力から同じ結果を出す

このpackageは、時計を読みません。現在時刻、policy、読んだ状態を呼出側が渡します。backoffのjitterもTask IDとAttempt番号から決まるため、Control Planeを再起動しても同じ`Pending`は同じ`NotBefore`になります。

`Pending`は耐久化してください。毎回`Classify`をやり直すと、jitterが動いてbackoffの意味が失われます。

## 重複した配送

同じAttemptの回収イベントが二度届くことは、通常の出来事です。`Classify`は同一内容の重複を数え、再投入候補を増やしません。同じAttempt IDで内容が食い違う場合は、上流の状態が壊れているため、どちらかを選ばずにエラーにします。

`Release`も同じです。同じTaskの同じ`Pending`が二度届いた場合は一件に畳み、内容が違う場合はエラーにします。

## まだ決めていないこと

- `Pending`、`Budget`、`Permit`を、どのState Storeの表へ、どのtransaction境界で保存するか。durableなAttempt / Leaseの更新と同じ境界にする必要があります。
- 回収イベントを誰が確定するか。Workerの自己申告ではなく、Leaseの失効とAttemptのcloseから導く必要があります。
- 窓をまたいだ公平性。urgentなTaskが続くとbest-effortが飢える可能性があり、aging、fair share、reserved recovery capacityの比較が要ります。
- 一つのTaskが窓の予算を超える場合の扱い。現在は`single_item_exceeds_window_budget`としてdeferし、暗黙に予算を上げません。
- planned reclaim noticeを使った事前の再配置。回復の境界を弱めずに滑らかにする最適化として、別に扱います。

Spikeの観測と一般化できる範囲は、`spikes/mass-reclaim-recovery/README.md`にあります。
