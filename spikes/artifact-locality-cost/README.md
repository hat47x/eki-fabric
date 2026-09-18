# Artifact Locality / Transfer Cost Spike

## このSpikeの役割

このSpikeは、EKI FabricがWorkerを選ぶとき、CPU / GPU性能だけを見て遠隔WorkerへTaskを送ると、Artifact転送によってかえって遅く・高コストになる問題を小さく実証します。

対象はRQ-008 Network-aware schedulingとQA-05 Network efficiencyです。

ArchitectureやScheduler algorithmを確定するものではありません。まず、**配置判断に最低限どの情報を残す必要があるか**を確認します。

## 背景

分散計算では、速いWorkerが最短完了Workerとは限りません。

たとえば8 GiBのModelが既にWorker Aにcacheされている一方、Worker Bは計算自体が40秒速くても、ModelをNetwork越しに新たに取得する必要がある場合があります。

単純化すると、

~~~text
estimated completion
  =
compute time
  + missing input transfer time
  + expected output transfer time
~~~

として初めて比較できます。

これは新しい考え方ではありません。

- Rayは、大きなTask argumentを既に保持するnodeをlocality-aware schedulingで優先します。
- Kubernetes schedulerのImageLocality pluginは、必要なcontainer imageを既に保持するnodeをscoreで優先します。
- BOINCにも、必要なdata fileが既にresidentなhostを優先するlocality schedulingがあります。

EKIでは、これらの知見をそのまま採用するのではなく、異質なWorker、業務Network、外部Provider、Data Classを跨ぐ前提へ広げて検証します。

## 最小モデル

'spikes/artifact-locality-cost/locality.go' では、TaskとWorkerについて次だけを使います。

### Task

- Input ArtifactのDigest
- Input Artifactのsize
- Expected output size
- Task単位の最大transfer budget

### Worker

- hard placement constraintsを満たすか
- そのWorkerでの予測compute time
- download / upload throughput
- 既にcacheされているArtifact Digest

各Workerについて、

~~~text
missing input bytes
output bytes
transfer time
compute time
estimated completion time
~~~

を算出して比較します。

## Hard constraintをscoreより先に扱う

最も重要な境界は、LocalityをSecurity条件より強くしないことです。

たとえば機密ModelがあるWorkerに既にcacheされていて、そのWorkerが最速でも、そのWorkerが現在のData Classを扱う保証を持たないなら候補から除外します。

~~~text
Security / Data / Resource feasibility
        ↓
eligible Workerだけを残す
        ↓
locality / transfer / compute costを比較
~~~

逆順にはしません。

LocalityはOptimizationであり、Authorityを作る根拠ではありません。

## Cache hitはSecurity claimではない

Workerが「このDigestをcacheしている」と広告すること自体を、Artifactの完全性根拠にはしません。

実行時には、実際に使用するArtifactが要求されたDigestと一致することを別途確認する必要があります。

また、cache情報はSchedulerから見ればstaleである可能性があります。

その場合に許容される失敗は、

- 想定よりtransferが増えた
- 想定より開始が遅れた

といったPerformance degradationです。

cacheの誤申告によって、

- 別Artifactへ差し替える
- Data Classを下げる
- Artifact read authorityを増やす

ことはできない構造にする必要があります。

## Unit Testで確認したこと

2026-09-18時点で 'go test ./...' が通過しています。

### 8 GiB Modelではcache済みの遅いWorkerが勝つ

次の条件を置いています。

~~~text
local-slower
  compute: 60s
  model: cached

remote-faster
  compute: 20s
  model: 8 GiB transfer required
  download: 100 MiB/s
~~~

remote-fasterは計算だけなら40秒速いですが、入力転送だけで約82秒必要です。

そのため、総完了時間ではlocal-slowerを優先します。

### 10 MiB Artifactでは高速Workerが勝つ

同じ40秒のcompute差があっても、Artifactが10 MiBで100 MiB/sなら転送は約0.1秒です。

この場合はremote-fasterが優先されます。

つまり、Localityそのものを絶対優先するのではなく、**Artifact量とNetwork条件に対して計算差を比較**します。

### Security constraintはLocalityより優先する

8 GiB ModelをcacheしていてComputeも速いWorkerであっても、hard placement constraintsを満たさなければ候補から除外されることをTestしています。

### Network budgetで配置を拒否できる

Taskが1 GiBまでしかNetwork transferを許容しない場合、8 GiBのmissing Artifactを持つWorkerは高速回線でも候補外になります。

これは「速く転送できるなら大量転送してよい」とはしないための境界です。

### OutputもNetwork costに含める

入力がcache済みでも、500 MiBのResultを50 MiB/sでArtifact Storeへ戻すなら約10秒かかります。

したがって、Input localityだけではなくExpected outputも配置比較へ含めます。

## このモデルから一般化してよいこと

1. Compute性能だけではPlacement costを表せない。
2. Artifact sizeとcache localityはfirst-classなPlacement inputになり得る。
3. Localityはhard Security / Data constraintを上書きしてはならない。
4. Network transferにはTask / Principal / Provider側のbudgetを設けられる方が安全である。
5. InputだけでなくOutput transferもCostに含める必要がある。
6. Cache情報はOptimization hintであり、Content IntegrityやAuthorityの根拠ではない。

## このモデルから一般化してはいけないこと

今回のModelは意図的に単純です。

まだ次を扱っていません。

- Network congestion
- RTT / connection setup
- shared linkで複数Taskが競合する場合
- compressed transfer
- peer / site-local Artifact cache
- CDN / object store location
- transfer monetary cost
- egress pricing
- Energy cost
- cache eviction probability
- WorkerがTask開始前に離脱する確率
- prefetchの費用対効果
- mass reclaim時の同時再転送
- multi-source download
- outputの保存先がWorkerごとに異なる場合
- bandwidth estimateの信頼性とfreshness

したがって、このSpikeの 'EstimatedMillis' をproduction Schedulerの最終scoreとして採用するものではありません。

## Prefetchについて

ModelやDatasetの事前配布はLocalityを高めますが、予測を外すと使われない巨大Artifactを配布することになります。

そのためPrefetchを導入する場合も、

~~~text
expected future reuse benefit
        >
prefetch transfer + cache occupancy + eviction cost
~~~

のような関係を考える必要があります。

特に業務Endpointへ「将来使うかもしれない」Modelを大量配布することは、RQ-008とhuman-first運用に反する可能性があります。

Prefetchは独立したPolicyとして、明示的なNetwork / Storage budgetの下で行う方がよいと考えます。

## Scheduler contractへの示唆

Architectureへ進む際には、少なくとも次の情報をSchedulerが受け取れる余地が必要です。

~~~text
Task
  input artifact digest + size
  expected output size
  transfer budget
  deadline / priority

Worker
  resource feasibility
  security / data feasibility
  verified or observed cache locality
  network capability / current constraint
  estimated compute duration
~~~

ここでもApplicationが具体的なHostを選ぶのではなく、必要条件とArtifact参照を宣言し、Fabric側がPlacementを判断する原則を維持します。

## 次の検証

次段階では、静的な計算式から一歩進み、複数Taskが同じArtifactを必要とするときのcache reuseとprefetchを小さく測定します。

特に次を確認します。

1. cache hit / missでの実transfer byte数
2. 連続Taskで同一Artifactを再転送しない効果
3. 複数Workerへの無駄なprefetch
4. bandwidth低下時にPlacement選択がどう変わるか
5. mass retry時のduplicate transfer amplification

## 参考資料

- Ray — Scheduling / Locality-Aware Scheduling
  - https://docs.ray.io/en/latest/ray-core/scheduling/index.html
- Kubernetes — Scheduler Configuration / ImageLocality
  - https://kubernetes.io/docs/reference/scheduling/config/
- BOINC — Locality scheduling
  - https://boinc.berkeley.edu/boinc_papers/locality/text.php
