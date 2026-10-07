# EKI Fabric Worker Assurance Model

## この文書の役割

この文書は、上流工程でRequirementを解釈するための文書です。Workerを、単純な`trusted / untrusted`や、一本のTrust Levelで表すことはしません。Taskごとに必要な保証を、複数の観点に分けて考えます。

具体的なProtocol、wire schema、Attestationの製品は、まだ定義しません。

ここで大切なのは、「そのWorkerを信頼できるか」という一つの問いにすべてを押し込めないことです。**何について、どの根拠から、どこまで確認できるか**を、分けて考えます。

## なぜ一つのTrust Levelでは足りないのか

たとえば、次の3つのWorkerを考えます。

- 企業が管理する端末でIdentityは強いが、Confidential Computingには対応していない。
- 匿名の参加者の端末だが、あるTaskについて、暗号学的なProofを生成できる。
- Hardware Attestationには対応しているが、参加の実績がなく、Resultが意味の上で正しいかを検証するValidatorもない。

これらを`L0 < L1 < L2`のように一列に並べると、本来は独立した性質を、互いに代わりになるものとみなしてしまう危険があります。

たとえば、「企業が管理する端末だから、Resultも正しい」「Attestation済みだから、AIの回答の内容も正しい」といった飛躍です。

EKIは、少なくとも次の軸を、独立して扱えることを要求します。

---

## Axis A — Identity Assurance

Workerが「どのIdentityとして参加しているか」「どの管理主体に属しているか」を、どの程度確認できるかを表します。

状態の候補は、たとえば次のとおりです。

- `anonymous`: 永続的なIdentityを要求しない。
- `self-asserted`: 暗号鍵はあるが、外部の主体とのbindingがない。
- `domain-issued`: Trust DomainがIdentityを発行している。
- `managed-device`: 管理されている端末としてのbindingがある。

Identity Assuranceが高くても、Resultが正しいことは意味しません。

「誰が返したか」を確認できることと、「返した内容が正しい」こととを、分けて考えます。

---

## Axis B — Execution Environment Assurance

Workerが主張する実行環境について、どの程度、独立したEvidenceが得られるかを表します。

状態の候補は、たとえば次のとおりです。

- `none`
- `self-reported`
- `software-measured`
- `hardware-attested`

`hardware-attested`であっても、Attestationの対象外のI/O、network、host componentまで、自動的に信頼できるとはみなしません。

また、実行環境が期待どおりであることと、Application上のResultが正しいこととは、別のことです。

---

## Axis C — Confidentiality Capability

Taskのinput、Model、途中のDataなどを、Worker hostのownerや他のProcessから、どの程度守れるかを表します。

状態の候補は、たとえば次のとおりです。

- `public-only`: Workerのownerから見えても問題ないDataだけを扱う。
- `managed-boundary`: 組織の管理、契約、運用などの、明示的な管理の境界に限る。
- `confidential-runtime`: HardwareやRuntimeの保護を使って、host ownerからも秘匿する構成を取れる。

`encrypted-in-transit`は、この軸の十分条件ではありません。

通信中に暗号化されていても、実行時に、通常のhost processから平文を読めるのなら、untrustedなhost ownerに対するConfidentialityは成り立っていないからです。

---

## Axis D — Result Integrity Capability

Workerを無条件に信用せずに、ResultがTaskの要求を満たしていることを、どの方式で確認できるかを表します。

capabilityの候補は、たとえば次のとおりです。

- `none`
- `application-validator`
- `replicable`
- `trusted-recompute-compatible`
- `attested-execution`
- `cryptographic-proof`

これらを、単純な強さの順には並べません。

あるTaskでは、小さなアプリケーション固有のvalidatorが最も確実かもしれません。別のTaskでは、Proofが適切かもしれません。生成AIの文章のように、「唯一の正解」を定義しにくいTaskでは、どの方式を使っても、意味の上での正しさを完全には保証できない場合があります。

---

## Axis E — Independence / Failure Domain

複製して実行したり、複数で検証したりする場合に、それぞれが同じ原因で同時に誤る可能性を、どの程度分離できるかを表します。

独立性を考えるdimensionの候補は、次のとおりです。

- principal
- Trust Domain
- device
- physical host
- operator
- Executorの実装
- hardwareのvendorとarchitecture
- networkとregion
- ValidatorとVerifier
- model lineage（モデルの重み、系統、学習データの由来。生成AIのTaskで、悪意がなくても同じ誤りを再現し得る単位）
- resource provider（実行を担った管理クラスタなどの運営者）

独立性が崩れる原因は、攻撃者が複数のIdentityを作ることだけではありません。同じモデルを同じ環境で3回動かして結果が一致しても、同じ誤りを3回再現しただけかもしれません。モデルの系譜が独立しているかどうかという、意味の判断を、EKIは所有しません（RQ-045）。判断に使える記録として、どのdimensionが同じで、どれが異なっていたかを残せることを求めます。

高い保証が必要なTaskでは、`worker_id`が違うというだけでは、独立しているとはみなしません。

同じ攻撃者が100個のWorker Identityを作れるなら、100台のWorkerによる多数決でも、独立した100の主体が確認したことにはならないからです。

---

## Axis F — Operational Reliability History

過去の、deadlineの遵守率、validationの成功率、availability、errorの率などを表します。

これは、Schedulerの効率を上げるために使えます。

たとえば、長い間正しい結果を返しているWorkerでは、複製の率を下げる。失敗が続くWorkerへの割り当ての量を減らす。こうした最適化が考えられます。

ただし、Operational Historyは、Security Root of Trustではありません。長い間、良好だったWorkerが、後から侵害される可能性は、排除できません。

---

## Axis G — Resource Measurement Assurance

CPUやGPUの時間、Memory、Network、Energyなど、Contributionの算定やResource accountingに使う測定値を、どの程度信頼できるかを表します。

状態の候補は、たとえば次のとおりです。

- `self-reported`
- `control-observed`
- `cross-validated`
- `attested-metering`

Resultが正しいことと、申告された量のcomputeを本当に提供したこととは、別のことです。

たとえば、予測しやすいResultを、実際には計算せずに返しても、Validatorを通ってしまうTaskがあり得ます。そのため、Result IntegrityとResource Measurementを、一つの軸にまとめません。

価値の交換に使うContributionでは、どの測定方式だったかを、Recordに残せる必要があります。

Measurement Assuranceは、測定値の意味を自由に強めるためのTrust Scoreではありません。たとえば、Control Planeが観測できる`lease-wall-time`は、同じ時間の`gpu-active-time`やCPU timeを意味しません。Metric、Unit、Observer、Assuranceを一体にして保持します。そして、「何を観測したか」を、保証の範囲より強い名前に読み替えないことを要求します。

また、Accepted Resultは、Resource Measurementの代わりになるEvidenceではありません。Resultが正しいことと、申告されたResourceの量が正しいこととは、独立して評価します。一方で、Contribution Policyが、self-dealingや意味のないWorkを抑えるために、Accepted Resultを発行の条件にすることは認めます。この場合も、Result Acceptanceが測定の精度を引き上げるわけではありません。別の条件です。

Contribution Recordは、Resource Class、Metric、Unit、Quantity、Measurement Assurance、Observer、Workのprovenanceを保持します。EKI Coreに共通の、Universal Scoreには変換しない方針を優先します。金銭、Credit、社会的な価値への換算は、外部のPolicyの責務です。

---

## Axis H — Verification Authority Assurance

ResultやAttestation Evidenceを評価した側を、どの程度、信頼の基盤として扱うかを表します。

Workerだけを疑っても、ValidatorやVerifierが侵害されていれば、偽のResultをAccepted Resultに昇格できてしまいます。

EKIでは、少なくとも次のことを、追跡できなければなりません。

- どのValidatorやVerifierを使ったか
- どのPolicyで評価したか
- そのVerifierを、誰が信頼の基盤として認めたか
- 高い保証が必要なTaskで、独立した複数の検証を要求したか

すべてのTaskで、複数のVerifierを必須にはしません。

大切なのは、Verifierを暗黙のうちに絶対的な信頼の対象にしないことです。「この保証は、どのVerification Authorityに依存しているか」を、隠さないようにします。

Remote Attestationでは、RFC 9334の用語に従い、Evidenceの評価と、Attestation Resultの利用とを分けます。VerifierはEvidenceにAppraisal Policyを適用して、Attestation Resultを作ります。Relying Partyは、そのResultに、用途ごとの別のPolicyを適用します。したがって、`Verifier accepted`を、そのまま`Task placement authorized`に読み替えません。Verifier Identity、Verifier Policy Revision、Attester / Trust Domain bindingを追跡できることを、Assuranceの上の条件とします。

---

## Axis I — Evidence Freshness / Revocation

Identity、Attestation Result、Lease、Grant、内部の実行権限などが、現在も有効であることを、どの程度確認できるかを表します。

過去に安全だったWorkerが、現在も安全だとは限りません。

そのため、古いEvidenceを無期限に使い回すのではなく、必要な保証の水準に応じて、次のものを扱える必要があります。

- expiry
- revocation
- Evidence freshness
- replay resistance
- keyとcredentialのrotation

nonce、counter、timestamp、clock sourceの具体的な方式は、Architectureで決めます。

---

# Taskの側から要求するAssurance

Taskは、あいまいに「trusted worker」を要求するのではなく、必要な保証を軸ごとに表現できることを、めざします。

たとえば、公開データを使う、検証できるTaskなら、概念の上では、次のように考えられます。

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

一方、機密のTaskなら、次のような要求になるかもしれません。

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

ここでの表現は、Requirementを考えるための概念の例です。将来のwire schemaではありません。

---

# Result Assurance Profiles

Result Assurance Profileは、Resultが正しいことについて、EKIがどの程度の追加の確認を行ったかを説明するための、候補です。

番号が大きいほど、常に優れているという意味ではありません。Taskごとに、適切な方法は異なります。

## RA-0 — Best effort

EKIとしては、Resultが正しいことの追加の検証を行いません。

公開データを使うTask、影響の小さいTask、利用する側が独自に確認できるTaskなどを想定します。

## RA-1 — Application validated

アプリケーション固有のvalidatorによって、不正なResultを検出できる構成です。

Validator自身を、どの程度信頼するかは、Axis Hとして別に扱います。

## RA-2 — Replicated

複数の実行結果を比較します。

比較の関数だけでなく、どの程度のIndependenceを要求するかが必要です。匿名のWorkerの単純な多数決だけでは、Sybilへの耐性を保証しません。

## RA-3 — Anchored recomputation

untrustedなWorkerのResultを、管理下のWorkerや、別の強いFailure Domainで、sampleしたりrecomputeしたりします。必要に応じて、検証の量を調整します。

BOINCのAdaptive Replicationのように、検証のコストとthroughputの釣り合いを取る考え方が、参考になります。

## RA-4 — Attested execution

Remote Attestationなどを使い、要求された実行環境の状態について、VerifierがEvidenceを評価します。

Attestationは、Execution Environment Assuranceを強めます。しかし、「そのApplicationのResultが、意味の上で正しい」ことを、常に保証するわけではありません。

また、Attestation Resultは、Worker全体に恒久的な`trusted=true`を付けるものではありません。Evidenceのfreshness、VerifierのAppraisal Policy、Resultのexpiry、Taskの側が要求するEnvironment Classを、別々に扱います。Raw Evidenceには、Vendor固有の情報や、端末を識別する情報が含まれ得ます。そのため、Relying Partyには、必要に応じて正規化したResultだけを渡し、Evidence Digestなどでprovenanceを保持する構成を認めます。

## RA-5 — Cryptographically verifiable execution

期待するProgramとInputの関係から、Resultが得られたことを、暗号学的なproofで確認します。

Worker Identityへの依存を、大きく減らせる可能性があります。ただし、すべてのTask、特に大規模なGPUやAIのworkloadで実用になるとは、仮定しません。

暗号学的なproofを使う場合は、少なくとも、Program Digest、Input Digest、Output Digest、Task / Attemptとのbindingを、追跡できることを要求します。Proofがvalidであっても、未承認のProgramが正しく実行されたことを証明しているだけかもしれません。そのため、Proofは、Executor / Program Admissionの代わりにはなりません。また、外部への副作用、Resource Measurement、Application上の意味の正しさも、別の保証です。

Proof System固有の検証の結果は、必要に応じて、Verification Authorityが、正規化したVerification Resultに変換します。そのうえで、Relying PartyがTask固有のPolicyを再評価できる構造を、優先します。VerifierがProofを受理したことを、そのままResult Acceptanceに読み替えません。

Verifiable ComputationとZero Knowledgeも、同一視しません。Private Inputやwitnessの秘匿性を要求する場合は、そのPrivacyの性質を、Proof SystemやProfileの検証済みの属性として扱います。Clientの自己申告だけでは強めません。

---

# Confidentiality Profiles

## CA-0 — Public / disposable

Worker ownerに、input、model、途中のデータが見えても問題のないTaskです。

アドホックな参加者に、最も配置しやすいProfileです。

## CA-1 — Managed boundary

組織が管理する端末や、契約上の管理の境界などに限ります。

技術的に、host ownerから秘匿されることまでは、主張しません。

## CA-2 — Confidential execution

Remote Attestationなどで、期待したConfidential Computingの環境を確認します。そのうえで、必要なkeyやmaterialを、その環境にだけreleaseする構成を認めます。

CPU、GPU、I/Oを含む、end-to-endの信頼の境界が成り立っているかは、採用する技術ごとに確認が必要です。

---

# Placementで守るべき原則

1. Schedulerは、性能の値だけでWorkerを選んではならない。
2. Taskが要求する各Assurance Axisを満たさないWorkerを、ReputationやWorkerの速さだけで代替してはならない。
3. `unknown`は、安全な側に評価する。情報が足りないことを、高い保証として扱ってはならない。
4. Assurance Evidenceが、期限切れ、失効、revoke済みの場合に、継続して利用することを、暗黙に許可してはならない。
5. 同じWorkerでも、Executorのversion、CPUやGPUのpathなどに応じて、Reliability Historyを分ける余地を持つ。
6. Providerが、強いAssuranceを自己宣言するだけでは、成り立たない。高い保証のClaimには、要求する水準に応じた外部のEvidence、または明示的なPolicy trustが必要である。
7. Result Verificationに使うValidatorやVerifierのIdentityとPolicyも、必要に応じて、PlacementとAcceptanceの判断に含める。
8. Confidentialityが足りないことを、「Taskの終了後に削除する」というuntrustedなWorkerの約束で補ってはならない。

# Sybil / Collusionについての、現在の結論

EKIは、匿名のParticipantを無制限に作成できる条件で、

> N個のうちM個が一致したから正しい

という一般的な完全な保証を、主張しません。

高いResult Assuranceが必要な場合は、少なくとも、次のいずれかが必要です。

- アプリケーション固有の検証
- 独立したfailure domainが分かっていること
- 管理下の、または信頼できるanchorによる再計算
- 適切なtrust rootを持つ、hardwareまたはsoftwareのattestation
- 暗号学的なproof

Reputationは、複製の率を下げる最適化には使えます。しかし、この限界そのものは消せません。

# 非決定的なAI Taskについての、現在の結論

生成AIの文章や候補の生成では、同じModelと同じInputでも、同じ出力にならない場合があります。また、複数のWorkerが同じ回答を返しても、それだけで事実として正しいとは限りません。

したがって、AI Taskでは、次のような情報を分けて扱う必要があります。

- どのModel、Executor、Inputを使ったか
- どのWorkerとEnvironmentで実行したか
- 複数の実行が独立していたか
- Application固有のValidatorを通ったか
- EKIとして確認できない、意味の上での判断は何か

EKIが確認できない部分を、Result Assurance Profileの高さだけで隠さないことが大切です。

# Contributionとの関係

Contribution accountingでは、次のことを混同しません。

```text
Resultが正しい
    ≠
申告された量のcomputeを本当に使った
    ≠
そのcomputeが社会的・経済的に価値を持つ
```

Result IntegrityはAxis D、Resource MeasurementはAxis Gとして扱います。価値の判断は、外部のPolicyとして分けます。

# 今後、具体化が必要な点

- Privacyを壊さずに、Independence Claimを表現する方法
- 匿名の参加者に対する、operatorの単位でのSybil検出の限界
- Attestationのfreshness、replay、Verifierのcompromise
- 複数のcomponent（CPU、GPU、I/O）にまたがる、confidential executionの保証の境界
- 非決定的なAIのResultについて、共通にできるValidationの語彙の範囲
- Proofを生成するコストを含めた、Schedulerのcost model
- Resource Measurement AssuranceとContribution accountingの結び付け
- Browser Workerで取得できるEvidenceと、その保証の限界
- 複数のValidatorやVerifierを要求する場合の、Independenceの表現
