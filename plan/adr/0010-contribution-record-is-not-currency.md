# ADR-0010 Contribution Recordは検証可能な事実の記録とし、残高・移転・価格をCoreへ持たない

- Status: **Proposed**
- Date: 2026-10-06
- Related: RQ-034, RQ-036, RQ-037, RQ-038, RQ-022, RQ-023, Issue #68

## 1. Context

[value.md](../../product/value.md)の「Verifiable contribution」と原則 §11は、資源の提供を検証可能な形で記録すると同時に、EKIが通貨、市場価格、現金化、交換レートを提供しないことを定めています。

この境界は、実装を進める途中で崩れやすいものです。

- 便利だからと「Providerごとの累計Credit」というfieldを足すと、それは残高になる。残高があれば、移転や売買をしたいという要求が出てくる。そうなると、EKIは実質的に通貨の発行者になる。
- Contributionを報酬に結びつけると、Sybil攻撃の動機が強まる。Sybil攻撃とは、一人の攻撃者が多数のWorker Identityを作り、この場合は記録を水増しすることである。

## 2. Decision

1. Contribution Recordは事実の記録とする。記録するのは、誰の資源が、どのTaskとAttemptで、どの期間にどれだけ提供され、何がどの方式で検証されたかである。数値として持つのは、CPU秒や転送量などの物理量と、検証の状態だけにする。
2. EKI CoreのschemaとAPIには、残高、Principal間の移転、価格、交換レート、Creditの発行を表すfieldやoperationを持たない。これをconformance testで、禁止されたfieldがないかを検査して機械的に守る。
3. Providerごとの合計のような集計値は、読み出すときに計算して返してよい。ただし、集計値を保存された状態として扱わず、移転の対象にもしない。
4. 社内配賦、寄付の証明、相互利用Creditなどへの写像は、外部のPolicyがContribution Recordを読み取って行う（RQ-037）。外部で写像した結果をEKIへ書き戻し、配置やAuthorityを変える経路は作らない。
5. 未検証のClaimにもとづくContributionは、検証済みのものと区別して記録する。Reputationと同じく、Contributionの量をsecurity root of trustにしない（RQ-023）。

## 3. Consequences

### Positive

- EKIが通貨や市場の運営者とみなされる、法的・運用上のリスクを避けられる。
- Public-interest Compute Grant（RQ-034）では、提供者と受益者が互いを全面的に信頼していなくても、事実の記録を共有できる。

### Negative / Limitations

- 報酬制度は利用者が自分で作る必要がある。EKIが提供するのは、記録の形式と検証方式だけである。
- 外部の制度が記録に強い経済的価値を与えた場合、Sybil攻撃に耐えられるかどうかはEKI単体では保証できない（RQ-022、Needs research）。
