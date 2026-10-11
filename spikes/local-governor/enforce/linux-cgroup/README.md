# Linux cgroup v2による制御を試すハーネス（Enforcement harness）

このProgramは、EKI Fabricの正式なWorker Agentではありません。

Local Governor Spikeで、**実際に書込み権限を委譲されたcgroupへ制約を設定し、そのcgroupへWorkloadを移したとき、要求した制約と観測結果が一致するか**を測るための実験用Harnessです。

## 何を確認するか

初期段階では次を扱います。

- `cpu.max` へCPU quotaを書き込めるか
- `memory.max` へMemory上限を書き込めるか
- `pids.max` へTask数上限を書き込めるか
- 必要な場合に `io.max` を設定できるか
- Workload PIDを `cgroup.procs` へ移せるか
- CPU Workloadの実測CPU時間が `cpu.max` と概ね整合するか
- Local opt-outを想定して `cgroup.kill` でWorkload treeを停止できるか
- 制約を書き込めない環境では、hard enforcementが成立したと誤報せず失敗するか

## 先に理解しておくこと

Linux kernelにcgroup v2があるだけでは、このHarnessは動きません。

cgroup v2ではResource controllerのdelegationに明確な境界があります。systemdを利用する環境では、`Delegate=` によってUnit配下のcgroup subtree管理をProcessへ委譲できます。

したがって、次の二つは別です。

```text
kernelがcgroup v2を持つ
        !=
このProcessがcgroupを管理できる
```

本Harnessは、既に作成済みで、現在のUser / Processから必要なcontrol fileへ書き込める**leaf cgroup directory**を `-target` で受け取ります。

cgroupのdelegation方法そのものをこのProgramへ隠しません。運用方式を決めるのは後段のArchitectureです。

## 実行例

まず単体テストを実行します。

```bash
go test ./...
```

20% of one CPU相当のquotaを設定し、3秒間CPU Workloadを動かす例です。

```bash
go run . \
  -target /sys/fs/cgroup/.../eki-spike \
  -cpu-max "20000 100000" \
  -memory-max 536870912 \
  -pids-max 64 \
  -duration 3s
```

JSONには、設定前後の値と、`cpu.stat` から観測したCPU usageの差分を出します。

`approx_one_cpu_share` は、

```text
cpu usage delta / wall time
```

です。

`cpu.max = 20000 100000` なら、他の条件が安定している限り、概ね0.2付近になることを期待します。厳密なSLOはこのSpikeではまだ定めません。

## Local opt-out相当の停止

長いCPU Workloadを開始し、1秒後にローカル停止する例です。

```bash
go run . \
  -target /sys/fs/cgroup/.../eki-spike \
  -cpu-max "50000 100000" \
  -duration 30s \
  -stop-after 1s
```

`cgroup.kill` を利用できる場合はcgroup tree全体を停止します。利用できなければ、このSpikeでは起動した直下Processのkillへfallbackします。

正式なLocal Governorでは、子Process漏れを許さないことが重要なので、fallbackをhard enforcement相当とは扱いません。

## 現在の実行環境での確認

2026-09-18の現在の検証環境では、このHarness自身のunit testは通過しました。

一方、現在の `/sys/fs/cgroup` はread-onlyであり、次のように明示的に失敗します。

```text
cpu.max was requested but could not be applied
```

この結果は失敗ではなく、今回確認したい安全側の挙動です。

**制御権限がないWorkerが、CPU hard enforcementを利用できると広告してはならない**ためです。

実際のCPU quota効果と `cgroup.kill` の停止時間測定は、delegation済みLinux hostで続けます。

## systemdを使う場合の考え方

systemdの`Delegate=`は、Service / Scope配下のResource Control partitioningをProcessへ委譲するための仕組みです。

EKIの将来のmanaged Endpointでは、たとえば次の二つを分ける構成が候補になります。

```text
eki-agent service cgroup
        ↓ delegation
agentが管理できるsubtree
        ↓
Taskごとのleaf cgroup
```

ただし、これを正式Architectureとして採用したわけではありません。

systemdを使わないLinux環境、Container内Worker、rootless環境なども比較対象として残します。

## 安全上の注意

このHarnessは、明示的にSpike用として用意したcgroupだけへ使用してください。

既存Service、Desktop session、Container root cgroupなどへ直接Limitを書き込む用途ではありません。

`memory.max` を小さくしすぎると、対象cgroup内のProcessはOOM killされる可能性があります。Linux kernelの仕様上、それがhard limitの一部です。

`pids.max` はThreadもTaskとして数えるため、Go Runtimeなどが作るThreadも消費します。

`io.max` はDeviceごとの指定が必要で、Filesystem / Device構成によって実効性が異なるため、初期Harnessでは値をそのまま受け取るだけにしています。

## この段階で一般化してよいこと

- hard enforcementを広告するには、control fileの存在だけでなく実際の書込みAuthorityを確認する必要がある。
- Workloadを開始してからcgroupへ移すまでのraceを避けるため、HarnessはWorkloadをgateで待機させ、`cgroup.procs`へのattach後に計算を開始する。
- Local opt-outの強い停止境界には、単一Process killよりProcess tree / cgroup tree全体を停止できる仕組みが望ましい。
- Resource limitの「設定成功」だけでなく、observed usageまで測る必要がある。

## この段階で一般化してはいけないこと

- すべてのLinux環境でcgroup delegationが同じ方法で利用できるという結論
- 20% quotaなら常に測定値が厳密に20.000%になるという結論
- Memory / PID / I/Oについて、まだ実負荷で制約効果を測らずに実証済みとすること
- GPUをcgroup v2だけで同様に制御できるという結論

## 次の測定

delegation済みhostで、少なくとも次を記録します。

1. CPU quotaごとのobserved CPU share
2. `memory.max` 到達時の `memory.events`
3. `pids.max` 到達時の `pids.events`
4. `cgroup.kill` からProcess tree停止までの時間
5. Agent / Harness異常終了後にProcessが残らないか
6. `io.max` のDevice別実効性

## 参考資料

- Linux Kernel Documentation — Control Group v2
  - https://docs.kernel.org/admin-guide/cgroup-v2.html
- systemd.resource-control — `Delegate=`
  - https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html
