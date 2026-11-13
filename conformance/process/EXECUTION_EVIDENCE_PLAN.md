# 同一Taskの実行証拠と外部効果の停止条件（#242・#745・#753）

Status: **独立Expectedを含むConformance計画（初版2026-11-09／実装状況を2026-11-12に再照合）**。既存コード・単体試験・実プロセス試験・外部ProviderのE2Eは、それぞれ確認範囲を区別する。Expectedの掲載だけでは実試験PASS・Owner受理を意味しない。

## 目的と唯一の責務

Client APIで受理された**一つのTask**について、配置・署名・実行・結果申告・検証・受理までを、別々の合成Taskの成功例を継ぎ合わせずに検査する。データ配送の比較は[#753](https://github.com/hat47x/eki-fabric/issues/753)、実行系の配線は[#242](https://github.com/hat47x/eki-fabric/issues/242)、期限切れLeaseのM0 EffectNone確認は完了済みの[#745](https://github.com/hat47x/eki-fabric/issues/745)が実装根拠、残る同一Taskの状態遷移・回復の検証は[#242](https://github.com/hat47x/eki-fabric/issues/242)が担当する。追加のHost／Registry／業務意味モデルを要求しない。

これまでの実装をゼロから作り直すのではない。`internal/control/service/run.go`には設定付きの`startDispatch`とLease回収ループがあり、`dispatch.go`には署名付きPlacement/Lease/WorkOrderを組み立てる経路がある。`conformance/process/dispatch_loop_test.go`はREADYを事前投入したTaskからの割当配送、`execution_agent_test.go`は**別途seedした**割当によるAgentの固定コマンド実行とClaim送信を試験する。後者のResult Acceptanceは、テスト自身がVerificationを合成して明示的に呼び出す。**一つの実Taskの自動的なAccepted Resultとは区別する。**

## 証拠の連鎖：一つの相関キーを保持する

| 境界 | 正本・観測すべき値 | 不正な昇格の例 |
| --- | --- | --- |
| Input / Admission | Client APIが受理したTask ID、保存された要求本文のdigest、TaskIR digest、入力Source・revisionと取得範囲、idempotency条件 | 送信要求の宣言を実データ取得／Consumerの読込完了と呼ぶ |
| Suitability / Placement | 同じTask・Attemptの現在Policy revision、Worker identity、Placement decision、Data Classと適性判断 | Schedulerの候補を承認済みAuthorityとみなす |
| Lease / WorkOrder | 署名済みの実Envelope、対象Task/Attempt/Worker、FenceEpoch、期限、現在のTrust/Grant、発行・回収台帳 | 署名を持つだけで実行資格が今も有効だとみなす |
| Execution / Claim | Agentが実際に開始・消費した入力、実行側の観測、Attemptに結び付くClaim、queue登録と送信/ACKの各時点 | `ready`や通信ACKを実処理・業務効果の確定にする |
| Verification / Acceptance | 検証主体と対象Output digest、独立な検証結果、Accepted Resultの正本更新 | テスト内の合成Verificationを本番自動受理とする |
| External Effect / Consumer | 権威ある外部Operation ID + Intent digest + Receipt、必要な独立Read、利用側の実際のConsumed | Attemptを閉じたので外部Effectは起きなかったとする |

各段階で`Observed / Asserted / Unavailable / Not Run`と**観測した主体・固定された入力版・根拠の所在**を分ける。これは試験記録の分類であって、本番のTask/Result状態を増やす提案ではない。独立Expectedを、生成した実行出力から逆算してはならない。固定されたTaskIRやbyte列がA→B→Aに戻っても、間のGrant・Policy・Provider・Worker資格の失効を再利用してはならない。

## #745のEffect出典確認：実装済みと未検証を分ける

2026-11-09の当初の`recall.go`は`PossibleExternalEffect: false`を定数で渡していた。**2026-11-12に確認したmainでは解消済み**で、`recallCloser`が保存済みAdmissionを`LoadAdmission`し、`claimreconcile.ProvenM0EffectNone`で本文、Fingerprint、TaskIR digest、Task／Attempt／Lease／Workerなどの束縛を検証する。証明できなければ`possibleExternalEffect=true`として回収の判断に渡す。`#745`はcompletedで閉じられている。

ただし、証明対象は**受理されたM0要求が`effect_mode=none`と宣言している事実**であり、現在のPolicy／Grant、Executorの実行内容、外部Providerの効果が無かったこと、Recovery Permit／再投入の資格は証明しない。回収台帳からの除去、Attemptの終了、Taskの次状態、回復・再投入、外部EffectのReceiptは別々に観測する。

### 現在のM0で「既に受理できるEffect」の範囲

`internal/core/admission/request.go`の`ParseTaskRequest`は現行`eki.task-request/v1`について`reliability.effect_mode == "none"`**だけを受理する**。`idempotent`や`non-idempotent`を持つTaskを、現行Client APIが通常の受付経路でAdmitできるわけではない。したがってES-02／ES-04は、将来の契約拡張・移行・敵対的／合成入力向けの**先行負例**であり、現行M0の正式に受理済みの非冪等Taskが既に実行されているという主張ではない。

一方、`statestore.Admission`は`Content`（受理した要求の原本文）、`RequestFingerprint`、`Task.TaskIRDigest`を保持し、`AdmissionStore.LoadAdmission`で取得可能。古い行・合成Taskには`Content == nil`が許容される。`TaskRecord`自身はEffect区分を保持しないが、現行`recallCloser.Reclaim`は保存済みAdmissionを読み直して束縛を照合する。本文欠損・読取エラー・不一致なら`effect_mode=none`と推論せず、回収時のUnknown側へ進む。

#### #745で実装されたM0のEffect出典境界と、その外側

1. 現在のAttempt／LeaseのTask ID、TaskIR digest、FenceEpochを確認し、同じTaskの`LoadAdmission`を読む。本文なし・読み取り失敗は「副作用なし」ではなく**不明**。
2. 受理時の本文を`admission.ParseTaskRequest`で検証・正規化し、`RequestFingerprint()`・`TaskIRDigest()`を再計算して保存されたAdmission、Attempt、Leaseと照合する。本文のbyte一致だけで現在のAuthorityに昇格させず、必要なPolicy／Grantの有効性は独立に検証する。
3. **現在のM0**では、この検証がすべて成立し`EffectMode=none`と証明できる場合に限って`claimreconcile.CloseLeaseExpired`の候補とする。非none・未対応・不明・不整合ならAttemptと台帳を保留・診断し、`PossibleExternalEffect=false`の定数による既定終了を許さない。
4. Attemptを`lease_expired`で閉じたことは再投入の許可ではない。次Attemptの発行は`recoveryadmission`のContractSource・Budget・Policy・Ownerの独立判断で行う。将来Effect Modeを増やす際はProvider側のOperation ID／Intent digest／Receiptを追加受入条件とする。

**現時点ではこの読み直しと純粋検証は実装済み**。`internal/control/service/recall_test.go`には真正のM0 Admissionの正例と、Admission不足時の保持負例がある。`conformance/process/lease_reclaim_test.go`は既存の合成的な期限切れ回収のプロセス試験であり、同一Taskの実Dispatch→実Expiry→Recoveryを通した受理試験は引き続き別Gateとする。新しい汎用Effect Registryは導入しない。

### 未実行の反証Fixture：Expectedは先に固定し、合否を後から記録する

| Case | 最小入力と独立Expected（候補） | 所有Issue |
| --- | --- | --- |
| ES-01 | **権威ある`effect_mode=none`**、Claim無し、grace超過 → Attemptは`lease_expired`で閉じ得る。ただし次Attemptは別のRecovery Permitが必要 | #745 |
| ES-02 | **将来の契約拡張／合成負例：非冪等な外部送出の可能性**、Claim無し、grace超過、Receipt無し → `Reconcile/UNKNOWN`相当で保持し、自動再投入しない | #745 |
| ES-03 | Effect情報が読めない、TaskIR本文と保存digestが不一致、またはProvider資格が変化 → `none`へ推定せず保留・診断 | #745 |
| ES-04 | **将来の契約拡張／合成負例：**冪等を申告しただけで安定Operation ID・Intent digest・Provider側のdedup証拠が無い → 申告だけを理由とする再送を拒否 | #745 |
| ES-05 | Lease失効・通信断の後に遅延Claimが届く → staleな権限から新しいAttemptを発行せず、元のResult／Effectとの調停を区別 | #242・#745 |
| ES-06 | Client APIで受理したTask `T` がDispatch、署名付きLease、Agent実行、Claim、検証、Accepted Resultに至る → **すべて同一Task/Attemptの実観測**が揃うまでE2E PASSを宣言しない | #242 |
| ES-07 | 固定Eventの`observed_disposition`だけは正しいが、実Adapterの送受信・Consumer・現在Grantを観測できない → Trace比較は限定一致、配送／Effect E2EはNOT RUN | #753 |

**Stop Gate**：外部Effectの資格が不明なまま、安全な再投入やProvider側での未実行を宣言しない。#745によって保存されたM0 Admissionを再検証する回収判断は実装済みだが、**TaskのRecovery Permit・現在のPolicy/Grant・Consumer／Provider Receiptは別Gate**である。将来の非none Effectは現行M0の正式受付対象ではなく、ES-02／ES-04は先行負例のままとする。

## 実施順序とEvidenceの保存

1. **基準を固定**：同じcommit、Client APIで実受理したTaskと独立Expected、正本要求のdigest/Policy revision、実Agent・ControlのバイナリSHA、Runner／Providerの版を保存する。
2. **#242の既存試験を連結**：`conformance/process`のDispatch・Agent・Claim・結果受理テストに、同一Taskを追う観測を追加する。各段階で`NOT RUN`を許容し、テストの合成値を実発行済みEvidenceと記載しない。
3. **#745の実装を回帰確認**：`recallCloser`の保存済みAdmission読込と`ProvenM0EffectNone`、欠損・不一致時の保留をES-01／03で確認する。ES-02／04は未対応Effect向けの負例、ES-05の遅延Claim・Task回復は#242で実プロセスを追う。これらを一件のUnit PASSだけで完了としない。
4. **#753の配送比較に接続**：同一Taskと現在Grantを保持する実Data Adapterが見つかる場合のみ、Local IPC／Connectの生入力・実受信・Consumedを比較する。固定Fixtureの自己申告Trace一致だけではPASSにしない。実Adapter未存在なら`NOT SUPPORTED`／`NOT RUN`を記録。
5. **#754の方式選定**：M0/MVPでの新しいWebRTC/WebTransport/WebSocket採用を見送る判断はADR-0041（Accepted・仮）で記録済み。D1実測後の新しい方式判断は**追加ADR**で行う。[#753のD0-A実行記録](../../spikes/data-delivery-transport/RESULTS_2026-11-09.md)は**commit `972bc57`上のGo 1.27.0パッケージ試験PASS（26件PASS・2件SKIP）**である。後から追加されたTrace順序負例（PR #810）や本計画のケースES-01〜07を同じ正本revisionで実行した証拠ではないため、対象別に未実行を明示する。

既存のM0/MVP優先順位を維持し、各ケースの正式な意味・実行許可はOwner／利用側の契約に残す。

## 2026-11-10 証明関数を分離した段階的な実装（#745）

**2026-11-10時点の経過記録**：M0のClient APIは`effect_mode=none`のみ受理し、旧・合成Taskには保存済みのAdmission本文がない場合がある。そこで[`claimreconcile.ProvenM0EffectNone`](../../internal/control/claimreconcile/effect_evidence.go)を、当初はruntimeと切り離した純粋な確認関数として追加した。対象Task/Attempt/Lease、RequestFingerprint、TaskIR digest、MaxAttempts、Contentの再Parseをすべて照合する。省略・改変・不一致では「effectなし」と判定しない。

この関数の`true`は**保存済み要求がM0契約に従ってEffectNoneを宣言している**という限定的な意味であり、現在のPolicy/GrantやProviderの実外部効果、回収後の再実行資格を証明しない。この時点では`LoadAdmission`から`recallCloser`への実配線は未完了だったが、2026-11-12のmainでは配線済み。**Go 1.27の最新main SHAによる回帰実行、同一TaskのRecovery／Provider EffectのE2Eについては、本計画で確認できた実行Evidenceがない**。

ES-01には「正本のEffectNoneと3種のdigest/ID bindingが一致する正例」、ES-03には「保存本文欠損、A→B→Aによる古い資格、別Attempt/Leaseの取り違え、Grant失効はEffectなしと推論しない負例」を追加の判断材料とする。独立Expectedの根拠は保存された要求と現在の認可であり、検証関数自身の出力から逆算しない。

## 2026-11-12 最新mainの実証範囲と横断的な引き渡し

| 段階 | 現在確認できる根拠 | 残る独立検証と担当 |
| --- | --- | --- |
| **M0 EffectNoneの宣言と回収** | `claimreconcile/effect_evidence.go`の出典検証、`service/recall.go`の実配線、正例・欠損負例のテストコード、#745 completed | 最新main SHAの対象テストログ、同一Taskでの失効→Task状態→Recovery Permit、現在のAuthority；#242で追跡 |
| **Control / Lease / Claim** | 条件付きDispatchの起動、署名付きLease/WorkOrder、AgentのClaim経路と部分的なprocessテスト | Client APIから同一Task/AttemptのAccepted Resultまで**途切れず実行**したEvidence；#242 |
| **Data / Delivery / Transport** | 固定Fixture、単一Trackの順序照合、限定Unix socketテスト。Go 1.27のD0-Aは`972bc57`で26 PASS／2 SKIP | 最新SHAでの再実行、Local IPC/Connectの**実Data Adapter**、実観測Trace、現在Grant、利用側のConsumed、p50/p95/p99；#753→#754 |
| **Business Operation / Effect** | EKIのResultClaimやLeaseは存在するが、それだけではProviderのCommitを示さない | 外部Operation ID／Intent digest、権威あるReceipt・必要な独立Readを利用側・Providerで検証。現行M0は非noneを受理しない |
| **Decision / Harness** | EKIの受理と署名は業務意味のOwner受理や外部Harnessの権限ではない | Intent→対象解決→Projection→Transport→Consumer→Harness実ロード／実Toolの各証拠。Contextに載ったID、Skill名、Traceの自己申告だけでは完了判定しない |

横断的なActivityは、**Control（Task/Attempt/Lease）、Delivery（実Tool呼出し／応答到着）、Business Operation（Provider実Effect／Receipt）、Decision（意味・根拠・Owner受理）**を別に扱う。この分類は運用・検証の視点であって、EKIの新しいruntime状態機械や共通Authority Registryではない。

SUI/SHOなどの対象選択では「選択されたID」と「現時点で再解決され許可された対象」と「Consumerが完全に受領した範囲」を分離する。TEIのSourceではDeclared→Discovered→Loaded→Consumedを区別し、部分取得を不存在・完全取得と混同しない。選択・参照・出典の意味は利用側で確定する。EKIがTransportを選べることから外部送信・業務書込のGrantを生成しない。

Evidenceの記録はGit commitと不変な入力・計画・結果の**参照情報**を対応させる。大きなログ・動画・TraceなどはGitへ一括格納する前提にせず、保管先（ローカル/NAS/S3等）を選択できるよう、URI、byte長、content digest、採取時刻、実行環境、権限・版、可用性（未取得／消失／権限拒否など）、取得／受領の証跡を記録する。**ハッシュ一致は現在のGrant・Source・Meaningの資格ではない**。機密データを公開IssueやPRへ貼り付けない。

### 次の実施単位

1. 最新main SHAを固定し、`go test ./internal/control/claimreconcile ./internal/control/service ./conformance/process ./spikes/data-delivery-transport`の結果とSKIP理由を保存する。過去のD0-Aを新しいrevisionのPASSに繰り上げない。
2. ES-01／03を本番の`LoadAdmission`→`recallCloser`で追い、正本欠損・digest不一致時にAttempt・台帳が保留されることを確認する。同じTaskの期限切れ→回収後の**Task状態とRecovery**はES-05／06として別記録する。
3. ES-06では一つのClient API受理Taskから同じTask/AttemptのDispatch、Agent実行、Claim、Verification、Accepted Resultを追い、途中に合成Verificationを挟む場合は合成と明示する。
4. ES-07では現行Worker制御を汎用メディア配送に読み替えず、Local IPC／Connectの実Data Adapterが存在するかから確認する。実Connection・Consumer受領が揃うまでは`NOT RUN`。`#754`はADR-0041によるM0/MVPの暫定非採用決定としてclosed。D1実測後の判断は新ADRで再検討する。

### 2026-11-12 ADR-0041の暫定判断をES-07の前提へ反映

[ADR-0041](../../plan/adr/0041-data-transport-measurement-deferral.md)は**Accepted（仮）**で、M0/MVPにWebRTC／WebTransport／WebSocketを追加採用せず、既存ADR-0001の通信面を維持する。対応する[#754](https://github.com/hat47x/eki-fabric/issues/754)はcompletedで閉じられている。これは「Local IPCとConnectの実Adapter比較に合格した」「新規方式を永続的に棄却した」という判断ではない。

#753のD0-B/C実Data Adapter、D1のp50/p95/p99・欠落・資源などは未実施であり、**実測が揃った場合の再判断は新しいADR**で行う。#754を再開して未実装コードを既採用と扱う必要はない。IssueコメントにはWSL Go1.27.3でD0-A PASSとの追記があるが、再現条件・SHA・ログを確認できた既存記録はcommit `972bc57`のGo1.27.0（26 PASS／2 SKIP）である。これらを混同せず、現行mainのテスト実行を別Gateとする。

### 2026-11-13 AI Harnessへの受け渡しとEKIの証拠境界

[HANDOFF_EVIDENCE_GATES.md](HANDOFF_EVIDENCE_GATES.md)で、プロンプトから固定した対象→Productの現在Revision再解決→許可されたProjection→実Transport→Consumer Load→Tool呼出し／応答→外部Provider Receiptを、同じ「成功」へまとめずに照合する。HE-01〜HE-10を独立Expected候補として置き、ES-06（同一Task E2E）・ES-07（実配送／実受領）の延長として扱う。対象の意味・選択・開示・Tool実行の正本をEKI Coreへ複製せず、各製品とHarnessの権限・出典を参照する。#745と#754はclosed、#242と#753はopen。現行mainのGo 1.27全対象試験、Harness実行、Consumer実Load、Provider Effectを未観測のままPASSにしない。

### 2026-11-13 ES-06：Client API→Agent→Claim→公開結果までの既存テストを再評価

最新mainの[`http_submit_agent_test.go`](http_submit_agent_test.go)には、**実Client APIのTask受付**から、Control Planeの設定付きDispatch、mTLS Agentの固定コマンド実行、Claim、テスト内で明示的に合成したVerificationによるResult Acceptanceまで、**同一Task**を追うテストが既に存在した。従来の「同一Taskの試験がまだない」は誤り。事前READY seedを使う別の`dispatch_loop_test.go`と混同しない。

今回の最小強化は、同じTask／Attempt／Worker／Lease・FenceEpochの**実State Store上の一致**と、Client APIの結果公開の境界（未検証Claimは`409 RESULT_NOT_READY`、テスト内の明示受理後だけ`200 eki.task-result/v1`で同じoutput digest）をアサーションに追加すること。これで**検証・受理が自動で行われたことは証明されない**。現行`internal/control/service/run.go`ではWorkerへのClaim受付に`ResultAcceptance` Serviceを接続済みだが、独立Verificationが無い場合は保留する。

**実装可能な次の小単位（#242）**：①上記強化後の`TestHTTPTaskSubmissionIsExecutedByTheAgent`を正本Go 1.27で実行、同じSHAと失敗/成功ログを保存、②Agent以外の独立Verificationを実プロセス・信頼済みProviderから供給できる境界を確認、③Claim後の保留からAcceptedResultへの遷移と公開APIを同一Taskで観測、④失効・再起動・別Worker・不完全Evidenceで誤受理しない負例を追加。Stage①のPASSをStage②〜④へ昇格させない。#745のM0 EffectNone、#753の任意Data Adapter、外部Providerの業務効果は別証拠。

### 2026-11-13 横断Consumer受領・Provider Effectの証拠ゲート（ES-08〜12）

TEI・SHO・SUI・外部Harnessで検討されている`Intent → Resolve → Projection → Transport → Consumer → Harness/Effect`の区分を、EKIのTask/Result受理へ**別の成功判定として接続する**。EKIに製品の意味モデル、正本Registry、Owner判断、Provider Commitを移管しない。各段階のEvidenceはGitにすべての大容量byteを保存する前提にせず、Manifestとdigest／保管場所の参照を分離できる。保管先の存在やdigest一致だけで、現在の利用権限・実際のConsumer受領を主張しない。

| Case | 事前固定する反例／独立Expected候補 | Owner |
| --- | --- | --- |
| ES-08 | `AcceptedResult`が存在するが、TEI/SHO/Harnessが結果を取得していない → EKI側Acceptedと利用側Consumedは**別票**。利用側E2Eは未達 | #242（EKI側の公開）＋利用製品 |
| ES-09 | 結果digestが一致しても、取得対象のrevision／意味対応／現在の権限が失効 → Consumerの再解決とGrant再確認が必要。過去の成功Receiptは新しい資格にならない | 利用製品＋#753（配送契約） |
| ES-10 | 結果が`Delivered`だが、Consumerの`Loaded/Invoked`を観測できない → 配送成功を実消費に昇格させない。実受領時のbyte digestとConsumer identityを別記録 | #753＋利用製品 |
| ES-11 | Tool/Providerへの要求送出・通信ACKはあるが、ProviderのOperation IDとCommit Receiptが無い → 外部効果は`UNKNOWN`。新Attemptの再実行許可を推定しない | #745の既存Effect規則＋利用側Provider |
| ES-12 | HarnessのSessionが再接続不能でTool実行結果が不明 → 新Sessionへの文脈縮退は可。ただし不明なTool副作用を成功扱いせず、独立照会と権限再確認まで保留 | Harness／利用製品。EKIは実行資格・Receipt境界のみ |

**受渡しの最小相関値**は`TaskID / AttemptID / LeaseID / FenceEpoch / AcceptedResult digest / Consumer identity / Source revision / Grant or Policy revision / Operation ID / Receipt reference`のうち各境界に必要なものとする。全境界へ同じ巨大Envelopeを強制しない。`Observed / Asserted / Unavailable / Not Run`を区別し、Consumerが実際に受領したときだけ、その主体の証拠を追加する。

**計画の優先順**：①#242で独立Verifierによる結果受理とPublic APIを実証、②#753で実Data Adapterの送受信・Consumer Loadedを測定、③利用製品側で固定Selection→再解決→実受領→必要ならProvider Receiptまで検証。①だけで②③をPASSにしない。Go 1.27の実試験、製品横断の実Consumer受領、Provider実効果は本追補では**NOT RUN**。ADR-0041のM0/MVP追加方式非採用判断を変更しない。

### 2026-11-13 #242：独立Verificationの取得・認可・適用を三つの境界に分ける

最新mainの`internal/control/service/run.go`を確認すると、Worker入口の`claimaccept`は`ResultAcceptance`を呼ぶが、`claimaccept.Accept`からの`resultacceptance.Request`には**AttemptIDだけ**を渡す。受理Serviceの`Accept`は`Verifications`または`Observations`を受け取れるが、起動経路には独立Verifierの実報告を取得してこの入力へ渡す処理が見当たらない。起動時の`defaultResultPolicy`はRA2／二つの独立Principal・Operatorを要求し、`defaultProducers`はWorker identityを両dimensionのfallbackとして用いる。**配線済みServiceがあることと独立検証を実施できることは別**である。

ここで三つの境界を明示する。

1. **取得**：Worker以外の実Verifierが、固定された同一Task／Attempt／TaskIR／Output digestに対して何を再実行・照合したかを観測する。試験が手で組み立てた`resultaccept.Verification`は`TEST_ONLY`。
2. **資格と独立性**：Verifier principal／operator／実行基盤／使用Executorの独立性を**信頼できる記録から**解決する。Verifier自己申告のdimensionや、単にIDが異なることだけでRA2の独立性を証明しない。現在PolicyとRevocation、期限・対象範囲も確認する。取得不能なら`NOT PROVEN`。
3. **適用と公開**：出典を検証した`resultverify.Observation`を既存`resultacceptance.Accept`へ渡し、同一Attemptの正本更新を確認する。Claim受付のACK、Verifierの送信ACK、Result AcceptanceのCAS、Client APIの`200`は別々に観測する。欠落・失効・競合は保留または拒否し、合成Evidenceで補完しない。

| Case | 独立Expected候補 | 判定の所有先 |
| --- | --- | --- |
| VA-01 | Claimだけを受理し、Verificationが無い → Attemptは検証待ち、Public Resultは`409` | #242 |
| VA-02 | 同じOutput digestに一致するがVerifier二件が同一Principal/Operator由来 → RA2を満たさず保留 | #242 |
| VA-03 | 別Task／Attempt／TaskIR／Output digestに紐づくObservation → 正本Claimへの結合を拒否 | #242 |
| VA-04 | 署名・Principal・Policy・Grantのいずれかを現在の正本から確認できない → 受理へ昇格しない | #242 |
| VA-05 | 有効な独立Verifier二件を既存受理Serviceへ投入 → CASでAcceptedResultを確定後、同一TaskのClient APIが`200` | #242 |
| VA-06 | 同じObservationを再送、または失効後に遅延投入 → 二重受理や旧Attemptの復活を許さない | #242 |

**実装の最小単位**：既存`resultverify.NormalizeAll`と`resultacceptance.Accept`を再利用し、まずテストProfileで実VerifierのObservationを発生させる経路と資格検証を追加する。ProducerSourceとPolicySourceを固定fallbackのまま本番RA2の根拠にせず、どの信頼済み記録を読むかを先に確定する。外部製品のOwner意味受理、Provider Commit、#753の実Data Adapterは対象外。VA-01〜06は**実行前Expected候補**であり、今回の検証PASSではない。Go 1.27の実テストは`NOT RUN`。

