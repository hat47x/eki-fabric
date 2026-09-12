# Worker Verification and Attestation Research

## 役割

malicious / modified Workerを前提としたResult AssuranceとConfidentiality Assuranceを具体化するための調査メモです。RequirementやArchitectureの正本ではありません。

## 1. BOINC — replication / validation / adaptive replication

BOINCはVolunteer Hostがhardware failureまたはmalicious modificationによって誤Resultを返すことを通常の前提として扱います。Job replicationでは、application特性に応じてreplicationなし、複製、validator等を使い分け、「one size fits allではない」としています。

2026年時点のAdaptive Replicationでは、host + app versionごとの連続validation成功数を使い、信頼度の高いhostでは複製頻度を減らしてvalidation overheadを抑えます。通常の常時複製は大きな計算コストを持つため、assuranceとcompute efficiencyを分けて最適化する発想が参考になります。

### EKIへの示唆

- Result validationはWorker identityと分離する。
- Executor/versionごとにreliability historyを分ける。
- reputation/historyはreplication量最適化には使えるが、root of trustにはしない。
- Task/applicationごとにvalidator方式を選ぶ。
- EKIではさらにSybil/collusionを明示し、単純なhost数一致を高保証として一般化しない。

References:
- https://github.com/BOINC/boinc/wiki/Job-replication
- https://github.com/BOINC/boinc/wiki/Adaptive-Replication

## 2. IETF RATS — attestation rolesを分離する

RFC 9334はRemote Attestationを特定TEEやwire protocolへ固定せず、次の役割を分離します。

- Attester — Evidenceを生成
- Verifier — EvidenceをReference Values / Endorsements / policyで評価しAttestation Resultsを生成
- Relying Party — Attestation Resultsを自身のpolicyで利用

特に重要なのは、EvidenceそのものをRelying Partyが盲信するのでなく、Verifierによるappraisalと、Relying Party側の利用policyを分離する点です。

### EKIへの示唆

EKIでWorker Agentが返すattestation blobを直接`trusted=true`へ変換せず、概念的に

```text
Worker / Attester
      ↓ Evidence
Attestation Verifier
      ↓ Attestation Result
EKI Placement / Relying Party
      ↓ own policy
placement / key release
```

と分ける候補が有力です。

またRFC 9334はEvidence自体がPIIやsystem-identifying informationを含み得ることを指摘しているため、ad-hoc参加でattestationを強く要求するとPrivacyとParticipation easeが衝突します。

Reference:
- https://www.rfc-editor.org/rfc/rfc9334.html

## 3. NVIDIA GPU Attestation / Confidential Computing

2026年のNVIDIA documentationでは、Hopper H100以降の対応GPUについてremote/local attestationが提供され、Blackwellも同様のSDK/API flowを利用します。Remote verifierからJWT/EAT形式等のattestation resultを得る実装例があります。

一方、Confidential Computingとして使う場合は対応GPUだけでなくConfidential VM、driver、CC mode等の前提があります。したがって「NVIDIA GPUだからCA-2」という扱いはできません。

### EKIへの示唆

- GPU有無とConfidentiality Capabilityを別軸にする。
- attestationはVendor-specific Evidence Providerとして差し替え可能にする方向がよい。
- CPU TEE + GPU + interconnect + I/Oを含むend-to-end confidentialityは、単GPU attestationだけから一般化しない。
- key releaseの条件にfresh attestation resultを使える余地を持つ。

References:
- https://docs.nvidia.com/attestation/quick-start-guide/latest/attestation-examples/hopper_single_gpu.html
- https://docs.nvidia.com/attestation/quick-start-guide/latest/attestation-examples/blackwell_single_gpu.html
- https://docs.nvidia.com/attestation/attestation-client-tools-sdk/latest/gpu_and_switch_attestation.html

## 4. zkVM / Verifiable Computation

RISC ZeroのzkVMは、expected guest programのImageIDに対して、untrusted hostで実行された結果がそのprogram executionから生成されたことをReceiptで検証できるmodelを提供します。Resultを返したhost自体を信頼する必要を減らせる点で、EKIのad-hoc Workerと非常に相性がよい考え方です。

ただしproof generation cost、対応命令/runtime、I/O model、大規模GPU/AI workloadへの適用性はTaskによって大きく異なります。

### EKIへの示唆

- `cryptographic-proof`をResult Verification Providerの一種として扱う価値が高い。
- proof-capable TaskではWorker identity / reputationへの依存を大きく減らせる。
- EKI Coreを一つのzkVMへ固定しない。
- proof生成コストを含めたplacement/economic modelが必要になる可能性がある。

References:
- https://github.com/risc0/risc0
- https://docs.rs/risc0-zkvm/latest/risc0_zkvm/struct.Receipt.html

## 5. Attestationとcorrectnessを混同しない

Remote Attestationは「期待された測定値・状態を持つenvironmentである」ことのEvidenceを評価する仕組みであり、application inputに対するbusiness-semantic correctnessそのものを自動的に保証するわけではありません。

例えば正規LLM Runtimeがattested environmentで正しく動いても、LLM出力自体が事実として正しいとは限りません。

したがってEKIでは概念上:

```text
Execution Environment Assurance
              !=
Result Semantic Assurance
```

を維持する必要があります。

## 6. 現時点の比較

| 方式 | Worker identity不要度 | Result correctness | Confidentiality | Overhead | 適用範囲 |
| --- | --- | --- | --- | --- | --- |
| Application Validator | 高 | workload依存で高くできる | なし | 低〜中 | validatorを作れるTask |
| Replication / Quorum | 中 | independence前提 | なし | 高 | 比較可能なTask |
| Trusted recompute | 中 | anchor trust次第 | anchor次第 | 中〜高 | sample検証可能 |
| Remote Attestation | 低〜中 | environment中心 | TEE構成なら強化可能 | 中 | 対応hardware/runtime |
| Cryptographic Proof | 高 | program executionに強い | proof方式次第 | 高〜非常に高 | proof可能なTask |

## 7. EKI要件への反映候補

- Attestation EvidenceとAttestation Resultを区別する。
- Verifier自体もtrust/policy対象とする。
- Result Verification ProviderとAttestation Providerを別extension pointとして検討する。
- assurance requirementをTask側、capability/evidenceをWorker側へ持たせる。
- assurance不足時に「最も近いWorkerへ落とす」のではなく、fail closed / queue / alternative verificationを選べるようにする。
- verification overheadをSchedulerのcost modelへ入れる。

## 8. 追加調査

- AMD SEV-SNP / Intel TDX等CPU側attestationとの統合
- TPM-backed managed endpoint assurance
- confidential GPU data pathの実際のtrust boundary
- privacy-preserving attestation / anonymous attestation
- zkVM / SNARK/STARKのGPU・LLM inferenceへの適用現状
- malicious verifier / reference-value providerへの耐性
- attestation revocation / freshness / clock trust
