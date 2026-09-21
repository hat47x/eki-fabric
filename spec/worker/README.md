# P1 Worker Plane Protocol

Status: **Baseline 0.1 — 2026-09-21**

正本:

- `v1/worker.proto` — Worker Plane Protobuf IDL
- Protocol technology: `../../plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`
- Contract semantics: `../../architecture/CONTRACT_BOUNDARIES.md`
- Task/Lease semantics: `../../architecture/TASK_STATE_MODEL.md`

## 目的

P1は、Control Plane内部QueueをWorkerへ直接公開せず、Native / Browser / Federated Workerへ共通して使えるlanguage-neutralなWorker Contractを定義します。

Workerから届くmessageは認証済みであってもClaim / Observationです。Workerが送信した値だけでPlacement、Lease、Result Acceptance、Contribution等のAuthorityを生成しません。

## Transport

Reference transportはProtobuf + Connectです。

MVPでは次の二方式を同じmessage semanticsで扱えます。

1. `WatchCommands`によるControl→Worker server streaming + `PostEvents`
2. `PollCommands` + `PostEvents`による完全unary fallback

企業Proxy等でstreamingが使いにくい環境でも、Task / Attempt / Leaseの意味を変えずにpollingへ退避できます。

WorkerからControlへのevent送信はP1では`PostEvents`へ統一し、bidirectional streamingをMVP必須にはしません。

## AuthenticationとWorker Identity

`OpenSessionRequest`に`worker_id`はありません。

Worker IdentityはmTLS / workload identity等の認証contextからControl Planeが確定します。`agent_instance_id`はrestartやdiagnosticのための一時的識別子であり、Worker Authorityではありません。

Client credentialがWorker Planeを呼べたり、Worker credentialがTask submit authorityを得たりしないよう、audience / scopeを分けます。

## Session

`OpenSession`でControl Planeが`session_id`を発行します。

Worker restart / Browser reload等では新sessionを作ります。古いsessionのmessageからLeaseやWorkOrder Authorityを復活させません。

`WorkerEvent.sequence`はsession内のObservation順序を補助する値で、Security Authorityではありません。

## Event delivery

`event_id`はWorkerがretryしても同じ値を再利用し、Control Planeはat-least-once deliveryをidempotentに処理します。

`EventReceipt`は`ACCEPTED / DUPLICATE / REJECTED`を返せます。

Queue delivery retryで新Attemptを作りません。

## Command delivery

`command_id`もduplicate deliveryを前提にします。

`WorkOrderDelivery.delivery_id`と`WorkOrderAck`を使い、同じWorkOrder carrierが複数回届いてもHonest Agentは二重実行しません。

malicious Workerが二重実行する可能性はResult / Effect / Contribution側の別境界で扱います。

## Signed Decision Carrier

WorkOrder、Lease等のSecurity-sensitive DecisionそのものはProtobuf serializationを署名しません。

`SignedDecisionCarrier.cose_sign1`はP2で定義するCOSE_Sign1 bytesを運ぶだけです。

`kind`はrouting hintでありAuthorityではありません。WorkerはCOSEを検証し、signed payload内のkind / subject / Task / Attempt / Worker / expiry等との一致を確認します。

## Resource Advertisement

`ResourceAdvertisement`はWorker self-reportです。

- CPU / memory / GPU
- Runtime availability
- Local Governor上限
- verified cache hint

をSchedulerのObservationとして送れます。

`claimed_worker_class`やGPU性能ClaimをSecurity Assuranceへ読み替えません。Confidential execution等のClaimには別のAttestation / managed evidenceが必要です。

## Heartbeat

Heartbeatはliveness / execution Observationです。

Heartbeat受信だけでLeaseを延長しません。

Lease renewalは`LeaseRenewalRequest`を受けてLease AuthorityがDecisionを作り、`LeaseRenewalDecision.signed_lease`として返します。

## Local Governor / Reclaim

`LocalAvailability`と`ReclaimNotice`は、Endpoint利用者・温度・battery・Provider reclaim等によるlocal-side constraintを表します。

Local Governorは中央より厳しい上限を適用でき、Taskが重要であることを理由にControl Planeが強制拡大しません。

`ReclaimNotice`はbest effortです。突然Workerが消えてNoticeを送れなくても、Lease expiryとRecoveryで扱います。

## ResultClaim

`ResultClaim`はWorkerのClaimです。

`EXECUTION_CLAIM_OUTCOME_COMPLETED`であってもTask完了を意味しません。

Output Artifactのfinalization / Digest、Result Verification、Result Acceptanceを通した後にのみAcceptedResultが成立します。

`exit_code`、failure code、実行時刻もself-reported Observationであり、高保証事実へ自動昇格しません。

## Artifact bytes

Artifact input/outputのbulk bytesはWorker Protocol messageへ埋め込みません。

Data PlaneのArtifactGrant / upload sessionを使い、P1ではResultClaimがfinalized Output Artifactを参照します。

## Validation

Protobuf decoderが成功しただけでは受理しません。

実装は少なくとも次をapplication validationします。

- protocol revision
- session binding
- ID format / maximum length
- enumの`UNSPECIFIED` / unknown value
- repeated field件数
- string / bytes長
- timestamp range
- Digest algorithm / length
- event batch size
- signed decision carrier size
- monotonic sequence where applicable

Security-critical unknown enumを既知の安全な値として扱いません。

## Topology confidentiality

このProtocolはWorker↔Control内部Surfaceです。

ResourceAdvertisementやcache hintをExternal Client APIへそのまま投影しません。ClientへWorker hostname / IP / cache内容 / concrete identityを公開しないArchitecture原則を維持します。

## P1で未固定のもの

- concrete authentication mechanism
- Connect code generation version
- exact HTTP endpoint
- message compression default
- Browser Worker特有のAPI
- Attestation Evidence carrier
- Contribution metering
- P2 CDDL / COSE payload schema
- Artifact Data Plane transport

## Conformance候補

- session identity mismatch
- stale session event
- duplicate event
- duplicate command
- unknown enum
- oversized batch / message
- heartbeat does not renew Lease
- old Lease revision renewal reject
- WorkOrder wrong Worker / expired / invalid prerequisite
- late ResultClaim does not revive Lease
- ReclaimNotice absence followed by Lease expiry
- polling / streaming modeでState semanticsが同じ

## 次

P1 merge後はP2 signed Decision CDDLを定義します。WorkOrder / Lease / Placement Approval / ArtifactGrant等のcanonical payloadをdeterministic CBORで固定し、P1の`SignedDecisionCarrier`で運べるようにします。
