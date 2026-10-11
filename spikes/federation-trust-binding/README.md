# Federationで信頼関係を結び付ける方法を確かめる（Trust Binding Spike）

## このSpikeの役割

このSpikeは、複数組織がEKI FabricをFederationするとき、Trust Domain名とTrust Materialの対応を取り違えたり、古いBundleをreplayしたり、別DomainのIdentityを誤って受け入れたりしないための最小モデルです。

対象は主に次です。

- RQ-031 Trust-domain federation
- RQ-048 Freshness and revocation of time-bounded authority
- RQ-049 Federation identity namespace binding
- SEC-015 Stale authority cannot be revived by replay
- SEC-019 Federated identity keeps its namespace binding
- PAS-10 複数組織間の限定Federation
- PAS-23 Federation namespace / Trust Materialのすり替え
- QA-01 Security containment

SPIFFE / SPIREの採用を確定するものではありません。

## 背景

Federationでは、外部組織のIdentityを検証するために、その組織のTrust Materialを取得します。

ここで危険なのは、

```text
この署名は、登録済みのどれかの鍵で検証できた
```

だけを確認することです。

それでは、Company Cの鍵で作られたIdentityをCompany BのIdentityとして扱う余地が生じます。

EKIでは、

```text
Trust Domain
  ↕
そのDomainにだけ対応するTrust Material
```

のbindingを維持します。

## SPIFFE仕様から得られる一般原則

SPIFFE Federation仕様では、異なるTrust DomainのBundleを一つの大きなBundleへmergeしてはいけないと明示されています。

Identityを検証するときは、そのIdentityが属するTrust Domainに対応したBundleだけを使う必要があります。

また、Bundle Endpointの設定には、

- Trust Domain名
- Endpoint URL
- Endpoint profile

などSecurity上重要なparameterが含まれ、それらの改変はIdentity namespaceそのもののすり替えにつながり得ます。

EKIで採用するかどうかにかかわらず、この原則は一般化できます。

## Spikeの最小モデル

このSpikeでは、

```text
Federation Administrator
  ↓ signed BundleUpdate

Federation Store
  TrustDomain A → Bundle A
  TrustDomain B → Bundle B
  ...

Identity
  TrustDomain
  Subject
  KeyID
  Expiry
  Signature
```

という単純な構造を使います。

### Federation Administrator

Trust DomainとTrust Materialの対応を変更できるAuthorityです。

Storeは、このAdministratorの公開鍵をあらかじめ保持します。

呼び出し側が、

```text
今回だけこの公開鍵をAdministratorとして信頼してほしい
```

と差し替えることはできません。

これは前のControl Plane Authority Separation Spikeで確認した「Trust Rootをcallerに選ばせない」という原則をFederationへ適用したものです。

### BundleUpdate

Bundle更新には次を含めます。

- Trust Domain
- Version
- Key ID
- Public Key

一つのTrust Domainはrotationのため複数Keyを同時に保持できます。

## DomainごとにBundleを分ける

Storeは、

```text
map[TrustDomain]Bundle
```

として保持します。

Identity検証時には、expected Trust Domainを先に決め、そのDomainのBundleだけを使います。

すべてのKeyを一つのglobal key poolへ入れ、

```text
どれかのKeyで署名検証できればよい
```

とはしません。

## Identity自身にもTrust Domainをbindingする

Identity payloadにもTrust Domainを含めます。

そのため、Company Cが発行したIdentityを、

```text
expectedDomain = Company B
```

として検証すると、署名検証より前にnamespace mismatchとして拒否できます。

これはKey reuseがある場合にも重要です。

## Key reuseがあってもnamespaceを潰さない

SPIFFE仕様では、同じauthoritative keyを複数Trust Domainで再利用することは推奨されていません。

それでも現実の設定ミスや移行過程を考えると、

```text
同じKeyが登録されている
  ↓
同じIdentity namespaceである
```

とは扱わない方が安全です。

このSpikeでは、prodとstageに同一公開鍵を登録した場合でも、stage向けIdentityをprodとして受理しないことをTestしています。

## Bundle Version

Bundleはrotationで変化します。

Spikeでは各Trust Domainについて単調増加のVersionを要求します。

たとえば、

```text
version 3を適用済み
  ↓
version 2が後から届く
```

場合はrollback / replayとして拒否します。

実際のArchitectureでSPIFFEのsequence numberを使うか、独自versionを持つかは後段で比較します。

## Rotation

Key rotationでは、一定期間旧Keyと新Keyを同時にBundleへ入れられる必要があります。

Spikeでは、

```text
v1: old
v2: old + new
v3: new
```

をTestしています。

v2ではold / new両方のIdentityを検証できます。

v3でold keyを削除した後は、old keyで発行されたIdentityを新規検証できません。

## Identity Expiry

Bundle側だけでなくIdentity自体にもexpiryを持たせています。

古いIdentityが正しい署名を持っていても、期限切れなら現在のAuthorization根拠として使いません。

これはRQ-048 / SEC-015と同じfreshness原則です。

## 推移的Trustを作らない

Company AがCompany BとFederationしていて、Company BがCompany CとFederationしていても、

```text
A trusts B
B trusts C
therefore A trusts C
```

とはしません。

AのStoreにCのTrust Domainが明示的に登録されていなければ、CのIdentityを検証しません。

SPIFFE仕様でも長いtrust chainはSecurity上慎重に扱うべきとされています。

EKIでは標準状態で暗黙のtransitive federationを作らない方向を維持します。

## Unit Testで確認したこと

2026-09-18時点で、

```bash
go test ./...
```

を実行し、Testは通過しています。

確認した主なScenarioは次です。

1. Company CのIdentityをCompany Bとして受理しない
2. Cが明示的にFederationされていない場合、Bとの関係からCを推移的にTrustしない
3. Federation Administrator以外の鍵で作ったBundleUpdateを拒否する
4. 古いBundle Versionへのrollback / replayを拒否する
5. Key rotation中はold + newを同時に受理できる
6. old keyをBundleから削除した後はold Identityを検証しない
7. 同じKeyが複数Domainに登録されてもnamespaceを統合しない
8. expired Identityを拒否する

## このSpikeで確認できたこと

- Trust DomainとBundleを別々に保持してbindingできる
- callerがTrust Rootを差し替えないAPIを作れる
- Bundle updateをversion管理してrollbackを拒否できる
- key rotationのoverlapを表現できる
- key reuseがあってもnamespaceを維持できる
- direct federationだけを受理し、implicit transitive trustを作らない構造にできる

## このSpikeでまだ確認していないこと

- Bundle Endpointからの自動取得
- Endpoint URL / profile自体の安全な配布
- Web PKIを使う場合の保証範囲
- SPIFFE Authentication profile
- Bundle refresh interval
- network partition中のrotation
- emergency revocation
- Administrator key rotation
- multiple administrator / approval
- Trust Domain削除
- Federation peerごとのAuthorization Grant
- Federation設定変更のAudit耐タンパ性
- Identity namespaceの外部Protocol表現
- OIDC / X.509 / SPIFFE以外のIdentity provider
- Cross-signing
- long trust chain

## FederationとAuthorizationは別

Identityを検証できることは、Resource accessを許可することではありません。

```text
Company BのIdentityである
        ↓
Authentication / identity validation成功
        ↓
このTask / Artifact / Grantを許可するか
        ↓
別Authorization Policy
```

という分離を維持します。

Company BとのFederationを設定したからといって、Bの全Client / WorkerへEKI Resourceを許可することはありません。

## Architectureへの示唆

将来のFederation contractでは、少なくとも次を別々に表現する必要があります。

```text
Trust Domain / Identity namespace
Trust Material
Trust Material version / freshness
Federation configuration authority
Identity expiry / revocation
Authorization Grant
```

特に、

> bundleそのものが正しいか

と、

> そのbundleをこのTrust Domainへ対応付けてよいか

は別の判断です。

## 参考資料

- SPIFFE Federation
  - https://spiffe.io/docs/latest/spiffe-specs/spiffe_federation/
- SPIFFE Trust Domain and Bundle
  - https://spiffe.io/docs/latest/spiffe-specs/spiffe_trust_domain_and_bundle/
- NIST SP 800-207A
  - https://csrc.nist.gov/pubs/sp/800/207/a/final
