# Task・Attempt・Lease・Recoveryの状態モデル（State Model）

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricでは、Workerがスリープや停止、通信断、ブラウザの破棄、利用者の参加停止、Providerによる回収で離脱することを、通常の状態として扱います。

そのため、次のような事実だけから、

- Workerから応答がない
- Leaseが切れた
- Taskがtimeoutした

> 実行は失敗した
> 副作用は起きなかった
> retryしても安全である

と判断することはできません。

応答がないだけでは、処理が失敗したのか、副作用が起きなかったのか、再実行してよいのかは分かりません。この文書ではTask、Attempt、Lease、Result、副作用、復旧を分け、メッセージの再配送やWorkerの頻繁な離脱があっても、安全性と監査に必要な意味を保つ状態モデルを定めます。

関連する文書は、次のとおりです。

- AUTHORITY_BOUNDARIES.md
- CONTRACT_BOUNDARIES.md
- DEPLOYMENT_PROFILES.md
- ../requirements/PRODUCT_REQUIREMENTS.md RQ-010〜RQ-013 / RQ-052
- ../spikes/browser-worker-lifecycle/README.md
- ../spikes/mass-reclaim-recovery/README.md
- ../spikes/side-effect-uncertain-outcome/README.md

---

# 2. 最重要の原則

## 2.1 TaskとAttemptを分ける

Taskは、Clientのintentです。

Attemptは、そのTaskを一度実行しようとした、試行です。

~~~text
Task T1
  Attempt A1
  Attempt A2
  Attempt A3
~~~

Retryで、Task IDを変えません。

同じAttemptを、別のWorkerに書き換えて再利用することもしません。

## 2.2 Leaseは、実行の結果ではない

Leaseは、

> このAttemptを、このWorkerに、この時間の範囲で割り当てた

という、一時的なAuthorityです。

Leaseのexpiryが意味するのは、

> そのWorkerの実行の権限が、失効した

ということです。

~~~text
Lease expired
  != execution definitely failed
  != external effect did not happen
  != safe to retry
~~~

## 2.3 Worker heartbeatは、Observationである

heartbeatを受信したことは、

> Worker sessionが、最近通信できた

というObservationです。

heartbeatのmessage自体が、Leaseを延長するのではありません。

Lease Authorityが、renewalを永続的に記録して、はじめて有効期限が更新されます。

## 2.4 Workerによる状態の申告は、Claimである

Workerが、

- running
- 80% complete
- failed
- result ready

と報告しても、それをTask Stateの、権威のある事実に直接変換しません。

必要なAuthorityとEvidenceで評価します。

## 2.5 UNKNOWNを保持する

外部への副作用をdispatch済みで、成功か失敗かを確認できない場合は、UNKNOWNを明示します。

failedに丸めません。

cancelledに丸めません。

retryableに丸めません。

---

# 3. 一つの巨大なState Enumを使わない

Task全体を、

~~~text
QUEUED
RUNNING
FAILED
DONE
~~~

だけで表すと、次の状態を表現できません。

- Workerが失われたが、別のAttemptを準備している
- ResultClaimを検証している
- 複数のAttemptを比較している
- cancelを要求済みだが、外部EffectがUNKNOWN
- accepted resultはあるが、他のAttemptを停止している
- retry budgetを使い切り、operatorの介入を待っている

そのため、Baselineでは、少なくとも次の軸を分けます。

~~~text
Task Lifecycle
Attempt Lifecycle
Lease Lifecycle
Result / Verification
External Effect
Cancellation Intent
Recovery / Budget
~~~

Public APIでは、簡略なstatusに投影できます。ただし、内部の正本は分離します。

---

# 4. TaskRecord

TaskRecordは、Task State Authorityが管理する、永続的な正本です。

概念上の、主要なFieldは、次のとおりです。

~~~text
Task ID
TaskIR Digest
Requester Principal
Task Lifecycle Phase
Terminal Outcome if any
Cancellation Intent
Accepted Result ref
External Effect State
Deadline
Retry Budget
Recovery Budget
Fan-out Budget
State Revision
Created At
Updated At
~~~

## 4.1 State Revision

TaskRecordの更新には、単調に増えるState Revisionを持たせます。

複数のControl Plane instanceが、古い状態を上書きしないよう、次のような方法でrevisionを確認できる構造を要求します。

- compare-and-swap
- transaction
- consensusに支えられたstate

具体的なDBの方式は、固定しません。

---

# 5. Task Lifecycle Phase

Taskの内部のLifecycleを、次の大きな分類で扱います。

## T-ADMITTED

Admission済みで、Task IDが存在する。

まだAttemptを実行できる状態にしていない場合を含みます。

## T-READY

新しいAttemptを作成できる状態。

Queueでの待ち、NotBeforeの待ち、Resourceの待ちを含めます。

## T-ACTIVE

少なくとも一つの、closeされていないAttemptが存在し、実行と検証が進行しています。

## T-VERIFYING

ResultClaimとVerificationResultを評価して、AcceptedResultを確定しようとしている状態。

T-ACTIVEと、内部的に重なり得るため、実装では、substateやderived stateでもかまいません。

重要なのは、ExecutionとVerificationを区別することです。

## T-RECONCILING

外部への副作用などについて、成功か失敗かを、Fabricだけでは確定できず、External Effect AuthorityやApplication固有のreconciliationを待っている状態。

## T-HELD

自動的なProgressを止めて、operatorやapplicationの判断を必要とする状態。

たとえば、次の場合です。

- retry budgetを使い切った
- reconciliation budgetを使い切った
- 冪等でないUNKNOWNを、自動では解消できない
- 高い保証のresultが、互いに矛盾している
- policyが変わり、安全に移行する方法がない

## T-TERMINAL

Task lifecycleが終了しています。

Terminal Outcomeを、別のFieldで持ちます。

---

# 6. Terminal Outcome

T-TERMINALでは、Outcomeを明示します。

## COMPLETED

Task Policyに従う、AcceptedResultまたは権威のあるcompletion evidenceが成立した。

## CANCELLED

Cancellation Intentにより、将来の実行を停止した。未解決のexternal effectが残っていない状態。

## FAILED

自動または手動の方針により、Task Intentを満たせないことが確定した。

単なるWorkerが消えたことは、FAILEDを確定する根拠ではありません。

## EXHAUSTED

Retry、Budget、Deadlineなどを使い切り、Task Intentを達成できなかった。

FAILEDのreason categoryとして実装することもできます。ただし、運用の上で区別できる余地を持ちます。

## ABANDONED

結果や外部のEffectを完全には確定できないまま、PolicyやOperatorが、これ以上reconciliationを行わないと判断した。

ABANDONEDは、

> Effectが起きなかった

という意味ではありません。

Auditの上で、UNKNOWNのevidenceを保持します。

---

# 7. Public Task Status

Clientには、内部の状態を、そのまま公開しません。

概念上のPublic Statusは、次のとおりです。

~~~text
queued
running
verifying
reconciling
attention-required
completed
cancelled
failed
~~~

## 7.1 runningの意味

runningは、便利な表示の状態です。

> Workerが、本当にいまCPUの命令を実行している

という、暗号学的な事実ではありません。

有効なAttempt、Lease、最近のObservationから、導きます。

## 7.2 reconciling

外部のEffectのoutcomeが未確定の場合、Clientに、failedと偽らずに、reconcilingやunknown系の状態を返せるようにします。

---

# 8. AttemptRecord

Attemptは、Task State Authorityが生成する、永続的なidentityです。

主なFieldは、次のとおりです。

~~~text
Task ID
TaskIR Digest
Attempt ID
Attempt Number
Previous Attempt ID
Recovery Reason
Created At
Attempt Phase
Current Lease ref
WorkOrder ref
Worker ref if assigned
Checkpoint Input ref
ResultClaim refs
Verification refs
Close Reason
State Revision
~~~

Retry、migration、speculativeな重複、validationのreplicationは、別のAttemptを作ります。

---

# 9. Attempt Phase

Attemptは、次の大きな分類を持ちます。

## A-CREATED

Attempt identityは発行済みだが、Placementが未確定。

## A-PLACING

Scheduler ProposalとPlacement Approvalを取得している。

## A-ASSIGNED

Worker、Lease、WorkOrderが確定した。

Dispatchの前を含みます。

## A-DISPATCHED

WorkOrderの配送を、開始または完了した。

Workerが受信したかどうかを、別のObservationとして持てます。

## A-CLAIMED

ResultClaimや、Executionの失敗のclaimなどを受信した。

Claimの内容を、まだAcceptedな事実にはしません。

## A-VERIFYING

Result Verificationの途中。

## A-CLOSED

このAttemptから、追加のExecutionを期待しない。

Close Reasonを、別に持ちます。

---

# 10. A-RUNNINGを、永続的な事実にしない理由

Worker自身がrunningと報告しても、悪意のあるWorkerは偽れます。

Control Planeが知れるのは、主に、次のObservationです。

- WorkOrderをdispatchした
- Worker sessionが応答した
- Leaseが有効
- progress claimが来た
- telemetryが来た

そのため、runningは、導いた、またはobservationalなstatusとして扱います。Task StateのSecurity Decisionを、runningの申告だけに依存させません。

---

# 11. Attempt Close Reason

A-CLOSEDでは、reasonを区別します。

たとえば、次のものです。

- RESULT_ACCEPTED
- RESULT_REJECTED
- EXECUTION_FAILURE_CONFIRMED
- WORKER_LOST
- LEASE_EXPIRED
- RECLAIMED
- CANCELLED
- SUPERSEDED
- DEADLINE
- BUDGET_EXHAUSTED
- UNKNOWN_EXTERNAL_EFFECT
- POLICY_REVOKED
- STALE_ATTEMPT

Close Reasonは、Task Terminal Outcomeではありません。

A1がWORKER_LOSTでも、Task T1は、A2で継続できます。

---

# 12. LeaseRecord

Leaseは、AttemptとWorkerを、期間を限って結び付けます。

概念上のFieldは、次のとおりです。

~~~text
Lease ID
Task ID
Attempt ID
Worker ID
Lease Epoch / Revision
Issued At
Expires At
Renewal Sequence
Revoked At
Revoke Reason
Lease State
~~~

## 12.1 Lease State

- L-ISSUED
- L-ACTIVE
- L-REVOKED
- L-EXPIRED
- L-RELEASED
- L-SUPERSEDED

## 12.2 Lease IDとRevision

Renewalでは、同じLease identityに、単調に増えるsequenceを持たせるか、新しいLeaseを発行する方式を、選べます。

どちらでも、古いrenewal messageで、期限を巻き戻せない必要があります。

Baselineでは、単調に増えるLease RevisionまたはSequenceを要求します。正確なwireの方式は、後続の実装で決めます。

---

# 13. Lease Fencing

新しいAssignmentが成立した後に、古いWorkerが戻ってきても、Authorityを復活できてはいけません。

AttemptとLeaseには、fencingできるepochを持たせます。

~~~text
Attempt A1
  Lease epoch 4 -> Worker W1

new assignment
  Lease epoch 5 -> Worker W2

late heartbeat from epoch 4
  -> rejected
~~~

古いheartbeat、renewal、progress claimで、現在のassignmentを巻き戻しません。

---

# 14. WorkOrderの配送は、at-least-onceでよい

QueueやNetworkは、重複して配送し得ます。

同じWorkOrderが、Workerに複数回届いても、新しいAttemptを作りません。

Workerは、次のもので、重複を認識します。

- WorkOrder ID
- Attempt ID
- Lease IDとRevision

誠実なWorker Agentは、同じWorkOrderを、二重に実行しないようにします。

悪意のあるWorkerが二重に実行する可能性は、ResultとContributionの側で、別に防ぎます。

---

# 15. Queueは、State Source of Truthではない

内部のQueueやBrokerに、messageがあることを、Task Stateの正本にしません。

正本は、永続的なTask、Attempt、LeaseのDecisionです。

推奨する論理パターンは、次のとおりです。

~~~text
durable state transaction
  create / update Attempt
  record WorkOrder intent
  record outbox event
        ↓
delivery layer
        ↓
Worker
~~~

配送が失敗したり重複したりしても、Attempt identityやAuthority Decisionが、あいまいにならない構造を優先します。

具体的なOutboxの実装は、固定しません。

---

# 16. Commit before dispatch

WorkerにWorkOrderを送る前に、次のものを永続的に記録することを、標準にします。

- Attempt
- Lease
- WorkOrder identity
- 前提となるDecisionの参照

~~~text
send succeeded
but state not committed
~~~

のように、Workerは実行しているのに、Control PlaneがAttemptを知らない状態を、減らします。

完全な分散transactionを要求するのではなく、永続的なintentと、冪等な配送で吸収します。

---

# 17. Heartbeat

heartbeatは、sessionのlivenessのObservationです。

Heartbeatの内容の例は、次のとおりです。

- Worker ID
- session ID
- 有効なLease IDとrevision
- local governorの状態の要約
- 任意の、progressのobservation
- timestampとsequence

## 17.1 Heartbeatがしないこと

heartbeatだけでは、次のことをしません。

- Leaseを延長する
- ResultをAcceptedにする
- Resource Measurementを確定する
- Security Assuranceを昇格する
- Artifact Grantを延長する

各Authorityが、別のDecisionを行います。

---

# 18. Lease Renewal

Lease Renewalは、Lease AuthorityのDecisionです。

入力は、次のとおりです。

- 現在のLease identity
- 現在のrevision
- Worker identityとsession
- Taskがまだactiveであること
- cancellationやrevokeがないこと
- Workerとproviderのcapacity
- policyとdeadline
- 任意の、progress

出力は、次のとおりです。

- 更新したexpiryとrevision
- または、reject

古いrevisionからのrenewは、拒否します。

---

# 19. ClockとExpiry

Lease、Grant、WorkOrderなどは、時間で区切られています。

具体的なClock synchronizationの方式は、固定しません。ただし、次のことを考慮します。

- Control Planeを、権威ある時刻とすること
- 上限のあるclock skew
- Worker側のsafety margin
- localの単調なtimerを使えるか

Workerは、自分の時計を理由に、署名されたexpiryより長く、Authorityを延長しません。

時刻の信頼性が不明な場合は、安全な側に、停止します。

---

# 20. Workerの切断

Control Planeがheartbeatを受け取れなくなっても、すぐにAttemptの失敗とはしません。

標準の流れは、次のとおりです。

~~~text
heartbeat missing
  ↓
Lease renewal stops
  ↓
Lease expires
  ↓
Attempt outcome classification
  ↓
safe retry?
  ├ yes -> Recovery Candidate
  ├ no / unknown effect -> Reconciliation
  └ budget exhausted -> Held / Terminal
~~~

Workerが、ローカルではまだ動いている可能性を考慮します。

WorkOrderとLocal Agentは、expiryで停止することを期待します。ただし、malicious Workerが無視する可能性はあります。

---

# 21. 遅れて戻ってきたWorker

Leaseのexpiryの後に、Workerが復帰しても、古いLeaseをrenewしません。

新しいLeaseが必要です。

Browserのreloadやresumeも、同じです。

古いsessionやepochからのmessageは、staleとして扱います。

---

# 22. 遅れて届いたResult

Leaseのexpiryの後に、古いAttemptから、ResultClaimが届く場合があります。

遅れて届いたResultは、Leaseを復活させません。

Baselineでは、次のように扱います。

1. ResultClaimのprovenanceを記録する。
2. Attemptがstaleであることを明示する。
3. Task Policyが、遅れて届いたresultのverificationを許可するかを確認する。
4. Security上重要なTaskや、side-effectのあるTaskでは、rejectを既定とする。
5. 決定的な純粋computeなどで、Application Validatorが強い場合は、任意で、Verificationに回せる。
6. Acceptedにする場合も、古いLease Authorityを復活させない。

遅れて届いたresultを利用するかどうかは、Result Assurance Policyです。

---

# 23. Speculative / ReplicatedなAttempt

Result Assuranceやdeadlineのために、同じTaskに複数のAttemptを、並行して実行する場合があります。

~~~text
Task T1
  A1 -> Worker W1
  A2 -> Worker W2
~~~

これは、Retryとは区別します。

Attemptの目的を、次のように記録できる余地を持ちます。

- PRIMARY
- RETRY
- SPECULATIVE
- VERIFICATION_REPLICA
- RECOVERY

すべて、同じTask budgetを消費します。

---

# 24. ResultClaimの扱い

ResultClaimを受信したときの処理は、次のとおりです。

1. TaskとAttemptのbindingを確認する。
2. WorkerとWorkOrderのprovenanceを確認する。
3. Output Artifactが確定したことを確認する。
4. AttemptをA-CLAIMEDにする。
5. Result Verificationに送る。
6. 重複したResultClaimは、冪等に処理する。
7. 異なるOutput Digestを持つ重複したClaimは、conflictとして記録する。

WorkerのClaimだけで、Attemptの成功を確定しません。

---

# 25. VerificationとAcceptance

VerificationResultが得られても、Task Policyが満たされるまでは、Taskは完了ではありません。

複数のAttemptやValidatorを使う場合は、次のようになります。

~~~text
A1 claim ─┐
          ├─ Verification ─┐
A2 claim ─┘                ├─ Result Acceptance
                           ▼
                     AcceptedResult
~~~

AcceptedResultが成立した後に、TaskをCOMPLETEDに進めます。

他の、terminalではないAttemptは、stop、revoke、closeできます。

---

# 26. AcceptedResultの後の、他のAttempt

AcceptedResultが成立したら、次のことを行います。

- 新しいAttemptの生成を止める。
- 不要なLeaseをrevokeする。
- WorkOrderの停止を要求する。
- Artifact outputのsessionを止められる。
- 他のAttemptを、SUPERSEDEDとしてcloseする。

ただし、外部への副作用を持つ他のAttemptが、すでにdispatchしている場合は、単にSUPERSEDEDとして忘れてはいけません。Effectのreconciliationを行います。

---

# 27. Cancellation Intent

ClientやOperatorのCancel requestを受理すると、TaskRecordに、Cancellation Intentを永続的に記録します。

Cancellation Intentは、

> 将来の実行を増やさない

ための、強い指示です。

## 27.1 Cancelのときに行うこと

- 新しいAttemptの作成を止める。
- 保留中のPlacementを止める。
- Leaseのrenewを止める。
- 可能であれば、現在のLeaseをrevokeする。
- Workerに、停止を要求する。
- Data Grantのrenewを止める。
- Local Agentが観測できれば、すぐに停止する。

## 27.2 Cancelで消えないもの

- すでに確定した外部のeffect
- すでにAcceptedになったResult
- すでに存在するContributionRecord
- Auditの履歴
- UNKNOWNのeffect

Cancellationは、過去の事実のrollbackではありません。

---

# 28. CancelとUNKNOWN

冪等でない外部のeffectをdispatch済みで、outcomeが不明なときに、Cancelされた場合は、次のようにします。

~~~text
Cancellation Intent = true
External Effect = UNKNOWN
Task Phase = RECONCILING
~~~

すぐにCANCELLEDのterminalにはしません。

Reconciliationの後は、次のように扱います。

- effectが適用されていないと確認できた場合は、CANCELLEDにできる。
- effectが適用されたと確認できた場合は、completionや、effectの後のcancelledなどを、Application Policyで評価する。
- 未解決のまま、operatorが諦める場合は、unknownのevidenceを残して、ABANDONEDにする。

---

# 29. External Effect State

Side-effectのsemanticsを、Task Stateから分離します。

たとえば、次のとおりです。

- E-NONE
- E-NOT-DISPATCHED
- E-DISPATCHED-UNCONFIRMED
- E-CONFIRMED-APPLIED
- E-CONFIRMED-NOT-APPLIED
- E-COMPENSATION-PENDING
- E-COMPENSATED

Compensationは、

> 元のEffectが起きなかった

という意味ではありません。

元のEffectの履歴を保持します。

---

# 30. Pure Compute

effect mode = noneのTaskは、Workerが消えた後に、比較的安全にretryできます。

ただし、次の条件を満たす必要があります。

- retryable flag
- max attempts
- deadline
- compute budget
- network budget
- cancellation

pure computeだからといって、無限にretryしてよいわけではありません。

---

# 31. 冪等でない外部のEffect

Effectをdispatchする前であれば、retryできます。

dispatchした後で、権威のあるReceiptがない場合は、UNKNOWNまたはRECONCILINGです。

~~~text
dispatched
  +
no authoritative confirmation
  =
do not auto retry
~~~

Workerによるfailedの自己申告は、retryのauthorityになりません。

---

# 32. 冪等な外部のEffect

外部のSystemや信頼できるAdapterが、次のものを使って、dedupできる場合は、dispatchの後でも、上限を設けたretryができます。

- 安定したOperation ID
- 安定したRequest Intent Digest

Retry Attemptでも、Operation IDとIntent Digestを変えません。

同じOperation IDに、別のIntentを割り当てたrequestは、rejectします。

---

# 33. External Effect Receipt

External Effect AuthorityのReceiptが、Task Intentに結び付けられる場合、Effect Stateを更新できます。

Receiptは、次のものを持ちます。

- Operation ID
- Request Digest
- Effect ID
- status
- authority
- observed time

Workerの自己署名は、Receiptではありません。

---

# 34. Reconciliation

T-RECONCILINGでは、Taskを、通常のRecovery Queueに入れません。

可能なActionは、次のとおりです。

- status APIをpollする
- 権威のあるreceiptをqueryする
- アプリケーション固有のreconciliation
- 人によるreview
- compensationのworkflow
- deadlineやpolicyにもとづくabandon

Reconciliation自体も、Resource Budgetを持ちます。

無限にpollしません。

---

# 35. Recovery Decision

Attemptが閉じた後、Task State Authorityは、次のことを評価します。

概念上の順序は、次のとおりです。

~~~text
AcceptedResult exists?
  yes -> complete

Cancellation Intent?
  yes -> stop new execution
         unknown effect? -> reconcile
         otherwise -> cancel path

External effect dispatched and unconfirmed?
  yes -> reconcile

Task retryable?
  no -> held / failed

Retry budget available?
  no -> exhausted / held

Deadline allows new attempt?
  no -> failed / exhausted

Recovery rate / transfer budget available?
  no -> Recovery Queue wait

otherwise
  -> create new Attempt
~~~

Workerが失われたことだけで、retryの判断を終えません。

---

# 36. Recovery Candidate

retryできると判定しても、すぐにAttemptを作りません。

Mass reclaimのときの増幅を避けるため、Recovery Candidateを別の状態として持てます。

概念上のFieldは、次のとおりです。

~~~text
Task ID
Previous Attempt
Reason
Priority
Deadline
NotBefore
Estimated Transfer Bytes
Estimated Compute
Retry Budget Remaining
Recovery Batch / Event ID
~~~

---

# 37. BackoffとJitter

同時にWorkerが失われた後の、retryの開始を分散します。

具体的なalgorithmと秒数は、固定しません。

重要なのは、次のものを別に持てることです。

- taskごとのbackoff
- jitter
- global release rate

Backoffだけでは、Network Budgetは保証できません。

---

# 38. Recovery Admission Budget

Recovery Queueから、新しいAttemptをreleaseするときに、次のものを評価します。

- windowごとの、attemptの最大数
- windowごとの、転送byte数の見積もり
- compute capacity
- storeごとの転送のconcurrency
- siteごとのbandwidth
- Taskのpriority
- deadline

Recovery処理自体が、次のresource exhaustionを起こさないようにします。

---

# 39. Mass Reclaim Event

大量のWorkerが同時にreclaimされた場合、共通のRecovery Event IDを付けられる余地を持ちます。

これによって、次のものを観測できます。

- 影響を受けたWorkerの数
- 影響を受けたTaskの数
- releaseしたretryの数
- 転送の増幅
- 回復にかかった時間

これは、Providerのreclaimの権利を待たせるための仕組みではありません。

---

# 40. Reclaim Notice

ProviderやWorkerが、事前のNoticeを送れる場合は、次のことに利用できます。

- 新しいLeaseを止める
- 既存のLeaseをrenewしない
- checkpointを要求する
- 新しいPlacementを避ける
- Recoveryを準備する

Noticeは、best effortです。

突然消えても回復できることが、基本です。

---

# 41. Local Governorによるreclaim

user return、thermal、battery、opt-outによるLocal Governorの停止は、中央のTaskの完了より優先します。

Workerは、可能であれば、Reclaim Reasonを報告します。

しかし、報告できなくても、Leaseのexpiryで回収します。

中央のControl Planeは、

> Taskが重要だから、local stopを無視して続行する

というauthorityを持ちません。

---

# 42. Checkpointからの回復

Checkpointできるtaskでは、reclaimのときに、Checkpointを生成できる場合があります。

新しいAttemptは、検証済みのCheckpoint Artifactを、Inputとして参照できます。

Checkpointがない、壊れている、安全に検証できないといった場合は、元のInputから再実行できることを、優先します。

Checkpointの完成を待つために、userのreclaimを遅らせることはしません。

---

# 43. Browser Worker

Browser Profileでは、短いLeaseを使い、lifecycle eventにFabricのrecoveryを依存させません。

~~~text
visible-only:
  hidden -> local stop
  -> no old lease reuse

freeze / discard:
  event may be absent
  -> heartbeat stops
  -> lease expires
  -> recovery
~~~

Browserのresumeやreloadの後は、新しいLeaseが必要です。

古いepochのmessageは、拒否します。

---

# 44. Multi-tab

Browserのmulti-tabのcoordinationは、まだ研究が必要です。ただし、State Modelの上では、同じWorker Identityから複数のsessionが来ても、session、Lease、epochを区別できる必要があります。

Sessionが衝突しても、古いtabが、現在のLeaseを奪い返さないように、fencingします。

---

# 45. Federationの切断

Federation Taskでは、次の2つのdomainの間の通信が、切れる場合があります。

- home domain
- provider domain

Provider側のローカルなWorkOrderとLeaseが有効であれば、Policyの範囲で、進行中のAttemptを継続できる余地があります。

ただし、新しい外部のauthorityを、offlineで生成することはしません。

Resultのexportができない場合は、Provider側で永続的に保持して、再接続の後にexportできます。

具体的なRetentionは、DeploymentとGrant Policyに従います。

---

# 46. Providerによる、Grantのreclaim

DP-5のCompute Grantが縮小された場合は、次のように進めます。

1. 将来のPlacementを止める。
2. Leaseのrenewを止める。
3. 影響を受けるAttemptを、reclaimとして分類する。
4. best effortでcheckpointする。
5. localで停止する。
6. 安全にretryできるかを評価する。
7. 上限を設けたRecovery Queueに入れる。
8. 別のpoolやproviderに再配置する。

BeneficiaryのTaskのpriorityが、Providerのreclaimを無効にすることはありません。

---

# 47. Deadline

Taskのdeadlineと、Leaseのexpiryを、同一にしません。

Taskのdeadlineは、Task Intentの上の、時間の条件です。

Leaseのexpiryは、Worker Assignment Authorityの有効期限です。

Deadlineに到達したときに、次のどれを選ぶかは、Task ContractとDeadlineのsemanticsで決めます。

- 新しいAttemptを止める
- 既存のAttemptをrevokeする
- 現在のAttemptの完了を待つ

Baselineでは、少なくとも、deadlineを越えて、無制限に新しいAttemptを生成しないことを、要求します。

---

# 48. Max Attempts

Attempt Budgetは、Taskの単位です。

Speculative、verification、retryを含めて、どのAttemptがBudgetを消費するかを、Policyで明示します。

Clientが、retryのrequestを繰り返すことで、Budgetをresetすることはできません。

---

# 49. Fan-out / Child Task

Taskが子Taskを生成できる拡張を導入する場合、子Taskは、親のBudgetを消費します。

必要な制約は、次のとおりです。

- 最大の深さ
- 最大の子の数
- 残りのcompute budget
- network budget
- deadlineの継承

Task graphのcycleや、分散したfork bombを防ぎます。

具体的なGraph Contractは、Extension SPI以降で扱います。

---

# 50. Quota Reservation

Admissionのときに、見積もった最大のcostを予約します。そして、Attemptの実行、retry、output、転送で消費します。

Attemptが失敗したときは、未使用の分を、refundできます。

ただし、実際に消費したResourceは、失敗でも、消費として扱います。

Retryによって、新しい無制限のQuotaを得ることはありません。

---

# 51. Task StateとQueue State

TaskがT-READYでも、Queueにmessageがない瞬間は、あり得ます。

Queueにmessageがあっても、Taskがcancelledやstaleであれば、実行してはいけません。

Workerへ配送する前に、現在のTask、Attempt、Leaseのstateを確認します。

Queueは、delivery mechanismであり、authorityではありません。

---

# 52. Task StateとScheduler

Schedulerは、Task stateを読んで、Proposalを作ります。

Schedulerが、Task stateを書き換えて、retryやcompletionを確定することはありません。

Schedulerがcrashしても、Task lifecycleを破損させない構造を選びます。

---

# 53. Task StateとResult Acceptance

Result Acceptance Authorityが、AcceptedResultを発行した後、Task State Authorityが、TaskRecordをCOMPLETEDに遷移させます。

AcceptanceとTask stateの書き込みを、同じtransactionにできない場合は、次の2つで、最終的に収束します。

- 永続的なAcceptedResult
- 冪等なcompletionの遷移

AcceptedResultが存在するのに、retryを増やさないためのguardが必要です。

---

# 54. Task StateとContribution

ContributionRecordは、Task completionに必須ではありません。

AcceptedResultが成立した後、またはAttemptが終了した後に、非同期で生成できます。

Contributionのpipelineが失敗しても、Taskを再実行しません。

Contribution accountingを、executionの正しさの経路から、分離します。

---

# 55. Control Planeの再起動後の、状態の回復

DP-1の単一のControl Planeが再起動しても、永続的なstoreから、次のものを復元できる必要があります。

- Task phase
- terminalではないAttempt
- 有効なLeaseとexpiry
- WorkOrder identity
- Cancellation Intent
- External EffectのUNKNOWN
- Recovery Candidate
- AcceptedResultの参照
- Budgetの消費

Processのmemoryだけに、重要な状態を置きません。

---

# 56. 起動時のreconciliation

Control Planeの起動時に、次のことを行える構造にします。

1. 永続的なstateを読み込む。
2. 期限切れのLeaseを分類する。
3. 有効なWorker sessionの再接続を待つ。
4. WorkOrderの配送のoutboxをreplayする。
5. AcceptedResultとTask stateの整合を取る。
6. UNKNOWNのEffectを、Reconciliationに回す。
7. Recovery budgetを再構築する。

起動したからといって、terminalではないすべてのTaskを、retryしません。

---

# 57. Split-brainの回避

DP-2で、複数のControl Plane instanceを動かす場合、一つのTaskやAttemptについて、互いに矛盾するLeaseやState Transitionを、発行しない必要があります。

必要なPropertyは、次のとおりです。

- 権威のある永続的なstate
- state revisionのfencing
- Lease epoch
- 一意なAttempt identity
- 冪等な遷移
- signerのpolicy

具体的なconsensus、database lock、transactionの方式は、実装の比較に残します。

---

# 58. 冪等なstateの遷移

同じEventが二度処理されても、次のことが起きないように、Transition IDやDecision IDを利用します。

- Attemptを二つ作る
- retry budgetを二回消費する
- AcceptedResultを二重に記録する
- Contributionを二重に発行する

---

# 59. Event logとaudit

State transitionは、少なくとも、次のものに関連付けられる必要があります。

- 前のstate revision
- transitionの種類
- actorとauthority
- 理由
- TaskとAttempt
- 関連するLease、Result、Receipt
- policy revision
- timestamp

Event sourcingは、必須にはしません。

現在のstateと、追記専用のauditでも、Requirementを満たせます。

---

# 60. Stateの保持

Taskがterminalになった後も、Audit、Result、Contribution、side-effectのprovenanceに必要な期間、TaskとAttemptのidentityを保持します。

ClientがTaskを削除しても、SecurityやAccountingの上で必要なAuditの履歴まで、すぐに消去するとは限りません。

Retentionとprivacyのpolicyに従います。

---

# 61. Retryの理由の分類

運用で説明できるように、retryや新しいAttemptの理由を、分類します。

たとえば、次のものです。

- WORKER_LOST
- USER_RECLAIM
- PROVIDER_RECLAIM
- THERMAL_RECLAIM
- LEASE_EXPIRED
- EXECUTION_FAILURE
- VERIFICATION_REJECTED
- DEADLINE_SPECULATION
- POLICY_MIGRATION
- MANUAL_RETRY

Reasonは、Security Authorityではありません。ただし、Auditと、Recoveryのtuningに使います。

---

# 62. Holdの理由の分類

T-HELDの理由の例は、次のとおりです。

- RETRY_BUDGET_EXHAUSTED
- UNKNOWN_EFFECT
- CONFLICTING_RESULTS
- NO_ELIGIBLE_WORKER
- DATA_GRANT_UNAVAILABLE
- POLICY_REVOKED
- DEADLINE_EXCEEDED
- MANUAL_REVIEW_REQUIRED

Client向けのmessageは、情報が漏えいしない範囲に、縮約します。

---

# 63. 失敗の分類

Attemptの失敗と、Taskの失敗を、分けます。

## Attemptのローカルな失敗

次のものは、通常、Attemptの単位です。

- processの終了
- sandboxのlimit
- Workerの喪失
- Artifactのfetchのerror
- outputのlimit

## Taskのsemanticな失敗

Application Validatorが、

> このInputでは、業務上、処理できない

と、権威のある形で判定する場合は、Task-levelの失敗に進める可能性があります。

Workerによる失敗の自己申告だけで、Taskをterminalにしません。

---

# 64. Retryの安全のmatrix

概略は、次のとおりです。

| Task / Effect | Workerが失われた後 |
| --- | --- |
| pure compute + retryable | 上限を設けたretryが可能 |
| pure compute + not retryable | holdまたはfail |
| 冪等でないeffect、dispatch前 | 上限を設けたretryが可能 |
| 冪等でないeffect、dispatch済みで未確認 | reconcile / UNKNOWN |
| 冪等なeffect + 安定したoperation identity | 上限を設けたretryが可能 |
| 権威のあるeffectが、適用済みと確認された | completion policyへ |
| cancellation + unknown effectなし | cancelの経路 |
| cancellation + unknown effectあり | reconcile |

---

# 65. 遅れて届くmessageのmatrix

| 遅れて届いたmessage | 扱い |
| --- | --- |
| 古いheartbeat | leaseを復活できない |
| 古いlease renewal | reject |
| 古いWorkOrder ack | auditのみ、stale |
| 遅れて届いたResultClaim | 任意のverification、leaseを復活できない |
| 遅れて届いたCheckpoint | validationの後にのみ、再利用の候補 |
| 遅れて届いたEffect Receipt | 権威のあるものなら、reconciliationを更新できる |
| 遅れて届いたcancellation ack | state authorityではない |

Effect Receiptは、外部の世界の事実を、あとから確定できます。そのため、TaskがT-HELDでも、受理する余地があります。

---

# 66. Reconciliation budget

UNKNOWNを、無期限にpollし続けないために、次のものを設定できます。

- queryの最大回数
- 最大のwall time
- APIのrate
- 金銭的なcost
- operatorのdeadline

Budgetを使い切った後は、T-HELDまたはABANDONEDに移ります。

---

# 67. 手動の介入

HumanやApplication Operatorが行えるActionの例は、次のとおりです。

- retryを許可する
- abandonする
- evidenceを添えて、external effectをreconciledにする
- compensationを起動する
- deadlineを延長する
- 別の認可の範囲で、budgetを増やす

Operatorのactionも、Auditします。Client userが、自分でPolicyの上限を、無制限に増やせるものにはしません。

---

# 68. Compensation

Compensationは、新しいExternal Effectです。

元のeffectを消したことには、なりません。

~~~text
Effect E1 applied
Compensation E2 applied
~~~

として、両方を記録します。

Task outcomeの上で、どう解釈するかは、Application Policyです。

---

# 69. Verification replicaのlifecycle

Result Assuranceのための、replicaのAttemptも、通常のAttemptです。

Workerの数による多数決ではなく、Independence requirementを、別に評価します。

accepted resultが成立した後、不要なreplicaは、stopできます。

---

# 70. Speculative execution

Deadlineへの対策として、遅いAttemptと並行して、speculativeなAttemptを作る場合は、次の条件を満たす必要があります。

- 追加のcompute budget
- network budget
- 並行するattemptの最大数
- resultのdedup
- side-effectがないこと

外部への副作用を持つTaskを、無条件にspeculativeに重複させません。

---

# 71. CheckpointableとCheckpoint-required

Checkpointableが意味するのは、

> checkpointを作れる可能性がある

ということです。

reclaimのときに、必ずcheckpointが成功するとは限りません。

Task Recoveryは、checkpointがなくても安全に動けるか、明示的にrequired profileとして扱います。

---

# 72. Worker sessionとWorker identity

Worker Identityが同じでも、sessionは変わります。

再起動やbrowserのreloadの後は、新しいsessionを作ります。

古いsessionのLease、nonce、heartbeatを、現在のsessionに持ち越しません。

Worker Identityの長い寿命と、sessionのauthorityを、分離します。

---

# 73. Workerの登録解除

WorkerをAdministrativeにdisableやrevokeした場合は、次のことを行います。

- 新しいPlacementを禁止する。
- Leaseのrenewを止める。
- 有効なLeaseをrevokeする。
- 可能であれば、Data Grantをrevokeする。
- 有効なTaskをrecoveryする。

Workerがofflineでも、revocationは、先にControl Plane stateに反映します。

---

# 74. Executorのrevocation

Executor Approvalがrevokeされた場合、Task Stateは、影響を受けるAttemptを評価します。

リスクの高いrevocationでは、有効なWorkOrderをrevokeしたり、stopしたりできます。

すでに生成されたResultを、受理してよいかは、revocationの理由とResult Policyに依存します。

単純に、すべての履歴を消しません。

---

# 75. Policy revision

Policyの更新によって、現在のAttemptが、新しいPolicyを満たさなくなった場合は、次のいずれかを、Policy Change Ruleで明示します。

- grandfather
- stopしてmigrate
- revalidate

古いPolicy RevisionのDecisionを、無条件に現在のものとして扱いません。

---

# 76. Resource budgetの突き合わせ

Attemptが終了したときに、予約したbudgetと、実際の使用量を突き合わせます。

次のものを、Task budgetに戻します。

- 未使用の予約のrefund
- 実際の使用量のcharge
- retry budgetの消費
- outputと転送の使用量

悪意のあるWorkerの自己申告だけで、実際のresource usageを確定しません。

---

# 77. Taskのpriority

Priorityは、Recoveryの順序に利用できます。ただし、Security、Local Governor、Budgetを上書きしません。

priorityが高くても、次のことはできません。

- confidentialなdataを、public Workerに置く
- Providerのreclaimを拒否する
- thermal stopを無視する
- retry budgetを無限にする

---

# 78. Recoveryの公平性

大量のRecoveryで、一つのTenantやBeneficiaryが、すべてのcapacityを占有しないように、Principal、Grant、priority classごとのfairnessを適用できる余地を持ちます。

具体的なqueueのalgorithmは、固定しません。

---

# 79. State ModelとDeployment Profile

## DP-1 Small

単一の永続的なstate storeで、revisionを守る。

## DP-2 Enterprise

複数のControl instanceでも、fencingとtransactionを維持する。

## DP-3 Browser

短いLease、高いchurn、大量のstaleなsessionに耐える。

## DP-4 Federation

domain-localなAttemptとLeaseのauthorityを維持する。remoteの切断を、Taskの失敗に直結させない。

## DP-5 Grant

mass reclaimを、Recovery Eventとして扱う。

## DP-6 Dedicated

Leaseを長めにできても、同じstateのsemanticsを維持する。

---

# 80. State Model Invariants

1. TaskとAttemptを、同一のIdentityにしない。
2. Retryは、新しいAttemptを作る。
3. Queueの配送のretryで、新しいAttemptを作らない。
4. Leaseのexpiryを、Taskの失敗と同一視しない。
5. Leaseのexpiryを、external effectの失敗と同一視しない。
6. heartbeatを、Lease Authorityにしない。
7. Workerのrunningの申告を、永続的な事実にしない。
8. 古いLeaseやepochで、現在のauthorityを復活させない。
9. WorkOrderをdispatchする前に、永続的なexecution intentを記録する。
10. Queueを、source of truthにしない。
11. Workerが消えたことだけで、retryを決めない。
12. dispatch済みの、冪等でないeffectのUNKNOWNを、自動でretryしない。
13. Cancellationで、UNKNOWNを消さない。
14. AcceptedResultが成立した後に、不要なAttemptを増やさない。
15. 遅れて届いたResultで、期限切れのLeaseを復活させない。
16. speculativeやreplicaのAttemptも、Budgetを消費する。
17. mass reclaimのrecoveryに、releaseと転送のbudgetを適用する。
18. Local Governorのreclaimを、Taskのcompletionより優先する。
19. Control Planeを再起動しても、UNKNOWN、Lease、Attemptの履歴を失わない。
20. State transitionを、冪等に処理できる。
21. state revisionとfencingで、staleなwriterを拒否する。
22. Taskのpriorityで、Security、Safety、Budgetを迂回しない。
23. Contributionの失敗を、Taskのretryの理由にしない。
24. Workerの自己申告だけで、Taskのterminal outcomeを確定しない。
25. ABANDONEDのUNKNOWNを、failed-not-appliedと誤って表現しない。

---

# 81. 次のWork Unitへの入力

次は、Extension SPIを定義します。

State Modelから引き継ぐ、Extension Pointの候補は、次のとおりです。

- Queue / Delivery
- State Store
- Scheduler Policy
- Artifact Store
- Identity
- Executor Runtime
- Result Verification
- Attestation
- External Effect Reconciliation
- Contribution Measurement
- Telemetry

SPIは、CoreのStateとAuthorityのsemanticsを、差し替えるものではありません。

Adapterが変わっても、次のものを維持することを、条件にします。

- TaskとAttemptのidentity
- Leaseのfencing
- UNKNOWNのeffect
- ResultClaimとAcceptedResult
- ArtifactGrant
- Authorityの分離
