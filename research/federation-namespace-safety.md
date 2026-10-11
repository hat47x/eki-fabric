# Federationで識別情報の名前空間を守る（Identity namespace safety）

## この文書の役割

この文書は、複数組織がEKI Fabricを限定的に相互利用するとき、Identity namespaceや信頼情報の取り違えによって権限境界が崩れないための要求を検討する調査メモです。

Product RequirementやArchitectureの正本ではありません。また、SPIFFE / SPIREを採用することをこの文書で決めるものでもありません。

## なぜFederationが必要か

EKIでは、一つの会社の中だけでWorkerを使うとは限りません。

たとえば、次のような利用形態を想定しています。

- 関連会社同士で、一部の計算資源だけを融通する
- 大手企業やCloud Providerが、公益団体向けに余剰GPUを提供する
- 個人や小規模団体が、公開データ向けTaskへ一時的に参加する
- オンプレミスと外部CloudのWorkerを同じFabricから利用する

このとき、すべての参加者を一つの巨大なIdentity基盤へ移すことは現実的ではありません。それぞれの主体が、自分の管理領域でIdentityを発行し続けられる必要があります。

一方で、「相手のIdentityを認証できる」ことと、「そのIdentityへ自組織内の権限を与える」ことは別です。

Federationは包括的な信頼関係ではなく、**外部で発行されたIdentityを、どの管理領域のIdentityとして検証するかを安全に共有する仕組み**として捉える必要があります。

## Identityは短い名前だけでは足りない

同じ短い名前を複数組織が使うことがあります。

たとえば`worker-1`や`team-a`という名前だけを見ても、どの管理主体が発行したIdentityなのかは分かりません。

```text
company-a.example / worker-1
company-b.example / worker-1
```

この二つは別のIdentityです。

EKIでは、Identityを発行したAuthorityとnamespaceの結び付きが失われないことが重要です。

## SPIFFE Federationから得られる示唆

SPIFFEではTrust DomainをIdentity namespaceとして扱い、各Trust Domainが独立した発行Authorityを持ちます。Federationでは、外部Trust DomainのBundleを取得してIdentityを検証します。

ここで特に重要なのは、**BundleとTrust Domain名の対応を保つこと**です。

SPIFFE FederationのSecurity Considerationsでは、Federation設定に含まれるTrust Domain名、Bundle Endpoint URL、Endpoint Profileなどが改変されると、別Trust Domainのなりすましにつながり得ることが明示されています。

また、複数のTrust Bundleを単純に一つへ混ぜ、「どれか一つのBundleで検証できれば有効」と扱うと、Trust Domain間のなりすましを許す危険があります。

EKIでSPIFFEを採用するかどうかとは独立して、この原則は有用です。

## EKIで守りたい性質

### 1. Identityは発行主体と切り離さない

EKI内のPrincipalやWorker Identityは、ローカル名だけでなく、少なくとも「どのTrust DomainまたはAuthorityが発行したIdentityか」を区別できる必要があります。

`worker-1`のような短い名前だけをSecurity判断のIdentityにしません。

### 2. Federation関係は包括的なTrustではない

Company AがCompany BのIdentityを認証できるようになっても、Bの全Client、全Worker、全Executor、全Data Classを許可したことにはなりません。

Federationは「相手のIdentityを検証できるようにすること」であり、Authorizationは別に行います。

```text
Identityを確認できた
        ↓
誰なのかは分かった
        ↓
何を許可するかは別のPolicyで決める
```

### 3. Trust informationのbindingを保つ

外部Trust Domainの公開鍵、Bundle、Issuer Metadataなどを保持する場合、その情報がどのAuthority / Namespaceへ対応するかを明示的に保つ必要があります。

「この署名はどれかの登録済み鍵で検証できた」というだけでは不十分です。

### 4. Federation設定そのものを高価値の設定として扱う

Federation先、Trust Domain名、Bundle Endpoint、認証Profileなどは、通常のClientが変更できる設定ではありません。

これらを改変するとIdentityの意味そのものが変わり得ます。そのため、少なくとも次をArchitecture比較の判断軸に残します。

- 誰がFederation関係を追加・変更・削除できるか
- 変更履歴を追跡できるか
- Trust Materialの更新・失効をどう扱うか
- 必要に応じて独立承認へ接続できるか

具体的な承認方式は上流Requirementでは固定しません。

### 5. 長い信頼連鎖を無意識に作らない

AがBをFederationし、BがCをFederationしているからといって、Aが自動的にCを信頼する構造は慎重に扱う必要があります。

信頼連鎖が長くなるほど、途中の一つの管理領域やBundle配布点の侵害が、離れた主体へ波及する可能性があります。

EKIの標準モデルでは、Federation関係を明示的なものとし、推移的な包括Trustを暗黙に作らない方向が安全です。

### 6. Federationのために内部Topologyを公開しすぎない

外部組織からWorkerを借りるために、そのWorker利用者の実名、端末hostname、内部IP、詳細な組織構造までClientへ公開する必要はありません。

利用側が本当に必要なのは、多くの場合、次のような判断結果です。

```text
このTaskを実行する権限があるか
必要なAssuranceを満たすか
このGrantの対象に含まれるか
```

Identityは内部Policy判断に利用しても、通常Clientへ必要以上に露出しない設計が望まれます。

## Requirementへの反映

第三巡目では、次を`RQ-049 — Federation identity namespace binding`としてProduct Requirementsへ反映しています。

- FederationされたIdentityは、発行Trust Domain / Authorityとのbindingを失わない
- 異なるTrust DomainのIdentity namespaceを暗黙に併合しない
- Federation設定を通常Client入力から変更できることを前提にしない
- FederationによるIdentity validationとAuthorization Grantを分離する
- 推移的な包括Trustを暗黙に成立させない

Threat Modelでは、Federation configuration / namespace substitutionを独立した攻撃面として扱います。

## Architectureへ持ち越すもの

- SPIFFE / SPIREを採用するか
- Trust BundleやIssuer Metadataの具体的な形式
- Federation設定の配布・更新Protocol
- 複数者承認をどの操作へ要求するか
- Privacyを保ちながらIndependenceやTrust Domainをどの粒度で外部へ示すか

## 参考資料

- SPIFFE Federation
  - https://spiffe.io/docs/latest/spiffe-specs/spiffe_federation/
- SPIFFE Trust Domain and Bundle
  - https://spiffe.io/docs/latest/spiffe-specs/spiffe_trust_domain_and_bundle/
- SPIFFE Workload API
  - https://spiffe.io/docs/latest/spiffe-specs/spiffe_workload_api/
- NIST SP 800-207A
  - https://csrc.nist.gov/pubs/sp/800/207/a/final
