# 資源提供実績の測定を確かめる（Contribution Measurement Spike）

## このSpikeの役割

このSpikeは、EKI Fabricで資源提供をContributionとして記録するときに、Worker自身の自己申告、Control Plane等が観測した値、Attestation等で裏付けた測定値を同じ強さの事実として扱わず、何を・誰が・どの根拠で測ったかをContribution Recordへ残せることを確認するための最小実証です。

対象は主にRQ-036 Contribution accounting、RQ-037 External value mapping、RQ-038 No native currency or cash market、RQ-039 End-to-end provenance、RQ-051 Control-plane authority separation、RQ-053 Energy / thermal / device-safety aware resource policy、Assurance Model Axis G、QA-11です。

このSpikeは、EKI独自の通貨、ポイント、市場価格、現金化方式を設計するものではありません。

## 中心となる分離

Contributionでは、少なくとも次の三つを混同しません。

~~~text
Resource Measurement
  どの資源が、どれだけ提供されたと観測したか

Result Integrity
  そのWorkのResultをどのように受理・検証したか

External Value
  そのContributionを外部制度がどう評価するか
~~~

正しいResultが返ったことから「GPUを30秒使った」とは証明できません。逆に、GPU active timeを強く測定できても、そのResultが意味的に正しいことや、その30秒に高い社会的価値があることは自動的には導けません。

## Resource Measurement Assurance

今回の最小モデルでは、Resource Measurement Assuranceとして次を扱います。

~~~text
self-reported
control-observed
attested-metering
~~~

これはContribution Policyが必要最低限の測定根拠を表現するための簡易モデルです。attested-meteringであっても、あらゆるMetricについて常に正確という意味ではありません。何を測っているかはMetric / Unitと一体で解釈します。

### self-reported

Worker自身が資源量を申告する方式です。低コストなTelemetryとしては利用できますが、悪意あるWorkerは任意の値を申告できます。

Policyがcontrol-observed以上を要求する場合、Worker署名のself-reported値だけではContribution Recordを発行しません。

### control-observed

Control PlaneやWorker外部の管理境界が直接観測できる値です。

Spikeでは例として次を使います。

~~~text
metric: lease-wall-time
unit: millisecond
observer: control-plane
~~~

Leaseが120秒続いたことは、GPUが120秒activeだったこととは同じではありません。そのため、この値をgpu-active-time、cpu-time、energy等へ読み替えません。

測定できた事実の名前を、その保証範囲より強くしないことを原則にします。

### attested-metering

Attestation等により、より強い測定根拠を持つ場合です。

Spikeでは例としてgpu-active-timeを表現しますが、実GPU counterやTEE meterを実装したわけではありません。ここで確認するのは、Observerごとに主張可能な最大AssuranceをPolicy側で固定できることです。

## Observer IdentityとAssurance Ceiling

Measurement Trustは次をbindingします。

~~~text
ObserverID
Public Key
MaxAssurance
~~~

Contribution Authorityは、Measurement署名、Observer ID、宣言Assurance、PolicyのMinimum Assuranceをそれぞれ確認します。

これにより、WorkerがControl PlaneのObserver IDを名乗って自分の鍵で署名しても拒否できます。またControl Plane observerがpayloadだけattested-meteringへ書き換えても、MaxAssuranceを越えるため拒否します。

## Work Context

Measurementは単なる数量ではなく、次のWork Contextへbindingします。

~~~text
ProviderID
WorkerID
TaskID
AttemptID
ResourceClass
Metric
Quantity
ObserverID
Assurance
ObservedAt
~~~

Contribution Recordから、誰が、どのWorkerで、どのTask Attemptへ、どのResource Classを提供し、どの測定根拠で何を観測したかを追跡できます。

## Result Evidenceは別入力

Contribution Policyによっては「Accepted Resultを生んだWorkだけをContributionとして認める」ことができます。

その場合でも、Result EvidenceはMeasurementとは別入力です。

~~~text
Measurement
  lease-wall-time = 120000 ms
  observer = control-plane

Result Evidence
  task/attempt = same
  accepted = true
  verification authority = validator-A
~~~

Contribution AuthorityはTask ID / Attempt IDの一致を確認しますが、Accepted ResultそのものをResource Measurementへ変換しません。

## Accepted Resultだけでは資源量を証明しない

予測可能なTaskでは、Workerが実際にはほとんど計算せず正しい答えだけ返せる場合があります。

したがって、

~~~text
Result accepted
  ↓
GPUを十分使った
  ↓
Contribution量を確定
~~~

とはしません。

Spikeでは、ResultがAcceptedでもself-reported measurementがPolicyのAssuranceを満たさなければContribution Recordを発行しません。

一方で、無意味なWorkを自作自演してContributionを稼ぐ問題に備え、PolicyはAccepted Resultを別条件として要求できます。Resource Measurement AssuranceとResult Acceptanceは別の保証です。

## 同一Attemptの二重計上

Contribution RecordはProvider / Worker / Task / Attempt / Metricを一意な測定対象として扱います。

同じMeasurement Evidenceを再送した場合は同じContribution Recordへ収束します。一方、同じAttempt / Metricについて後から別quantityを提示した場合は競合として拒否します。

これにより、retryや重複deliveryによって同じWorkのContribution Recordが増殖することを防ぎます。

## Contribution Record

最小Recordには次を保持します。

~~~text
RecordID
ProviderID
WorkerID
TaskID
AttemptID
ResourceClass
Metric
Quantity
MeasurementAssurance
ObserverID
MeasurementDigest
ResultAccepted
VerificationAuthority
PolicyRevision
IssuedAt
ExpiresAt
~~~

Contribution Authorityが署名するため、Recordの後付け改変を検出できます。

## Universal Scoreを作らない

Contribution RecordにはEKI points、credits、yen value、market price、social valueといったUniversal Scoreを入れていません。

GPU active time、CPU lease wall time、Network transfer等を一つの数字へ正規化すると、その換算式自体が価値Policyになります。EKI CoreはそのPolicyを固定しません。

外部主体はContribution Recordを社内配賦、相互利用Credit、公益活動の寄付実績、Community評価等へ接続できますが、その換算方法は各主体へ委ねます。

## 公益的なCompute Grantとの関係

Cloud Providerや大企業が公共性の高い団体へ遊休資源を提供する場合、Contribution Recordは「どれだけの資源が、どのWorkへ、どの測定根拠で提供されたか」を記録する基盤になり得ます。

その記録をESG / CSR報告、公益活動の証跡、相互利用枠等へ使うかどうかは各主体に委ね、EKI Coreは用途を規定しません。

## Local SafetyをContribution Incentiveより上位に置く

Contributionを外部Creditへ接続できるようになると、より多くのCreditを得るためEndpointを限界まで動かす逆インセンティブが生じる可能性があります。

既存のLocal Governor原則を維持し、Contribution獲得を理由にuser opt-out、thermal stop、battery policy、energy budget、resource limitを上書きできない構造を要求します。

## BOINCから得られる示唆

Volunteer Computingの先行例であるBOINCでは、単純なclaimed credit / CPU timeベースの仕組みが不正申告に弱く、Result validationとCredit付与の結合が必要になりました。

EKIではこの知見を参考にしつつ、さらにResultがvalidだったか、Resourceをどれだけ提供したか、そのContributionを何Creditと評価するかを分けます。

特に、valid ResultだからResource量まで正しいとは扱いません。

## Privacy

Contribution RecordはProvider identity、Worker identity、Task / Attempt、Resource usage、活動時間帯などを結び付けるため、Privacy上の情報になり得ます。

Architectureでは、pseudonymous identifier、集計Record、detailed measurementのretention期限、外部価値制度へ渡すFieldの最小化、public contribution reportとinternal audit recordの分離などを比較します。

検証可能性を理由にRaw telemetryを無制限に公開しません。

## Unit Testで確認したこと

2026-09-21時点でGo 1.23.2を使い、go test ./... を実行してTestが通過しています。

確認した主なScenarioは次です。

1. Control-observed measurementから署名済みContribution Recordを作れる。
2. self-reported measurementは、より強いPolicyを満たせない。
3. Workerの鍵でControl Plane Observerを偽装できない。
4. Control observerがpayloadだけでattested assuranceへ昇格できない。
5. Attested meterは、許可されたstronger assuranceを作れる。
6. Accepted ResultだけではResource quantityを証明できない。
7. Contribution PolicyはAccepted Resultを別条件として要求できる。
8. 同じMeasurementはidempotentに扱い、同じAttempt / Metricの競合Measurementを拒否する。
9. RecordはResource Class / Metric / Unitを保持し、Universal Scoreへ変換しない。
10. Result Evidenceは測定したTask / Attemptと一致しなければならない。

## GitHub転記確認

ローカルでTestした三つの実装ファイルについて、GitHub blob SHAとgit hash-objectを比較し、完全一致を確認しています。

~~~text
go.mod
782a520d4f812b5874366c52f0a93f599603abf6

contribution.go
a4cd32ee4a05063ec15d0facc93eed0900280e8b

contribution_test.go
230b50e10ff0d65e5d696b69a552a1cabd43b2da
~~~

## このSpikeで一般化してよいこと

- Measurement ObserverとContribution Authorityを分離できる。
- Observerごとに最大Measurement Assuranceを固定できる。
- Worker self-reportを弱いEvidenceとして保持しつつ、高保証Policyから除外できる。
- Resource MeasurementとResult Acceptanceを別々に要求できる。
- MeasurementをWork Attemptへbindingできる。
- 同一Attempt / Metricのdouble countingを抑制できる。
- Resource Class / Metric / Unit / AssuranceをRecordへ残せる。
- Contribution RecordからUniversal Scoreや通貨を排除できる。
- 外部価値制度が後段で独自換算できる。

## このSpikeで一般化してはいけないこと

まだ実OS / GPU driver Counter、power / energy meter、TPM / TEE meter、Cloud billing meter、cross-validation、multi-observer consensus、Sybil / self-dealing、useless-work farming、Privacy-preserving aggregation、Record correction / revocation、法務・税務・会計上の扱い等は確認していません。

## Architectureへの示唆

Core Contractでは、Measurement、Result Evidence、Contribution Decision、External Value Mappingを別々に表現できる余地を持つ方が自然です。

この分離を維持すれば、アドホックな善意参加から企業の公益Compute Grantまで同じ基盤Recordを使いつつ、価値制度だけを各主体へ委ねられます。

## 参考資料

- BOINC — Credit system
  - https://boinc.berkeley.edu/trac/wiki/CreditGeneralized
- BOINC — A Platform for Volunteer Computing
  - https://boinc.berkeley.edu/boinc_papers/boinc.pdf
