# Control Plane内部の侵害とblast radius

## この文書の役割

この文書は、EKI FabricのControl Planeを一つの完全に信頼できる箱として扱わず、内部Componentの一部が侵害・誤動作した場合に、どこまで被害を限定すべきかを整理する調査メモです。

Product RequirementやArchitectureの正本ではありません。Control Planeを必ずMicroservice化することや、特定のZero Trust製品を採用することを決めるものでもありません。

## 1. なぜControl Plane内部まで考えるのか

これまでのThreat Modelでは、ClientもWorkerも悪意を持ち得ることを明示しています。

一方で、Control Plane root signing authorityやAdministrator Policy authorityまで完全に奪われた場合は、通常のSecurity Guaranteeの外に置いています。

この境界自体は妥当ですが、その間に大きな領域が残っています。

たとえば、次のどれか一つだけが侵害された場合です。

- Scheduler
- Admission component
- Result Verifier
- Artifact metadata service
- Contribution accounting component
- Telemetry / Audit component

一つのComponent侵害が自動的に全権限取得へつながる構造は、Client / Worker側でどれだけ権限分離してもblast radiusが大きくなります。

## 2. Zero Trustから得られる考え方

NIST SP 800-207のZero Trust Architectureは、ネットワーク位置や「社内資産であること」だけを暗黙の信頼根拠にせず、Resourceごとに必要なAuthentication / Authorizationを行う考え方を示しています。

NISTの実装ガイドでもLeast Privilegeを重要なControlとして扱い、必要なResourceだけへ権限を限定することでlateral movementを抑える考え方が示されています。

EKIに置き換えると、Control Plane内部でも「内部Componentだから何でもできる」とはしない方が安全です。

## 3. RoleごとのAuthorityを分ける

概念的には、次のようなAuthorityがあります。

```text
Taskを受理する
Taskを配置する
WorkOrderを発行する
Artifact accessを許可する
Resultを検証する
Accepted Resultへ昇格する
Contributionを確定する
Grantを変更する
Policyを変更する
```

これらを一つのCredential、一つのProcess、一つのDatabase accountへ無条件に集約すると、その一箇所の侵害がFabric全体の侵害へ直結します。

Product Requirementとしては、具体的なProcess分割を決めるのではなく、**Security-sensitiveなAuthorityを必要以上に一つの主体へ集約しないこと**を要求するのが適切です。

## 4. Schedulerは配置を決めても、権限を作らない

たとえばSchedulerが「Worker Aで実行する」と判断できても、Scheduler自身が次のことまで自由にできる必要はありません。

- 任意Artifactの復号鍵を取得する
- 任意Executorを承認する
- Data Classを下げる
- ProviderのGrant上限を変更する

Placement DecisionとAuthority Issuanceを分離できれば、Schedulerが侵害されたときの被害を小さくできます。

同じ考え方はResult Verifierにも適用できます。

Verifierが一つのResultを検証するAuthorityを持つことと、Verifier自身が任意Taskを作成したりGrantを変更したりできることは別です。

## 5. Root authorityの侵害とは区別する

EKIで「Control Plane Componentの侵害へ耐える」と言っても、Root signing key、Policy root、すべてのAdministrator credentialまで同時に侵害された状態で完全な保証を行うことは要求しません。

重要なのは、次の中間状態です。

> 一部Componentの脆弱性やCredential漏えいがあっても、そこから無関係なAuthorityへ当然には横移動できない。

これは完全なByzantine Control Planeを要求するものではなく、Least Privilegeとblast-radius reductionの要求です。

## 6. Auditも独立した意味を持つ

侵害を完全に防げない場合、後から「どのAuthorityを使って何が変更されたか」を追えることも重要です。

ただし、Audit componentが書き換えられた場合の完全な耐タンパ性まで一律に要求するかは、別途検討が必要です。

少なくとも、Security-sensitiveな変更について、実行主体、変更対象、時刻、関連するGrant / Policy / Result等を追跡できることは既存Requirementと整合します。

## 7. EKIへの要求候補

第三巡目では、少なくとも次を要求へ反映する価値があります。

1. 一つのControl Plane ComponentのAuthorityを、その役割に必要な範囲へ限定できること
2. Placement、Artifact access、Executor admission、Result acceptance、Grant / Policy変更などの権限を、必要以上に一つへ統合しないこと
3. 一部Componentの侵害だけで、無関係なAuthorityへ自動的に横移動できない構成を選べること
4. Root authority全体の侵害と、一部Componentの侵害をThreat Model上で区別すること
5. Security-sensitiveな変更のProvenanceを追跡できること

## 参考資料

- NIST SP 800-207 — Zero Trust Architecture
  - https://csrc.nist.gov/pubs/sp/800/207/final
- NIST SP 1800-35 — Implementing a Zero Trust Architecture
  - https://csrc.nist.gov/pubs/sp/1800/35/final
- NIST Zero Trust Architecture Project — Risks Addressed
  - https://pages.nist.gov/zero-trust-architecture/VolumeE/RisksAddressed.html
