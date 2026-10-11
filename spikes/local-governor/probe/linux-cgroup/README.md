# Linux cgroup v2の利用可能な機能を調べる（Capability probe）

この小さなProgramは、EKI FabricのLocal Governorを実装するものではありません。

Linux Workerが動いている環境について、「cgroup v2のResource Controlが存在するか」と「現在のProcessから実際にそのControlへ書き込めるか」を分けて観測するためのSpike用Probeです。

## なぜ必要か

Linux kernelにcgroup v2が存在していても、eki-agentへ必要なControllerが委譲されているとは限りません。

Container、systemd service、一般User processなど、起動環境によって見えるControllerや書込み権限は異なります。

そのため、将来のWorker Agentが単に

```text
OS = Linux
cgroup v2 = yes
```

という理由だけで `CPU hard limit supported` と広告すると、実際には強制できないCapabilityを上位へ伝える可能性があります。

このProbeは、その誤認を避けるための最小確認です。

## 実行

```bash
go test ./...
go run .
```

JSONで、現在のcgroup path、利用可能なController、主要Interfaceの存在・読取り・書込み可否を表示します。

ProbeはLimit値を書き換えません。書込み可否の確認では、対象fileをwrite-onlyでopenできるかだけを確認します。

## 2026-09-18の検証環境での観測

検証環境では次を確認しました。

```text
cgroup filesystem: cgroup v2

controllers:
  cpuset
  cpu
  io
  memory
  hugetlb
  pids

cpu.max:
  400000 100000

memory.max:
  4294967296

pids.max:
  max
```

一方、cgroup filesystem自体はread-onlyで、Probeから `cpu.max`、`memory.max`、`pids.max` 等へ書き込むことはできませんでした。

これはLinux一般の制約を意味しません。この検証環境では、Processへcgroup管理権限が委譲されていないことを示しています。

## このProbeから一般化してよいこと

- Controllerが存在することと、eki-agentがそのControllerを利用できることは別である。
- Governor capabilityはOS名だけでなく、実行時に利用可能なAuthorityを確認して広告する必要がある。
- Containerや制限されたService環境では、Resource Control機能がHostに存在していてもWorkerから利用できない場合がある。

## このProbeから一般化してはいけないこと

- Linuxではcgroupを書き込めない、という結論
- cgroup v2を使えば全Resourceを同じ精度で制御できる、という結論
- GPUやNetworkまでこのProbeだけでhard limitできる、という結論

次段階では、cgroup v2を明示的にdelegationした検証環境で、実際のCPU / Memory / PID / I/O enforcementを測定します。
