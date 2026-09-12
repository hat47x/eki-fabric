# EKI Worker Assurance Model

## 役割

この文書は、Workerを単純な`trusted / untrusted`や一つのTrust Levelで表現せず、Taskごとに必要な保証を複数軸で照合するための上流Requirement interpretationです。具体的なProtocolや製品はまだ定義しません。

## なぜ単一Trust Levelでは足りないか

あるWorkerは企業管理端末でidentityは強いが、Confidential Computingを持たないかもしれません。別のWorkerは匿名参加者でも、暗号学的proofを生成できるかもしれません。さらに別のWorkerはattested GPUを持つが、長期reputationはありません。

これらを一列の`L0 < L1 < L2`へ並べると、本来独立した保証を誤って代替可能とみなす危険があります。

EKIでは、少なくとも次を独立軸として扱えることを要求します。

---

## Axis A — Identity Assurance

Workerが「誰／どの管理主体に属するか」をどの程度確認できるか。

候補状態:

- `anonymous` — persistent identityを要求しない
- `self-asserted` — cryptographic keyはあるが外部bindingなし
- `domain-issued` — trust domainがidentityを発行
- `managed-device` — 管理端末としてのbindingあり

Identity AssuranceはResult correctnessを意味しません。

---

## Axis B — Execution Environment Assurance

Workerが主張する実行環境について、どの程度独立したEvidenceが得られるか。

候補状態:

- `none`
- `self-reported`
- `software-measured`
- `hardware-attested`

`hardware-attested`であっても、attestation対象外のI/O、network、host componentまで自動的に信頼できるとはみなしません。

---

## Axis C — Confidentiality Capability

Task inputやmodelをWorker host ownerから秘匿できる保証。

候補状態:

- `public-only`
- `managed-boundary`
- `confidential-runtime`

`encrypted-in-transit`はこの軸の十分条件ではありません。実行時にhostから読めるなら、untrusted hostに対するConfidentialityは成立していないためです。

---

## Axis D — Result Integrity Capability

Workerを信用せずにResult correctnessをどの方式で確認できるか。

候補capability:

- `none`
- `application-validator`
- `replicable`
- `trusted-recompute-compatible`
- `attested-execution`
- `cryptographic-proof`

これらは必ずしも強さ順ではありません。例えばapplication-specific validatorがproofより安価かつ十分な場合があります。

---

## Axis E — Independence / Failure Domain

複製実行時に、複数Workerが同一攻撃者・同一管理者・同一物理host・同一software image等へ依存していないことをどの程度主張できるか。

候補dimension:

- principal
- trust domain
- device
- physical host
- operator
- executor implementation
- hardware vendor / architecture
- network / region

高保証Taskでは、単なる`worker_id`の相違だけを独立性とみなしてはなりません。

---

## Axis F — Operational Reliability History

過去のdeadline遵守率、validation成功率、availability等。

これはScheduler効率改善には利用できますが、Security Root of Trustではありません。

長期良好なWorkerが後から悪意を持つ可能性を排除しません。

---

## Axis G — Resource Measurement Assurance

CPU/GPU time、memory、network、energy等のContribution算定に使う測定値について、どの程度信頼できるか。

候補状態:

- `self-reported`
- `control-observed`
- `cross-validated`
- `attested-metering`

価値交換に利用するContributionでは、どの測定方式かをRecordへ残せる必要があります。

---

# Task側のAssurance Requirement

Taskは「trusted worker」を要求するのではなく、必要な保証を軸ごとに指定できることを目指します。

概念例:

```text
Confidentiality:
  public-only

Result Integrity:
  application-validator OR
  independent-replication(2)

Independence:
  distinct operator where known

Execution Environment:
  no attestation required
```

機密Taskなら概念的に:

```text
Confidentiality:
  confidential-runtime

Execution Environment:
  hardware-attested

Result Integrity:
  attested-execution + validator
```

ここでの表現はRequirement確認用であり、将来のwire schemaではありません。

---

# Result Assurance Profiles

## RA-0 — Best effort

Result correctnessをEKIとして追加検証しない。公開データ・低影響・利用側が独自に確認可能なTask向け。

## RA-1 — Application validated

application-specific validatorによって不正Resultを高確率で検出できる。

## RA-2 — Replicated

複数実行結果を比較する。比較関数とindependence conditionをTaskごとに必要とする。

単純な匿名Worker多数決はSybil耐性を保証しない。

## RA-3 — Anchored recomputation

untrusted Worker結果を、管理下または別の強いfailure domainでsample/recomputeし、適応的に検証量を調整する。

## RA-4 — Attested execution

Remote Attestation等によって、要求された実行環境状態に関するEvidenceをVerifierが評価する。

Attestationは「正しいprogramが正しい意味のResultを出した」ことを常に保証するわけではないため、Result semantic validationと分離する。

## RA-5 — Cryptographically verifiable execution

期待するprogram/input relationからResultが得られたことをcryptographic proofで確認する。

すべてのTask、特に大規模GPU/AI workloadで実用可能とは仮定しない。

---

# Confidentiality Profiles

## CA-0 — Public / disposable

Worker ownerにinput、model、intermediateが見えても問題ない。

## CA-1 — Managed boundary

組織管理端末・契約上の信頼境界等へ限定する。技術的にhost ownerから秘匿されるとは主張しない。

## CA-2 — Confidential execution

Remote Attestation等で期待したConfidential Computing環境を確認し、必要なkey/materialをその環境にだけreleaseする構成を許容する。

---

# Placement上の要求

1. Schedulerは性能値だけでWorkerを選んではならない。
2. Taskが要求する各Assurance Axisを満たさないWorkerを、reputation等で代替してはならない。
3. `unknown`は安全側へ評価する。情報不足を高保証として扱わない。
4. Assurance情報が失効・期限切れ・revokedの場合、継続利用を暗黙許可しない。
5. 同じWorkerでもExecutor version / CPU / GPU path等によりReliability historyを分ける余地を持つ。
6. Providerが過剰に強いAssuranceを自己宣言するだけでは成立しない。外部Evidenceまたは明示的Policy trustが必要。

# Sybil / Collusionについての現時点の結論

EKIは、匿名Participantを無制限に作成可能な条件で「N個中M個一致したから正しい」という一般的完全保証を主張しません。

高保証が必要な場合は、少なくとも次のいずれかを必要とします。

- application-specific verification
- known independent failure domains
- managed/trusted anchor recomputation
- hardware/software attestation with appropriate trust roots
- cryptographic proof

Reputationは複製率を下げる最適化には使えても、この限界そのものを消しません。

# Requirement Gap

今後具体化が必要:

- independence claimをprivacyを壊さず表現する方法
- anonymous participantに対するoperator-level Sybil detectionの限界
- attestation freshness / replay / verifier compromise
- multi-component confidential execution（CPU + GPU + I/O）の保証境界
- non-deterministic AI Resultのsemantic verifier標準化可否
- proof generation costを含むScheduler cost model
- Resource Measurement AssuranceとContribution accountingの結合
