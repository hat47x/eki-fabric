# FederationとIdentity namespaceの安全性

## 役割

この文書は、複数組織がEKI Fabricを限定的に相互利用するとき、Identity namespaceや信頼情報の取り違えによって権限境界が崩れないための要求を検討する調査メモです。Product RequirementやArchitectureの正本ではありません。

## なぜFederationが難しいのか

Federationでは、相手組織のIdentityを認証できるようにする必要があります。しかし、「相手のIdentityを認証できる」ことと、「相手の主体へ自組織内の権限を与える」ことは別です。

さらに、同じ短い名前を複数組織が使うことがあります。たとえば`worker-1`や`team-a`という名前だけを見ても、どの管理主体が発行したIdentityなのかは分かりません。Federationでは、Identityを発行したauthorityとnamespaceの結び付きが失われないことが重要です。

## SPIFFE Federationから得られる示唆

SPIFFEではTrust DomainをIdentity namespaceとして扱い、各Trust Domainが独立した発行authorityを持ちます。Federationでは、外部Trust DomainのBundleを取得してIdentityを検証しますが、BundleとTrust Domain名の対応を保つことがSecurity上重要とされています。

特にSPIFFE FederationのSecurity Considerationsでは、Federation設定に含まれるTrust Domain名、Bundle Endpoint URL、Endpoint Profileなどが改変されると、別Trust Domainのなりすましにつながり得ることが明示されています。また、複数のTrust Bundleを単純に一つへ混ぜ、どれか一つのBundleで検証できればよいという扱いは、Trust Domain間のなりすましを許すため避ける必要があります。

EKIでSPIFFEを採用するかどうかとは独立して、この原則はそのまま有用です。

## EKIで守りたい性質

### 1. Identityは発行主体と切り離さない

EKI内のPrincipalやWorker Identityは、ローカル名だけでなく、少なくとも「どのTrust DomainまたはAuthorityが発行したIdentityか」を区別できる必要があります。

`worker-1`のような短い名前だけをSecurity判断のIdentityにしません。

### 2. Federation関係は包括的なTrustではない

Company AがCompany BのIdentityを認証できるようになっても、Bの全Client、全Worker、全Executor、全Data Classを許可したことにはなりません。

Federationは「相手のIdentityを検証できるようにすること」であり、Authorizationは別に行います。

### 3. Trust informationのbindingを保つ

外部Trust Domainの公開鍵やBundle、Issuer Metadataなどを保持する場合、その情報がどのAuthority / Namespaceへ対応するかを明示的に保つ必要があります。

「この署名はどれかの登録済み鍵で検証できた」というだけでは不十分です。

### 4. Federation設定そのものを高価値の設定として扱う

Federation先、Trust Domain名、Bundle Endpoint、認証Profileなどは、通常のClientが変更できる設定ではありません。

これらを改変するとIdentityの意味そのものが変わり得るため、変更履歴、権限分離、必要に応じた複数者承認などをArchitecture候補として検討する価値があります。

### 5. 長い信頼連鎖を無意識に作らない

SPIFFE Federationでは、外部Bundleの取得に別Trust Domainを利用するような信頼連鎖を作れますが、連鎖の途中が侵害されると下流へ影響が広がり得ます。

EKIでも、AがBを信頼し、BがCを信頼しているからAもCを自動的に信頼する、といったtransitive trustを標準動作にしない方が安全です。

## Requirementへの反映候補

- FederationされたIdentityは、発行Trust Domain / Authorityとのbindingを失ってはならない。
- 異なるTrust DomainのIdentity namespaceを暗黙に併合してはならない。
- Federation設定をClient入力から変更できてはならない。
- Federation関係はIdentity validationを可能にするものであり、Authorization Grantとは分離する。
- Trust chainが存在する場合、どのauthority chainを根拠にしたかを追跡できる余地を持つ。

## 参考

- SPIFFE Federation: https://spiffe.io/docs/latest/spiffe-specs/spiffe_federation/
- SPIFFE Trust Domain and Bundle: https://spiffe.io/docs/latest/spiffe-specs/spiffe_trust_domain_and_bundle/
- NIST SP 800-207A: https://csrc.nist.gov/pubs/sp/800/207/a/final
