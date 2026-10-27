# ADR-0028 ResultClaimと受理確認は、Worker Protocolをprotocol revision 2へ加算拡張して運び、Coreの値を読み替えない

- 状態: **採用（仮承認）**
- 日付: 2026-10-27
- 関連: Issue [#198](https://github.com/hat47x/eki-fabric/issues/198)、[#173](https://github.com/hat47x/eki-fabric/issues/173)、[#57](https://github.com/hat47x/eki-fabric/issues/57)、[#233](https://github.com/hat47x/eki-fabric/issues/233)、[ADR-0001](0001-protocol-surfaces-and-canonical-encoding.md)、[ADR-0027](0027-worker-authentication-and-enrollment.md)

## 1. 背景

Workerは、実行が終わると`resultclaim.Claim`をControl Planeへ申告します。Control Planeは受け取った申告を分類し、`ClaimAck`（`new`・`duplicate`・`stale`・`late`と、適用後のAttemptの段階、検証の要否）を返します。この二つを`spec/worker/v1/worker.proto`のmessageで過不足なく運べるかを、まだ決めていませんでした。実際にあるのは、JSONの試験用profile（`internal/spi/workerplane/testprofile`）と、Protobufの実験（`spikes/claim-wire-roundtrip`）だけです。どちらも契約ではありません。

現行のprotoは、Coreの申告を運べません。

- Coreの結果の種類は7つ（`completed`・`failed`・`reclaimed`・`deadline_exceeded`・`cancelled`・`output_exceeded`・`runner_stuck`）です。protoには`COMPLETED`・`EXECUTOR_FAILED`・`SANDBOX_LIMIT`・`CANCELLED`・`RECLAIMED`の5つしかありません。
- TaskIRのダイジェスト、Executorのダイジェスト、WorkOrderのnonce、出力のダイジェストを運ぶ項目がありません。この4つは、Workerが「このTaskIRを、このExecutorで、このWorkOrderに従って実行した」と申告する束縛です。Control Planeが自分の記録から補うと、Workerが別の値を申告した事実が消えます。
- `observed_lease_revision`はあっても、Coreの`FenceEpoch`との対応が書かれていません。`work_order_id`はあっても、Coreが持つのはWorkOrderのDecision IDで、別の値です。
- 終了コードの`int32`は、Windowsの異常終了コード（たとえば`0xC0000005`）を表せません。
- `ClaimAck`は4種類の判定と、適用後の段階、検証の要否を持ちます。`EventReceipt`の`ACCEPTED`・`DUPLICATE`・`REJECTED`からは、`new`と`stale`と`late`を区別できません。

評価の全文と、不一致ごとの選択肢は、Issue #198のコメントにあります。このADRは、その結論を仮承認で記録します。

守ることは、次の3つです。

- Workerの主体は、認証された通信路からだけ得る（ADR-0027 §2.3）。bodyの項目からは与えない。
- 未知のenum、未知の項目、不足する項目は、受理しない（fail closed）。
- 失敗した申告が、完了として読めてはならない。種類の対応で、結果の種類をまとめて読み替えない。

## 2. 決定

### 2.1 方針

1. **protoを加算で拡張する。** 既存の項目の番号・型・意味は変えない。足りない項目と、受理確認の構造を、新しい番号で加える。Coreの`Claim`と`ClaimAck`は、狭めない。
2. **adapterは、構造の変換だけを行う。** 値の意味を変える写像（種類の統合、補完、読み替え）を、adapterに持たせない。変換できない値は、切り捨てずに拒否する。
3. 拡張は**protocol revision 2**とする。現行のproto（このADRの変更前）をrevision 1とする。revision 1にはCoreの申告を運ぶ項目がないので、`OpenSession`で選ばれたrevisionが2未満のsessionは、`result_claim`のeventを運べない。Control Planeは、そのeventを`unsupported_revision`で拒否する。Workerは、申告を耐久queueに残したまま、revision 2を選べるsessionを待つ。旧版へ縮退して送らない。
4. protocol revisionは、通信仕様の版であり、Coreの`Claim.Digest`の版（`eki.result-claim/v1`）とは別に数える。ただしCoreのダイジェストの計算が変わるときは、protocol revisionも上げる。

### 2.2 fail closedの規則

受信側（Control Planeは申告を、Workerは受理確認を受ける）は、復号したあとに、次をすべて検査する。1つでも満たさなければ、申告や受理確認を受理しない。

1. 列挙値が`UNSPECIFIED`、または知らない値。
2. 復号器が保持した未知の項目（ネストしたmessageを含む）。Protobufの復号は、未知の項目を黙って残す。復号後に、未知の項目が空であることを明示して確かめる。
3. 必須の項目の欠落。数値や真偽値は、`0`や`false`と欠落を区別するため、`optional`（presence）で運び、presenceを必須にする。
4. 範囲、長さ、形式の違反。IDは`ids`の構文検査と1〜128 bytes（`decisions.cddl`の`task-id`等と同じ）、Digestは`DIGEST_ALGORITHM_SHA256`と32 bytes、nonceは16〜32 bytes。
5. message全体が、`OpenSessionResponse.maximum_message_bytes`を超える場合。復号の前に拒否する。

拒否は、`EventReceipt`の`REJECTED`と、安定した`error_code`で返す。人間向けの文を、分岐の鍵にしない。

### 2.3 主体

6. `WorkerIdentity`は、**どのmessageにも項目を置かない**。Control Planeは、mTLSで認証した`Principal.WorkerIdentity`（ADR-0027 §2.3）から`Claim`を組み立てる。bodyに主体の項目を足した版は、この契約の外であり、未知の項目として拒否する。
7. `Claim.Digest`は`WorkerIdentity`を含む。したがって、別のWorkerの名義で同じbodyを送っても、Control Planeが再計算する`claim_id`が一致せず、拒否される。
8. 証明書を更新しても、`WorkerIdentity`は変わらないので、更新の前後で同じ申告は同じ`claim_id`になる。再登録で`WorkerIdentity`が変わった場合は、queueの古い申告の`claim_id`が一致せず、拒否される（ADR-0027 §5の「同じ`WorkerIdentity`の引き継ぎ」が未決のため、安全側に倒す）。

### 2.4 申告の対応表（Core → 通信仕様、revision 2）

「無損失」は、受け付ける範囲の値が、往復して`Claim.Equal`と`Claim.Digest`を保つことを指す。

| Coreの値 | 通信仕様 | 規則 | 損失 |
|---|---|---|---|
| `TaskID` | `task_id`（2） | `ids.ParseTaskID`。1〜128 bytesのUTF-8 | 無損失 |
| `TaskIRDigest` | `task_ir_digest`（10、新） | SHA-256、32 bytes。欠落・ゼロ値は拒否 | 無損失 |
| `AttemptID` | `attempt_id`（3） | `ids.ParseAttemptID` | 無損失 |
| `WorkerIdentity` | なし（認証された`Principal`） | §2.3。bodyで運ばない | 無損失（通信路から再構成し、`Claim.Digest`と`claim_id`に入る） |
| `LeaseID` | `lease_id`（5） | `ids.ParseLeaseID` | 無損失 |
| `FenceEpoch` | `observed_lease_revision`（6） | 同じ値。`decisions.cddl`のLeaseのbody項目6が「lease revision / fencing epoch」であることを根拠とする。0は拒否。`RenewalSequence`（同項目7）とは別の値で、申告に含めない | 無損失 |
| `WorkOrderDecision` | `work_order_decision_id`（11、新） | 署名済みWorkOrderの`decision-id`（1〜128 bytes）。`work_order_id`とは別の値 | 無損失 |
| （Coreにない） | `work_order_id`（4） | 署名済みWorkOrderの本体にあるID。Control Planeが、そのLeaseに発行したWorkOrderのIDと照合する。一致しなければ`binding_mismatch`で拒否。照合後は保存しない | 損失あり（照合に使い、Claimには入れない。理由は、Coreに対応する値がなく、発行記録で照合できるため） |
| `ExecutorDigest` | `executor_digest`（12、新） | SHA-256、32 bytes | 無損失 |
| `Nonce` | `nonce`（13、新） | 16〜32 bytes | 無損失 |
| `Status` | `execution.outcome`（§2.5の表） | 全単射。対応のない値は拒否 | 無損失 |
| `ExitCode` | `execution.exit_status`（6、新、`optional sint64`） | presence必須。`int`に収まらなければ拒否（32bitのプラットフォームで切り詰めない）。`completed`は0だけ（Coreの規則） | 無損失。既存の`exit_code`（2、`int32`）は、revision 2では存在してはならない |
| `OutputBytes` | `execution.output_bytes`（7、新、`optional uint64`） | presence必須。`outputs`の`size_bytes`の合計で代用しない | 無損失 |
| `OutputDigest` | `output_digest`（14、新） | `completed`では必須、それ以外では存在してはならない（Coreの規則）。SHA-256、32 bytes | 無損失 |
| `StartedAt` | `execution.started_at`（3） | presence必須。秒とナノ秒が符号付き64bitのUnixナノ秒に収まる範囲。ナノ秒の部分は0〜999,999,999 | 無損失（Coreは常にUTCへ正規化する） |
| `EndedAt` | `execution.finished_at`（4） | 同上。`started_at`以後。`claimed_at`で代用しない | 無損失 |
| （Coreにない） | `claim_id`（1） | 受信側が再計算する`Claim.Digest`の小文字hexと一致しなければならない（§2.6） | 導出値（検証して捨てる） |
| （Coreにない） | `claimed_at`（9） | 診断用。復号後は無視する。ダイジェストに入れず、終了時刻として使わない | 損失あり（送信時刻で、判断にも証拠にも使わないため） |
| （Coreにない） | `execution.failure_code`（5） | revision 2では空でなければならない。空でなければ拒否 | 運ばない（Coreに置き場がなく、黙って捨てると記録されたと誤解される） |
| （Coreにない） | `outputs`（7） | revision 2では空でなければならない。空でなければ拒否 | 運ばない（§2.7） |

### 2.5 結果の種類の対応表

| Core `Status` | 通信仕様 `ExecutionClaimOutcome` | 備考 |
|---|---|---|
| `completed` | `COMPLETED`（1） | 完了の申告。受理ではない（RQ-019） |
| `failed` | `EXECUTOR_FAILED`（2） | |
| `reclaimed` | `RECLAIMED`（5） | |
| `cancelled` | `CANCELLED`（4） | |
| `deadline_exceeded` | `DEADLINE_EXCEEDED`（6、新） | Attemptを閉じる |
| `output_exceeded` | `OUTPUT_EXCEEDED`（7、新） | Attemptを閉じる |
| `runner_stuck` | `RUNNER_STUCK`（8、新） | Attemptを閉じない。強制停止が確認できるまで開いたまま（`Status.ClosesAttempt`） |
| （対応なし） | `SANDBOX_LIMIT`（3） | Coreに対応する種類がない。Agentは送らない。Control Planeは`unsupported_outcome`で拒否する。`output_exceeded`へ自動で変換しない |
| （対応なし） | `UNSPECIFIED`（0）と未知の値 | 拒否 |

`EXECUTOR_FAILED`以外の失敗系を`EXECUTOR_FAILED`へまとめない。`runner_stuck`を`failed`へまとめると、実行が続いているAttemptを閉じてしまう。`completed`に写るのは`COMPLETED`だけであり、既定の分岐や`0`の値が`completed`へ落ちる実装を認めない。

### 2.6 `claim_id`、`event_id`、`Claim.Digest`

1. `claim_id`は、`Claim.Digest`の小文字のhex（64文字）とする。Workerが任意の値を決めない。Control Planeは、認証された`Principal`とbodyから`Claim`を組み立てて`Claim.Digest`を再計算し、`claim_id`と一致しなければ`claim_id_mismatch`で拒否する。
2. 一つの申告の同一性は、`(AttemptID, Claim.Digest)`である。重複の判定は、Coreの`Decide`（`Equal`による比較）が行う。`event_id`は、配送ごとの受理確認の対応づけに使い、冪等性の鍵にしない。同じ申告を別の`event_id`で再送しても、`duplicate`になる。
3. 同じAttemptに、別の申告（`Claim.Digest`が違う）が届いたら、先の申告が残り、後の申告は`claim_conflict`で拒否する（`resultclaim.ErrConflict`）。
4. `WorkerEvent.sequence`は診断用であり、重複や古さの判定に使わない。

### 2.7 出力

1. Coreの`OutputDigest`は、確定した出力の一覧を正規化したもののSHA-256とする（#57のコメントでの提案）。revision 2は、`output_digest`だけを運ぶ。
2. `outputs`（`ArtifactOutputClaim`の一覧）は、revision 2では空でなければならない。一覧を正規化する規則はまだなく、Control Planeが再計算できない一覧を受け取ると、自己申告の詳細が検証済みのように読めるからである。
3. 正規化を定めた後に、`outputs`を許す。そのときは、受信側が一覧から`output_digest`を再計算して一致を確かめる規則とともに、protocol revisionを3へ上げる。正規化の規則は、Artifactの確定と、結果の取得（#233）を決める側で定める。

### 2.8 受理確認の対応表（Core `ClaimAck` → 通信仕様）

`EventReceipt`に、`result_claim`（4、`ResultClaimReceipt`）を加える。`ResultClaim`のeventに対する受理確認で、statusが`REJECTED`でないときだけ存在する。

| Coreの値 | 通信仕様 | 規則 |
|---|---|---|
| `Decision` `new` | `EventReceipt.status = ACCEPTED`、`result_claim.decision = CLAIM_DECISION_NEW` | |
| `Decision` `stale` | `ACCEPTED`、`CLAIM_DECISION_STALE` | 証拠として記録したが、Attemptを閉じない |
| `Decision` `late` | `ACCEPTED`、`CLAIM_DECISION_LATE` | 証拠として記録したが、Attemptを再び開かない |
| `Decision` `duplicate` | `DUPLICATE`、`CLAIM_DECISION_DUPLICATE` | |
| `AttemptPhase`（7種類） | `result_claim.attempt_phase`（`ATTEMPT_PHASE_CREATED`から`CLOSED`まで7値） | 適用後の段階。申告の結果の種類から推測しない |
| `VerificationRequired` | `result_claim.verification_required`（`optional bool`） | presence必須。`attempt_phase`が`VERIFYING`であることと同値でなければならない（`claimclient`の現在の検査と同じ） |
| （Coreにない） | `result_claim.claim_id` | 受理確認が答えた申告の`claim_id`の写し |
| 受理しなかった | `REJECTED`、`error_code`、`result_claim`は存在しない | |

- `ACCEPTED`は、「申告を記録した」ことだけを意味する。結果が受理された意味にしない。意味は`decision`が持ち、`status`や結果の種類から推測しない。
- statusと`decision`が食い違う受理確認（たとえば`DUPLICATE`なのに`NEW`）、`claim_id`が自分の申告と違う受理確認、列挙が未知・`UNSPECIFIED`の受理確認、presenceのない`verification_required`は、不正である。Workerは、申告をqueueから削除せず、再送の対象に残す。
- 新しい`AttemptPhase`をCoreが加えるときは、protocol revisionを上げる。古いWorkerは未知の値として拒否し、queueに残す。

`error_code`の仮の語彙は、`unsupported_revision`、`invalid_claim`、`unsupported_outcome`、`claim_id_mismatch`、`binding_mismatch`（TaskIR・Executor・nonce・WorkOrderの決定の食い違い）、`attempt_not_owned`（認証されたWorkerが、そのAttemptの割当先でない）、`claim_conflict`、`unknown_attempt`とする。拒否が再試行に値するか（恒久か一時か）の分類は、未決である。

### 2.9 冪等性、再送、署名・ダイジェストの束縛、版

- **再送**: 応答が失われた再送は、同じ`Claim.Digest`なので`duplicate`になる。Attemptの段階や改訂番号を二度進めない。Coreの`Decide`が、前回の記録の直後にcloseが失敗していた場合の再試行も扱う。
- **束縛**: 申告のbodyは、mTLSの上で運び、申告ごとの署名を持たない。内容の同一性は、`Claim.Digest`と`claim_id`で保つ。これは、取り違えと改変の検出と、別Workerの名義の拒否を支える。申告ごとの否認防止は、与えない。
- **版**: 追加の項目は、revision 2のsessionでだけ使える。revision 2のControl Planeは、必須の新項目を欠く申告を拒否する。revision 1の申告を、新項目なしで受け付けない。

## 3. 検討した代替案

不一致ごとの詳細な比較は、Issue #198のコメントにある。要点だけを示す。

| 不一致 | 加算拡張（採用） | adapterで写す | Coreを狭める |
|---|---|---|---|
| 結果の種類（7対5） | 値を3つ足す | `failure_code`に種類を入れる案は、安全に関わる分岐を自由な文字列に置き、古い受信側が`runner_stuck`を一般の失敗として読む | `runner_stuck`を`failed`にまとめると、実行が続くAttemptを閉じる |
| TaskIR・Executor・nonce・出力のダイジェスト | 項目を足す | Control Planeが補うと、Workerが別の値を申告した事実が消える | 束縛が弱まり、`Claim.Digest`が変わって保存済みの行を検証できなくなる |
| `FenceEpoch`と`observed_lease_revision` | 意味を文書で固定する（項目の追加は不要） | 同上 | 不要 |
| `work_order_id`と決定ID | 決定IDの項目を足す | 両IDは同じLeaseから決まる文字列だが、規則が将来変わると壊れる。導出に頼らない | 不要 |
| 終了コード | `sint64`の項目を足し、旧項目を使わない | 切り詰めと折り返しは、別の値に見える | 異常終了したプロセスの申告が作れなくなる |
| 受理確認 | 構造を持つ項目を足す | `ACCEPTED`から`new`・`stale`・`late`を区別できない | WorkerがfencedされたことをStaleとして知れない |

申告を専用の単項RPCにする案は採らない。問題は通信の形ではなく、運ぶ値の不足であり、sessionとbatchと受理確認の仕組みを二つに分けるだけだからである。

## 4. 影響

### 保証すること（成立条件つき）

- 受け付ける範囲の7種類のStatusは、往復して`Claim.Equal`と`Claim.Digest`を保つ。失敗系の種類が、`completed`として読まれる経路を、仕様の上で閉じる。
- Workerの主体は、認証された通信路だけから来る。別のWorkerの名義の申告は、`claim_id`の不一致か所有の検査で拒否される。ただし、この保証は、受信側が2.2と2.6を実装し、復号後に未知の項目を確かめる場合に成り立つ。
- 受理確認の4種類の判定、適用後の段階、検証の要否が、失われずに戻る。不正な受理確認で、申告はqueueから消えない。

### 保証しないこと

- Workerの申告の内容が正しいこと。申告は自己申告であり、検証は別に行う（RQ-019、RQ-020）。
- 申告ごとの署名による否認防止。
- 出力の一覧の正規化と、一覧との照合（§2.7）。
- 実通信、生成コード、Connect、実際のtransportでの2つのWorkerの分離は、この文書では確かめていない。

### 制約

- `failure_code`と`outputs`は、revision 2では使えない。
- `SANDBOX_LIMIT`は、Coreが対応する種類を持つまで使えない。
- 既存の`exit_code`（`int32`）は、revision 2では使わない。

## 5. protoの変更

`spec/worker/v1/worker.proto`へ、次を反映した。

- `ExecutionClaimOutcome`に`DEADLINE_EXCEEDED`（6）、`OUTPUT_EXCEEDED`（7）、`RUNNER_STUCK`（8）を追加。
- `ExecutionClaim`に`optional sint64 exit_status`（6）と`optional uint64 output_bytes`（7）を追加。
- `ResultClaim`に`task_ir_digest`（10）、`work_order_decision_id`（11）、`executor_digest`（12）、`nonce`（13）、`output_digest`（14）を追加。
- `EventReceipt`に`result_claim`（4）を追加。`ClaimDecision`、`AttemptPhase`、`ResultClaimReceipt`を新設。
- 既存の項目の番号・型・名前は変えていない。変更の中の説明は、コメントだけである。

検証は、静的なものに限る。`conformance/protocol`の既存の検査（項目番号の一意性、すべての列挙が`UNSPECIFIED`の0を持つこと、`worker_id`の項目がないこと）が通ること、および、独立したGo moduleのprotocompileでprotoが構文と型の上でコンパイルできることを確かめた。生成コード、往復、実通信は確かめていない。

## 6. 未決事項

- `outputs`の正規化と、revision 3での解禁（#233と、Artifactの確定の側）。
- `SANDBOX_LIMIT`に対応するCoreの種類を作るか、廃止するか。
- `failure_code`をCoreのどこに置くか。置かないなら、protoから外すか。
- `error_code`の語彙の確定と、拒否の恒久・一時の分類（#221、#218の、恒久的な拒否の扱い）。
- 申告ごとの署名（Workerの鍵）。
- 再登録で`WorkerIdentity`が変わった場合のqueueの扱い（ADR-0027 §5）。
- 実通信（Connect）、生成コード、2つのWorkerの分離、session期限切れの扱いの確認（#198の完了条件の後半）。
- `workerplane.ClaimAck`（Go）とprotoのenumの名前の対応を、どちらが正本として保つか。
- 仮承認の見直し: 利用者が本承認するまで、状態は「採用（仮承認）」とする。方針が変わる場合は、このADRを置き換えるADRを書く。
