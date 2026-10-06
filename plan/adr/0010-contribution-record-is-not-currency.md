# ADR-0010 — Contribution Recordは検証可能な事実の記録とし、残高・移転・価格をCoreへ持たない

- Status: **Proposed**
- Date: 2026-10-06
- Related: RQ-034, RQ-036, RQ-037, RQ-038, RQ-022, RQ-023, Issue #68

## 1. Context

[value.md](../../product/value.md)「Verifiable contribution」と原則 §11は、資源提供を検証可能に記録しつつ、通貨・市場価格・現金化・交換レートをEKIが提供しないことを定めています。

この境界は、実装の途中で崩れやすいものです。

- 例: 利便性のため「Providerごとの累計Credit」fieldを足すと、それは残高になる。残高があれば移転や売買の要求が生まれ、EKIが事実上の通貨発行者になる。
- 例: Contributionを報酬に結びつけると、一人の攻撃者が多数のWorker Identityを作って記録を水増しする動機（Sybil攻撃）が強くなる。

## 2. Decision

1. Contribution Recordは、「誰の資源が、どのTask・Attemptで、どの期間・量を提供し、何がどの方式で検証されたか」という**事実の記録**とする。数値は物理量（CPU秒、転送量等）と検証状態だけを持つ。
2. EKI Coreのschemaと APIに、残高、Principal間の移転、価格、交換レート、Creditの発行を表すfield・operationを持たない。これをconformance test（schemaの禁止field検査）で機械的に守る。
3. 集計（Providerごとの合計等）は読み出し時の派生値として提供してよいが、保存された状態として扱わず、移転の対象にしない。
4. 社内配賦、寄付証明、相互利用Credit等への写像は、外部PolicyがContribution Recordを読み取って行う（RQ-037）。外部の写像結果をEKIへ書き戻して配置やAuthorityを変える経路は作らない。
5. 未検証のClaimに基づくContributionは、検証済みと区別して記録する。Reputationと同様、Contributionの量はsecurity root of trustにしない（RQ-023）。

## 3. Consequences

### Positive

- EKIが通貨・市場の運営者として扱われる法的・運用上のリスクを避けられる。
- Public-interest Compute Grant（RQ-034）で、提供者と受益者が互いを全面的に信頼しなくても、事実の記録を共有できる。

### Negative / Limitations

- 利用者は報酬制度を自前で作る必要がある。EKIは記録の形式と検証方式を提供するだけである。
- 外部制度が記録へ強い経済的価値を与えた場合のSybil耐性は、EKI単体では保証できない（RQ-022、Needs research）。
