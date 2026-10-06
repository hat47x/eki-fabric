# ADR-0004 署名付きDecisionの暗号Profile

- Status: **Accepted for M0 / Enterprise baseline**
- Date: 2026-09-22
- Scope: P2のCOSE_Sign1によるAuthority Decision

## 1. Context

P2では、WorkOrder、Lease、Placement Approval、Executor Approval、Artifact Grantについて、署名対象となる正規のpayloadをdeterministic CBORとして固定しました。

次に固定するのは、次の境界です。署名algorithm、keyの識別、keyの交換（rotation）と失効（revocation）、Worker側での検証、KMSやHSMとの接続。

このとき重要なのは、暗号上の署名が正しいことと、EKI上で権限が認められることを同じものとみなさないことです。

~~~text
valid signature
  != trusted Authority role
  != valid Decision semantics
  != current execution authorization
~~~

## 2. Algorithm profile

EKI Baselineでは、次の2つのprofileを第一級に扱います。

### Software Reference Profile

- COSE algorithm: **EdDSA (-8)**
- curve: **Ed25519**
- key type: **OKP**
- COSEではPure EdDSAを使う。
- M0のsoftwareによる参照用のSignerとVerifierで、最初に実装する。

### Enterprise Interoperability Profile

- COSE algorithm: **ES256 (-7)**
- curve: **P-256**
- hash: **SHA-256**
- key type: **EC2**
- KMS、HSM、企業のPKIと連携するときの第一候補とする。

どのprofileを使うかは、Deploymentが明示的な許可リストで選びます。Client、Worker、Taskの入力でalgorithmを指定して、許可リストを広げることはできません。

## 3. Algorithm agility

Protocolそのものを、Ed25519専用やP-256専用にはしません。

ただし、「どのalgorithmでも受け入れる」という意味での柔軟性は設けません。

~~~text
protected alg
  ∩ key record allowed algorithm
  ∩ Authority role policy
  ∩ deployment allowlist
~~~

上の4つがすべて一致する場合にだけ検証します。未知のalgorithm、非推奨のalgorithm、無効にしたalgorithmへ代わりに切り替えることはしません。

## 4. Keyの分離

少なくとも次のAuthority roleには、それぞれ別の署名keyを使います。

- EXE-1 Executor Admission
- PLC-1 Placement Authorization
- LSE-1 Lease
- DAT-1 Artifact / Data Capability
- WOR-1 WorkOrder

一つのprivate keyをこれら複数のroleで共有する構成を、Baselineにはしません。

Small Managed Profileで複数のAuthorityを同じprocessに同居させる場合も、keyの識別子とhandleは分けます。

## 5. 標準との整合

COSEのalgorithm識別子と、keyの種類やcurveとの整合は、RFC 9053に従います。

- EdDSAは-8とする。
- ES256は-7とする。
- EdDSAでは、OKPと、対応するEdwards curveを使う。
- ES256では、EC2、P-256、SHA-256を使う。

Verifierは、algorithmだけでなく、keyの種類とcurveも確認します。

FIPS 186-5は、ECDSAとEdDSAをDigital Signature Standardとして扱っています。Key Registryの設計では、NIST SP 800-57 Part 1 Rev.5が示すkeyのlifecycleと漏えい時の扱いを参考にします。

## 6. M0でEd25519を先に実装する理由

M0のReference Implementationでは、Ed25519を先に実装します。理由は次のとおりです。

- Goの標準libraryで使える。
- 署名がdeterministicになる。
- keyと署名の長さが固定なので、実装が単純になる。
- ASN.1 DERへの変換が要らない。
- softwareだけで動く参照用のSignerを小さく作れる。

これは、Enterprise環境にEd25519を強制する判断ではありません。

## 7. ES256を第一級にする理由

企業のKMS、HSM、PKIとの接続を考え、P-256とES256を第一級のprofileとして扱います。

COSEのES256の署名は、ASN.1 DERではなく、RFC 9053で定められた固定長の`R || S`というbyte列として扱います。

参照実装にES256を加えるときは、Goのcrypto APIとの間のencodingの境界を、testで固定します。

## 8. COSEのprotected headerのprofile

M0とBaselineでは、COSE_Sign1のprotected headerに、少なくとも次の項目を含めます。

- `alg`
- `kid`
- 署名付きDecisionであることを示すcontentとprofileの識別子

protected headerのmapそのものも、deterministic CBORでencodeします。

`alg`は、Key RegistryとAuthority Policyの許可リストと照合します。

`kid`は中身に意味を持たない識別子で、手元のTrust StoreやKey Registryの中からrecordを選ぶための索引です。COSE messageの中に入っている未知のkeyを、Trust Rootとして自動的に採用することはしません。

contentとprofileは、P2のpayloadとしてdecodeする前に確認します。unprotected headerだけを判定の根拠にはしません。

MVPでは、external AADを空のbstrに固定します。detached payload、countersignature、multisignatureは使いません。

## 9. Key Registry

Verifierは、Administrative Planeで管理されたKey Registryを参照します。

論理上のrecordは次のとおりです。

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

private keyをWorkerへ配りません。Workerが持つのは、検証に必要な公開の信頼情報だけです。

## 10. `kid`の再利用

同じTrust Domainの中では、同じ`kid`を別の公開keyに使い回しません。

keyを交換するときは新しい`kid`を発行します。こうして、古いDecisionの`kid`が、将来別のkeyとして解釈されないようにします。

## 11. Keyの状態

Baselineでは、少なくとも次の状態を区別します。

### PENDING

登録は済んでいるが、署名にも検証にもまだ使わない状態です。

### ACTIVE

新しいDecisionの署名と検証に使える状態です。

### VERIFY_ONLY

新しい署名には使わず、有効期間中に発行された既存のDecisionの検証にだけ使う状態です。通常のkey交換の後の、古いkeyに使います。

### REVOKED

現在の権限の検証には使わない状態です。定期的な引退以外の理由による失効に使います。

### COMPROMISED

private keyが漏えいしたと想定する状態です。このkeyで署名され、まだ期限が切れていないDecisionを、現在の権限として信用しません。

過去のAudit Artifactとして署名のbytesを保管しておくことと、それを現在の権限の判断に使えることとは、分けて扱います。

## 12. Keyの交換

標準的な交換の流れは次のとおりです。

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

新旧のkeyが重なる期間は認めます。ただし、Signerがどちらのkeyを使うかを決めるのはAuthority Policyです。ClientやWorkerは`kid`を選べません。

## 13. 失効の伝わり方

Workerは、手元のTrust StoreやKey Registryのsnapshotを使って検証できます。

ただし、古いTrust Storeのまま、新しいWorkOrderを期限なく受け入れ続けることはしません。Deployment Profileで、trust bundleのrevision、検証した時刻、許容する古さの上限を設定します。

失効の更新を受け取ったら、それ以降のWorkOrderの受け入れに反映します。許容する古さの具体的な長さは、MVPの実装の設定で決めます。

## 14. Keyが漏えいしたときの被害の封じ込め

Authority roleごとにkeyを分けているので、たとえばWOR-1のkeyが漏えいしても、それだけではExecutor Admission、Placement Authorization、Artifact Grant、Leaseの有効なDecisionを偽造できません。この構造を保ちます。

Workerは、WorkOrderだけでなく、その前提となるDecisionもそれぞれ独立に検証します。

## 15. Signerの境界

Signer SPIを、`Sign(bytes) -> signature`のような汎用の形で、Schedulerなどへ広く渡しません。

論理上のrequestには、少なくとも次の項目を含めます。Authority role、Decisionの種類、正規のpayload、期待するAuthority Principal、policyのrevisionや署名の文脈。

Signer AdapterやAuthority Serviceは、role、種類、ACTIVEなkey、algorithm、payloadのprofileを確認してから、COSE_Sign1を生成します。

SchedulerはSignerを直接持ちません。

## 16. Keyの選び方

呼び出し側が任意の`kid`を渡して署名keyを選ぶ構造は、Baselineにしません。

ACTIVEなkeyは、AuthorityやSigner側が、role、trust domain、algorithmのprofile、keyの状態、有効化のpolicyにもとづいて選びます。

testやmigrationのためにkeyを明示的に選ぶ必要がある場合も、その操作はAdministrativeな権限として分けます。

## 17. Verifierの確認順序

Worker側のVerifierは、概念上、次の順序で確認します。

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

署名が正しかったというだけで、step 14以降を省きません。

## 18. 時刻の検証

Verifierは、管理された時計を基準に次のことを確認します。発行時刻が不自然に未来でないこと。期限が発行時刻より後であること。現在時刻が有効期間の中にあること。時計のずれは上限の範囲内だけ許すこと。

Worker自身の任意の時計を、Authorityの根拠にしません。許容するずれの具体的な秒数はDeploymentの設定で固定し、ClientがTaskごとに変えることはできません。

## 19. Decisionの有効期間

Decisionの有効期間（TTL）には、種類ごとにPolicyで上限を設けます。

- Leaseの有効期間は短くする。
- WorkOrderの有効期間は、前提となるDecisionの期限を越えない。
- Artifact Grantの有効範囲は、TaskやLeaseの用途に必要な、短い範囲に限る。
- Executor Approvalは比較的長くできるが、失効させられるようにする。

ClientがTTLを指定して延ばすことはできません。

## 20. Private keyの保護

### Development

使い捨てのkeyや、開発用のlocalなkeyを認めます。ただし、Production用のprofileとは区別します。

### DP-1 Small Managed

少なくとも次のことを求めます。専用のkey fileまたはOSのkey storeを使う。filesystemのACLを厳しくする。roleごとに別のkeyを使う。secretをlogや設定のdumpに出さない。

### DP-2 Enterprise

KMSやHSMのように、private keyをControl Planeのapplicationのmemoryへ取り出さないAdapterを、第一候補とします。

Signer SPIは、localのsoftware keyとKMSやHSMの両方を、同じAuthorityの意味で扱います。

## 21. 公開keyの配布

Workerへ渡す公開の信頼情報は、管理者が管理するbundleです。

Workerは、受け取ったCOSE objectに含まれる公開key、任意の証明書、URL、JWKSのendpointを、自動的にTrust Rootへ加えません。

将来x5chainなどを使う場合も、設定されたtrust anchorとpolicyに照らして証明書のchainを検証する、別のprofileとします。

## 22. Federation

他のTrust DomainのAuthority keyを手元のTrust Storeに加えただけで、それを手元のWorkOrderのAuthorityとして扱うことはしません。

標準的なFederationでは、Provider側のdomainが、外部から来た依頼を自分のAdmissionに通します。そのうえで、Provider側のAuthorityが、自分のWorker向けのDecisionを発行します。

## 23. Errorの扱い

Verifierは、機械が読める理由の分類を持ちます。

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

WorkerからClientへ、内部の詳細をそのまま公開することは標準にしません。

## 24. Sizeの上限

M0の参照用Verifierは、署名付きDecisionを小さなcontrol用のmetadataとして扱います。

- COSE_Sign1の全体は256 KiB以下とする。
- encodeしたprotected headerは4 KiB以下とする。
- `kid`は128 bytes以下とする。

Deploymentでより厳しい上限を設定できます。大きなdataや証拠のArtifactを、署名付きDecisionの中に直接入れません。

## 25. Audit

署名と検証については、次の項目を、secretを含めずにAuditできるようにします。Authority role、Decisionの種類とID、kid、algorithm、payloadのdigest、policyのrevision、結果、失敗の分類、timestamp。

private key、生のsecret、不要なArtifactの中身はlogに出しません。

## 26. Conformance

### Algorithmの取り違え

- Ed25519のkeyとES256のalgの組み合わせを拒否する。
- P-256のkeyとEdDSAのalgの組み合わせを拒否する。
- 未知のalgや無効にしたalgを拒否する。

### Keyの識別とlifecycle

- 未知のkidを拒否する。
- kidの再利用や、key materialの変更を、registryの読み込み時に拒否する。
- 誤ったAuthority roleやPrincipalを拒否する。
- PENDINGやVERIFY_ONLYのkeyでの署名を拒否する。
- REVOKEDやCOMPROMISEDのkeyを、現在の権限の根拠としては拒否する。

### COSE

- algがunprotected headerにしかない場合は拒否する。
- kidがunprotected headerにしかない場合は拒否する。
- 誤ったprofileを拒否する。
- detached payloadを拒否する。
- 形式が不正なCOSEや、大きすぎるCOSEを拒否する。

### Payload

- deterministicでないpayloadを拒否する。
- 誤ったDecisionの種類を拒否する。
- 期限切れを拒否する。
- Task、Attempt、Workerの不一致を拒否する。
- 古いLeaseやpolicyを拒否する。

## 27. Non-goals

このProfileでは、次のものを定義しません。TLSの証明書のprofile、OAuthのtokenの署名、Artifactの暗号化、Secretの暗号化、公開WebPKI、複数者による承認。

用途の違うkeyを、なんでもできる一つのEKI keyへまとめることはしません。

## 28. Sources

- RFC 9052: https://www.rfc-editor.org/rfc/rfc9052.html
- RFC 9053: https://www.rfc-editor.org/rfc/rfc9053.html
- RFC 8032: https://www.rfc-editor.org/rfc/rfc8032.html
- FIPS 186-5: https://csrc.nist.gov/pubs/fips/186-5/final
- NIST SP 800-57 Part 1 Rev.5: https://csrc.nist.gov/pubs/sp/800/57/pt1/r5/final

## 29. 実装の状況とFollow-up

M0のSignerとVerifierのdomainの境界、memory上のTrust Registry、暗号処理の前に行うpolicyの検査、標準libraryの暗号機能との境界までを実装しました。

参照用のSoftware Signerは、EdDSA/Ed25519とES256/P-256を扱います。ES256の署名には、COSEの固定長64 bytesの`R || S`形式を使います。これはM0、開発、DP-1向けの参照実装です。DP-2 Enterpriseでは、引き続き、private keyをapplicationのmemoryへ取り出さないKMSやHSMのAdapterを第一候補とします。

Signerは、型付きのAuthorityのrequestと、Registryが選んだprimary keyを要求します。汎用の`Sign(bytes)`という権限として、Schedulerなどへ公開することはしません。署名の検証の前後でも、暗号処理の前に行うpolicyの検査をもう一度行います。ただし、最終的な権限判断の原子性は、暗号の層だけでは保証しません。実行の直前に、現在のLease、Policy、Decisionを別に検証することを求めます。

次の実装作業は、正規のDecision payloadのdecoderと、COSEのprotected headerのparserです。COSEやCBORのlibraryへの依存は、SQLiteと同じく、Goのtoolで取得、lock、testができる環境で導入します。検証していない依存関係のlockを、推測でcommitしません。
