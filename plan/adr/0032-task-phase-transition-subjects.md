# ADR-0032 Taskのphase遷移を起こす主体と条件

- 状態: **提案中**
- 日付: 2026-11-01
- 関連: Issue [#267](https://github.com/hat47x/eki-fabric/issues/267)、[#242](https://github.com/hat47x/eki-fabric/issues/242)、[#249](https://github.com/hat47x/eki-fabric/issues/249)、[#263](https://github.com/hat47x/eki-fabric/issues/263)、[ADR-0015](0015-reclaim-as-authority-revocation-and-bounded-recovery.md)、[ADR-0020](0020-task-state-for-groups-sessions-resident.md)、`architecture/TASK_STATE_MODEL.md`

## 1. 背景

### 1.1 いまの動作

Taskは、受理のときに`admitted`で作られます（`taskstate.NewTask`）。design（`architecture/TASK_STATE_MODEL.md` §5）は各phaseの意味を定めていますが、**どの部品が、どの条件で、phaseを次へ進めるか**は、どこにも書かれていません。

実装でphaseを書いているのは、次の2か所だけです。

- 取消: `admitted`・`ready`から`terminal`/`cancelled`（`admission.Service.Cancel`）。
- 結果受理: `terminal`/`completed`（`resultacceptance`のcommit）。

一方、Attemptの作成、Leaseの予約、Lease・WorkOrderの発行は、Taskが`ready`か`active`であることを要求します。結果の受理は`ready`・`active`・`verifying`だけを受け付けます。

したがって、**実装のどの経路を通っても、`admitted`のTaskは`ready`になれません**。`ready`にしているのは試験が直接書いている箇所だけです。配置ループ（`internal/control/dispatch`）は`ready`のTaskを拾いますが、そこへ到達する道がありません。

### 1.2 何が問題か

- 受理したTaskは、`queued`のまま永久に動かない。TAI Pleromaのような呼び出し元からは「受理されたのに進まない」としか見えず、原因（policy待ち、配置待ち、実装の穴）を区別できません（#249）。
- phaseを書く権限が「誰でもCASで書ける」状態のままだと、配置ループ・回復・取消が同じTaskを別々の前提で書き、revisionの競合としてしか食い違いが現れません。誰が遷移の主体かを決めておかないと、後から並行する部品を足すたびに、同じ判断を書き直すことになります。
- `admitted`と`ready`の区別は、公開状態（#249）と、Lease予約の可否（境界での拒否）の両方に効きます。遷移の条件を決めずに実装すると、境界の拒否だけが先にできて、Taskが進まない状態が固定されます。

### 1.3 用語

- **遷移の主体（transition subject）**: あるphaseの書き込みを始める部品。書き込みそのものはState Storeのcompare-and-swap（CAS）が行い、revisionの一致と`taskstate.ValidateTransition`の表で守られます。
- **Task State Authority**: Taskの状態の正本を持つ境界。このADRではState Storeを指し、phaseの判断は主体が行い、境界が検証して書く、と役割を分けます。

## 2. 決定

### 2.1 遷移ごとに主体を1つ決める

| 遷移 | 主体 | 条件 |
| --- | --- | --- |
| `admitted` → `ready` | Control Planeの配置ループ | Taskが`admitted`で、取消の意図が無く、policyが現在で、開始時刻の指定（あれば）を過ぎている（§2.2） |
| `ready` → `active` | 同じ配置ループ（Attemptの作成と同じtick） | そのTaskの最初のAttemptが`PLACING`になった（§2.3） |
| `active` → `verifying` | 結果の受理（ResultClaimの受理経路） | 完了のResultClaimを受け取った（§2.4） |
| `active` → `ready` | 結果の受理または回復 | closeされていないAttemptが無く、再試行の余地がある（§2.5） |
| 任意 → `terminal`（`cancelled`） | 取消（`admission.Service.Cancel`） | 既存のまま |
| `verifying` → `terminal`（`completed`） | 結果の受理（`CommitAcceptedResult`） | 既存のまま |
| `任意` → `held` | 回復（Local Governor・予算の枯渇） | 設計のまま。実装は#242の配線で足す |

主体は1つに限ります。「誰かが書く」を許すと、条件の判断が複数箇所に散ります。

### 2.2 `admitted` → `ready` は配置ループが行う。受理の直後には自動で進めない

受理（`admission.Submit`）は、Taskを`admitted`のまま返します。`ready`へ進めるのは配置ループのtickです。

根拠:

1. **受理と配置の判断を混ぜない。** 受理は「この要求を受け付けるか」を決めます。配置は「いま、この端末・このpolicyで始めてよいか」を決めます。後者はpolicyの現在性、予算、容量、開始時刻の指定に依存し、受理の時点では確定しません。
2. **取消の意図を尊重できる。** 受理直後に`ready`へ進めると、その直後に取消が来た場合、`admitted`から`terminal`へ進む経路と、`ready`から`terminal`へ進む経路の2つを持つことになります。tickで進めれば、取消の意図があるTaskを`ready`にしない判断を1か所でできます。
3. **再起動に強い。** tickは状態を読んでから書くので、Control Planeが落ちても次のtickが同じ判断をやり直します。受理のトランザクションに配置の判断を混ぜると、失敗したときに「受理はされたが配置の判断が失われた」状態を作ります。

条件（すべて満たすときだけ進める）:

- `phase == admitted`、`CancellationRequested == false`。
- 現在のpolicyが読め、そのrevisionがTaskの受理時と矛盾しない。
- 開始時刻の指定がある場合、その時刻を過ぎている。要求の本文はまだ保存していないため、この条件はTaskの内容の保存（#265）と合わせて実装する。
- 書き込みはCAS（期待revision付き）。競合したら何も書かず、次のtickで読み直す。

### 2.3 `ready` → `active` は、最初のAttemptの作成と同じtickで行う

配置ループがAttemptを作る流れ（suitability → scheduler → placement → Lease予約 → WorkOrder発行）のうち、Attemptが`CREATED`から`PLACING`へ進んだ時点で、Taskを`active`にします。

根拠: `active`の意味は「closeされていないAttemptが存在する」です（§5）。Attemptの作成とTaskのphaseを別のtickに分けると、その間に「AttemptはあるがTaskは`ready`」という、意味と合わない状態が現れます。同じtickで書けば、この中間状態は外から見えません。

- Attemptの作成が失敗した場合、Taskは`ready`のままにします。次tickで再計画します。
- Leaseの予約が失敗した場合も同じです。Attemptが`PLACING`のまま残る場合は、回復（#242）が扱います。

### 2.4 `active` → `verifying` は、完了のResultClaimの受理で行う

`verifying`は「ResultClaimとVerificationResultを評価して、AcceptedResultを確定しようとしている」状態です（§5）。したがって、遷移を起こすのは完了のResultClaimを受け取った経路で、検証の前です。

- 完了でないResultClaim（失敗・取消）では`verifying`にしません。Attemptのcloseと、`active`→`ready`（再試行）または`terminal`への遷移で扱います。
- `verifying`をphaseとして持つか、`active`の導出状態にするかは、実装の都合で選べます（§5が許しています）。このADRでは、**保存するphaseとして扱う**ことを既定にします。検証が非同期に走る場合、`active`のままでは「検証中」と「実行中」を外から区別できないためです。
- **遷移の主体は2つあり、優先順位を決めます**（2026-11-02 追記）。ResultClaimを受理する経路が2つあるためです。`claimaccept`（Claimの受理とAttemptの`verifying`を同じ流れで書く経路）が**第一の主体**で、Claimが届いた時点でTaskを`verifying`にします。`resultacceptance`（受理のcommitの直前）の遷移は**代替の主体**で、Taskがまだ`active`のときだけ発火します。前者を通ったClaimでは発火しないため、二重遷移にはなりません。`claimaccept`を通らない構成（Claimが別経路で記録される構成）でも、`verifying`を経て完了することを保証するための保険です。

Control Planeの受理経路（`internal/control/service`）は`claimaccept`を必ず配線するため、通常の構成で動くのは第一の主体だけです。`resultacceptance`を単独で使う構成（Taskが`ready`のまま受理する経路）を無くすと決めたときは、代替の主体を削除できます。

### 2.5 `active` → `ready` は、closeされていないAttemptが無くなったときに行う

Attemptが閉じ（成功・失敗・回収のいずれでも）、再試行の余地がある場合に`ready`へ戻します。再試行の余地が無い場合は`terminal`（`exhausted`・`failed`）へ進めます。

- 判断は結果の受理と回復の経路に置きます（Attemptのcloseを書くのと同じトランザクション、または直後のCAS）。
- Taskに複数のAttemptがある場合（同じTaskの並行Attempt）は、すべてが閉じたときにだけ`ready`または`terminal`へ進めます。

### 2.6 権限と失敗時の扱い

- phaseを書けるのは、上の表の主体だけです。ほかの部品は読むだけにします。
- 書き込みは必ずState StoreのCAS（期待revision付き）を通し、`taskstate.ValidateTransition`の表に無い遷移は`ErrInvalidTaskTransition`で拒否します（#280）。revisionの競合は`ErrConflict`で、読み直して次のtickでやり直します。
- 部分的な書き込みをしません。TaskのphaseとAttemptの作成・closeを同時に書く必要がある箇所（`verifying`への遷移、`active`→`ready`）は、同じトランザクションにします。

### 2.7 公開状態との関係

`admitted`と`ready`は、公開状態ではどちらも`queued`です。両者を区別して見せるかは#249で決めます。このADRは、区別に必要な情報（phaseと、`admitted`に留まっている理由）を、遷移の主体が判断できる形で持つことだけを定めます。

## 3. 影響と移行

- 配置ループ（`internal/control/dispatch`）に、`admitted`→`ready`と`ready`→`active`のCAS書き込みを足します。既存の`ready`のTaskを拾う経路はそのまま動きます。
- State Storeの適合試験に、次を足します。`admitted`のTaskにLeaseを予約できないこと（現行の境界の拒否）、`admitted`→`ready`→`active`のCASが通ること、表に無い遷移が`ErrInvalidTaskTransition`で拒否されること（#280で追加済み）。
- 試験が直接`ready`を書いている箇所（`conformance/process`、`conformance/statestoretest`）は、fixtureとして残せます。ただし、productionの経路で`ready`に到達できることを別の試験で確かめます。
- 文書: `architecture/TASK_STATE_MODEL.md` §5に、このADRへの参照を足します。

## 4. 選ばなかったもの

1. **受理のトランザクションで`ready`まで進める**: 受理と配置の判断を混ぜ、取消の意図・policy・容量の変化を反映できません。2.2の根拠1・2。
2. **独立した「promoter」サービスを作る**: 配置ループと同じ状態を読み、同じ判断をします。部品を増やすだけで、権限の所在も曖昧になります。
3. **phaseをすべて導出状態にする**: `verifying`のように、外から見える必要のある状態を、保存されたphaseとして持つ方が単純です。導出にするのは実装の都合の範囲に留めます（2.4）。
4. **結果の受理が`active`→`terminal`を直接書く**: `verifying`を経ないと、「検証中」と「実行中」を区別できず、#249の公開状態とも噛み合いません。

## 5. 未決

- `admitted`→`ready`を、受理直後に自動で行う構成（配置ループを持たない単一プロセスなど）を許すか。
- `verifying`を保存するか、`active`の導出状態にするか。検証が同期か非同期かで変わる。
- 取消の収束（#263）を、このADRの遷移の主体と同じ部品に置くか。
- 並行Attemptを持つTask（同じTaskの複数Attempt）で、`active`→`ready`の判断をどこに置くか。
- `held`・`reconciling`への遷移の主体（#242の配線、ADR-0015）。
