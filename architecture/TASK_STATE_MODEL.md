# Task / Attempt / Lease / Recovery State Model

Status: **Baseline 0.1 — 2026-09-21**

## 1. この文書の目的

EKI Fabricでは、Workerがsleep、shutdown、network loss、Browser discard、user opt-out、Provider reclaim等によって頻繁に消えることを通常状態として扱います。

そのため、

- Workerから応答がない
- Leaseが切れた
- Taskがtimeoutした

という事実だけから、

> 実行は失敗した
> 副作用は起きなかった
> retryして安全である

とは判断できません。

この文書では、Task、Attempt、Lease、Result、External Effect、Recoveryを分離し、at-least-once deliveryやWorker churnの中でもSecurity / Safety / Audit上の意味を失わない状態モデルを定義します。

関連:

- AUTHORITY_BOUNDARIES.md
- CONTRACT_BOUNDARIES.md
- DEPLOYMENT_PROFILES.md
- ../requirements/PRODUCT_REQUIREMENTS.md RQ-010〜RQ-013 / RQ-052
- ../spikes/browser-worker-lifecycle/README.md
- ../spikes/mass-reclaim-recovery/README.md
- ../spikes/side-effect-uncertain-outcome/README.md

---

# 2. 最重要原則

## 2.1 TaskとAttemptを分ける

TaskはClient Intentです。

Attemptは、そのTaskを一度実行しようとした試行です。

~~~text
Task T1
  Attempt A1
  Attempt A2
  Attempt A3
~~~

RetryでTask IDを変えません。

同じAttemptを別Workerへ書き換えて再利用もしません。

## 2.2 Leaseは実行結果ではない

Leaseは、

> このAttemptをこのWorkerへ、この時間範囲で割り当てた

という一時Authorityです。

Lease expiryは、

> そのWorkerの実行権限が失効した

ことを意味します。

~~~text
Lease expired
  != execution definitely failed
  != external effect did not happen
  != safe to retry
~~~

です。

## 2.3 Worker heartbeatはObservation

heartbeatを受信したことは、

> Worker sessionが最近通信できた

というObservationです。

heartbeat message自体がLeaseを延長するのではありません。

Lease Authorityがrenewalをdurableに記録して初めて有効期限が更新されます。

## 2.4 Worker状態申告はClaim

Workerが、

- running
- 80% complete
- failed
- result ready

と報告しても、それをTask Stateの権威ある事実へ直接変換しません。

必要なAuthority / Evidenceで評価します。

## 2.5 UNKNOWNを保持する

外部副作用がdispatch済みで、成功・失敗を確認できない場合はUNKNOWNを明示します。

failedへ丸めません。

cancelledへ丸めません。

retryableへ丸めません。

---

# 3. 一つの巨大State Enumを使わない

Task全体を、

~~~text
QUEUED
RUNNING
FAILED
DONE
~~~

だけで表すと、次の状態を表現できません。

- Worker lostだが別Attemptを準備中
- ResultClaimを検証中
- 複数Attemptを比較中
- cancel要求済みだが外部Effect UNKNOWN
- accepted resultはあるが他Attemptを停止中
- retry budgetを使い切りoperator intervention待ち

そのためBaselineでは、少なくとも次の軸を分けます。

~~~text
Task Lifecycle
Attempt Lifecycle
Lease Lifecycle
Result / Verification
External Effect
Cancellation Intent
Recovery / Budget
~~~

Public APIでは簡略statusへ投影できますが、内部正本は分離します。

---

# 4. TaskRecord

TaskRecordはTask State Authorityが管理するdurableな正本です。

概念的な主要Field:

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

TaskRecord更新にはmonotonicなState Revisionを持たせます。

複数Control Plane instanceが古い状態を上書きしないよう、

- compare-and-swap
- transaction
- consensus-backed state

等でrevisionを確認できる構造を要求します。

具体DB方式は固定しません。

---

# 5. Task Lifecycle Phase

Taskの内部Lifecycleを次の大分類で扱います。

## T-ADMITTED

Admission済みでTask IDが存在する。

まだAttemptを実行可能状態にしていない場合を含みます。

## T-READY

新しいAttemptを作成できる状態。

Queue待ち、NotBefore待ち、Resource待ちを含めます。

## T-ACTIVE

少なくとも一つのnon-closed Attemptが存在し、実行・検証が進行しています。

## T-VERIFYING

ResultClaim / VerificationResultを評価し、AcceptedResultを確定しようとしている状態。

T-ACTIVEと内部的に重なり得るため、実装ではsubstate / derived stateでも構いません。

重要なのはExecutionとVerificationを区別することです。

## T-RECONCILING

外部副作用等について、成功・失敗をFabricだけでは確定できず、External Effect AuthorityやApplication-specific reconciliationを待っている状態。

## T-HELD

自動Progressを停止し、operator / application decisionを必要とする状態。

例:

- retry budget exhausted
- reconciliation budget exhausted
- non-idempotent UNKNOWNを自動解消できない
- conflicting high-assurance results
- policy changed and no safe migration exists

## T-TERMINAL

Task lifecycleが終了しています。

Terminal Outcomeを別Fieldで持ちます。

---

# 6. Terminal Outcome

T-TERMINALではOutcomeを明示します。

## COMPLETED

Task Policyに従うAcceptedResultまたはauthoritative completion evidenceが成立した。

## CANCELLED

Cancellation Intentにより将来実行を停止し、未解決external effectが残っていない状態。

## FAILED

自動・手動方針によりTask Intentを満たせないことが確定した。

単なるWorker disappearanceはFAILED確定根拠ではありません。

## EXHAUSTED

Retry / Budget / Deadline等を使い切り、Task Intentを達成できなかった。

FAILEDのreason categoryとして実装することもできますが、運用上区別できる余地を持ちます。

## ABANDONED

結果または外部Effectを完全に確定できないまま、Policy / Operatorがこれ以上のreconciliationを行わないと判断した。

ABANDONEDは、

> Effectが起きなかった

という意味ではありません。

Audit上UNKNOWN evidenceを保持します。

---

# 7. Public Task Status

Clientへは内部状態をそのまま公開しません。

概念的なPublic Status:

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

runningは便利な表示状態であり、

> Workerが本当に現在CPU命令を実行している

という暗号学的事実ではありません。

有効なAttempt / Lease / recent Observationからderivedします。

## 7.2 reconciling

外部Effect outcomeが未確定の場合、Clientへfailedと偽らずreconciling / unknown系の状態を返せるようにします。

---

# 8. AttemptRecord

AttemptはTask State Authorityが生成するdurable identityです。

主なField:

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

Retry / migration / speculative duplicate / validation replicationは別Attemptを作ります。

---

# 9. Attempt Phase

Attemptは次の大分類を持ちます。

## A-CREATED

Attempt identityは発行済みだがPlacement未確定。

## A-PLACING

Scheduler Proposal / Placement Approvalを取得中。

## A-ASSIGNED

Worker、Lease、WorkOrderが確定した。

Dispatch前を含みます。

## A-DISPATCHED

WorkOrder deliveryを開始・完了した。

Workerが受信したかを別Observationとして持てます。

## A-CLAIMED

ResultClaimまたはExecution failure claim等を受信した。

Claimの内容をまだAccepted factへしません。

## A-VERIFYING

Result Verification中。

## A-CLOSED

このAttemptから追加Executionを期待しない。

Close Reasonを別に持ちます。

---

# 10. A-RUNNINGをdurable factにしない理由

Worker自身がrunningと報告しても、悪意Workerは偽れます。

Control Planeが知れるのは主に、

- WorkOrderをdispatchした
- Worker sessionが応答した
- Leaseが有効
- progress claimが来た
- telemetryが来た

というObservationです。

そのためrunningはderived / observational statusとして扱い、Task StateのSecurity Decisionをrunning申告だけに依存させません。

---

# 11. Attempt Close Reason

A-CLOSEDではreasonを区別します。

例:

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

Close ReasonはTask Terminal Outcomeではありません。

A1がWORKER_LOSTでも、Task T1はA2で継続できます。

---

# 12. LeaseRecord

LeaseはAttemptとWorkerを期間限定でbindingします。

概念Field:

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

Renewalでは、同じLease identityにmonotonic sequenceを持たせるか、新Leaseを発行する方式を選べます。

どちらでも古いrenewal messageで期限を巻き戻せない必要があります。

Baselineではmonotonic Lease Revision / Sequenceを要求し、exact wire方式は後続実装で決めます。

---

# 13. Lease Fencing

新しいAssignmentが成立した後、古いWorkerが戻ってきてもAuthorityを復活できてはいけません。

Attempt / Leaseにはfencing可能なepochを持たせます。

~~~text
Attempt A1
  Lease epoch 4 -> Worker W1

new assignment
  Lease epoch 5 -> Worker W2

late heartbeat from epoch 4
  -> rejected
~~~

古いheartbeat、renewal、progress claimでcurrent assignmentを巻き戻しません。

---

# 14. WorkOrder deliveryはat-least-onceでよい

Queue / Networkは重複deliveryし得ます。

同じWorkOrderがWorkerへ複数回届いても、新Attemptを作りません。

Workerは、

- WorkOrder ID
- Attempt ID
- Lease ID / Revision

で重複を認識します。

Honest Worker Agentは同一WorkOrderを二重実行しないようにします。

malicious Workerが二重実行する可能性はResult / Contribution側で別途防ぎます。

---

# 15. QueueはState Source of Truthではない

Internal Queue / BrokerにmessageがあることをTask Stateの正本にしません。

正本はdurable Task / Attempt / Lease Decisionです。

推奨Logical Pattern:

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

Deliveryが失敗・重複しても、Attempt identityやAuthority Decisionが曖昧にならない構造を優先します。

具体Outbox implementationは固定しません。

---

# 16. Commit before dispatch

WorkerへWorkOrderを送る前に、

- Attempt
- Lease
- WorkOrder identity
- prerequisite Decision refs

をdurableに記録することを標準にします。

~~~text
send succeeded
but state not committed
~~~

のように、Workerは実行中なのにControl PlaneがAttemptを知らない状態を減らします。

完全なdistributed transactionを要求するのではなく、durable intent + idempotent deliveryで吸収します。

---

# 17. Heartbeat

heartbeatはsession liveness Observationです。

Heartbeat内容例:

- Worker ID
- session ID
- active Lease IDs / revisions
- local governor state summary
- optional progress observations
- timestamp / sequence

## 17.1 Heartbeatがしないこと

heartbeatだけで、

- Leaseを延長
- ResultをAccepted
- Resource Measurementを確定
- Security Assuranceを昇格
- Artifact Grantを延長

しません。

各Authorityが別Decisionを行います。

---

# 18. Lease Renewal

Lease RenewalはLease AuthorityのDecisionです。

入力:

- current Lease identity
- current revision
- Worker identity / session
- Task still active
- no cancellation / revoke
- Worker / Provider capacity
- policy / deadline
- optional progress

出力:

- renewed expiry / revision
- or reject

古いrevisionからのrenewを拒否します。

---

# 19. Clock / expiry

Lease、Grant、WorkOrder等はtime-boundedです。

具体Clock synchronization方式は固定しませんが、

- Control Plane authoritative time
- bounded clock skew
- Worker側safety margin
- local monotonic timer利用可能性

を考慮します。

Workerは自分の時計を理由に、signed expiryより長くAuthorityを延長しません。

時刻信頼性が不明なら安全側へ停止します。

---

# 20. Worker disconnect

Control Planeがheartbeatを受け取れなくなっても、即Attempt failureへしません。

標準流れ:

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

Workerがlocalではまだ動いている可能性を考慮します。

WorkOrder / Local Agentはexpiryで停止することを期待しますが、malicious Workerが無視する可能性はあります。

---

# 21. Late Worker return

Lease expiry後にWorkerが復帰しても、古いLeaseをrenewしません。

新しいLeaseが必要です。

Browser reload / resumeも同じです。

旧session / epochからのmessageはstaleとして扱います。

---

# 22. Late Result

Lease expiry後に古いAttemptからResultClaimが届く場合があります。

Late ResultはLeaseを復活させません。

Baselineでは次のように扱います。

1. ResultClaim provenanceを記録する。
2. Attemptがstaleであることを明示する。
3. Task Policyがlate result verificationを許可するか確認する。
4. Security-sensitive / side-effect Taskではdefault reject寄りに扱う。
5. deterministic pure compute等でApplication Validatorが強い場合、optionalにVerificationへ回せる。
6. Acceptedする場合も旧Lease Authorityを復活させない。

late resultを利用するかはResult Assurance Policyです。

---

# 23. Speculative / Replicated Attempts

Result Assuranceやdeadlineのため、同じTaskへ複数Attemptを並行実行する場合があります。

~~~text
Task T1
  A1 -> Worker W1
  A2 -> Worker W2
~~~

これはRetryと区別します。

Attempt Purposeを、

- PRIMARY
- RETRY
- SPECULATIVE
- VERIFICATION_REPLICA
- RECOVERY

等で記録できる余地を持ちます。

すべて同じTask budgetを消費します。

---

# 24. ResultClaim handling

ResultClaim受信時:

1. Task / Attempt binding確認
2. Worker / WorkOrder provenance確認
3. Output Artifact finalization確認
4. AttemptをA-CLAIMEDへ
5. Result Verificationへ送る
6. ResultClaim重複はidempotentに処理
7. 異なるOutput Digestの重複Claimはconflictとして記録

Worker ClaimだけでAttemptを成功確定しません。

---

# 25. Verification and Acceptance

VerificationResultが得られても、Task Policyが満たされるまではTask completeではありません。

複数Attempt / Validatorを使う場合:

~~~text
A1 claim ─┐
          ├─ Verification ─┐
A2 claim ─┘                ├─ Result Acceptance
                           ▼
                     AcceptedResult
~~~

AcceptedResult成立後にTaskをCOMPLETEDへ進めます。

他のnon-terminal Attemptはstop / revoke / closeできます。

---

# 26. AcceptedResult後の他Attempt

AcceptedResultが成立したら、

- new Attempt生成停止
- unnecessary Lease revoke
- WorkOrder stop request
- Artifact output session停止可能
- other Attempt close as SUPERSEDED

を行います。

ただし外部副作用を持つ他Attemptがすでにdispatchしている場合、単にSUPERSEDEDとして忘れずEffect reconciliationを行います。

---

# 27. Cancellation Intent

Client / OperatorのCancel requestを受理すると、TaskRecordへCancellation Intentをdurableに記録します。

Cancellation Intentは、

> 将来の実行を増やさない

ための強い指示です。

## 27.1 Cancel時に行うこと

- new Attempt作成停止
- pending Placement停止
- Lease renew停止
- current Lease revoke where possible
- Worker stop request
- Data Grant renew停止
- Local Agentが観測できれば即停止

## 27.2 Cancelで消えないもの

- already committed external effect
- already AcceptedResult
- existing ContributionRecord
- Audit history
- UNKNOWN effect

Cancellationは過去事実のrollbackではありません。

---

# 28. Cancel + UNKNOWN

non-idempotent external effectをdispatch済みでoutcome不明のときCancelされた場合:

~~~text
Cancellation Intent = true
External Effect = UNKNOWN
Task Phase = RECONCILING
~~~

とします。

即CANCELLED terminalへしません。

Reconciliation後、

- effect not applied confirmed -> CANCELLED可能
- effect applied confirmed -> completion / cancelled-after-effect等をApplication Policyで評価
- unresolved and operator abandons -> ABANDONED with unknown evidence

のように扱います。

---

# 29. External Effect State

Side-effect semanticsをTask Stateから分離します。

例:

- E-NONE
- E-NOT-DISPATCHED
- E-DISPATCHED-UNCONFIRMED
- E-CONFIRMED-APPLIED
- E-CONFIRMED-NOT-APPLIED
- E-COMPENSATION-PENDING
- E-COMPENSATED

Compensationは、

> 元Effectが起きなかった

という意味ではありません。

Original Effect historyを保持します。

---

# 30. Pure Compute

effect mode = noneのTaskは、Worker disappearance後に比較的安全にretryできます。

ただし:

- retryable flag
- max attempts
- deadline
- compute budget
- network budget
- cancellation

を満たす必要があります。

pure computeだから無限retryしてよいわけではありません。

---

# 31. Non-idempotent External Effect

Effect dispatch前ならretry可能です。

dispatch後でauthoritative Receiptがない場合はUNKNOWN / RECONCILINGです。

~~~text
dispatched
  +
no authoritative confirmation
  =
do not auto retry
~~~

Workerのfailed自己申告はretry authorityになりません。

---

# 32. Idempotent External Effect

外部System / trusted Adapterが、

- stable Operation ID
- stable Request Intent Digest

を使ってdedupできる場合、dispatch後でもbounded retry可能です。

Retry AttemptでもOperation IDとIntent Digestを変えません。

同じOperation IDへ別Intentを割り当てたrequestはrejectします。

---

# 33. External Effect Receipt

External Effect AuthorityのReceiptがTask Intentへbindingされる場合、Effect Stateを更新できます。

Receiptは、

- Operation ID
- Request Digest
- Effect ID
- status
- authority
- observed time

等を持ちます。

Worker self-signatureはReceiptではありません。

---

# 34. Reconciliation

T-RECONCILINGでは、Taskを通常Recovery Queueへ入れません。

可能なAction:

- status API poll
- authoritative receipt query
- application-specific reconciliation
- human review
- compensation workflow
- deadline / policy based abandon

Reconciliation自体もResource Budgetを持ちます。

無限pollをしません。

---

# 35. Recovery Decision

Attemptが閉じた後、Task State Authorityは次を評価します。

概念順序:

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

Worker lostだけでretry decisionを終えません。

---

# 36. Recovery Candidate

retry可能と判定しても、すぐAttemptを作りません。

Mass reclaim時のamplificationを避けるため、Recovery Candidateを別状態として持てます。

概念Field:

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

# 37. Backoff / Jitter

同時Worker loss後のretry開始を分散します。

具体algorithm / secondsは固定しません。

重要なのは、

- per-task backoff
- jitter
- global release rate

を別に持てることです。

BackoffだけでNetwork Budgetは保証できません。

---

# 38. Recovery Admission Budget

Recovery Queueから新Attemptをreleaseする際、

- max attempts per window
- estimated transfer bytes per window
- compute capacity
- per-store transfer concurrency
- per-site bandwidth
- Task priority
- deadline

を評価します。

Recovery処理自体が次のresource exhaustionを起こさないようにします。

---

# 39. Mass Reclaim Event

大量Workerが同時にreclaimされた場合、共通Recovery Event IDを付けられる余地を持ちます。

これにより、

- affected Worker count
- affected Task count
- released retry count
- transfer amplification
- recovery duration

を観測できます。

Provider reclaim権を待たせるための仕組みではありません。

---

# 40. Reclaim Notice

Provider / Workerが事前Noticeを送れる場合:

- new Lease停止
- existing Lease non-renew
- checkpoint request
- new Placement回避
- Recovery preparation

へ利用できます。

Noticeはbest effortです。

突然消えても回復できることが基本です。

---

# 41. Local Governor Reclaim

user return / thermal / battery / opt-outによるLocal Governor stopは中央Task完遂より優先します。

Workerは可能ならReclaim Reasonを報告します。

しかし報告できなくてもLease expiryで回収します。

Central Control Planeは、

> Taskが重要だからlocal stopを無視して続行

というauthorityを持ちません。

---

# 42. Checkpoint Recovery

Checkpointable Taskでは、reclaim時にCheckpointを生成できる場合があります。

新Attemptはvalidated Checkpoint ArtifactをInputとして参照できます。

Checkpointがない、壊れている、安全に検証できない場合は、元Inputから再実行できることを優先します。

Checkpoint完成を待つためにuser reclaimを遅延しません。

---

# 43. Browser Worker

Browser Profileでは短いLeaseを使い、lifecycle eventにFabric recoveryを依存させません。

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

Browser resume / reload後は新Leaseを必要とします。

old epoch messageは拒否します。

---

# 44. Multi-tab

Browserのmulti-tab coordinationは未実装研究ですが、State Model上は同一Worker Identityから複数sessionが来ても、session / Lease / epochを区別できる必要があります。

Session collisionで古いtabがcurrent Leaseを奪い返さないようfencingします。

---

# 45. Federation Disconnect

Federation Taskでは、

- home domain
- provider domain

の通信が切れる場合があります。

Provider側local WorkOrder / Leaseが有効なら、Policy範囲で進行中Attemptを継続できる余地があります。

ただし新しいforeign authorityをofflineで生成しません。

Result exportができない場合、Provider側でdurableに保持し、再接続後にexportできます。

具体RetentionはDeployment / Grant Policyに従います。

---

# 46. Provider Grant Reclaim

DP-5 Compute Grantが縮小された場合:

1. future Placement停止
2. Lease renew停止
3. affected Attemptをreclaim分類
4. checkpoint best effort
5. local stop
6. safe retryability評価
7. bounded Recovery Queue
8. alternate pool / providerへ再配置

BeneficiaryのTask priorityがProvider reclaimを無効化しません。

---

# 47. Deadline

Task deadlineとLease expiryを同一にしません。

Task deadlineはTask Intent上の時間条件です。

Lease expiryはWorker Assignment Authorityの有効期限です。

Deadline到達時に、

- new Attempt停止
- existing Attempt revoke
- current Attempt completion待ち

のどれを選ぶかはTask Contract / deadline semanticsで決めます。

Baselineでは少なくとも、deadlineを越えて無制限にnew Attemptを生成しないことを要求します。

---

# 48. Max Attempts

Attempt BudgetはTask単位です。

Speculative / verification / retryを含め、どのAttemptがBudgetを消費するかをPolicyで明示します。

Clientがretry requestを繰り返すことでBudgetをresetできません。

---

# 49. Fan-out / Child Task

Taskが子Taskを生成できる拡張を導入する場合、子Taskは親Budgetを消費します。

必要な制約:

- max depth
- max children
- remaining compute budget
- network budget
- deadline inheritance

Task graph cycleやdistributed fork bombを防ぎます。

具体Graph ContractはExtension SPI以降で扱います。

---

# 50. Quota Reservation

Admission時にestimated maximum costをreserveし、Attempt実行・retry・output・transferで消費します。

Attempt failureで未使用分をrefundできます。

ただし実消費したResourceはfailureでも消費として扱います。

retryで新しい無制限Quotaを得ません。

---

# 51. Task StateとQueue State

TaskがT-READYでも、Queueにmessageがない瞬間はあり得ます。

Queue messageがあっても、Taskがcancelled / staleなら実行してはいけません。

Worker delivery前にcurrent Task / Attempt / Lease stateを確認します。

Queueはdelivery mechanismでありauthorityではありません。

---

# 52. Task StateとScheduler

SchedulerはTask stateを読んでProposalを作ります。

SchedulerがTask stateを書き換えてretry / completionを確定しません。

Scheduler crashはTask lifecycleを破損させない構造を選びます。

---

# 53. Task StateとResult Acceptance

Result Acceptance AuthorityがAcceptedResultを発行した後、Task State AuthorityがTaskRecordをCOMPLETEDへ遷移させます。

AcceptanceとTask state writeを同一transactionへできない場合、

- durable AcceptedResult
- idempotent completion transition

でeventual convergenceします。

AcceptedResultが存在するのにretryを増やさないguardが必要です。

---

# 54. Task StateとContribution

ContributionRecordはTask completionに必須ではありません。

AcceptedResult成立後またはAttempt終了後に非同期で生成できます。

Contribution pipeline failureでTaskを再実行しません。

Contribution accountingをexecution correctness pathから分離します。

---

# 55. State recovery after Control Plane restart

DP-1単一Control Planeがrestartしても、durable storeから次を復元できる必要があります。

- Task phase
- non-terminal Attempts
- active Lease / expiry
- WorkOrder identity
- Cancellation Intent
- External Effect UNKNOWN
- Recovery Candidate
- AcceptedResult ref
- Budget consumption

Process memoryだけにcritical stateを置きません。

---

# 56. Startup reconciliation

Control Plane startup時:

1. durable state load
2. expired Lease分類
3. active Worker session再接続待ち
4. WorkOrder delivery outbox replay
5. AcceptedResultとのTask state整合
6. UNKNOWN EffectをReconciliationへ
7. Recovery budget再構築

を行える構造にします。

Startupだから全non-terminal Taskをretryしません。

---

# 57. Split-brain control avoidance

DP-2で複数Control Plane instanceを動かす場合、一つのTask / Attemptについて互いに矛盾するLease / State Transitionを発行しない必要があります。

必要Property:

- authoritative durable state
- state revision fencing
- Lease epoch
- unique Attempt identity
- idempotent transition
- signer policy

具体consensus / database lock / transaction方式は実装比較へ残します。

---

# 58. Idempotent state transition

同じEventが二度処理されても、

- Attemptを二つ作る
- retry budgetを二回消費
- AcceptedResultを二重記録
- Contributionを二重発行

しないようTransition ID / Decision IDを利用します。

---

# 59. Event log / audit

State transitionは少なくとも:

- previous state revision
- transition type
- actor / authority
- reason
- Task / Attempt
- related Lease / Result / Receipt
- policy revision
- timestamp

へ関連付けられる必要があります。

Event sourcingを必須にはしません。

Current state + append-only auditでもRequirementを満たせます。

---

# 60. State retention

Task terminal後もAudit / Result / Contribution / side-effect provenanceに必要な期間、Task / Attempt identityを保持します。

ClientがTaskを削除しても、Security / Accounting上必要なAudit historyまで即消去するとは限りません。

Retention / privacy policyに従います。

---

# 61. Retry reason taxonomy

Operational explainabilityのため、retry / new Attempt理由を分類します。

例:

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

ReasonはSecurity Authorityではありませんが、Audit / Recovery tuningに使います。

---

# 62. Hold reason taxonomy

T-HELDの理由例:

- RETRY_BUDGET_EXHAUSTED
- UNKNOWN_EFFECT
- CONFLICTING_RESULTS
- NO_ELIGIBLE_WORKER
- DATA_GRANT_UNAVAILABLE
- POLICY_REVOKED
- DEADLINE_EXCEEDED
- MANUAL_REVIEW_REQUIRED

Client向けmessageは情報漏えいしない範囲へ縮約します。

---

# 63. Failure classification

Attempt failureとTask failureを分けます。

## Attempt local failure

- process exit
- sandbox limit
- Worker loss
- Artifact fetch error
- output limit

は通常Attempt単位です。

## Task semantic failure

Application Validatorが、

> このInputでは業務上処理不能

と権威ある形で判定する場合、Task-level failureへ進める可能性があります。

Worker self-reported failureだけでTask terminalへしません。

---

# 64. Retry safety matrix

概略:

| Task / Effect | Worker loss後 |
| --- | --- |
| pure compute + retryable | bounded retry可能 |
| pure compute + not retryable | hold / fail |
| non-idempotent effect, not dispatched | bounded retry可能 |
| non-idempotent effect, dispatched unconfirmed | reconcile / UNKNOWN |
| idempotent effect + stable operation identity | bounded retry可能 |
| authoritative effect confirmed applied | completion policyへ |
| cancellation + no unknown effect | cancel path |
| cancellation + unknown effect | reconcile |

---

# 65. Late message matrix

| Late message | 扱い |
| --- | --- |
| old heartbeat | lease復活不可 |
| old lease renewal | reject |
| old WorkOrder ack | audit only / stale |
| late ResultClaim | optional verification、lease復活不可 |
| late Checkpoint | validation後にのみreuse候補 |
| late Effect Receipt | authoritativeならreconciliation更新可能 |
| late cancellation ack | state authorityではない |

Effect Receiptは外部世界の事実を後から確定できるため、TaskがT-HELDでも受理する余地があります。

---

# 66. Reconciliation budget

UNKNOWNを無期限にpollし続けないため:

- max queries
- max wall time
- API rate
- monetary cost
- operator deadline

を設定できます。

Budget exhausted後はT-HELDまたはABANDONEDへ移行します。

---

# 67. Manual intervention

Human / Application Operatorが行えるAction例:

- retry authorize
- abandon
- mark external effect reconciled with evidence
- trigger compensation
- extend deadline
- increase budget within separate authorization

Operator actionもAuditし、Client userが自分でPolicy上限を無制限に増やせるものにはしません。

---

# 68. Compensation

Compensationは新しいExternal Effectです。

Original effectを消したことにはしません。

~~~text
Effect E1 applied
Compensation E2 applied
~~~

として双方を記録します。

Task outcome上どう解釈するかはApplication Policyです。

---

# 69. Verification replica lifecycle

Result Assuranceのためのreplica Attemptも通常Attemptです。

Worker数の多数決ではなくIndependence requirementを別に評価します。

accepted result成立後、不要replicaはstopできます。

---

# 70. Speculative execution

Deadline対策でslow Attemptと並行してspeculative Attemptを作る場合:

- additional compute budget
- network budget
- max parallel attempts
- result dedup
- side-effect-free requirement

を満たす必要があります。

external side-effect Taskを無条件にspeculative duplicateしません。

---

# 71. Checkpointable vs checkpoint-required

Checkpointableは、

> checkpointを作れる可能性がある

という意味です。

reclaim時に必ずcheckpointが成功するとは限りません。

Task recoveryはcheckpointなしでも安全に動けるか、明示的にrequired profileとして扱います。

---

# 72. Worker sessionとWorker identity

Worker Identityが同じでもsessionは変わります。

restart / browser reload後に新sessionを作ります。

古sessionのLease / nonce / heartbeatをcurrent sessionへ持ち越しません。

Worker Identity long-livednessとsession authorityを分離します。

---

# 73. Worker deregistration

WorkerをAdministrativeにdisable / revokeした場合:

- new Placement禁止
- Lease renew停止
- active Lease revoke
- Data Grant revoke where possible
- active Task recovery

を行います。

WorkerがofflineでもrevocationはControl Plane stateへ先に反映します。

---

# 74. Executor revocation

Executor Approvalがrevokedされた場合、Task Stateは影響Attemptを評価します。

高risk revocationではactive WorkOrderをrevoke / stopできます。

既に生成されたResultを受理してよいかはrevocation reason / Result Policyへ依存します。

単純に全履歴を消しません。

---

# 75. Policy revision

Policy更新によって現在Attemptが新Policyを満たさなくなった場合:

- grandfather
- stop / migrate
- revalidate

のいずれかをPolicy Change Ruleで明示します。

古いPolicy RevisionのDecisionを無条件にcurrentとして扱いません。

---

# 76. Resource budget reconciliation

Attempt終了時にreserved budgetとactual usageをreconcileします。

- unused reservation refund
- actual usage charge
- retry budget consume
- output / transfer usage

をTask budgetへ戻します。

悪意Worker self-reportだけでactual resource usageを確定しません。

---

# 77. Task priority

PriorityはRecovery順序に利用できますが、Security / Local Governor / Budgetを上書きしません。

high priorityだから、

- confidential dataをpublic Workerへ置く
- Provider reclaimを拒否する
- thermal stopを無視する
- retry budgetを無限化する

ことはできません。

---

# 78. Recovery fairness

大量Recoveryで一つのTenant / Beneficiaryが全capacityを占有しないよう、Principal / Grant / priority classごとのfairnessを適用できる余地を持ちます。

具体queue algorithmは固定しません。

---

# 79. State ModelとDeployment Profile

## DP-1 Small

single durable state storeでrevisionを守る。

## DP-2 Enterprise

multiple Control instancesでもfencing / transactionを維持。

## DP-3 Browser

short Lease、high churn、大量stale sessionに耐える。

## DP-4 Federation

domain-local Attempt / Lease authorityを維持し、remote disconnectをTask failureへ直結させない。

## DP-5 Grant

mass reclaimをRecovery Eventとして扱う。

## DP-6 Dedicated

Leaseは長めにできても同じstate semanticsを維持。

---

# 80. State Model Invariants

1. TaskとAttemptを同一Identityにしない。
2. Retryは新Attemptを作る。
3. Queue delivery retryで新Attemptを作らない。
4. Lease expiryをTask failureと同一視しない。
5. Lease expiryをexternal effect failureと同一視しない。
6. heartbeatをLease Authorityにしない。
7. Worker running申告をdurable factにしない。
8. old Lease / epochでcurrent authorityを復活させない。
9. WorkOrder dispatch前にdurable execution intentを記録する。
10. Queueをsource of truthにしない。
11. Worker disappearanceだけでretryを決めない。
12. non-idempotent dispatched effectのUNKNOWNを自動retryしない。
13. CancellationでUNKNOWNを消さない。
14. AcceptedResult成立後に不要Attemptを増やさない。
15. late Resultでexpired Leaseを復活させない。
16. speculative / replica AttemptもBudgetを消費する。
17. mass reclaim recoveryへrelease / transfer budgetを適用する。
18. Local Governor reclaimをTask completionより優先する。
19. Control Plane restartでUNKNOWN / Lease / Attempt historyを失わない。
20. State transitionをidempotentに処理できる。
21. state revision / fencingでstale writerを拒否する。
22. Task priorityでSecurity / Safety / Budgetを迂回しない。
23. Contribution failureをTask retry reasonにしない。
24. Worker self-reportだけでTask terminal outcomeを確定しない。
25. ABANDONED UNKNOWNをfailed-not-appliedと誤表現しない。

---

# 81. 次のWork Unitへの入力

次はExtension SPIを定義します。

State Modelから引き継ぐExtension Point候補:

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

SPIはCore State / Authority semanticsを差し替えるものではありません。

Adapterが変わっても、

- Task / Attempt identity
- Lease fencing
- UNKNOWN effect
- ResultClaim / AcceptedResult
- ArtifactGrant
- Authority separation

を維持することを条件にします。
