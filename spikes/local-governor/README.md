# Local Governor Feasibility Spike

## このSpikeの役割

この文書は、EKI FabricのPhase 4で最初に行う実現可能性検証です。

対象は、業務端末や個人端末で動くWorkerが、利用者の作業を妨げず、中央Control Planeが誤動作または切断されている場合でも、ローカルだけで計算資源の提供を縮小・停止できるか、という点です。

ここでArchitectureを確定することはしません。Windows、Linux、将来のmacOSやBrowserなど、実行環境ごとに利用できるOS機能が異なることを前提に、EKI共通のGovernor契約として何を約束できるかを見極めます。

関連する主な要求:

- RQ-005 Human-first endpoint governance
- RQ-006 Immediate local opt-out
- RQ-007 Effective capacity is bounded locally
- RQ-009 Resource-bounded execution
- RQ-010 Opportunistic availability
- RQ-053 Energy / thermal / device-safety aware resource policy
- SEC-022 Resource reclaim cannot override local safety

## 最初に確認したい仮説

### H1 — CPUとMemoryは、主要OSで「お願い」ではなく強制境界を作れる

Worker AgentがExecutorに「CPUを使いすぎないでください」と協調を求めるだけでは、改変・暴走したExecutorへ耐えられません。

そこで、少なくともCPU、Memory、Process数については、OS側のResource Controlを使ってWorker Agentより下の層で上限を強制できることを期待します。

### H2 — すべてのResourceを同じ精度では制御できない

CPU、Memory、Disk I/O、Network、GPU、電力、温度は、OSから見た性質が異なります。

そのため、EKI共通APIが

```text
cpu = 20%
memory = 1 GiB
gpu = 20%
network = 2 Mbps
```

と一見同じ形式を持っていても、実際には一部しか強制できない環境があり得ます。

EKIはこの差を隠して「設定できたこと」にしてはいけません。

### H3 — Opt-outはControl Planeへの依頼ではなく、Endpoint側の停止境界として成立させられる

利用者が「停止」を選んだあと、中央Control Planeからの応答を待たなければ新規Taskを止められない構造は、RQ-006を満たしません。

ローカルAgentだけで新規Leaseを拒否し、実行中Executorへ停止を要求し、必要ならProcess treeを強制終了できる必要があります。

---

# 1. Windowsで確認できたこと

## 1.1 Job ObjectはCPU / Memory / Processを強制できる

Windows Job Objectには、複数Processを一つのJobとして束ね、そのJobへResource Limitを設定する仕組みがあります。

Microsoftの公式資料では、少なくとも次の制御が確認できます。

- CPU rateのhard cap
- Process単位 / Job全体のMemory limit
- Active Process数の上限
- Process affinity
- Scheduling / Priorityの制約
- Job handleが閉じられたときに関連Processを終了する `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`

CPU hard capでは、指定された期間内のCPU利用量へ到達すると、次の期間までJob内のThreadが実行されなくなります。

これは「Executorが自発的にCPUを譲る」方式ではなく、OS Schedulerによる制約として利用できます。

### EKIへの示唆

Windows版Local Governorでは、Executorとその子ProcessをJob Objectへ所属させる方式が第一候補になります。

特にOpt-out時には、次の二段階停止が考えられます。

```text
graceful cancellation
        ↓
短いgrace period
        ↓
応答しなければJob全体をterminate
```

これにより、Executorが子Processを起動していても、Process treeの回収境界を作りやすくなります。

## 1.2 EcoQoSは有用だが、hard limitではない

WindowsにはEcoQoSがあり、Process / Threadを性能最大化より電力効率側へ寄せることができます。

これはBackground Computeとの相性がよく、Battery寿命、Fan noise、電力効率の改善に利用できる可能性があります。

ただし、EcoQoSは「CPUを必ず20%以下にする」といったhard capではありません。

したがってEKIでは、

```text
Job Object CPU cap = enforcement
EcoQoS             = scheduling / energy hint
```

と役割を分ける必要があります。

## 1.3 Disk I/O limitをJob Objectの共通能力として扱うのは危険

Windowsには `SetIoRateControlInformationJobObject` というAPIがあります。

しかしMicrosoftの現行Documentationでは、Windows 10 version 1607以降ではこの関数はサポートされていないと明記されています。

したがって、現代の一般的なWindows Endpointについて、

> Job ObjectがCPU、Memory、Disk I/Oを一様にhard limitできる

とは扱えません。

Disk / Networkについては別のOS機構やApplication-level controlを調査する必要があります。

### 暫定結論

Windowsでの初期Capabilityは、少なくとも次のように分けるべきです。

| Resource | 現時点の見立て |
| --- | --- |
| CPU | hard enforcement可能 |
| Memory | hard enforcement可能 |
| Process count | hard enforcement可能 |
| Process tree termination | hard enforcement可能 |
| Energy efficiency hint | EcoQoSで可能 |
| Disk I/O rate | Job Objectの一般能力としては依存不可 |
| Network rate | 追加調査が必要 |
| GPU usage rate | 追加調査が必要 |

---

# 2. Linuxで確認できたこと

## 2.1 cgroup v2はResource Boundaryとしてかなり強い

Linux kernelのcgroup v2は、Processを階層的なGroupへ分け、そのGroupごとにResourceを制御する仕組みです。

公式Documentationでは、親の制約を子が緩められない階層モデルになっています。

EKIのLocal Governorに関係する主なInterfaceは次です。

### CPU

`cpu.max` は、一定期間内に利用できるCPU bandwidthの最大量を設定できます。

### Memory

`memory.max` はMemory usageのhard limitです。上限に達し、reclaimできない場合はそのcgroup内でOOM処理が行われます。

`memory.high` のようなsoft / throttle側の境界も存在します。

### Disk I/O

`io.max` はDeviceごとにread / writeのBPSやIOPSを制限できます。

### Process数

`pids.max` はProcess / Thread数のhard limitです。fork bombのようにMemory limitより先にPIDを使い切る攻撃へも独立して対処できます。

## 2.2 「cgroup v2がある」と「Workerが自由に制御できる」は別

今回の検証環境では、実際に次を確認しました。

```text
filesystem: cgroup2
controllers:
  cpuset cpu io memory hugetlb pids

cpu.max:
  400000 100000

memory.max:
  4294967296

pids.max:
  max
```

一方、この環境の `/sys/fs/cgroup` はread-only mountでした。

したがって、このProcessから新しいcgroupを作ったり、Limitを書き換えたりすることはできません。

これはLinux全般の制約ではなく、今回の実行環境の構成です。しかし重要な示唆があります。

> OSにResource Control機能が存在することと、eki-agentにその制御権限が委譲されていることは別である。

EKIは `linux = cgroup available` だけを見てhard enforcement可能と判断してはいけません。

起動時に、実際に利用できるControllerとAuthorityを検出する必要があります。

### 暫定結論

Linuxでは、適切にcgroup delegationを構成できる環境なら、CPU、Memory、I/O、PIDについて強いLocal Enforcementを実現できる見込みがあります。

ただし、一般User権限のProcessが常に自分でcgroupを作成・変更できるとは仮定しません。

systemd、service manager、container runtime、管理者によるdelegation等との関係を次のSpikeで確認する必要があります。

---

# 3. 共通Governor APIで最も重要な発見

## 「設定値」ではなく「保証水準」を返す必要がある

たとえば上位層が

```text
network.max = 2 Mbps
```

と要求したとします。

あるOSではhard enforcementでき、別のOSではExecutor側の協調的throttleしかできないかもしれません。

両者を同じ「設定成功」と返すと、Security Requirementを誤って満たしたことになります。

そのため、Local GovernorのCapabilityは少なくとも次のような保証種別を持つ必要があります。

```text
HARD
  OS / sandbox境界で強制する。
  Executorが協力しなくても上限を越えにくい。

BEST_EFFORT
  Scheduler priority、EcoQoS、cooperative throttle等。
  強い上限保証ではない。

OBSERVE_ONLY
  計測はできるが制御できない。

UNAVAILABLE
  計測・制御のどちらも利用できない。
```

たとえば、概念的には次のようになります。

```text
CPU:
  enforcement = HARD

Memory:
  enforcement = HARD

GPU utilization:
  enforcement = OBSERVE_ONLY

Thermal:
  enforcement = OBSERVE_ONLY
  action      = local capacity reduction

EcoQoS:
  enforcement = BEST_EFFORT
```

ここで重要なのは、Thermal sensor自体をhard limitするのではなく、

> Thermal状態を観測し、その結果としてEKI capacityを0へ落とす

という形でもRQ-053を満たし得ることです。

---

# 4. GPUはCPUと同じResourceとして扱わない方がよい

この初期Spikeでは、Windows / Linuxを横断する汎用的なGPU utilization hard capは確認できていません。

GPUはCPUと異なり、

- Vendor
- Driver
- Runtime
- GPU scheduling方式
- Model / kernelの実行特性
- VRAM
- MIG / vGPU等のHardware / Enterprise機能

によって制御可能性が大きく異なります。

したがって初期EKIでは、

```text
gpu.max_percent = 20
```

のような値を、全Workerで同じ強さで保証するAPIにはしない方が安全です。

代わりに、少なくとも初期段階では次の制御を重視します。

- 同時GPU Task数を制限する
- VRAM要求でAdmissionする
- 承認済みExecutor / Modelだけを業務Endpointで実行する
- User return / thermal event時に新規GPU Taskを止める
- 必要ならExecutor processを終了してGPU workを回収する
- Vendor固有のhard controlが利用できる環境だけCapabilityとして広告する

特に業務Endpointでは、任意のClient supplied GPU kernelを実行しないという既存Security方針と整合させる必要があります。

GPU GovernorはLocal Governorの中でも独立した追加Spikeにする価値があります。

---

# 5. Opt-outのLocal State Machine案

RQ-006を満たすため、停止操作は中央Control Planeへの単なるRequestにしません。

概念的には、Endpoint内で次のように遷移させます。

```text
AVAILABLE
   │
   │ user opt-out / thermal / battery / policy
   ▼
DRAINING_LOCAL
   │
   ├─ 新規Leaseを拒否
   ├─ Control Planeへcapacity=0を通知できれば通知
   └─ 実行中Taskへgraceful cancel
   │
   │ grace period終了
   ▼
FORCE_QUIESCE
   │
   └─ sandbox / process treeを強制停止
   ▼
UNAVAILABLE
```

Control PlaneとのNetworkが切れていても、

```text
AVAILABLE → UNAVAILABLE
```

のローカル遷移自体は成立させます。

Control Plane側ではLease expiry等によってTaskを後から回収できますが、それはEndpointが停止できることの前提にはしません。

## Opt-outで測るべき時間

Local Governorの品質を見るため、少なくとも次を測定候補にします。

- `time_to_reject_new_work_after_opt_out`
- `time_to_graceful_cancel_running_executor`
- `time_to_force_quiescence`
- `cpu_usage_after_quiescence`
- `memory_retained_after_quiescence`
- `child_process_leak_count`

RQ-006の「即時」は、UIを押した瞬間にすべてのProcessが0 CPUになるという意味ではありません。

ただし、**新しいWorkを受けない判断はローカルで即座に成立し、実行中Workも短く明確な境界で停止へ向かう**必要があります。

具体的SLOは実装Spike後に決めます。

---

# 6. Local Governorの共通責務案

現時点では、次の責務へ絞るとArchitectureを過度に先取りせずに済みます。

```text
Local Governor
├─ Observe
│   ├─ user activity
│   ├─ CPU / Memory pressure
│   ├─ power source
│   ├─ thermal state
│   └─ optional GPU / network state
│
├─ Decide
│   └─ effective local capacity
│
├─ Enforce
│   ├─ process / sandbox limits
│   ├─ new-work admission
│   └─ local stop / kill
│
└─ Advertise
    ├─ available capacity
    └─ assurance of each enforcement capability
```

中央Schedulerから受け取るResource Policyは、Local Governorの上限を広げる命令ではなく、あくまで**中央側から許可される最大値**として扱います。

```text
effective capacity
  =
min(
  central permitted capacity,
  administrator local policy,
  user preference,
  runtime safety judgement
)
```

この原則は維持します。

---

# 7. 現時点の判定

## 実現可能性が高い

- Windows業務EndpointでCPU / Memory / Process treeをhard limitする
- Linux管理Endpointでcgroup v2を使いCPU / Memory / I/O / PIDをhard limitする
- Control Planeから独立して新規Work受付を停止する
- Process treeをローカルで強制終了する
- Energy / Thermal / Batteryをcapacity縮小の入力として扱う
- Capabilityごとにhard / best-effort / observe-onlyを区別する

## 追加検証が必要

- Windowsでの現実的なDisk / Network hard limit
- Linux cgroup delegationの最小運用構成
- macOSでのhard enforcement可能範囲
- GPU utilization / power / VRAMのcross-vendor enforcement
- User activity検出方法とPrivacy
- Thermal sensor / Battery / AC状態のOS差
- Executor termination後にGPU workがどの程度速く解放されるか
- Local Governor自身が改変されたWorkerに対して何を保証できるか

最後の点は特に重要です。

Local Governorは**端末利用者を守るためのローカル境界**として有効ですが、Worker owner自身がAgentを改変できるad-hoc Workerでは、そのGovernorが正しく働くことを他者が信頼する根拠にはなりません。

つまり、

```text
Local Governor
  → そのEndpoint利用者を守る

Remote Assurance
  → 他主体がそのWorkerのSecurity能力を判断する
```

は別問題です。

---

# 8. 実装Spikeの現在地

この調査だけでLocal Governorを成立済みとはしません。

Linuxについては、`enforce/linux-cgroup` にdelegation済みcgroupを対象とする測定Harnessを追加しました。CPU quota、Memory / PID / I/O limitの設定、Workloadのcgroup attach、`cpu.stat`による実測、`cgroup.kill`を使ったLocal opt-out相当の停止を一つの実験経路で確認できるようにしています。

現在の検証環境ではcgroup filesystemがread-onlyであるため、hard limitの実効値そのものはまだ測定できていません。一方、制御Authorityがない環境ではhard enforcement成立と誤認せず明示的に失敗すること、Harnessのunit testが通ることは確認済みです。

詳細は次を参照してください。

- `enforce/linux-cgroup/README.md`
- `enforce/linux-cgroup/RESULTS_2026-09-18.md`

残る実測では、少なくとも次を確認します。

## Windows

- Job Objectで子Processを束ねる
- CPU hard cap
- Job memory cap
- active process cap
- kill-on-close / TerminateJobObject
- EcoQoS適用
- opt-outからProcess停止までの時間

## Linux

- delegated cgroup v2の作成
- `cpu.max`
- `memory.high` / `memory.max`
- `pids.max`
- `io.max`
- cgroup kill / Process回収
- Agent crash時の後始末

## 共通

同じExecutor負荷に対して、

```text
Requested Policy
Observed Enforcement
Reported Assurance
```

が一致することを確認します。

「設定APIが成功した」だけではSpike成功としません。

---

# 参考資料

## Windows

- Microsoft Learn — JOBOBJECT_CPU_RATE_CONTROL_INFORMATION
  - https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_cpu_rate_control_information
- Microsoft Learn — JOBOBJECT_BASIC_LIMIT_INFORMATION
  - https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_basic_limit_information
- Microsoft Learn — JOBOBJECT_EXTENDED_LIMIT_INFORMATION
  - https://learn.microsoft.com/en-us/windows/win32/api/winnt/ns-winnt-jobobject_extended_limit_information
- Microsoft Learn — Job Objects
  - https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects
- Microsoft Learn — SetProcessInformation
  - https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-setprocessinformation
- Microsoft Learn — Quality of Service
  - https://learn.microsoft.com/en-us/windows/win32/procthread/quality-of-service
- Microsoft Learn — SetIoRateControlInformationJobObject
  - https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-setioratecontrolinformationjobobject

## Linux

- Linux Kernel Documentation — Control Group v2
  - https://docs.kernel.org/admin-guide/cgroup-v2.html
