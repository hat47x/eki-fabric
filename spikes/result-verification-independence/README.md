# Result Verification Independence Spike

## このSpikeの役割

EKI Fabricでは、Workerが返した値を、そのWorkerが認証済みであるという理由だけで正しいResultとは扱いません。

特にアドホック参加を許す場合、一人の攻撃者が多数のWorker Identityを作り、見かけ上の多数決を支配するSybil攻撃を考える必要があります。

このSpikeでは、決定論的で完全一致できるResultだけを対象に、次の二つを小さく比較します。

1. Worker台数だけを見る素朴なquorum
2. Workerの背後にある独立性を条件へ含めるquorum

正式なResult Verification APIやIdentity modelを決めるものではありません。

## BOINCから学べることと、EKIで一段強く考えること

BOINCのVolunteer Computingでは、匿名に近い多数のhostが故障・改変・悪意を持ち得るため、同じjobを複数computerで実行し、application-specific validatorで結果を比較してquorumを作る方式が使われています。

これはEKIにとって重要な先行例です。

一方、EKIのThreat ModelではSybilとcollusionを明示的に扱います。

そのため、

```text
3 Workerが同じResultを返した
```

だけでは、

```text
3つの独立した主体が確認した
```

ことを意味しません。

たとえば一人の攻撃者が`worker-1`、`worker-2`、`worker-3`を同時に運用していれば、3票は実質1主体です。

## このSpikeの最小モデル

`verify.go`では、Result Claimとは別に、Control Plane側で検証済みであることを前提とする`Subject`を置きます。

```text
Subject
  WorkerID
  PrincipalID
  TrustDomain
  Managed
```

重要なのは、これらをWorkerの自己申告文字列として扱わないことです。

たとえば悪意あるWorkerがRequestへ

```text
principal = "different-person"
trust_domain = "trusted-company.example"
```

と書いただけで独立性を獲得できるなら、このモデルには意味がありません。

実際のIdentity / Attestation / Federationとの結び付け方はArchitecture段階で検討します。

## 検証Policy

Spikeでは、完全一致するDigest groupについて、次を条件にできます。

- matching Claim数
- distinct Principal数
- distinct Trust Domain数
- managed anchorが少なくとも一つ含まれること

例:

```text
matching claims       >= 3
distinct principals   >= 2
distinct trust domains >= 2
managed anchor        = required
```

これは「この値が絶対に正しい」という数孊的証明ではありません。

**どの程度異なるfailure domainから同じResultが得られたかを、単純なWorker台数より正直に表現する**ための一つの方法です。

## Conflictを多数決で隠さない

異なる二つのDigest groupが、どちらもPolicyを満たした場合、このSpikeは一方を適当に選びません。

```text
A: 2 independent principals / 2 domains
B: 2 independent principals / 2 domains
```

なら、Resultは`ambiguous`です。

より強いValidator、迾加replication、managed recomputation、人間判断などへ進む必要があります。

## Unit Testで確認したこと

2026-09-18時点で`go test ./...`が通過しています。

主なScenario:

- 同一Principalの3 Sybil Workerが、素朴な3票quorumを支配できる
- distinct Principal条件を加えると、同じ3 Workerではquorumを満たせない
- 2 Principal / 2 Trust Domainが同じDigestを返せば、対応Policyを満たせる
- Principalが別でも同一Trust Domainなら、domain independenceを満たさない
- managed anchorを必要条件にできる
- 競合する二つのResult groupが両方条件を満たした場合はambiguousにする
- 同じWorker IDを複数票として数えない

## このSpikeで確認できたこと

- Worker countとindependenceは別の概念として扱える。
- Resultの一致判定と、誰から得たResultかという独立性判定を分離できる。
- 独立性条件はTask / Assurance Profileごとに変えられる。
- 「条件を満たすResultが複数ある」状態を、無理にAccepted Resultへ潰す必要はない。

## このSpikeで確認していないこと

- Principal / Trust Domain / Operatorの独立性を現実にどう証明するか
- 一つの企業やCloud Provider内部での共通failure domain
- Device attestationによる物理Device独立性
- NAT / IP addressを独立性根拠に使えるか
- collusionを完全に防げるか
- fuzzy numeric result
- non-deterministic AI result
- cryptographic proof
- TEE attestation
- replicationによる追加Compute costの実測

## Assurance上の限界

このSpikeの方式でも、2つの異なるTrust Domainが実際には同じ攻撃者に支配されていれば破られます。

したがって、`PrincipalID`や`TrustDomain`を増やすだけで「完全なByzantine耐性」が得られるわけではありません。

EKIが重要視するのは、

> 独立していると何を根拠に判断したのか

を明示し、要求する保証水準より弱い根拠しかない場合は、強いAssuranceを名乗らないことです。

高保証Taskでは、managed anchor、別方式Validator、trusted recomputation、TEE、verifiable computation等を組み合わせる余地を残します。

## Architectureへ持ち越す重要点

将来のResult Verificationでは、少なくとも次を別の情報として保持する価値があります。

```text
Result equivalence
  何を「同じ結果」とみなしたか

Independence evidence
  何を根拠に別failure domainとみなしたか

Verification authority
  誰が・どのPolicyでAccepted Resultへ昇格したか

Verification cost
  追加replication / validationにどれだけResourceを使ったか
```

これらを一つの`quorum=3`という数孋に押し込めないことが、EKIの悪意耐性には重要です。

## 参考資料

- BOINC — A Platform for Volunteer Computing
  - https://boinc.berkeley.edu/boinc_a_platform_for_volunteer_computing.pdf
- BOINC — persistent redundant computing / data locality paper
  - https://boinc.berkeley.edu/boinc_papers/locality/text.php
- EKI要求
  - `../../requirements/PRODUCT_REQUIREMENTS.md`
  - `../../requirements/ASSURANCE_MODEL.md`
