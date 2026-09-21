# ADR-0004 — Signed Decision cryptographic profile

- Status: **Accepted for M0 / Enterprise baseline**
- Date: 2026-09-22
- Scope: P2 COSE_Sign1 Authority Decisions

## 1. Context

P2でWorkOrder、Lease、Placement Approval、Executor Approval、Artifact Grantのcanonical signed payloadをdeterministic CBORとして固定しました。

次に、signature algorithm、key identity、rotation / revocation、Worker-side verification、KMS / HSM接続の境界を固定します。

重要なのは、暗号signatureが正しいこととEKI上のAuthorizationが成立することを同一視しないことです。

~~~text
valid signature
  != trusted Authority role
  != valid Decision semantics
  != current execution authorization
~~~

## 2. Algorithm profile

EKI Baselineでは次の2 profileを第一級に扱います。

### Software Reference Profile

- COSE algorithm: **EdDSA (-8)**
- curve: **Ed25519**
- key type: **OKP**
- COSEではPure EdDSAを使用
- M0 software reference signer / verifierの最初の実装対象

### Enterprise Interoperability Profile

- COSE algorithm: **ES256 (-7)**
- curve: **P-256**
- hash: **SHA-256**
- key type: **EC2**
- KMS / HSM / enterprise PKI連携の第一候補

Deploymentは明示的allowlistで利用profileを選びます。Client / Worker / Task inputがalgorithmを指定してallowlistを広げることはできません。

## 3. Algorithm agility

Protocol自体をEd25519またはP-256専用にはしません。

ただし「何でも受理するalgorithm agility」は設けません。

~~~text
protected alg
  ∩ key record allowed algorithm
  ∩ Authority role policy
  ∩ deployment allowlist
~~~

が一致する場合だけverifyします。unknown / deprecated / disabled algorithmへfallbackしません。

## 4. Key isolation

最低限、次のAuthority roleは別signing keyへ分けます。

- EXE-1 Executor Admission
- PLC-1 Placement Authorization
- LSE-1 Lease
- DAT-1 Artifact / Data Capability
- WOR-1 WorkOrder

一つのprivate keyをこれら複数roleで共有することをBaselineにはしません。

Small Managed Profileで同一processへAuthorityを同居させる場合も、key identity / handleは分離します。

## 5. Standards alignment

COSE algorithm identifierとkey type / curveの整合はRFC 9053へ従います。

- EdDSA = -8
- ES256 = -7
- EdDSAではOKP + supported Edwards curve
- ES256ではEC2 + P-256 + SHA-256

Verifierはalgorithmだけでなくkey type / curveも確認します。

FIPS 186-5はECDSAとEdDSAをDigital Signature Standardとして扱います。NIST SP 800-57 Part 1 Rev.5のkey lifecycle / compromise guidanceをKey Registry設計の参考にします。

## 6. Why Ed25519 first for M0

M0 Reference ImplementationではEd25519を先に実装します。

- Go標準libraryで利用できる
- deterministic signature
- fixed-size key / signatureで実装が単純
- ASN.1 DER変換を必要としない
- software-only Reference Signerを小さく作れる

これはEnterprise環境へEd25519を強制する判断ではありません。

## 7. Why ES256 is first-class

Enterprise KMS / HSM / PKIとの接続を考慮し、P-256 / ES256を第一級profileとして扱います。

COSE ES256 signatureはASN.1 DERではなく、RFC 9053で定義されるfixed-width `R || S` byte stringとして扱います。

Reference implementationでES256を追加する際は、Go crypto APIとのencoding boundaryをtestで固定します。

## 8. COSE protected header profile

M0 / BaselineのCOSE_Sign1 protected headerは少なくとも次を含みます。

- `alg`
- `kid`
- signed-decision content/profile identifier

protected header map自体もdeterministic CBORでencodeします。

`alg`はKey RegistryとAuthority Policyのallowlistへ照合します。

`kid`はopaque identifierであり、local Trust / Key Registry内のrecordを選ぶindexです。COSE message内の未知key materialをTrust Rootとして自動採用しません。

content/profileはP2 payloadとしてdecodeする前に確認し、unprotected headerだけを判定根拠にはしません。

MVPのexternal AADはempty bstrへ固定し、detached payload、countersignature、multisignatureは利用しません。

## 9. Key Registry

VerifierはAdministrative Planeで管理されたKey Registryを参照します。

Logical record:

~~~text
Key ID / kid
Authority role
Authority Principal
Trust Domain / namespace
Algorithm
Key type
Curve
Public key material or KMS key reference
Public key digest
State
Signing validity start
Signing validity end
Revocation / compromise metadata
Policy revision
Created / updated audit metadata
~~~

Private key materialをWorkerへ配布しません。Workerはverificationに必要なpublic trust materialだけを持ちます。

## 10. `kid` reuse

同一Trust Domain内で、同じ`kid`を別public keyへ再利用しません。

Rotationでは新しい`kid`を発行し、古いDecisionの`kid`が将来別keyへ解釈されないようにします。

## 11. Key states

Baselineでは少なくとも次を区別します。

### PENDING

登録済みだがsign / verify authorityにはまだ使わない。

### ACTIVE

新しいDecisionのsigningとverificationに利用できる。

### VERIFY_ONLY

新規signingには使わない。有効期間中に発行された既存Decisionのverificationにだけ利用する。通常rotation後の旧keyに使います。

### REVOKED

live authorizationのverificationへ利用しない。routine retirement以外の失効に使います。

### COMPROMISED

private key compromiseを想定するstate。そのkeyで署名された未期限Decisionをlive authorizationとして信用しません。

過去Audit Artifactとしてsignature bytesを保持することと、現在Authorizationへ使えることを分けます。

## 12. Rotation

標準rotation:

~~~text
K1 ACTIVE
  ↓
K2 PENDING
  ↓ administrative activation
K1 ACTIVE + K2 ACTIVE (short overlap if needed)
  ↓
K1 VERIFY_ONLY
K2 ACTIVE
  ↓ all live K1 decisions expire
K1 archival / removal from online verifier set
~~~

新旧keyのoverlap期間を許容できますが、Signerがどのkeyを使うかはAuthority Policyが決めます。Client / Workerは`kid`を選べません。

## 13. Revocation propagation

Workerはlocal Trust Store / Key Registry snapshotを使ってverifyできます。

ただしstale Trust Storeで無期限に新WorkOrderを受理しません。Deployment Profileはtrust bundle revision、validated time、maximum allowed stalenessを設定します。

revocation updateを受けたら以後のWorkOrder acceptanceへ反映します。exact staleness durationはMVP implementation configで決めます。

## 14. Key compromise containment

Authority roleごとのkey separationにより、たとえばWOR-1 key compromiseだけでExecutor Admission、Placement Authorization、Artifact Grant、Leaseの有効なDecisionを偽造できない構造を維持します。

WorkerはWorkOrderだけでなくprerequisite Decisionを独立verifyします。

## 15. Signer boundary

Signer SPIをgenericな `Sign(bytes) -> signature` としてScheduler等へ広く渡しません。

Logical requestには少なくともAuthority role、Decision kind、canonical payload、expected Authority Principal、policy revision / signing contextを含めます。

Signer Adapter / Authority Serviceがrole、kind、active key、algorithm、payload profileを確認してからCOSE_Sign1を生成します。

SchedulerはSignerを直接持ちません。

## 16. Key selection

Callerが任意`kid`を渡してsigning keyを選ぶ構造をBaselineにしません。

Authority / Signer側がrole、trust domain、algorithm profile、key state、activation policyからactive keyを選びます。

test / migration用のexplicit key selectionが必要でもAdministrative capabilityとして分離します。

## 17. Verifier order

Worker-side verifierは概念的に次の順序で確認します。

~~~text
1. total COSE object size / nesting limit
2. COSE_Sign1 structure
3. protected header is deterministic CBOR
4. required protected fields exist
5. content/profile matches EKI signed-decision profile
6. kid resolves in configured Trust Store
7. key state permits verification
8. protected alg matches Key Registry algorithm
9. key type / curve matches algorithm
10. cryptographic signature
11. P2 payload deterministic CBOR
12. CDDL schema / version
13. Decision kind
14. Authority role and principal match Key Registry / policy
15. issued / expires / bounded clock skew
16. Task / Attempt / Worker / TaskIR binding
17. policy / revocation / current state
18. Decision-specific semantic checks
~~~

signatureが正しいだけでstep 14以降を省略しません。

## 18. Time validation

Verifierはmanaged clockを基準に、issued timeが不合理な未来ではないこと、expires > issued、現在時刻が有効期間内であること、bounded clock skewだけを許容することを確認します。

Worker自身の任意wall clockをAuthorityへしません。exact skew秒数はDeployment configurationで固定し、Client Taskごとに変更できません。

## 19. Decision lifetime

Decision TTLは種類ごとにPolicyで上限を持ちます。

- Leaseは短命
- WorkOrderはprerequisite expiryを越えない
- Artifact GrantはTask / Lease用途に必要な短命scope
- Executor Approvalは比較的長くできるがrevocation可能

ClientがTTLを指定して延長できません。

## 20. Private key protection

### Development

ephemeral / local development keyを許容し、Production profileと区別します。

### DP-1 Small Managed

少なくともdedicated key file / OS key store、restrictive filesystem ACL、roleごとの別key、secretのlog/config dump禁止を要求します。

### DP-2 Enterprise

KMS / HSM等、private keyをControl Plane application memoryへexportしないAdapterを第一候補とします。

Signer SPIはlocal software keyとKMS/HSMの両方を同じAuthority semanticsで扱います。

## 21. Public key distribution

Workerへ渡すpublic trust materialはAdmin-managed bundleです。

Workerが受信したCOSE objectからembedded public key、arbitrary certificate、URL、JWKS endpointを自動Trust Rootとして追加しません。

将来x5chain等を使う場合もconfigured trust anchorとpolicyへchain validationする別profileとします。

## 22. Federation

Foreign Trust DomainのAuthority keyをlocal Trust Storeへ追加しただけでlocal WorkOrder Authorityとして扱いません。

標準FederationではProvider domainがforeign intentをlocal Admissionへ通し、Provider local Authorityがlocal Worker向けDecisionを発行します。

## 23. Error handling

Verifierはmachine-readable reason categoryを持ちます。

- MALFORMED_COSE
- PROTECTED_HEADER_INVALID
- UNSUPPORTED_PROFILE
- UNKNOWN_KEY
- KEY_NOT_ACTIVE_FOR_VERIFY
- ALGORITHM_NOT_ALLOWED
- KEY_ALGORITHM_MISMATCH
- SIGNATURE_INVALID
- PAYLOAD_NOT_DETERMINISTIC
- SCHEMA_UNSUPPORTED
- AUTHORITY_ROLE_MISMATCH
- EXPIRED
- NOT_YET_VALID
- SUBJECT_MISMATCH
- STALE_DECISION
- POLICY_REVOKED
- TRUST_BUNDLE_INVALID
- TRUST_BUNDLE_STALE
- TIME_WINDOW_INVALID

WorkerからClientへ内部詳細をそのまま公開することは標準にしません。

## 24. Size limits

M0 reference verifierはsigned Decisionをsmall control metadataとして扱います。

- COSE_Sign1 total bytes: 256 KiB以下
- protected header encoded bytes: 4 KiB以下
- `kid`: 128 bytes以下

より厳しいDeployment limitを設定できます。bulk dataや証拠Artifactをsigned Decisionへinlineしません。

## 25. Audit

Signing / verificationではAuthority role、Decision kind / ID、kid、algorithm、payload digest、policy revision、result、failure category、timestampをsecretなしでAudit可能にします。

private key、raw secret、unnecessary Artifact contentをlogしません。

## 26. Conformance

### Algorithm confusion

- Ed25519 key + ES256 alg reject
- P-256 key + EdDSA alg reject
- unknown / disabled alg reject

### Key identity / lifecycle

- unknown kid reject
- reused kid / changed key material reject at registry load
- wrong Authority role / Principal reject
- PENDING / VERIFY_ONLY signing reject
- REVOKED / COMPROMISED live authorization reject

### COSE

- alg only in unprotected header reject
- kid only in unprotected header reject
- wrong profile reject
- detached payload reject
- malformed / oversized COSE reject

### Payload

- non-deterministic payload reject
- wrong Decision kind reject
- expired reject
- Task / Attempt / Worker mismatch reject
- stale Lease / policy reject

## 27. Non-goals

このProfileはTLS certificate profile、OAuth token signing、Artifact encryption、Secret encryption、public WebPKI、multi-party approvalを定義しません。

用途の違うkeyを一つの万能EKI keyへ統合しません。

## 28. Sources

- RFC 9052 — https://www.rfc-editor.org/rfc/rfc9052.html
- RFC 9053 — https://www.rfc-editor.org/rfc/rfc9053.html
- RFC 8032 — https://www.rfc-editor.org/rfc/rfc8032.html
- FIPS 186-5 — https://csrc.nist.gov/pubs/fips/186-5/final
- NIST SP 800-57 Part 1 Rev.5 — https://csrc.nist.gov/pubs/sp/800/57/pt1/r5/final

## 29. Follow-up

次の実装workでは、まずstandard libraryでEd25519を使うM0 Signer / Verifier domain boundaryとin-memory Trust Registry conformanceを作ります。

COSE / CBOR library dependencyはSQLiteと同様にGo toolingで取得・lock・testできる環境で導入し、未検証dependency lockを推測でcommitしません。
