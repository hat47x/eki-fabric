# Verifiable Computation Contract Spike

## このSpikeの役割

このSpikeは、EKI Fabricでcryptographic proofをResult Assuranceへ利用するときに、特定zkVMやproof systemへCoreを結び付けず、

- どのProgramを実行したか
- どのInputに対する実行か
- どのOutputがその実行結果か
- どのProof Systemで検証したか
- 誰がProofを検証したか
- そのVerification ResultをどのTask Policyが受理したか

を分離して扱えることを確認するための最小実証です。

対象は主にRQ-025 Attestation / proof extensibility、RQ-039 End-to-end provenance、RQ-048 Freshness and revocation of time-bounded authority、RQ-051 Control-plane authority separation、Assurance Model RA-5、QA-02 Result assurance honestyです。

このSpikeは、RISC Zero、Jolt、SP1等の採用を決定するものではありません。また、実proof生成性能を測定したSpikeでもありません。

## 先に固定する境界

Verifiable Computationを導入しても、次を同一視しません。

~~~text
Proof valid
  ≠ Program approved
  ≠ Application result is semantically useful
  ≠ External side effect happened
  ≠ Resource usage was measured correctly
  ≠ Input was private
~~~

Proofが強く保証できる範囲を、Program / Input / Output relationへ限定して捉えます。

## Execution Claim

最小Claimは次へbindingします。

~~~text
StatementVersion
TaskID
AttemptID
ProgramDigest
InputDigest
OutputDigest
~~~

Program名やtagではなくDigestを使います。

直前のExecutor Provenance Spikeで確認したExecutor Admissionと接続すると、

~~~text
Executor Admission
  approved Program Digest
        ↓
Execution Claim
  same Program Digest
        ↓
Proof Verification
~~~

という関係になります。

## Program Digestを必ず再確認する理由

cryptographic proofは、悪意あるProgramの正しい実行を証明することもできます。

したがって、

> Proofがvalidだったので、そのProgramはEKIで実行してよい

とは扱いません。

Spikeでは、VerifierがvalidとしたClaimでも、Relying Partyが要求するApproved Program Digestと異なればrejectします。

Program AdmissionとExecution Correctnessは別の保証です。

## Input / Output binding

同じProgramでも、Inputが異なれば別のTaskです。

そのためInput DigestをTask Request側の期待値へbindingします。

Outputも、Proofが拘束したOutput Digestと、実際に返却されたResult bytesを再hashして比較します。

これにより、

- 別Input向けのProofを流用する
- Proof検証後にOutput bytesだけ差し替える
- 別AttemptのProofを再利用する

といった経路を閉じます。

## Proof ArtifactとVerification Resultを分ける

Workerが提出するものはProof Artifactです。

~~~text
Proof Artifact
  Proof System ID
  Execution Claim
  Proof bytes
~~~

EKI側のVerifierはProof System固有adapterを使って検証し、成功した場合だけnormalized Verification Resultを発行します。

~~~text
Verification Result
  Task / Attempt
  Program / Input / Output Digest
  Proof System ID
  Disclosure Profile
  Proof Digest
  Verifier ID
  Verifier Policy Revision
  VerifiedAt
  ExpiresAt
~~~

Relying PartyはRaw Proof System固有形式へ直接依存しません。

## Mock Proofについて

このSpikeの`MockProof`は、Contract bindingをUnit Testするためだけのstand-inです。

Ed25519署名でExecution Claimを拘束していますが、これはzkVMやverifiable computationの実装ではありません。

Production Adapterでは、この部分を各Proof Systemのnative verifierへ置き換える必要があります。

この点を誤解しないことが重要です。

## Verifier Authority

Worker自身が、

~~~text
proof verified = true
~~~

と自己申告しても受理しません。

Relying PartyはPinned Verification Authorityの署名を確認します。

これはRemote Attestation Spikeで確認した、

~~~text
Evidence producer
  ≠ Verifier
  ≠ Relying Party
~~~

という役割分離と同型です。

## Relying Party Policy

VerifierがProofをvalidと判定した後も、Task固有Policyを別に適用します。

少なくとも次を再確認します。

- Task ID / Attempt ID
- Approved Program Digest
- Expected Input Digest
- actual Output Digest
- Accepted Proof System
- Accepted Verifier ID
- Minimum Verifier Policy Revision
- Verification Result expiry
- Private Input Proof要否

したがって、

~~~text
Verifier accepted
  ≠
Task result accepted
~~~

です。

## Proof SystemをPolicy対象にする理由

Proof Systemごとに、

- security assumptions
- supported instruction set
- preprocessing / setup
- verifier implementation
- privacy semantics
- proof size
- prover cost
- hardware acceleration
- version lifecycle

が異なります。

そのため、`ProofSystemID`を単なるmetadataではなくRelying Party Policyの一部として扱います。

## Verifiable ComputationとZero Knowledgeは別

Verifiable Computationだからといって、Inputが秘匿されるとは限りません。

Joltの現行documentでも通常Proofとzero-knowledge featureを分けています。

このSpikeでは、Verifier側のProof Profileが

~~~text
public-io
private-input
~~~

のどちらかをnormalized claimとして付与します。

Taskがprivate-input proofを要求する場合、通常のpublic-io profileではrejectします。

Clientが自分で「このProofはZK」と宣言しても受理する構造にはしません。

## Semantic correctnessとの分離

Proofが確認するのは、指定Programが指定Inputから指定Outputを生成したというrelationです。

たとえばAI Taskでは、

> 指定Model / RuntimeがこのOutputを生成した

ことを証明できたとしても、

> その回答が正しい
> 有害でない
> 業務上適切

ことは別です。

Application validator、comparison、human review等のResult Assuranceを必要に応じて重ねます。

## External side effectとの分離

Proofでlocal computationを証明しても、

- 決済が実際に成立した
- Emailが届いた
- 外部DB transactionがcommitした
- GitHub Issueが作成された

といった外部世界の事実までは通常証明できません。

Side-effect Task Uncertain Outcome Spikeで確認したReceipt / reconciliation境界を別に必要とします。

## Resource Measurementとの分離

ProofはExecution Correctnessを強くできても、Contributionに使うCPU / GPU / Energy量を自動的には証明しません。

Contribution Measurement SpikeのResource Measurement Assuranceを別に維持します。

## 適用候補の暫定分類

### Candidate

最初に試しやすいのは次です。

- deterministic
- side-effect free
- bounded input / output
- stable Program Digest
- integer / hash / parser / validation等の再現可能な処理
- 高いResult Assurance価値があり、redundant recomputation削減余地がある

例として、hash chain、Merkle / signature verification、deterministic manifest validation、整数ベースの変換等が考えられます。

### Supplemental

外部副作用を持つTaskでは、Proofは副作用前後のlocal computationを保証する補助材料にはなりますが、外部Effect Receiptを置き換えません。

### Research

次は実測が必要です。

- 大規模GPU workload
- LLM inference
- floating-point依存の数値処理
- 非決定的runtime
- 大規模memory workload
- long-running Task

### Poor fit

現時点では次をProofだけで解決しようとしません。

- arbitrary network world stateへ強く依存するTask
- 外部transaction自体の成立確認
- Proof生成overheadがnative実行価値を大幅に上回る極小Task

## 実Prover Benchmarkは未実施

現在の実行環境にはRust / Cargo / zkVM toolchainがありません。

したがって、このSpikeでは実prover runtime、peak memory、proof size、verification latencyの値を生成していません。

既存の公式資料から値を転記してEKIの実測値として扱うこともしません。

別紙`BENCHMARK_PLAN.md`に、再現可能な実測条件を定義しています。

## Unit Testで確認したこと

2026-09-21時点でGo 1.23.2を使い、`go test ./...`を実行して10 Testが通過しています。

主なScenarioは次です。

1. Program / Input / Output / AttemptへProof Verification Resultをbindingできる。
2. valid Proofでも未承認ProgramならRelying Partyが拒否する。
3. 別Input向けProofを流用できない。
4. Proof検証後にOutput bytesを改変すると拒否する。
5. Worker自己署名のVerification Resultを拒否する。
6. stale Verifier Policy Revisionを拒否する。
7. expired Verification Resultを拒否する。
8. public-io Proofをprivate-input requirementへ使えない。
9. private-input profileならprivate-input requirementを満たせる。
10. Execution Claimのschema / statement versionをPolicyへbindingできる。

## Blob整合性

ローカルTest済みファイルとGitHub blob SHAを照合しています。

~~~text
go.mod
8014ce4faca1a5e662d35e6647390e7c309105c1

contract.go
0371bab15642478c277a0570c477610923665673

contract_test.go
03bf24f14518c848c72cbc907ae442d8ec4e3a6f
~~~

## このSpikeで一般化してよいこと

- ProofをProgram / Input / Output / AttemptへbindingするContractを作れる。
- Proof System固有検証とEKI Relying Party Policyを分離できる。
- Proof Verification AuthorityをWorkerから分離できる。
- Executor AdmissionとProof validityを別Decisionとして維持できる。
- Verification ResultのPolicy Revision / expiryを扱える。
- Verifiable ComputationとPrivate Input / ZK要件を分離できる。
- Raw ProofをCore全体へ露出せずnormalized Verification Resultへ縮約できる。

## このSpikeで一般化してはいけないこと

まだ次は実証していません。

- 実zkVM proof
- 実soundness / security parameter
- trusted setup / transparent setup比較
- real proof size
- prover runtime
- verifier runtime
- peak memory
- GPU proving
- recursive proof
- aggregation
- proof cache
- private witness handling
- verifier compromise
- proof-system version migration
- nondeterministic / floating-point workload
- AI / GPU workload

## Architectureへの示唆

Core Contractでは、少なくとも次を別々に扱う方が自然です。

~~~text
Execution Claim
Proof Artifact
Proof System Adapter
Verification Authority
Normalized Verification Result
Relying Party Requirement
Application Result Validation
~~~

Proof Artifactのwire formatを一種類へ固定せず、Proof System Adapterがnormalized Verification Resultへ変換する構造を優先します。

## 参考資料

- RISC Zero Developer Docs
  - https://dev.risczero.com/
- RISC Zero Performance Datasheet
  - https://dev.risczero.com/datasheet.pdf
- Jolt Quickstart
  - https://jolt.a16zcrypto.com/usage/quickstart.html
- Jolt Architecture
  - https://jolt.a16zcrypto.com/how/architecture/architecture.html
- Jolt zkVM profiling
  - https://jolt.a16zcrypto.com/usage/profiling/zkvm_profiling.html
