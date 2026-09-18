# Mass Reclaim Recovery Spike

## このSpikeの役割

このSpikeは、多数Workerが短時間にcapacityを失うとき、未完了Taskを一斉にretryして別の障害を起こさないための最小モデルです。

対象は主に次です。

- RQ-035 Provider revocation and reclaim
- RQ-052 Coordinated reclaim without retry amplification
- SEC-007 Retry and fan-out consume bounded budget
- SEC-023 Reclaim and retry must not amplify without bound
- PAS-26 大規模Compute Grantの一斉reclaim
- QA-05 Network efficiency
- QA-06 Recoverability under churn

Queue製品、Scheduler製品、具体的なbackoff秒数を確定するものではありません。

## 背景

EKIでは、ProviderやEndpoint利用者が資源を取り戻す権利を、Task完遂より優先します。

そのため、

```text
Taskが実行中だからreclaimできない
```

という設計にはしません。

一方で、数千Workerが同時に離脱した直後、数千Taskを同時に再投入すると、残存Workerだけでなく、Artifact Store、Network、Queue、Control Planeへ新しい負荷を集中させます。

これは単純なWorker failureではなく、回復処理そのものが障害を増幅する問題です。

## 先行例から得られる示唆

### Spot / preemptible資源では中断を通常状態として扱う

AWS EC2 Spotでは中断通知を利用できますが、通知はbest effortです。

したがって、事前通知を使える場合は有用でも、

> 通知が必ず届くから安全に回収できる

という保証にはできません。

EKIでも同様に、Providerから事前reclaim noticeが届いた場合は、

- 新規Leaseの停止
- checkpoint可能Taskへの早期通知
- 新規Placementの回避

へ利用できます。

ただし、通知なしの突然reclaimでもFabricが安全側に回復できることを基本にします。

### backoffだけでは十分ではない

同時失敗後のretryでは、exponential backoffとjitterがretry stormの平準化に有効です。

一方、EKIではTaskごとに大きなArtifact transferが発生する可能性があります。

そのため、

```text
retry時刻を散らす
```

だけでは、

```text
その時間窓に何件まで再投入するか
何byteまで再転送するか
```

を保証できません。

このSpikeでは、backoff/jitterとre-admission budgetを別の境界として扱います。

## 最小モデル

`recovery.go` では、mass reclaim後のTaskを二段階で扱います。

### 1. Intake

reclaimされたAttemptをまず分類します。

自動retryできるのは、

- Taskが明示的にretryable
- max attemptsを超えていない

場合だけです。

副作用Taskなど、安全な再実行根拠がないTaskは`Held`へ残します。

retry可能Taskには、

- 次Attempt番号
- priority
- deadline
- estimated transfer bytes
- compute見積り
- `NotBefore`

を付けてRecovery Queueへ入れます。

### 2. Re-admission

`NotBefore`へ到達したTaskを、そのまますべて実行可能にしません。

時間窓ごとに、

- 最大release件数
- 最大transfer bytes

を別々に制限します。

概念的には次です。

```text
reclaim
  ↓
retryability / attempt budget
  ↓
backoff + jitter
  ↓
eligible retry queue
  ↓
global re-admission count budget
  +
network transfer budget
  ↓
placement
```

## なぜ複数のBudgetを分けるのか

### Retry Budget

一つのTaskが何度までAttemptを増やせるかを制限します。

これは、

```text
1 Task → 無限retry
```

を防ぎます。

### Release Rate Budget

一つの時間窓に何Taskを再投入できるかを制限します。

これは、

```text
1000 Task → 同時に1000 retry
```

を防ぎます。

### Transfer Budget

時間窓あたりの推定Artifact転送量を制限します。

これは、Task数が少なくても、一つ一つが巨大Artifactを必要とする場合にNetworkを守ります。

三つは同じBudgetではありません。

## Spikeで実行したScenario

1,000 Taskが同時にreclaimされるScenarioを用意しました。

各Taskは次の条件です。

```text
retryable: true
input transfer estimate: 64 MiB
max attempts: 4
```

Recovery Policyは例として、

```text
base backoff: 1s
max backoff: 30s
max releases / window: 50
max transfer / window: 2 GiB
window: 1s
```

としています。

この数値はproduction defaultではありません。

## 観測結果

`go run ./cmd/sim` の結果は次です。

```json
{
  "interrupted_tasks": 1000,
  "naive_immediate_retries": 1000,
  "naive_transfer_bytes": 67108864000,
  "controlled_first_window_releases": 32,
  "controlled_first_window_transfer_bytes": 2147483648,
  "deferred_after_first_window": 968
}
```

素朴な即時retryでは、1,000件が同時再投入され、約62.5 GiB相当の再転送要求が一度に発生します。

一方、2 GiB / windowのtransfer budgetでは、64 MiB Taskなら一つのwindowで32件までしかreleaseされません。

50件というcount budgetより先にNetwork Budgetが効いていることも確認できます。

## Unit Testで確認したこと

2026-09-18時点で、

```bash
go test ./...
```

を実行し、Testは通過しています。

確認している主な条件は次です。

- 1,000 Taskが一斉reclaimされても一度に全件releaseしない
- transfer budgetを超えない
- count budgetも独立して機能する
- retry不可Taskを自動再実行しない
- max attempts到達Taskを自動再実行しない
- 同一条件ならurgent Taskを先にreleaseできる
- 同一priorityならdeadlineを比較できる
- 1 Taskだけでwindow transfer budgetを超える場合も安全側にdeferする
- backoff + jitterによりretry時刻を一つへ集中させない
- 同じTaskを同一reclaim event内で重複投入しない

## PriorityはBudgetを越える権限ではない

urgent Taskはbest-effortより先にre-admissionできます。

ただし、

```text
urgentだからNetwork Budgetを越えてよい
```

とは扱いません。

Priorityは限られた回復枠の中で順序を決める情報であり、安全上限を無効化するauthorityではありません。

これはLocal GovernorでTask priorityがEndpoint safetyを上書きしない原則と同型です。

## Deadlineと公平性

このSpikeでは、

1. priority
2. deadline
3. NotBefore
4. Task ID

で単純に順序付けしています。

これは最終Scheduler Policyではありません。

高priority Taskが継続的に流入した場合、best-effort Taskがstarvationする可能性があります。

Architectureでは、

- aging
- fair share
- Principal / Beneficiaryごとのquota
- reserved recovery capacity
- deadline miss時のdrop / escalation

などを比較する必要があります。

## Artifact localityとの接続

mass reclaim後の再配置では、元Workerのcacheが失われる場合があります。

したがって、

```text
retry可能
```

であることと、

```text
今すぐ別Workerへ送るべき
```

ことは同じではありません。

前のArtifact locality Spikeで扱った、

- missing Artifact bytes
- target Worker cache
- Network condition

をre-admission後のPlacementへ引き継ぐ必要があります。

将来的には、transfer budgetの見積りを静的Task値ではなく、候補Workerごとのlocality評価から得る方が自然です。

## planned reclaimの扱い

Providerが事前に、

```text
5分後に80% capacityを回収する
```

と通知できる場合には、次が可能です。

- 対象Workerへの新規Lease停止
- 短時間で終わるTaskだけ残す
- checkpointを促す
- まだ混雑していない時間帯から段階的に再配置する

ただし、突然のreclaimでも成立する回復境界を弱めません。

事前通知はRecoveryを滑らかにするOptimizationです。

## failure domainも将来必要になる

数千Taskが一つのProvider Grantへ集中していれば、そのGrantのreclaimで一斉に失われます。

したがって高可用性Taskでは、

```text
Worker単位の分散
```

だけでなく、

```text
Provider / site / trust domain / power domain等のfailure domain
```

を配置判断へ含める余地があります。

ただし、すべてのTaskを複数Providerへ常時複製することはCostを増やします。

これはTask assurance / availability profileごとのTrade-offとして後続検証へ残します。

## このSpikeで一般化してよいこと

1. Reclaim権はTask完遂より優先できる。
2. retryable Taskでも、即時再投入する必要はない。
3. Backoff / jitterと、global re-admission budgetは別の役割を持つ。
4. Network transfer budgetをrecoveryのfirst-classな制約にできる。
5. Priority / deadlineはrecovery順序に使えるが、安全上限を上書きしてはならない。
6. 副作用Taskやretry budget切れTaskは自動retryから外せる。
7. planned noticeを使いつつ、noticeなしでも安全な回復を要求できる。

## このSpikeで一般化してはいけないこと

まだ次は確認していません。

- 実Queueでのthroughput
- 数万〜数百万Task規模
- Artifact Storeの実負荷
- shared network link
- cache hit / missの動的変化
- Worker capacityの復旧速度
- checkpoint転送cost
- 複数Provider同時reclaim
- Control Plane restartとの複合障害
- Priority starvation
- fair share
- deadline miss policy
- retry release後に再度即reclaimされるflapping
- Cloud fallback時の金銭budget
- verification replication分の追加Task
- parent / child Task graph全体のretry budget

## 次の検証

次段階では、次のどちらかを進める価値があります。

### Dynamic recovery simulation

複数時間窓を進め、

- capacity recovery
- cache hit率
- Network congestion
- 再reclaim
- priority aging

を含むsimulationへ広げます。

### Control Plane authority separation

Schedulerがretry releaseを決めても、

- retry budgetを書き換える
- Artifact authorityを増やす
- Network budgetを無視する

ことはできないという権限分離を、最小構成で確認します。

## 参考資料

- AWS Architecture Blog — Exponential Backoff And Jitter
  - https://aws.amazon.com/blogs/architecture/exponential-backoff-and-jitter/
- AWS EC2 — Spot Instance interruption notices
  - https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/spot-instance-termination-notices.html
- AWS EC2 — Spot best practices
  - https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/spot-best-practices.html
- AWS Well-Architected — Control and limit retry calls
  - https://docs.aws.amazon.com/wellarchitected/2022-03-31/framework/rel_mitigate_interaction_failure_limit_retries.html
- Kubernetes client-go workqueue
  - https://pkg.go.dev/k8s.io/client-go/util/workqueue
