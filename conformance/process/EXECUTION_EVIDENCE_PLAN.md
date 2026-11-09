# 同一Taskの実行証拠と外部効果の停止条件（#242・#745・#753）

Status: **Conformance計画案（2026-11-09）**。ここにあるExpectedは既存のTask/Effect設計に基づく**実行前に固定する候補**であり、実装済みのふるまい・テストPASS・Owner受理を意味しない。

## 目的と唯一の責務

Client APIで受理された**一つのTask**について、配置・署名・実行・結果申告・検証・受理までを、別々の合成Taskの成功例を継ぎ合わせずに検査する。データ配送の比較は[#753](https://github.com/hat47x/eki-fabric/issues/753)、実行系の配線は[#242](https://github.com/hat47x/eki-fabric/issues/242)、期限切れ・副作用不明時の扱いは[#745](https://github.com/hat47x/eki-fabric/issues/745)をそれぞれ所有Issueとする。追加のHost／Registry／業務意味モデルを要求しない。

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

## 特に優先する#745の反例

現行`internal/control/service/recall.go`の`CloseDecision`は、`claimreconcile.Decide`に`PossibleExternalEffect: false`を**定数で渡している**。`TaskRecord`はTaskIRのdigestを持つが外部効果の確定情報は持たず、現行コードのコメントにも「Task model carries no external-effect marker yet」とある。したがって、外部効果の有無を信頼できる正本から解決していないAttemptまで、grace後に`CloseLeaseExpired`へ進み得る。回収台帳の記録、期限切れAttemptの終了、回収後の自動再投入は**別の判断**であり、Attempt終了だけでは後者の実行を証明しない。

### 現在のM0で「既に受理できるEffect」の範囲

`internal/core/admission/request.go`の`ParseTaskRequest`は現行`eki.task-request/v1`について`reliability.effect_mode == "none"`**だけを受理する**。`idempotent`や`non-idempotent`を持つTaskを、現行Client APIが通常の受付経路でAdmitできるわけではない。したがってES-02／ES-04は、将来の契約拡張・移行・敵対的／合成入力向けの**先行負例**であり、現行M0の正式に受理済みの非冪等Taskが既に実行されているという主張ではない。

一方、`statestore.Admission`は`Content`（受理した要求の原本文）、`RequestFingerprint`、`Task.TaskIRDigest`を保持し、`AdmissionStore.LoadAdmission`で取得可能。古い行・合成Taskには`Content == nil`が許容される。`TaskRecord`自身はEffect区分を保持せず、現行`recall.CloseDecision`はこの受理済み本文を参照しないため、**「M0はnoneしかAdmitしない」から任意のLeaseがnoneと推論することもできない**。

#### #745で採用候補となる最小のEffect出典境界

1. 現在のAttempt／LeaseのTask ID、TaskIR digest、FenceEpochを確認し、同じTaskの`LoadAdmission`を読む。本文なし・読み取り失敗は「副作用なし」ではなく**不明**。
2. 受理時の本文を`admission.ParseTaskRequest`で検証・正規化し、`RequestFingerprint()`・`TaskIRDigest()`を再計算して保存されたAdmission、Attempt、Leaseと照合する。本文のbyte一致だけで現在のAuthorityに昇格させず、必要なPolicy／Grantの有効性は独立に検証する。
3. **現在のM0**では、この検証がすべて成立し`EffectMode=none`と証明できる場合に限って`claimreconcile.CloseLeaseExpired`の候補とする。非none・未対応・不明・不整合ならAttemptと台帳を保留・診断し、`PossibleExternalEffect=false`の定数による既定終了を許さない。
4. Attemptを`lease_expired`で閉じたことは再投入の許可ではない。次Attemptの発行は`recoveryadmission`のContractSource・Budget・Policy・Ownerの独立判断で行う。将来Effect Modeを増やす際はProvider側のOperation ID／Intent digest／Receiptを追加受入条件とする。

この境界は**実装案であり、現時点で回収コードがこの検証を行っているわけではない**。M0の通常受理Taskを停止させず、旧合成Taskや本文の欠落・破損を安全側に扱えるよう、既存の`conformance/process/lease_reclaim_test.go`（合成Taskを`CreateTask`で直接作る）と`service/recall_test.go`の正例・負例を分けてから実装する。新しい汎用Effect Registryは導入しない。

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

**Stop Gate**：外部Effectの資格が不明なまま、`PossibleExternalEffect=false`を根拠として安全に再投入できると報告しない。原因を閉じる実装変更は#745で、契約の出典、既存の副作用無しTaskとの互換、再投入・失効・回復負例を含めて判断する。ここでは新しいruntimeロジックを採用しない。

## 実施順序とEvidenceの保存

1. **基準を固定**：同じcommit、Client APIで実受理したTaskと独立Expected、正本要求のdigest/Policy revision、実Agent・ControlのバイナリSHA、Runner／Providerの版を保存する。
2. **#242の既存試験を連結**：`conformance/process`のDispatch・Agent・Claim・結果受理テストに、同一Taskを追う観測を追加する。各段階で`NOT RUN`を許容し、テストの合成値を実発行済みEvidenceと記載しない。
3. **#745のEffect Fenceを先に確認**：ES-01〜05の独立Expectedで、権威あるEffect状態が不明なときに暗黙の`none`へ落とさないことを反証する。現行の`PossibleExternalEffect:false`は未達として残す。
4. **#753の配送比較に接続**：同一Taskと現在Grantを保持する実Data Adapterが見つかる場合のみ、Local IPC／Connectの生入力・実受信・Consumedを比較する。固定Fixtureの自己申告Trace一致だけではPASSにしない。実Adapter未存在なら`NOT SUPPORTED`／`NOT RUN`を記録。
5. **#754の方式選定**：D1の実測と権限・Effectの独立検証を揃えてから判断する。[#753のD0-A実行記録](../../spikes/data-delivery-transport/RESULTS_2026-11-09.md)は**commit `972bc57`上のGo 1.27.0パッケージ試験PASS（26件PASS・2件SKIP）**である。後から追加されたTrace順序負例（PR #810）や本計画のケースES-01〜07を同じ正本revisionで実行した証拠ではないため、対象別に未実行を明示する。

既存のM0/MVP優先順位を維持し、各ケースの正式な意味・実行許可はOwner／利用側の契約に残す。

## 2026-11-10 証明関数を分離した段階的な実装（#745）

M0のClient APIは現在`effect_mode=none`だけを受理するが、**旧・合成Taskには保存済みのAdmission本文がない場合がある**。そこで[`claimreconcile.ProvenM0EffectNone`](../../internal/control/claimreconcile/effect_evidence.go)を、まだruntimeに接続しない純粋な確認関数として追加する。対象Task/Attempt/Lease、RequestFingerprint、TaskIR digest、MaxAttempts、Contentの再Parseをすべて照合する。省略・改変・不一致では「effectなし」と判定しない。

この関数の`true`は**保存済み要求がM0契約に従ってEffectNoneを宣言している**という限定的な意味であり、現在のPolicy/GrantやProviderの実外部効果、回収後の再実行資格を証明しない。正しい`Admission`を`LoadAdmission`で読み取って`recallCloser`へ渡す実配線、旧合成Taskの保留、未知Effect時の診断と回復、実Go 1.27テストは次の#745の作業。現行`recall.go`の`PossibleExternalEffect:false`は**依然未解決**である。

ES-01には「正本のEffectNoneと3種のdigest/ID bindingが一致する正例」、ES-03には「保存本文欠損、A→B→Aによる古い資格、別Attempt/Leaseの取り違え、Grant失効はEffectなしと推論しない負例」を追加の判断材料とする。独立Expectedの根拠は保存された要求と現在の認可であり、検証関数自身の出力から逆算しない。
