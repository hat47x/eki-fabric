# Remote Attestation Abstraction Spike

## このSpikeの役割

このSpikeは、EKI FabricでRemote Attestationを利用する場合に、特定TEEやVendor固有形式へCoreを結び付けず、

- Attester
- Evidence
- Verifier
- Attestation Result
- Relying Party

を分離して扱えることを確認するための最小実証です。

対象は主に次です。

- RQ-025 Attestation / proof extensibility
- RQ-040 Audit without unnecessary disclosure
- RQ-047 Security-relevant capability claims require a trust basis
- RQ-048 Freshness and revocation of time-bounded authority
- RQ-051 Control-plane authority separation
- Assurance Model Axis C — Execution Environment
- Axis H — Verification Authority Assurance
- Axis I — Evidence Freshness / Revocation
- RA-4 — Attested execution
- QA-01 Security containment
- QA-03 Confidentiality fit

このSpikeは、Intel TDX、AMD SEV-SNP、Arm CCA、TPM、AWS Nitro Enclaves等の採用を決定するものではありません。

## 背景

EKIでは、Workerが

> Confidential Runtimeです

と自己申告しただけで、機密Taskの配置条件を満たしたとは扱いません。

一方で、Remote Attestationを導入したからといって、

> Attestation済みなので、このWorkerへどんなTaskでも置いてよい

とも扱いません。

確認したいことは、より限定的です。

> 要求されたExecution Environmentについて、どのEvidenceを、誰が、どのPolicyで評価し、その結果をどのRelying Party Policyが利用したか。

この連鎖を追跡できることが重要です。

## RFC 9334との対応

IETF RATS Architecture（RFC 9334）は、Remote Attestationを概念的に次の役割へ分けます。

~~~text
Attester
  ↓ Evidence

Verifier
  Evidence
  + Reference Values
  + Endorsements
  + Appraisal Policy for Evidence
  ↓
Attestation Result

Relying Party
  Attestation Result
  + Appraisal Policy for Attestation Results
  ↓
application-specific decision
~~~

EKIでは、この分離を有力な参照モデルとして扱います。

特に重要なのは、

> VerifierがEvidenceを受理したことと、Relying PartyがTask配置を許可することは別の判断

という点です。

## Spikeの最小モデル

今回の実装では、次の流れを使います。

~~~text
Worker / Attester
  ↓ signed Evidence

EKI Verifier
  - Attester / Trust Domain / Evidence Key binding
  - Challenge Nonce
  - Evidence freshness
  - Environment Class
  - Reference Measurement相当
  - Verifier Policy Revision
  ↓ signed Attestation Result

Placement Relying Party
  - Verifier Trust Root
  - Worker Identity binding
  - Trust Domain binding
  - Required Environment Class
  - Minimum Verifier Policy Revision
  - Result expiry
  ↓
placement decision
~~~

実装上は簡略化していますが、Evidenceを評価するAuthorityと、Taskを置いてよいかを判断するAuthorityを分けています。

## Evidence

最小Evidenceは次を持ちます。

~~~text
AttesterID
TrustDomain
EnvironmentClass
Measurement
Nonce
CollectedAt
~~~

実際のTEE Evidenceには、より多くのVendor固有Claimが含まれます。

今回の`Measurement`は、Reference Valuesとの照合対象を一つに単純化したものです。

## Evidence Key Binding

Evidence署名鍵は、

~~~text
AttesterID
TrustDomain
KeyID
PublicKey
~~~

へbindingします。

したがって、署名自体が正しくても、別Trust DomainのWorkerとして読み替えることはできません。

これはFederation Trust Binding Spikeで確認したnamespace bindingと同じ原則です。

## Freshness

Remote AttestationではReplay耐性が重要です。

RFC 9334はfreshnessをArchitecture上の重要論点として扱い、RFC 9711 EATもfreshness mechanismを必須としています。

このSpikeでは、単純化して次を併用します。

- Challenge Nonce
- Evidence age
- Nonce replay cache
- Attestation Result expiry

### Challenge Nonce

Verifierが期待するNonceとEvidence内のNonceが一致しなければ拒否します。

### Nonce Replay

同じAttester / NonceのEvidenceを再利用して、別の新しい評価として扱うことを拒否します。

### Evidence Age

Evidenceが許容されたfreshness windowより古い場合は拒否します。

### Result Expiry

Verifierが一度発行したAttestation Resultも無期限には使いません。

Relying PartyはResult expiryを確認します。

## Freshnessは絶対的な「現在」を保証しない

Evidenceを取得した直後にWorker状態が変わる可能性は残ります。

Remote Attestationのfreshnessは、

> この状態が永続する

ことを証明するものではありません。

実際にはTaskの機密性、実行時間、TEE特性等に応じて、

- 短いTTL
- Taskごとの再Attestation
- Session binding
- Key release直前の確認

などを比較する必要があります。

今回のSpikeでは具体TTL値をArchitectureとして固定しません。

## Verifier PolicyとRelying Party Policyを分ける

Verifierは、

> EvidenceがVerifier Policyを満たした

というAttestation Resultを作ります。

しかし、Taskによって必要な条件は異なります。

たとえばVerifierが

~~~text
environment = confidential-vm
~~~

を妥当と判断しても、Task側が

~~~text
required = gpu-confidential
~~~

を要求しているなら、Placementは許可しません。

つまり、

~~~text
Evidence valid
    ≠
Task placement authorized
~~~

です。

Unit Testでも、この境界を確認しています。

## Policy Revision

VerifierのAppraisal Policy自体が更新される場合があります。

たとえば、あるFirmware Measurementが脆弱と判明し、Verifier Policy Revision 7から8へ更新された場合です。

Relying Partyは、

~~~text
MinVerifierPolicyRevision
~~~

を要求でき、古いPolicyで発行されたResultを拒否できます。

これはExecutor ApprovalのPolicy Revisionと同じfreshness原則です。

## WorkerはAttestation Resultを自己発行できない

悪意あるWorkerが、

~~~text
EnvironmentClass = confidential-vm
VerifierID = trusted-verifier
~~~

という構造体を自分の鍵で署名しても、Relying Partyは受理しません。

Relying PartyはPinned Verifier Trust Rootを使ってAttestation Resultを検証します。

AuthenticationしたWorker Identityと、Verification Authorityは別Roleです。

## Raw EvidenceをRelying Partyへ広く渡さない

RFC 9334でもEvidenceにはprivacy-sensitiveな情報が含まれ得ることが指摘されています。

EKIでは、SchedulerやClientが必要とするのが

> このWorkerは要求したEnvironment Classを満たす

という判断であるなら、Vendor固有のRaw Evidenceを広く配布する必要はありません。

このSpikeのAttestation Resultには、

- Attester ID
- Trust Domain
- normalized Environment Class
- Verifier ID
- Policy Revision
- expiry
- Evidence Digest

だけを持たせ、Raw Measurement自体はRelying Party contractへ出していません。

Evidence Digestは、後からAudit / provenanceへ辿るための最小参照として残しています。

## PrivacyとAuditの両立

Raw Evidenceを全利用者へ公開しないことと、Audit不能にすることは別です。

概念的には、

~~~text
Raw Evidence
  ↓ restricted retention / verifier side

Evidence Digest
  ↓

Attestation Result
  ↓ placement / audit
~~~

とできます。

実際にRaw Evidenceを保存するか、どれだけ保存するかはData Classや運用Policyによって変わります。

## AttestationとResult Correctnessを分ける

Attestationは主に、

> どのExecution Environmentで動いているか

の保証を強めます。

これは、

> Application Resultが意味的に正しい

こととは別です。

悪意あるApplication codeが、正しくAttestedされたTEE内で意図的に誤結果を返すことも理論上可能です。

したがってEKIでは、

~~~text
Execution Environment Assurance
Result Integrity Assurance
Executor Provenance
~~~

を独立軸として維持します。

## Executor Admissionとの接続

直前のExecutor Provenance Spikeでは、

> 何を実行してよいか

をAdmissionしました。

今回のRemote Attestation Spikeでは、

> どの環境へ置いてよいか

を扱います。

概念的には、

~~~text
Executor Approval
  このDigestのExecutorを実行してよい

Attestation Result
  このWorkerのExecution Environmentは要求条件を満たす

Placement Policy
  両方を満たす場合のみWorkOrderへ進める
~~~

という合成になります。

どちらか一方だけでは十分ではありません。

## Confidential Computingとの接続

機密Taskでは、Attestation Resultを確認した後にのみ、

- Artifact decryption key
- Model key
- Secret
- Data capability

をreleaseする構成が考えられます。

ただしこのSpikeではKey Release自体は実装していません。

また、

- CPUだけがConfidential
- GPUはAttestation対象外
- Host I/Oは信頼外

といった境界もあり得ます。

「TEE」という単語だけでend-to-end Confidentialityを主張しません。

## Unit Testで確認したこと

2026-09-21時点でGo 1.23.2を使い、

~~~bash
go test ./...
~~~

を実行し、Testは通過しています。

確認した主なScenarioは次です。

1. Evidence評価とPlacement判断を別Roleで成立させられる
2. Verifierが受理したEnvironmentでも、Task要求が異なればPlacementを拒否できる
3. Worker A向けAttestation ResultをWorker Bへ流用できない
4. Trust Domainをすり替えたEvidenceをVerifierが拒否する
5. Challenge Nonce不一致を拒否する
6. 同じNonceのEvidence replayを拒否する
7. freshness window外のEvidenceを拒否する
8. Reference Measurement相当のPolicyに合わないEvidenceを拒否する
9. Worker自己署名の偽Attestation ResultをRelying Partyが拒否する
10. 古いVerifier Policy Revisionとexpired Resultを拒否する

## このSpikeで一般化してよいこと

- Evidence / Verifier / Attestation Result / Relying Partyを分離できる
- Vendor固有Evidenceをnormalized Attestation Resultへ変換できる余地がある
- Relying Party側にTask固有Policyを残せる
- Attestation ResultをWorker Identity / Trust Domainへbindingできる
- Nonce / age / expiryを異なるfreshness境界として扱える
- Verifier Policy RevisionをRelying Partyが要求できる
- Raw EvidenceをScheduler / Clientへ直接公開しない契約を作れる
- Verifierを独立したVerification Authorityとして追跡できる

## このSpikeで一般化してはいけないこと

まだ次は確認していません。

- 実TEE Quote / Report
- Intel TDX
- AMD SEV-SNP
- Arm CCA
- TPM Quote
- AWS Nitro Enclaves
- Azure / GCP Confidential VM
- EAT / CWT / JWT具体形式
- Endorsement取得
- Reference Value distribution
- Vendor certificate chain
- CRL / OCSP / TCB revocation
- Verifier service federation
- Verifier compromise
- multi-Verifier
- GPU Attestation
- device / I/O Attestation
- Key Release
- session binding
- workload measurementとExecutor Digestのbinding
- Privacy-preserving Evidence
- raw Evidence retention policy

## Verifier compromiseについて

Verifierは明示的なTrust Boundaryです。

Verifier自体が侵害されれば、偽Attestation Resultを署名できる可能性があります。

このSpikeはその問題を解決しません。

EKIのAssurance Modelでは、

- Verification Authority
- Verifier Policy Revision
- Verifier Identity
- 必要に応じた複数Verifier

を追跡できる余地を残します。

高保証Taskで複数Verifierを要求する価値は、Result Verificationのindependenceとは別に今後検討します。

## Architectureへの示唆

Core ContractではVendor-specific raw Evidence schemaを共通化し過ぎず、まず次の抽象を持てる方が自然です。

~~~text
Evidence Provider / Attester
Evidence profile
Verifier identity
Verifier policy revision
Normalized environment claims
Freshness / expiry
Evidence provenance reference
Relying Party requirement
~~~

具体的TEE Adapterはextensionとして接続します。

## 参考資料

- RFC 9334 — Remote ATtestation procedureS (RATS) Architecture
  - https://www.rfc-editor.org/rfc/rfc9334.html
- RFC 9711 — Entity Attestation Token (EAT)
  - https://www.rfc-editor.org/rfc/rfc9711.html
- IETF RATS Endorsements draft
  - https://datatracker.ietf.org/doc/draft-ietf-rats-endorsements/
