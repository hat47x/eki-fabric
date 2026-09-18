# Side-effect Task Uncertain Outcome Spike

## このSpikeの役割

このSpikeは、EKI Fabricで外部副作用を伴うTaskを扱うとき、Workerとの通信断やLease失効を理由に無条件retryしてしまうと、通知・作成・更新などを二重実行し得る問題を小さく実証します。

対象は主に次です。

- RQ-011 Failure is normal
- RQ-012 At-least-once execution is explicit
- RQ-017 Retry / fan-out budget
- RQ-039 End-to-end provenance
- RQ-046 External side effects require explicit retry semantics
- PAS-20 副作用Taskの実行済み状態が不明になる
- QA-06 Recoverability under churn

このSpikeは、EKI全体へgenericなexactly-once executionを実装するものではありません。

## 問題の中心

純粋な計算Taskでは、WorkerがResultを返す前に消えた場合、

> もう一度計算する

ことで回復できる場合が多くあります。

しかし、外部世界へ副作用を起こすTaskでは事情が異なります。

たとえばWorkerが外部APIへ「通知を送る」requestを送信した直後に通信が切れた場合、Control Planeからは次のどちらかを判別できないことがあります。

~~~text
A. 外部APIへrequestが届く前に失敗した
B. 外部APIでは成功したが、その後Workerが消えた
~~~

Bで同じ操作をもう一度実行すると、通知が二重送信される可能性があります。

したがって、

~~~text
Worker disappeared
        ↓
Task failed
        ↓
retry
~~~

という単純な状態遷移は成立しません。

## UNKNOWNを正式な状態として扱う

このSpikeではRecovery Stateを次のように分けています。

~~~text
retryable
complete
unknown
retry-budget-exhausted
~~~

### retryable

外部副作用が起きていないことを判断できる、または同じOperation Identityによる安全なretryが成立する状態です。

### complete

外部側の権威あるReceiptによって、副作用が成立したことを確認できた状態です。

### unknown

副作用を送出した後に確認不能となり、

> すでに成功している可能性も、失敗している可能性もある

状態です。

これは単なるfailedではありません。

### retry-budget-exhausted

idempotentな外部操作であっても、無限retryは許可しません。

Resource Budget上のretry上限は別に適用します。

## 三つのEffect Mode

最小モデルではTaskを次の三つに分けます。

### EffectNone

外部副作用を持たないTaskです。

Workerが消えても、retry budgetの範囲で再実行できます。

### EffectIdempotent

外部System側に、同じOperation Identityでretryしても副作用が一度だけになる根拠があるTaskです。

例として、

~~~text
OperationID = operation-123
RequestDigest = sha256:...
~~~

を固定し、retryでも同じ組を使います。

### EffectNonIdempotent

同じrequestを再送すると、副作用が二重に発生する可能性があるTaskです。

副作用送出後にReceiptを確認できずWorkerが消えた場合、自動retryせずUNKNOWNへ移します。

## Idempotencyは「同じrequestを何度送ってもよい」という意味ではない

重要なのは、retry時に同じOperation Identityだけでなく、**同じ意図**を維持することです。

このSpikeでは、

~~~text
OperationID
RequestDigest
~~~

を組として扱います。

同じOperationIDへ異なるRequestDigestを割り当てることを拒否します。

これは、

~~~text
operation-123
  1回目: 100円を送る
  2回目: 1000円を送る
~~~

のように、同じidempotency keyを別の操作へ再利用することを防ぐためです。

AWSのidempotent API設計でも、caller-provided request identifierを使い、同じidentifierで異なるparameterが来た場合を別扱いにする考え方が示されています。

## Idempotencyの成立主体

EKI側でOperationIDを付けただけでは、外部副作用はidempotentになりません。

外部Systemまたは信頼できるAdapterが、

- 同じOperationID
- 同じRequestDigest

を認識し、重複requestを同じ操作として扱う必要があります。

このSpikeの`EffectService`は、その性質を小さく模擬しています。

同じOperationID / RequestDigestで二度呼び出しても、

- 実際のapplication countは1
- 同じEffectIDを持つReceiptを返す

ことをTestしています。

## Workerの「成功しました」は権威あるReceiptではない

悪意あるWorkerを前提とするEKIでは、Worker自身の自己申告だけで外部副作用を確定できません。

Spikeでは、外部Effect Authorityが署名したReceiptを使います。

Receiptは次へbindingされます。

~~~text
OperationID
RequestDigest
EffectID
AppliedAt
~~~

Workerが自分の鍵で偽Receiptを作っても、`ReceiptGuard`はPinned External Effect Authorityの鍵で検証するため拒否します。

実際のArchitectureでは、このAuthorityは次のような形になり得ます。

- 外部API自身のstatus / receipt API
- EKI管理下のeffect adapter
- DB側のtransaction / operation record
- message broker側のdelivery / deduplication record
- application-specific reconciliation provider

具体的方式は外部Systemごとに異なります。

## ReceiptもTask Intentへbindingする

署名が正しくても、別TaskのReceiptを流用できてはいけません。

そのため、

~~~text
Receipt.OperationID == Contract.OperationID
Receipt.RequestDigest == Contract.RequestDigest
~~~

を確認します。

これはExecutor AdmissionでSubject DigestをArtifactへbindingした考え方と同型です。

## Disconnect時のRecovery

Spikeの基本判定は次です。

~~~text
confirmed?
  yes → complete

retry budget exhausted?
  yes → retry-budget-exhausted

effect not dispatched?
  yes → retryable

pure compute?
  yes → retryable

idempotent external effect?
  yes → retryable
         ただし同じOperationID / RequestDigestのみ

non-idempotent external effect dispatched?
  yes → unknown
~~~

この順序により、Worker disappearanceそのものをretry authorityにはしません。

## CancellationでUNKNOWNを消さない

Taskをcancelした場合でも、すでに外部副作用を送出していたなら、

> cancelしたから副作用も起きなかった

とは言えません。

このSpikeでは、non-idempotentな副作用をdispatch済みで未確認の場合、

~~~text
cancelled-but-effect-outcome-unknown
~~~

を返します。

Cancellationは将来の追加実行を止める操作であり、過去の外部副作用を巻き戻す証明ではありません。

## Timeoutも同じ問題を持つ

Timeoutは、

> 外部操作が失敗した

ことを意味しません。

意味するのは多くの場合、

> 一定時間内に結果を確認できなかった

だけです。

そのため、timeout / disconnect / lease expiryを、外部副作用のfailure receiptとして扱いません。

## Unit Testで確認したこと

2026-09-18時点でGo 1.23を使い、

~~~bash
go test ./...
~~~

を実行し、Testは通過しています。

確認した主なScenarioは次です。

1. Pure computeのdisconnectはretryable
2. non-idempotent Taskでもeffect dispatch前ならretryable
3. non-idempotent Taskがdispatch後に切断するとUNKNOWN
4. idempotent Taskはdispatch後でも同じOperation Identityでretry可能
5. retry時にOperationIDを変更できない
6. 同じOperationIDを異なるRequestDigestへ再利用できない
7. 外部idempotent serviceは重複requestでも副作用を一度だけ適用する
8. Workerが偽造したReceiptを外部副作用の確定根拠にできない
9. pinned external authorityのReceiptでTaskをcompleteへ移せる
10. cancellationによってUNKNOWNが消えない
11. idempotent Taskでもretry budgetは適用される

## Generic exactly-onceを約束しない

EKIはTask executionについてat-least-onceを基本にできますが、外部世界の副作用まで含めてgenericなexactly-onceを保証することはできません。

たとえば、

~~~text
Worker
  ↓
External API
  ↓ side effect committed
Worker crashes
  ↓
Control Plane does not receive confirmation
~~~

という境界では、外部System側のdeduplicationやreconciliationなしに、EKIだけで事実を復元できない場合があります。

そのためEKIが持つべきなのは「exactly-once」という強い標語より、

- effect semantics
- stable operation identity
- intent digest
- authoritative confirmation
- explicit UNKNOWN state
- bounded retry

を表現できる契約です。

## Task Contractへの示唆

Architecture段階では、Side-effect Taskについて少なくとも次の情報を表現できる余地が必要です。

~~~text
effect mode
  none
  idempotent external
  non-idempotent external

operation identity
request intent digest
max attempts
confirmation / reconciliation provider
unknown outcome handling
~~~

ただし、すべてをexternal Task APIへそのまま露出するとは限りません。

Application AdapterやExecutor定義側で固定する方が安全な情報もあります。

## Application-specific semanticsをCoreへ入れない

EKI Coreは、

- 決済とは何か
- メール送信とは何か
- SQL transactionとは何か
- GitHub Issue作成とは何か

を理解する必要はありません。

Coreが理解するのは、

> このTaskには外部副作用があり、そのretry安全性とconfirmation根拠がどのProfileか

という一般契約までです。

具体的なreconciliationはAdapter / Executor / Application側へ委ねます。

## 外部Systemがidempotencyを提供しない場合

外部Systemがdeduplicationを提供せず、結果照会もできない場合、non-idempotent TaskのUNKNOWNを機械的に解消できない可能性があります。

その場合は、

- 人間による確認
- 上位Applicationによる照合
- compensation action
- manual retry
- deadline後のabandon

などへ遷移する必要があります。

EKIが事実を推測して`failed`や`complete`へ変換してはいけません。

## Compensationはrollbackではない

将来Saga的なcompensationを扱う場合も、

~~~text
original effect did not happen
~~~

と、

~~~text
original effect happened後に逆向きのeffectを追加した
~~~

は別です。

Audit / provenance上も区別する必要があります。

## Mass Reclaimとの接続

前のMass Reclaim Spikeでは、大量離脱後のretryをrate / transfer budgetで抑制しました。

Side-effect Taskでは、その前にもう一段あります。

~~~text
worker lost
  ↓
retry semantics evaluation
  ├ retryable → Recovery Queue
  ├ complete  → Done
  ├ unknown   → Reconciliation
  └ exhausted → Hold
~~~

UNKNOWN TaskをRecovery Queueへ入れてしまうと、rate limitがあっても二重副作用を発生させる可能性があります。

したがって、**bounded retryとsafe retryabilityは別の問題**です。

## Security上の意味

悪意あるWorkerが、

> 副作用は失敗したのでretryしてください

と報告しても、その自己申告だけで自動retryしない構造が必要です。

逆に、

> 成功しました

という自己申告だけでもcompleteへしません。

外部世界に関する事実は、その事実を観測できるAuthorityへ結び付けます。

## このSpikeで一般化してよいこと

- 外部副作用TaskにUNKNOWN stateを持てる
- retryabilityをWorker disappearanceだけから判断しない構造を作れる
- idempotent retryではOperationIDとRequestDigestを固定できる
- external authoritative ReceiptをTask Intentへbindingできる
- Worker自己申告と外部effect confirmationを分離できる
- cancellation後もUNKNOWNを保持できる
- idempotencyがあってもretry budgetを別に適用できる

## このSpikeで一般化してはいけないこと

まだ次は確認していません。

- 実際のCloud API / SaaS APIとのintegration
- DB transaction
- message broker
- payment API
- email / notification provider
- compensation / Saga
- reconciliation polling cost
- receipt revocation
- receipt freshness
- External Effect Authorityのpartial compromise
- long-running operation
- effect adapterのexactly-once persistence
- Control Plane restart中のUNKNOWN復元
- parent / child Task graph内のside effect

## 次の検証候補

このSpikeを踏まえると、次は二つの方向があります。

### TEE Remote Attestation abstraction

Executor Admissionとは別に、

> どのExecution EnvironmentならこのTaskを置いてよいか

をEvidence / Verifier / Relying Partyへ分けて検証できます。

### Contribution measurement

Worker自己申告と、Control Plane / external authorityが確認できる事実を分けるという今回の構造は、Contribution measurementにもそのままつながります。

## 参考資料

- AWS Builders' Library — Making retries safe with idempotent APIs
  - https://aws.amazon.com/builders-library/making-retries-safe-with-idempotent-APIs/
- Google Cloud Spanner — Handling unknown commit status
  - https://docs.cloud.google.com/spanner/docs/queues/queues-at-most-once
