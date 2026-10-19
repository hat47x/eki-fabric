# Workerの検証と遠隔証明の調査（Verification and Attestation Research）

## 役割

悪意のある、または改変されたWorkerを前提にして、Result AssuranceとConfidentiality Assuranceを具体化するための、調査メモです。RequirementやArchitectureの正本ではありません。

## 1. BOINC — replication、validation、adaptive replication

BOINCは、Volunteer Hostが、hardwareの障害や悪意のある改変によって、誤ったResultを返すことを、通常の前提として扱います。Jobのreplicationでは、applicationの特性に応じて、replicationなし、複製、validatorなどを使い分け、「one size fits allではない」としています。

2026年時点のAdaptive Replicationでは、hostとapp versionごとの、連続したvalidationの成功数を使います。信頼度の高いhostでは、複製の頻度を減らして、validationのoverheadを抑えます。通常の、常時の複製は、大きな計算コストを持ちます。そのため、assuranceとcompute efficiencyを分けて最適化する発想が、参考になります。

### EKIへの示唆

- Result validationを、Worker identityから分離する。
- Executorとversionごとに、reliability historyを分ける。
- reputationとhistoryは、replicationの量を最適化するのには使えるが、root of trustにはしない。
- Taskとapplicationごとに、validatorの方式を選ぶ。
- EKIでは、さらにSybilとcollusionを明示する。単純なhostの数の一致を、高い保証として一般化しない。

References:
- https://github.com/BOINC/boinc/wiki/Job-replication
- https://github.com/BOINC/boinc/wiki/Adaptive-Replication

## 2. IETF RATS — attestationのroleを分離する

RFC 9334は、Remote Attestationを、特定のTEEやwire protocolに固定しません。そして、次のroleを分離します。

- Attester — Evidenceを生成する。
- Verifier — EvidenceをReference Values、Endorsements、policyで評価して、Attestation Resultsを生成する。
- Relying Party — Attestation Resultsを、自分のpolicyで利用する。

特に重要なのは、EvidenceそのものをRelying Partyが盲信するのではなく、Verifierによるappraisalと、Relying Party側の利用policyを、分離する点です。

### EKIへの示唆

EKIで、Worker Agentが返すattestationのblobを、直接`trusted=true`に変換しません。概念上、次のように分ける候補が、有力です。

```text
Worker / Attester
      ↓ Evidence
Attestation Verifier
      ↓ Attestation Result
EKI Placement / Relying Party
      ↓ own policy
placement / key release
```

また、RFC 9334は、Evidence自体が、PIIやsystemを識別する情報を含み得ることを、指摘しています。そのため、アドホックな参加でattestationを強く要求すると、Privacyと、参加のしやすさが、衝突します。

Reference:
- https://www.rfc-editor.org/rfc/rfc9334.html

## 3. NVIDIA GPU Attestation / Confidential Computing

2026年のNVIDIAのdocumentationでは、Hopper H100以降に対応するGPUについて、remoteとlocalのattestationが提供されています。Blackwellも、同様のSDKとAPIの流れを利用します。remote verifierから、JWTやEAT形式などのattestation resultを得る、実装の例があります。

一方、Confidential Computingとして使う場合は、対応するGPUだけでなく、Confidential VM、driver、CC modeなどの前提があります。したがって、「NVIDIAのGPUだからCA-2」という扱いはできません。

### EKIへの示唆

- GPUがあることと、Confidentiality Capabilityとを、別の軸にする。
- attestationは、Vendor固有のEvidence Providerとして、差し替えられる方向がよい。
- CPUのTEE、GPU、interconnect、I/Oを含む、end-to-endの機密性を、単一のGPUのattestationだけから一般化しない。
- key releaseの条件に、新しいattestation resultを使える余地を持つ。

References:
- https://docs.nvidia.com/attestation/quick-start-guide/latest/attestation-examples/hopper_single_gpu.html
- https://docs.nvidia.com/attestation/quick-start-guide/latest/attestation-examples/blackwell_single_gpu.html
- https://docs.nvidia.com/attestation/attestation-client-tools-sdk/latest/gpu_and_switch_attestation.html

## 4. zkVM / Verifiable Computation

RISC ZeroのzkVMは、期待するguest programのImageIDに対して、untrustedなhostで実行された結果が、そのprogramの実行から生成されたことを、Receiptで検証できるmodelを提供します。Resultを返したhost自体を信頼する必要を減らせます。EKIのアドホックなWorkerと、非常に相性がよい考え方です。

ただし、proofを生成するコスト、対応する命令とruntime、I/O model、大規模なGPUやAIのworkloadへの適用の可能性は、Taskによって大きく異なります。

### EKIへの示唆

- `cryptographic-proof`を、Result Verification Providerの一種として扱う価値が高い。
- proofに対応できるTaskでは、Worker identityやreputationへの依存を、大きく減らせる。
- EKI Coreを、一つのzkVMに固定しない。
- proofを生成するコストを含めた、placementと経済のmodelが必要になる可能性がある。

References:
- https://github.com/risc0/risc0
- https://docs.rs/risc0-zkvm/latest/risc0_zkvm/struct.Receipt.html

## 5. Attestationと、正しさを混同しない

Remote Attestationは、「期待された測定値と状態を持つenvironmentである」ことのEvidenceを評価する仕組みです。applicationのinputに対する、業務の意味の上での正しさそのものを、自動的に保証するわけではありません。

たとえば、正規のLLM Runtimeが、attestされたenvironmentで正しく動いても、LLMの出力自体が、事実として正しいとは限りません。

したがって、EKIでは、概念上、次の関係を維持する必要があります。

```text
Execution Environment Assurance
              !=
Result Semantic Assurance
```

## 6. 現時点での比較

| 方式 | Worker identityが不要な度合い | Resultが正しいこと | Confidentiality | Overhead | 適用範囲 |
| --- | --- | --- | --- | --- | --- |
| Application Validator | 高 | workloadによっては高くできる | なし | 低〜中 | validatorを作れるTask |
| Replication / Quorum | 中 | independenceが前提 | なし | 高 | 比較できるTask |
| Trusted recompute | 中 | anchorのtrust次第 | anchor次第 | 中〜高 | sampleで検証できる |
| Remote Attestation | 低〜中 | environmentが中心 | TEEの構成なら強化できる | 中 | 対応するhardwareとruntime |
| Cryptographic Proof | 高 | program executionに強い | proofの方式次第 | 高〜非常に高 | proofを作れるTask |

## 7. EKIの要件への反映の候補

- Attestation EvidenceとAttestation Resultを区別する。
- Verifier自体も、trustとpolicyの対象とする。
- Result Verification ProviderとAttestation Providerを、別のextension pointとして検討する。
- assurance requirementをTask側に、capabilityとevidenceをWorker側に、持たせる。
- assuranceが足りないときに、「最も近いWorkerに落とす」のではなく、fail closed、queue、別のverificationを選べるようにする。
- verificationのoverheadを、Schedulerのcost modelに入れる。

## 8. 追加の調査

- AMD SEV-SNPやIntel TDXなど、CPU側のattestationとの統合
- TPMに支えられた、managed endpointのassurance
- confidentialなGPUのdata pathの、実際のtrust boundary
- プライバシーを保護するattestationと、匿名のattestation
- zkVM、SNARK、STARKの、GPUとLLM inferenceへの適用の現状
- 悪意のあるverifierやreference-valueのproviderへの耐性
- attestationのrevocation、freshness、clockのtrust
