# EKI Fabric Worker Assurance Model

## この文書の役割

この文書は、Workerを単純な`trusted / untrusted`や一本のTrust Levelで表現せず、Taskごとに必要な保証を複数の観点へ分けて考えるための上流Requirement interpretationです。

具体的なProtocolやwire schema、Attestation製品はまだ定義しません。

ここで重要なのは、「そのWorkerを信頼できるか」という一問へすべてを押し込めず、**何について、どの根拠から、どこまで確認できるか**を分けることです。

## なぜ一つのTrust Levelでは足りないのか

たとえば、次の三つのWorkerを考えます。

- 企業管理端末でIdentityは強いが、Confidential Computingには対応していない
- 匿名参加者の端末だが、あるTaskについて暗号学的Proofを生成できる
- Hardware Attestationには対応しているが、参加実績がなく、Resultの意味上の正しさを検証するValidatorもない

これらを`L0 < L1 < L2`のように一列へ並べると、本来独立した性質を誤って代替可能とみなす危険があります。

たとえば「企業管理端末だからResultも正しい」「Attestation済みだからAIの回答内容も正しい」といった飛躍です。

EKIでは、少なくとも次の軸を独立して扱えることを要求します。

---

## Axis A — Identity Assurance

Workerが「どのIdentityとして参加しているか」「どの管理主体に属するか」を、どの程度確認できるかを表します。

候補状態の例は次のとおりです。

- `anonymous` — persistent Identityを要求しない
- `self-asserted` — cryptographic keyはあるが、外部主体とのbindingがない
- `domain-issued` — Trust DomainがIdentityを発行している
- `managed-device` — 管理端末としてのbindingがある

Identity Assuranceが高くても、Result correctnessを意味しません。

「誰が返したか」を確認できることと、「返した内容が正しい」ことを分けます。

---

## Axis B — Execution Environment Assurance

Workerが主張する実行環境について、どの程度独立したEvidenceが得られるかを表します。

候補状態の例は次のとおりです。

- `none`
- `self-reported`
- `software-measured`
- `hardware-attested`

`hardware-attested`であっても、Attestation対象外のI/O、network、host componentまで自動的に信頼できるとはみなしません。

また、実行環境が期待どおりであることと、Application上のResultが正しいことも別です。

---

## Axis C — Confidentiality Capability

Task input、Model、Intermediate Dataなどを、Worker host ownerや他Processからどの程度守れるかを表します。

候補状態の例は次のとおりです。

- `public-only` — Worker ownerから見えても問題ないDataだけを扱う
- `managed-boundary` — 組織管理、契約、運用などの明示的な管理境界へ限定する
- `confidential-runtime` — Hardware / Runtimeの保護を使い、host ownerからも秘匿する構成を取れる

`encrypted-in-transit`はこの軸の十分条件ではありません。

通信中に暗号化されていても、実行時に通常のhost processから平文を読めるなら、untrusted host ownerに対するConfidentialityは成立していないためです。

---

## Axis D — Result Integrity Capability

Workerを無条件に信用せず、ResultがTaskの要求を満たしたことをどの方式で確認できるかを表します。

候補capabilityの例:

- `none`
- `application-validator`
- `replicable`
- `trusted-recompute-compatible`
- `attested-execution`
- `cryptographic-proof`

これらを単純な強さ順には並べません。

あるTaskでは小さなapplication-specific validatorが最も確実かもしれず、別のTaskではProofが適切かもしれません。生成AIの文章のように「唯一の正解」を定義しにくいTaskでは、どの方式でもsemantic correctnessを完全には保証できない場合があります。

---

## Axis E — Independence / Failure Domain

複製実行や複数検証を行う場合に、それぞれが同じ原因で同時に誤る可能性をどの程度分離できるかを表します。

独立性の候補dimension:

- principal
- Trust Domain
- device
- physical host
- operator
- Executor implementation
- hardware vendor / architecture
- network / region
- Validator / Verifier

高保証Taskでは、単に`worker_id`が違うだけで独立しているとはみなしません。

同じ攻撃者が100個のWorker Identityを作れるなら、100 Workerによる多数決でも独立した100主体による確認にはならないためです。

---

## Axis F — Operational Reliability History

過去のdeadline遵守率、validation成功率、availability、error率などを表します。

これはScheduler効率の改善に利用できます。

たとえば、長期間正しい結果を返しているWorkerでは複製率を下げる、失敗が続くWorkerへの割当量を減らす、といった最適化が考えられます。

ただし、Operational HistoryはSecurity Root of Trustではありません。長期間良好だったWorkerが後から侵害される可能性を排除しません。

---

## Axis G — Resource Measurement Assurance

CPU / GPU time、Memory、Network、Energyなど、Contribution算定やResource accountingに使う測定値を、どの程度信頼できるかを表します。

候補状態の例は次のとおりです。

- `self-reported`
- `control-observed`
- `cross-validated`
- `attested-metering`

Resultが正しいことと、申告された量のcomputeを本当に提供したことは別です。

たとえば、予測しやすいResultを実際には計算せず返してもValidatorを通るTaskがあり得ます。そのためResult IntegrityとResource Measurementを一つの軸にまとめません。

価値交換に利用するContributionでは、どの測定方式だったかをRecordへ残せる必要があります。

Measurement Assuranceは、測定値の意味を自由に強くするためのTrust Scoreではありません。たとえばControl Planeが観測できる`lease-wall-time`は、同じ時間の`gpu-active-time`やCPU timeを意味しません。Metric、Unit、Observer、Assuranceを一体として保持し、「何を観測したか」を保証範囲より強い名称へ読み替えないことを要求します。

また、Accepted ResultはResource Measurementの代替Evidenceではありません。Resultが正しいことと、申告されたResource量が正しいことは独立して評価します。一方、Contribution Policyがself-dealingや無意味なWorkを抑えるためAccepted Resultを発行条件にすることは許容します。この場合も、Result Acceptanceは測定精度を引き上げるのではなく別の条件です。

Contribution RecordはResource Class、Metric、Unit、Quantity、Measurement Assurance、Observer、Work provenanceを保持し、EKI Core共通のUniversal Scoreへ変換しない方針を優先します。金銭・Credit・社会的価値への換算は外部Policyの責務です。

---

## Axis H — Verification Authority Assurance

ResultやAttestation Evidenceを評価した側を、どの程度信頼基盤として扱うかを表します。

Workerだけを疑っても、ValidatorやVerifierが侵害されていれば偽ResultをAccepted Resultへ昇格できます。

EKIでは少なくとも、次を追跡できる必要があります。

- どのValidator / Verifierを使ったか
- どのPolicyで評価したか
- そのVerifierを誰が信頼基盤として認めたか
- 高保証Taskで独立した複数検証を要求したか

すべてのTaskで複数Verifierを必須にはしません。

重要なのは、Verifierを暗黙の絶対的信頼へ置かず、「この保証はどのVerification Authorityへ依存しているか」を隠さないことです。

Remote Attestationでは、RFC 9334の用語でいうEvidenceの評価とAttestation Resultの利用を分けます。VerifierはEvidenceへAppraisal Policyを適用してAttestation Resultを作り、Relying PartyはそのResultへ別の用途別Policyを適用します。したがって、`Verifier accepted`をそのまま`Task placement authorized`へ読み替えません。Verifier Identity、Verifier Policy Revision、Attester / Trust Domain bindingを追跡できることをAssurance上の条件とします。

---

## Axis I — Evidence Freshness / Revocation

Identity、Attestation Result、Lease、Grant、内部実行権限などが、現在も有効であるとどの程度確認できるかを表します。

過去に安全だったWorkerが、現在も安全とは限りません。

そのため、古いEvidenceを無期限に使い回すのではなく、必要な保証水準に応じて次を扱える必要があります。

- expiry
- revocation
- Evidence freshness
- replay resistance
- key / credential rotation

具体的なnonce、counter、timestamp、clock sourceはArchitectureで決定します。

---

# Task側から要求するAssurance

Taskは、曖昧に「trusted worker」を要求するのではなく、必要な保証を軸ごとに表現できることを目指します。

たとえば公開データを使う検証可能なTaskなら、概念的には次のように考えられます。

```text
Confidentiality:
  public-only

Result Integrity:
  application-validator OR
  independent-replication(2)

Independence:
  distinct operator where known

Execution Environment:
  no attestation required
```

一方、機密Taskなら次のような要求になるかもしれません。

```text
Confidentiality:
  confidential-runtime

Execution Environment:
  hardware-attested

Evidence Freshness:
  fresh-attestation-required

Result Integrity:
  attested-execution + validator
```

ここでの表現はRequirementを考えるための概念例であり、将来のwire schemaではありません。

---

# Result Assurance Profiles

Result Assurance Profileは、Result correctnessについてEKIがどの程度の追加確認を行ったかを説明するための候補です。

番号が大きいほど常に優れているという意味ではありません。Taskごとに適切な方法は異なります。

## RA-0 — Best effort

EKIとしてResult correctnessの追加検証を行わない。

公開データ、低影響、利用側が独自に確認できるTaskなどを想定します。

## RA-1 — Application validated

Application-specific validatorによって、不正Resultを検出できる構成です。

Validator自身をどの程度信頼するかはAxis Hとして別に扱います。

## RA-2 — Replicated

複数の実行結果を比較します。

比較関数だけでなく、どの程度のIndependenceを要求するかが必要です。匿名Workerの単純多数決だけではSybil耐性を保証しません。

## RA-3 — Anchored recomputation

untrusted WorkerのResultを、管理下Workerや別の強いFailure Domainでsample / recomputeし、必要に応じて検証量を調整します。

BOINCのAdaptive Replicationのように、検証コストとthroughputのバランスを取る考え方が参考になります。

## RA-4 — Attested execution

Remote Attestationなどを使い、要求された実行環境状態についてVerifierがEvidenceを評価します。

AttestationはExecution Environment Assuranceを強めますが、「そのApplication Resultが意味的に正しい」ことを常に保証するわけではありません。

また、Attestation ResultはWorker全体へ恒久的な`trusted=true`を付与するものではありません。Evidenceのfreshness、VerifierのAppraisal Policy、Result expiry、Task側が要求するEnvironment Classを別々に扱います。Raw EvidenceにはVendor固有情報や端末識別情報が含まれ得るため、Relying Partyへは必要に応じてnormalized Resultのみを渡し、Evidence Digest等でprovenanceを保持する構成を許容します。

## RA-5 — Cryptographically verifiable execution

期待するProgram / Input relationからResultが得られたことを、cryptographic proofで確認します。

Worker Identityへの依存を大きく減らせる可能性がありますが、すべてのTask、特に大規模GPU / AI workloadで実用的とは仮定しません。

Cryptographic proofを利用する場合は、少なくともProgram Digest、Input Digest、Output Digest、Task / Attemptとのbindingを追跡できることを要求します。Proofがvalidであっても、未承認Programの正しい実行を証明しているだけかもしれないため、Executor / Program Admissionを置き換えません。また、外部副作用、Resource Measurement、Application上の意味的な正しさも別の保証です。

Proof System固有の検証結果は、必要に応じてVerification Authorityがnormalized Verification Resultへ変換し、Relying PartyがTask固有Policyを再評価できる構造を優先します。VerifierがProofを受理したことを、そのままResult Acceptanceへ読み替えません。

Verifiable ComputationとZero Knowledgeも同一視しません。Private Inputやwitness secrecyを要求する場合、そのPrivacy propertyをProof System / Profileの検証済み属性として扱い、Clientの自己申告だけでは強化しません。

---

# Confidentiality Profiles

## CA-0 — Public / disposable

Worker ownerにinput、model、intermediateが見えても問題ないTaskです。

アドホック参加者へ最も配置しやすいProfileです。

## CA-1 — Managed boundary

組織管理端末、契約上の管理境界などへ限定します。

技術的にhost ownerから秘匿されることまでは主張しません。

## CA-2 — Confidential execution

Remote Attestationなどで期待したConfidential Computing環境を確認し、必要なkey / materialをその環境へだけreleaseする構成を許容します。

CPU、GPU、I/Oを含むend-to-endの信頼境界が成立しているかは、採用する技術ごとに確認が必要です。

---

# Placementで守るべき原則

1. Schedulerは性能値だけでWorkerを選んではならない。
2. Taskが要求する各Assurance Axisを満たさないWorkerを、Reputationや高速性だけで代替してはならない。
3. `unknown`は安全側へ評価する。情報不足を高保証として扱わない。
4. Assurance Evidenceが期限切れ、失効、revoke済みの場合、継続利用を暗黙に許可しない。
5. 同じWorkerでも、Executor version、CPU / GPU pathなどに応じてReliability Historyを分ける余地を持つ。
6. Providerが強いAssuranceを自己宣言するだけでは成立しない。高保証Claimには、要求水準に応じた外部Evidenceまたは明示的Policy trustを必要とする。
7. Result Verificationに使うValidator / VerifierのIdentityとPolicyも、必要に応じてPlacement / Acceptance判断へ含める。
8. Confidentiality不足を「Task終了後に削除する」というuntrusted Workerの約束で補わない。

# Sybil / Collusionについての現在の結論

EKIは、匿名Participantを無制限に作成できる条件で、

> N個中M個が一致したから正しい

という一般的な完全保証を主張しません。

高いResult Assuranceが必要な場合は、少なくとも次のいずれかを必要とします。

- application-specific verification
- known independent failure domains
- managed / trusted anchor recomputation
- hardware / software attestation with appropriate trust roots
- cryptographic proof

Reputationは複製率を下げる最適化には使えても、この限界そのものを消しません。

# 非決定的AI Taskについての現在の結論

生成AIの文章や候補生成では、同じModel / Inputでも同一の出力にならない場合があります。また、複数Workerが同じ回答を返しても、それだけで事実として正しいとは限りません。

したがって、AI Taskでは次のような情報を分けて扱う必要があります。

- どのModel / Executor / Inputを使ったか
- どのWorker / Environmentで実行したか
- 複数実行が独立していたか
- Application-specific Validatorを通ったか
- EKIとして確認できないsemantic judgementは何か

EKIが確認できない部分を、Result Assurance Profileの高さだけで隠さないことが重要です。

# Contributionとの関係

Contribution accountingでは、次を混同しません。

```text
Resultが正しい
    ≠
申告された量のcomputeを本当に使った
    ≠
そのcomputeが社会的・経済的に価値を持つ
```

Result IntegrityはAxis D、Resource MeasurementはAxis G、価値判断は外部Policyとして分離します。

# 今後具体化が必要な点

- Independence ClaimをPrivacyを壊さず表現する方法
- anonymous participantに対するoperator-level Sybil detectionの限界
- Attestation freshness、replay、Verifier compromise
- multi-component confidential execution（CPU + GPU + I/O）の保証境界
- non-deterministic AI Resultに共通化できるValidation語彙の範囲
- Proof generation costを含むScheduler cost model
- Resource Measurement AssuranceとContribution accountingの結合
- Browser Workerで取得できるEvidenceと、その保証限界
- 複数Validator / Verifierを要求する場合のIndependence表現
