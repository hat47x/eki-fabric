# Signed Decision Trust Registry

このpackageは、EKI Fabricの**署名付きAuthority Decisionを検証・署名するときに使うkey metadataとlifecycle semantics**を扱います。

名前に`trust`を含みますが、EKI全体の「信頼」を一つに集約する万能Registryではありません。

## 扱うもの

- P2 Decision kindとArchitecture Authority roleの対応
- COSE algorithm profile
  - EdDSA / Ed25519
  - ES256 / P-256
- public key metadata
- key ID (`kid`)
- Authority Principal
- Trust Domain
- signing validity window
- policy revision
- key state
  - PENDING
  - ACTIVE
  - VERIFY_ONLY
  - REVOKED
  - COMPROMISED
- primary signing key selection
- rotation / retirementに必要なstate transition
- verification前のkey / role / algorithm binding

## 扱わないもの

- Client authentication
- Worker authentication
- OAuth / OIDC token
- TLS certificate lifecycle
- Artifact encryption
- Secret storage
- general capability eligibility
- Provider selection
- semantic trust ranking
- Federation authorizationそのもの
- business Decision
- Task execution authorization全体

特に、`valid signature != Authorization`です。

signatureとkey bindingが正しくても、Task / Attempt / Worker / Lease / current Policy等のDecision-specific条件を別途確認する必要があります。

## `authority` packageとの関係

`internal/core/authority`は、P2で利用するArchitecture Authority roleとDecision kindの対応だけを定義します。

~~~text
eki.lease/v1      -> LSE-1
eki.work-order/v1 -> WOR-1
~~~

`decisiontrust.SigningRequest` / `VerificationRequest`はこの対応を必ず検証します。

そのため、`WOR-1` keyを使って`Lease` Decisionを署名・検証する迂回経路をpublic APIとして持ちません。

## AlgorithmとCurveを分ける

Coreでは次を別概念として扱います。

~~~text
COSE Algorithm: EdDSA
Curve:          Ed25519

COSE Algorithm: ES256
Curve:          P-256
~~~

Ed25519をAlgorithm名として扱いません。これはCOSE `alg=-8`とcurveを混同しないためです。

`Algorithm.COSEIdentifier()`は現在、EdDSA -> `-8`、ES256 -> `-7`を返します。

## Key Record

`KeyRecord`はpublic keyと、そのkeyをEKI Authority Decisionへ使うためのmetadataを保持します。private keyは保持しません。

M0のsoftware signerや将来のKMS / HSM Adapterは`KeyID`を使ってprivate signing capabilityへ接続します。Workerへprivate keyを配布しません。

## Key ID reuse

同じ`kid`を別keyへ再利用しません。

また、同じpublic keyであってもvalidity window、policy revision、Authority role、Authority Principal、Trust Domain、Algorithm等が変わる場合は同じregistrationとは扱いません。変更には新しい`kid`を使います。

これにより、過去Decisionの`kid`が後から別meaningへ解釈されることを防ぎます。

## Lifecycle

基本遷移:

~~~text
PENDING
  -> ACTIVE
  -> VERIFY_ONLY
  -> REVOKED

PENDING / ACTIVE / VERIFY_ONLY / REVOKED
  -> COMPROMISED
~~~

初回`ACTIVE`化の時刻を`activatedAt`として保持します。Decisionの`issuedAt`がそれ以前であれば、設定上の`SigningNotBefore`内であってもverifyしません。Administrative activation前に作られた署名を後から正当化しないためです。

`VERIFY_ONLY`へ移す時刻は`verifyOnlyAt`として保持します。この状態では`issuedAt < verifyOnlyAt`のDecisionだけを検証対象にできます。退役後に旧private keyで新しく作られた署名を、historical verificationの名目で受理しません。

`VERIFY_ONLY -> ACTIVE`の再昇格は許可しません。signingへ戻す必要がある場合は新しい`kid`を発行します。

Administrative state transitionの時刻は現在stateの`stateChangedAt`より前へ戻せません。activation / retirement / revocationの履歴を、後から古い時刻で上書きしないためです。

`REVOKED -> COMPROMISED`だけは、後からprivate key compromiseが判明した場合の意味強化として許可します。

## Signing key selection

`ResolveSigningKey`はTrust Domain、Authority role、Authority Principal、Decision kind、Algorithmを含む`SigningRequest`を要求します。

Callerが任意`kid`を直接指定してprimary signing keyを選ぶ設計にはしていません。Administrative Planeが`SetPrimary`でscopeごとのprimary keyを管理します。

primaryにできるのは、現在のsigning validity window内にある`ACTIVE` keyだけです。

## Verification key selection

`ResolveVerificationKey`はprotected `kid`、protected algorithm、Trust Domain、Authority role、Authority Principal、Decision kind、issued timeをbindingしてkeyを解決します。

ここではまだCOSE signatureそのものを検証しません。

後続Verifierはこのkey selectionに加えてCOSE structure、protected header、cryptographic signature、deterministic CBOR、CDDL、expiry、Task / Attempt / Worker / TaskIR binding、current policy / revocationを確認します。

その前段として`VerifierBoundary.ResolvePreCrypto`は、Trust Bundle revision/freshness、Deployment algorithm allowlist、Decisionのissued/expires、種類別maximum lifetimeを検査し、Registry lookupの失敗をmachine-readableな`VerificationReason`へ正規化します。これはsignature検証の代替ではなく、COSE/CBOR decoderやcryptographic verifierへ進む前のfail-closed gateです。

## In-memory Registryの位置づけ

現在の`Registry`はM0のdomain semanticsとworker-side trust snapshotを検証するためのin-memory implementationです。

Control Planeの永続的なAdministrative source-of-truthをin-memory Mapへ固定するものではありません。

将来のdurable Key Registry / KMS metadata storeは別Adapterとして追加し、このpackageのlifecycle invariantsを維持します。

## Testで固定している不変条件

- Decision kindとAuthority roleの対応
- EdDSA/Ed25519とES256/P-256のalgorithm confusion拒否
- wrong curve metadata拒否
- `kid` reuse拒否
- validity / policy変更を同一registrationとみなさない
- primary signing keyの明示選択
- validity外keyをprimaryにしない
- key lifecycleの時刻逆行を拒否
- activation前に発行されたDecisionのverification禁止
- VERIFY_ONLYのsigning再利用禁止
- VERIFY_ONLY移行後に発行されたDecisionのverification禁止
- REVOKED / COMPROMISED keyのlive verification禁止
- cross-Authority Decision拒否
- Trust Domain mismatch拒否
- Authority Principal mismatch拒否
- protected algorithm mismatch拒否
- public keyのdefensive copy
- private keyとpublic KeyRecordの一致検証
- Software Signer内のprivate key defensive copy
- ES256 signatureを64-byte raw `R || S`へ固定
- ASN.1 DER形式のES256 signature拒否
- sign処理中revocation時にsignatureを返さない
- crypto検証後のcurrent trust state再検査

## Protocolとの整合

P2 CDDLのDecision kind / Authority roleとGo constantsの一致は`conformance/protocol/decision_schema_test.go`でも確認します。

Go実装側だけを更新してP2 machine-readable schemaと意味がずれることを防ぎます。

## Cryptographic primitive boundary

pre-crypto boundaryの次段として、Go standard libraryだけで署名・検証primitiveを接続しています。

- EdDSAはEd25519でexact `Sig_structure` bytesを署名する。
- ES256は`SHA-256(Sig_structure)`へECDSA署名し、COSEで要求するfixed-width 64-byte `R || S`形式を生成・検証する。ASN.1 DER signatureはこの境界では受理しない。
- Software SignerはRegistryが選んだprimary keyだけを利用し、Callerが任意`kid`を指定してsigning keyを選ぶAPIにはしない。
- private key登録時にpublic KeyRecordとの一致を検証し、Software Signer内ではdefensive copyを保持する。
- sign前後でRegistryを再確認し、sign処理中にrevocation等でAuthorizationが変化した場合は生成済みsignatureをCallerへ返さない。
- verifyは既存`ResolvePreCrypto`を必ず通した後にcrypto検証し、crypto成功後にもpre-crypto gateを再評価する。

`ReferenceSoftwareSigner`はM0 / development / DP-1向けのreferenceです。private keyをprocess memoryへ保持するため、DP-2 Enterpriseで想定するKMS / HSMの代替ではありません。KMS / HSM Adapterも将来このtyped Authority semanticsを維持し、genericな`Sign(bytes)` capabilityをScheduler等へ公開しません。

`SignCOSESigStructure`が受け取るbytesは、将来のtrusted internal COSE encoderが構築したSig_structureを想定します。Clientが供給した任意bytesをそのまま署名する入口ではありません。現在はconcrete COSE/CBOR parserをまだ接続していないため、この点は呼び出し側boundaryで維持します。

ES256では同じmessage/public keyに対して複数のvalid signature表現があり得るため、signature bytesそのものをDecision identity、idempotency key、replay protection tokenとして利用しません。Decision ID、payload digest、nonce等のsigned semanticsを使います。

crypto後の再検査はrevocationとのTOCTOU窓を縮めますが、Verifier returnとの完全な原子性を保証するものではありません。WorkerのExecution Authorizationは実行直前にもcurrent Lease / Policy / Decision stateを確認します。

## 次

Trust Bundle freshness / revision、verification reason taxonomy、Decision time/lifetime policy、およびstandard-library cryptographic primitive boundaryまで実装しました。

次の独立work unitはcanonical Decision payload decoderとCOSE protected-header parserです。そこで初めてraw COSE_Sign1からprotected `alg` / `kid` / profileとpayloadを安全に抽出し、deterministic CBOR / CDDL検査を現在のVerifier boundaryへ接続します。COSE / CBOR library dependencyは、Go toolingで取得・lock・testできる環境でのみ導入し、未検証dependency lockはcommitしません。
