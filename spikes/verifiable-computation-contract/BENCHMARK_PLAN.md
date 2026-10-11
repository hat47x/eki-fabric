# 検証可能計算のベンチマーク計画

## 目的

このPlanは、EKI Fabricでcryptographic proofをResult Assuranceとして利用する価値が、どのTask Classで実際に成立するかを測定するためのものです。

Framework間の勝敗を決めることより、

> Proofを使うことでEKI全体の安全性・計算費・Network費・検証費のどこが改善し、どこが悪化するか

を判断できるDataを得ることを目的とします。

## なぜ実測が必要か

Proof生成costはProgram、Input、execution trace / cycle、Proof System、Backend、Hardware、Privacy mode、preprocessing再利用条件等で大きく変化します。

そのため、他者benchmarkの単一値をEKIへそのまま適用しません。

RISC Zeroの公式datasheetでもcycle数に応じてProver Time、RAM、Proof Sizeが変化します。Joltもprofiling commandとmemory profilingを独立して提供しています。

## Version固定

各Runで必ず次を記録します。

~~~text
framework
framework version / git commit
proof backend
privacy mode
compiler / toolchain version
host OS
CPU
RAM
GPU
driver
EKI benchmark schema version
~~~

latest tagだけをRecordに残すことは禁止します。

## 最初のProof System候補

最低2系統を比較できるのが望ましいですが、同一Hardwareで再現できることを優先します。

初期候補:

- RISC Zero zkVM
- Jolt

SP1等はtoolchainと再現条件を確認できた段階で追加します。

EKI Core Contractはこの候補へ固定しません。

## Workload Matrix

### VC-B1 Hash chain

目的:
- pure deterministic CPU
- 小さいI/O
- execution量を段階的に増やしやすい

Input size / iterationを変え、native実行とproof生成のscaleを測ります。

### VC-B2 Deterministic structured-data validation

JSON相当またはbinary manifestのvalidation / transformationを使います。

目的:
- branch
- parsing
- memory access
- realistic control flow

を含めることです。

### VC-B3 Merkle / signature verification batch

目的:
- cryptographic integer workload
- verification workload
- EKI Artifact / provenance系との近さ

を確認します。

### VC-B4 Integer matrix / signal transform

floating-pointを避けた固定整数演算で、計算量とmemory量を増やします。

目的は、単純hash以外のcompute-heavy workloadを測ることです。

### VC-B5 EKI representative contract task

EKI自身の純粋関数から、小さく独立したdeterministic処理を選びます。

候補:

- manifest digest verification
- policy input normalization
- deterministic artifact metadata validation

Application semanticsをzkVMへ過剰に持ち込まないものを選びます。

## 初回benchmarkから除外するTask

- external network I/O
- external side effect
- database transaction
- LLM inference
- GPU kernel
- floating-point sensitive workload
- non-deterministic runtime

これらはBenchmark基線ができてから個別研究します。

## 各Runで測る値

### Native baseline

- native execution time
- native peak RSS
- input bytes
- output bytes

### Guest / proof execution

- guest cycles / trace length相当
- preprocessing / setup time
- reusable preprocessing size
- prover wall time
- prover CPU time
- prover peak RSS
- GPU utilization / VRAM（利用する場合）
- proof bytes
- verifier wall time
- verifier peak RSS
- verification success / failure

### EKI側overhead

- proof upload bytes
- proof verification queue wait
- normalized Verification Result size
- Artifact transfer追加量
- retry時のproof再生成要否

## Derived Metrics

最低限、次を計算します。

~~~text
prover_slowdown
  = prover_wall_time / native_wall_time

verification_ratio
  = verifier_wall_time / native_wall_time

proof_expansion
  = proof_bytes / output_bytes

peak_memory_ratio
  = prover_peak_rss / native_peak_rss
~~~

また、replicated recomputationとの比較のため、

~~~text
proof_path_cost
  = prover cost
  + proof transfer cost
  + verifier cost

replication_path_cost(k)
  = k * native execution cost
  + result transfer / comparison cost
~~~

を同じ単位へ変換できる材料を残します。

単純なCPU秒だけでなく、Network / GPU / Cloud costを後から外部Policyで換算できるraw measurementを保存します。

## Break-evenを一つの数字へ固定しない

Proofを使う目的はcost削減だけではありません。

悪意Worker耐性、Sybil耐性、独立Worker不足、機密性要件等によって、多少高価でもProofを選ぶ価値があります。

したがってBenchmarkは、

> slowdownがN倍未満なら採用

という単一Gateにしません。

Task Assurance requirementとresource costを分けて比較します。

## Applicability判定

各Task Classについて、最終的に次のいずれかへ分類します。

~~~text
candidate
  current proof technologyで実用候補

conditional
  高Assurance Task等、条件付きで有力

research
  cost / runtime / determinism問題が未解決

poor-fit
  EKIの現在用途ではproofより他方式が適切
~~~

## AI / GPU workloadへの後続Gate

LLM / GPUについては、次を確認するまで「対応可能」と記述しません。

- execution modelがProof Systemで再現可能か
- floating-point / quantization semantics
- Model Digest binding
- GPU kernel / accelerator traceの扱い
- prover hardware requirement
- end-to-end latency
- Model privacy
- output semantic correctnessとの分離

まずCPU deterministic workloadの実測基線を作ります。

## 実行環境不足時の扱い

Benchmark runnerに必要toolchainがない場合、他者benchmark値をEKI実測として転記しません。

代わりに、

- 未実施理由
- 必要toolchain
- exact version
- 実行command
- expected output schema

を残し、実行可能runnerへhandoffします。

## Result schema案

~~~json
{
  "schema": "eki.vc-benchmark/v1",
  "framework": "...",
  "framework_revision": "...",
  "proof_backend": "...",
  "privacy_mode": "...",
  "workload": "...",
  "workload_params": {},
  "hardware": {},
  "native": {
    "wall_ms": 0,
    "peak_rss_bytes": 0
  },
  "proof": {
    "cycles": 0,
    "preprocess_ms": 0,
    "prove_ms": 0,
    "verify_ms": 0,
    "peak_rss_bytes": 0,
    "proof_bytes": 0
  }
}
~~~

## Acceptance条件

初期Benchmarkは次を満たせば比較可能とみなします。

1. 同一Hardwareでnative baselineとproof pathを測る。
2. Framework version / commitを固定する。
3. 少なくとも3種類のdeterministic workloadを測る。
4. 各Workloadで3段階以上のsize / cyclesを測る。
5. prove timeだけでなくpeak memory / proof bytes / verify timeを取る。
6. warm-up / preprocessing再利用条件を明記する。
7. public-io / private-input等のPrivacy modeを混同しない。
8. raw JSONを保存し、表だけを正本にしない。

## 現在地

2026-09-21時点のこの実行環境にはRust / Cargo / zkVM toolchainがなく、実prover benchmarkは未実施です。

Contract Spikeは先行して完了させ、Benchmark結果が得られるまでPhase 4の「proof生成cost / GPU・AI適用限界」は未解決として残します。
