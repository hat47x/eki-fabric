# FederationにおけるIdentity境界の安全性

## この文書の役割

この文書は、EKI Fabricが複数の企業・団体・個人の管理領域をまたいで計算資源を利用するとき、Identityをどの範囲まで信用し、どこで混同を防ぐ必要があるかを整理するための調査メモです。

Product RequirementやArchitectureの正本ではありません。特定のIdentity製品を採用することも、この文書では決めません。

## 1. なぜFederationが必要か

EKIでは、一つの会社の中だけでWorkerを使うとは限りません。

たとえば、次のような利用形態を想定しています。

- 関連会社同士で、一部の計算資源だけを融通する
- 大手企業が公益団体向けに余剰GPUを提供する
- 個人や小規模団体が、公開データ向けTaskへ一時的に参加する
- Cloud ProviderとオンプレミスWorkerを同じFabricから利用する

このとき、すべての参加者を一つの巨大なIdentity基盤へ移すことは現実的ではありません。それぞれの主体が、自分の管理領域でIdentityを発行し続けられる必要があります。

一方で、「外部のIdentityを認証できる」ことと、「そのIdentityへ何でも許可する」ことは別です。

Federationは、包括的な信頼関係ではなく、**外部で発行されたIdentityを、どの管理領域のIdentityとして検証するかを安全に共有する仕組み**として捉える必要があります。

## 2. SPIFFEから得られる重要な示唆

SPIFFEでは、Trust DomainをIdentity namespaceとして扱います。各Trust Domainには、そのDomainのIdentityを検証するためのTrust Bundleが対応します。

Federationでは、外部Trust DomainのBundleを取得し、そのDomainが発行したIdentityを検証できるようにします。

ここで重要なのは、複数のTrust DomainのBundleを一つに混ぜて扱ってはならないことです。

SPIFFE Federation仕様は、異なるTrust DomainのBundleを一つの大きなBundleとして統合すると、一つのTrust Domainが別のTrust Domainになりすませる危険があると明示しています。そのため、`Trust Domain name` と `Bundle` の結び付きを保ったまま管理する必要があります。

EKIに置き換えると、次のようになります。

```text
company-a.example
        ↓
A社が発行するIdentityを検証する根拠

company-b.example
        ↓
B社が発行するIdentityを検証する根拠
```

この二つを、単に「外部参加者を検証できる鍵一覧」として混ぜてはいけません。

## 3. Identityを確認できても、Authorityは自動的には増えない

たとえばA社がB社のIdentityを正しく認証できたとしても、そこから次の権限が自動的に生まれてはいけません。

- B社のすべてのWorkerを利用する権限
- B社のすべてのArtifactを読む権限
- B社のGrantを変更する権限
- B社のControl Planeへ管理者として接続する権限

EKIでは、Identity検証とAuthority付与を分離する必要があります。

```text
Identityを確認できた
        ↓
誰なのかは分かった
        ↓
何を許可するかは別のPolicyで決める
```

この原則は、社内Identityにも外部Identityにも同じように適用します。

## 4. Federation設定そのものがSecurity-sensitiveである

外部Trust Domainを追加するときには、少なくとも次のような対応関係を設定する必要があります。

```text
Trust Domain
Bundle / 検証鍵
取得先
検証方法
許可する用途
```

SPIFFE Federation仕様では、Trust Domain名やBundle Endpointの対応関係が改ざんされると、別の主体を正規のTrust Domainとして扱ってしまう危険があると説明されています。

そのためEKIでも、Federation設定は単なる接続先一覧ではなく、**Identityの意味を決めるSecurity-sensitiveなPolicy**として扱う必要があります。

少なくとも、次を要件候補とします。

- Trust Domainと検証根拠の対応を明示する
- 異なるTrust Domainの検証根拠を無差別に統合しない
- Federation設定の変更主体と変更履歴を追跡できる
- 古いBundleや失効したFederation関係を無期限に利用し続けない
- Federationした相手のIdentity namespaceを、自分のnamespaceとして解釈しない

## 5. 長い信頼連鎖を暗黙に作らない

AがBをFederationし、BがCをFederationしているからといって、Aが自動的にCを信頼する構造は慎重に扱う必要があります。

信頼連鎖が長くなるほど、途中の一つの管理領域やBundle配布点の侵害が、離れた主体へ波及する可能性があります。

EKIの標準モデルでは、Federation関係を明示的なものとし、推移的な包括信頼を暗黙に作らない方向が安全です。

## 6. Privacyとの関係

外部組織からWorkerを借りるために、そのWorkerの人間の実名、端末hostname、内部IP、詳細な組織構造までClientへ公開する必要はありません。

利用側が本当に必要なのは、多くの場合、次のような情報です。

```text
このTaskを実行する権限があるか
必要なAssuranceを満たすか
このGrantの対象に含まれるか
```

そのため、Identityは内部のPolicy判断には使っても、Clientへ必要以上に露出しない設計が望まれます。

## 7. EKIへの要求候補

第三巡目では、少なくとも次をProduct Requirementへ反映する価値があります。

1. Federationで利用するIdentity namespaceと、その検証根拠を混同しないこと
2. 外部Identityを認証できることと、そのIdentityへAuthorityを与えることを分離すること
3. Federation設定をSecurity-sensitiveなPolicyとして扱うこと
4. 推移的な包括信頼を暗黙に成立させないこと
5. Federationのために不要なTopologyや個人情報を広く開示しないこと

## 参考資料

- SPIFFE Federation
  - https://spiffe.io/docs/latest/spiffe-specs/spiffe_federation/
- SPIFFE Trust Domain and Bundle
  - https://spiffe.io/docs/latest/spiffe-specs/spiffe_trust_domain_and_bundle/
- SPIFFE Workload API
  - https://spiffe.io/docs/latest/spiffe-specs/spiffe_workload_api/
