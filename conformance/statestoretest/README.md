# State Storeの適合性試験（Conformance）

このpackageは、`internal/spi/statestore.TaskStore`を実装するAdapterが共通して満たすべき挙動をblack-boxで確認するTest Harnessです。

Architectureの正本は`architecture/TASK_STATE_MODEL.md`、SPIの正本は`architecture/EXTENSION_SPI.md`です。

## `RunTaskStoreContract`

in-memory / SQLite / 将来のPostgreSQL等、すべてのTask Store Adapterが通す基本契約です。

現在確認するもの。

- Create → LoadでTaskRecordが保持される
- missing Taskは`ErrNotFound`
- duplicate Createは`ErrAlreadyExists`
- revision Compare-And-Swap
- stale revisionは`ErrConflict`
- `next.ID`の差し替え拒否
- revisionを1より大きく飛ばす更新の拒否
- 同じrevisionから同時に更新した場合、成功するwriterは一つだけ
- invalid TaskRecordを永続化しない
- cancelled contextを尊重する

Store implementationは、これらのError分類をBackend固有errorへ置き換えないでください。

## `RunDurabilityContract`

Process / connectionを閉じても状態を保持すべきdurable Adapter向け契約です。

現在確認するもの。

- Create後にStoreをcloseし、同じlocationでreopenしてもTaskをLoadできる
- revision更新後にreopenしても最新revisionが残る
- reopen後も古いrevisionによるCASを拒否する

in-memory Adapterはこの契約を通す対象ではありません。

## Concrete Adapterでの利用例

~~~go
func TestTaskStoreContract(t *testing.T) {
    statestoretest.RunTaskStoreContract(t, func(t testing.TB) statestore.TaskStore {
        return newStoreForTest(t)
    })
}

func TestDurabilityContract(t *testing.T) {
    statestoretest.RunDurabilityContract(t, statestoretest.DurableFactory{
        Open: func(t testing.TB, location string) (statestore.TaskStore, func() error) {
            store := openDurableStoreForTest(t, location)
            return store, store.Close
        },
    })
}
~~~

SQLite Reference Adapterは両方を通す必要があります。

## `RunAdmissionContract` / `RunAdmissionDurabilityContract`

Task Admissionを保存するStoreが通す契約です。冪等性とquotaの確認を、Taskの作成と同じ原子性で行うことを要求します。

- 同じ(principal, key, fingerprint)は同じTaskへ収束し、別のTask IDで作り直さない
- 同じkeyで別のfingerprintは`ErrIdempotencyConflict`で、Taskを作らない
- keyはprincipalごと
- principalごとの未完了Task数の上限を、同時の提出でも超えない
- 再提出は上限に達していても拒否しない。terminalになったTaskは上限を解放する
- 再起動後も、admission、冪等性、上限の使用量が残る

## `RunLeaseStoreContract` / `RunLeaseDurabilityContract` / `RunLeaseBypassContract`

LSE-1がWorkerへLeaseを発行する前に、State Storeへ新しいLeaseを予約する契約です。予約は署名済みのLeaseそのものではありません。予約の時点で、Taskと対象Attemptがまだ有効であることを、Leaseの保存とAttemptの状態遷移と同じtransactionで確かめます。

予約時に確認すること。

- Taskが存在し、取消されておらず、`ready`または`active`で、TaskIR digestが予約と一致する
- Attemptが存在し、予約のTaskとTaskIR digestに結び付いている
- Attemptが`A-PLACING`で、Worker bindingが予約と一致する
- 呼び出し側が確認したAttempt revisionが、現在のrevisionと一致する
- `A-CREATED`、`A-ASSIGNED`以降、`A-CLOSED`のAttemptからは新しいLeaseを作らない
- 成功時は、FenceEpochを一つ進め、Lease保存とAttemptの`A-PLACING -> A-ASSIGNED`（Current Lease ref設定とrevisionの前進）を同時に確定する

同じPlacement Decisionの再送は、既存のLeaseへ収束させ、FenceEpochもAttempt revisionも増やしません。ただし、現在のAttemptがその同じLeaseを保持している場合に限ります。保持とみなすのは`A-ASSIGNED`、`A-DISPATCHED`、`A-CLAIMED`、`A-VERIFYING`の四つの段階です。close理由によらず、Attemptが`A-CLOSED`になった後は、保存済みLeaseを返すことも許しません。Attemptのcloseと新しいLeaseの予約が競合した場合は、どちらか一方だけが成功します。

`RunLeaseBypassContract`は、SPIの通常の経路では作れない状態を確かめます。通常の経路では、TaskのTaskIR digestは`CompareAndSwapTask`で変更できません（`RunTaskStoreContract`の`taskir-digest-is-immutable`）。Store外からの書き換えに備えて、次の二つを確認します。

- Task行のTaskIR digestが書き換えられても、Attemptの作成時のTaskIR bindingが単独で予約を拒否すること（`RebindTaskIRDigest`）。SQLite Adapterは、schema version 7以降のデータベースで、Task行のdigest変更もtriggerで拒否します。この試験では、そのtriggerを外したうえで、Attempt側の確認だけを見ます。
- 同じPlacement Decisionの再送が、保存済みLeaseと照合を通ったあとで、Attemptの結び付け（LeaseID、Worker identity、Task ID、TaskIR digest）の一つでも食い違うと拒否されること（`OverwriteAttempt`）。食い違いのない同じ再送は、拒否の前に成功することを確かめます。そのため、拒否の原因はAttemptの結び付けだけになります。

Store implementationは、これらの予約に対する拒否をErrConflictまたはErrNotFoundとして返してください。Backend固有errorへ置き換えないでください。

- Attemptが「Workerが止まった」と確認できる理由（reclaimed、failed、deadline、cancelled、出力の上限超過）か、結果が決まった理由（accepted、rejected）で閉じたとき、そのLeaseは次の配置を妨げない。FenceEpochは前より大きくなる。`worker_lost`など、Workerが止まったと分からない理由で閉じたときは、Leaseの期限まで`ErrLeaseActive`のまま（ADR-0015）

`RunLeaseStoreContract`は、Attemptが閉じたときのLeaseの扱いも確かめます（`lease-after-attempt-closed-*`）。

- Workerが止まったと確認できる理由（reclaimed、failed、deadline、cancelled、出力の上限超過）か、結果が決まった理由（accepted、rejected）でAttemptが閉じたとき、そのLeaseは次の配置を妨げない。FenceEpochは前より大きくなる
- `worker_lost`など、Workerが止まったと分からない理由で閉じたときは、Leaseの期限まで`ErrLeaseActive`のままになる。Workerがまだ動いているかもしれないため（ADR-0015）

## `RunLeaseSigningContract` / `RunLeaseSigningDurabilityContract`

LSE-1の署名境界（`leaseissue.Emitter`）を、実際のState Storeに対して動かす契約です。署名側はReferenceのCOSE / CBOR codecとEd25519 keyを使います。どのStore Adapterも同じbytesで判定されます。

確認する行は次のとおりです（#78）。

- 署名中にPolicyを取り消すと、EMITTEDにならずABORTED（`policy_revoked`）になり、Attemptが閉じる。次の再配置のFenceEpochは前より大きい
- Emitterの最後の確認とcommitの間にPolicyを取り消しても、State Storeがcommitを拒否し、ABORTEDになる
- 署名後にLSE-1 keyを失効させると、自己検証で拒否されABORTEDになる。commit直前の確認でkeyの侵害を検出した場合も同じ
- commit前にcrashすると、何も耐久化されずRESERVEDのまま残る。再試行は一度だけEMITTEDを作り、その後の再送は再署名せず同じbytesを返す
- commit後に応答が失われても、再試行は保存済みのbytesを返す。Policyを取り消した後でも同じ
- Policyの取消をまたいで残ったreservationは、signerに届かずABORTEDになり、同じPlacementからLeaseを作り直せない
- Taskの取消はABORTED（`cancelled`）になる
- Policyを読めない場合、Trust Bundleが古い場合は、何も記録せずerrorを返す。Trust Bundleを更新すれば再試行で発行できる
- 複数のEmitterが同時に同じreservationを処理しても、全員が同じEMITTED bytesを返す
- SQLiteを再起動しても、commit前crashからの再試行、EMITTEDの再送、ABORTEDの再送が同じ結果になる

## `RunWorkOrderSigningContract` / `RunWorkOrderSigningDurabilityContract`

WOR-1の署名境界（`workorderissue.Issuer`）を、実際のState Storeに対して動かす契約です。LeaseはLSE-1の`leaseissue.Emitter`でEMITTEDにしてから使います。WOR-1 keyは、LSE-1 keyと同じRegistryに別のscopeで登録します。

確認する行は次のとおりです（#57、#160）。

- 発行したWorkOrderが、Worker側の`workorderverify`、`workorderaccept`、`agent/startgate`をそのまま通る。同じbytesの二度目の受理はnonceの再利用として拒否される
- 古いPolicy、署名中のPolicy取消、commit直前のPolicy取消（State Storeが拒否）、WOR-1 keyの失効・侵害、Leaseの期限切れ、Leaseの置き換え（Attemptのclose）、Workerの不一致は、いずれもABORTEDになる。ABORTEDは署名済みbytesを返さず、Lease、Lease emission、Attempt、FenceEpochを変えない
- LeaseがRESERVEDのまま、Policyを読めない、Trust Bundleが古い場合は、何も記録しない
- commit前にcrashしても何も記録されず、再試行は一度だけEMITTEDを作る。その後の再送は再署名せず同じbytesを返す
- commit後に応答が失われても、再試行は保存済みのbytesを返す。Policyを取り消した後でも同じ
- 条件の違う再送にはbytesを返さない
- 複数のIssuerが同時に同じLeaseを処理しても、全員が同じEMITTED bytesを返す
- SQLiteを再起動しても、commit前crashからの再試行、EMITTEDの再送、ABORTEDの再送が同じ結果になる

## `RunResultAcceptanceContract` / `RunResultAcceptanceDurabilityContract`

検証を終えた結果を受理済みの結果（AcceptedResult）へ昇格させる、RAC-1の記録の契約です（#57）。受理は、AcceptedResultの記録、Attemptのaccepted終了、Taskのcompleted化を、一つのcommitで行います。

確認する行は次のとおりです。

- commitが成功すると、三つがそろって反映される。受理済みの結果はTask単位で読み出せる
- 同じ内容の再送は`ErrAlreadyExists`で、同じTaskへの別の内容は`ErrConflict`。最初の受理済みの結果は置き換わらない
- 拒否されたcommit（古いAttempt revision、古いTask revision、A-VERIFYINGではないAttempt、記録された申告がない、申告のdigestや出力のdigestが違う、Taskが終端に達している、取消しの要求がある、別のTaskのAttempt）は、受理済みの結果を作らず、AttemptもTaskも変えない
- 形が壊れたcommit（Attemptを別の理由で閉じる、Taskを完了にしない、など）は、状態を読む前に`ErrInvalidAcceptance`で拒否される
- SQLiteを再起動しても、受理済みの結果、閉じたAttempt、完了したTaskがそろって残り、再送は`ErrAlreadyExists`のまま

## `RunResultRejectionContract` / `RunResultRejectionDurabilityContract`

RAC-1が結果を却下したときの記録の契約です（#57の証跡）。却下の記録と、Attemptの`result_rejected`での終了を、一つのcommitで行います。Taskは変えません。

確認する行は次のとおりです。

- commitが成功すると、記録とAttemptの終了がそろって反映される。記録には食い違った検証、根拠にできた検証者、除外した検証者が残り、Taskは変わらず、受理済みの結果もできない
- 同じ内容の再送は`ErrAlreadyExists`で、同じAttemptへの別の内容は`ErrConflict`
- 拒否されたcommit（古いAttempt revision、A-VERIFYINGではないAttempt、記録された申告がない、`completed`でない、申告のdigestが違う）は、記録を作らず、Attemptも変えない
- 形が壊れたcommitは、状態を読む前に`ErrInvalidAcceptance`で拒否される
- commitの前にSQLiteを閉じても何も残らず、再起動後のcommitの後は、さらに再起動しても記録と閉じたAttemptがそろって残る

## `RunRecoveryStoreContract` / `RunRecoveryDurabilityContract`

回収された後の再投入を支える、回復の状態の契約です（#154）。待っている再投入（Pending）、時間窓の予算の消費量、発行済みのPermitを、AttemptやTaskと同じStateに耐久的に置きます。

確認する行は次のとおりです。

- 中断（`reclaimed`、`worker_lost`、`lease_expired`、`deadline`など）で閉じたAttemptについてだけ、再投入を積める。結果が決まった閉じ方、取消し、`superseded`、外部副作用の不明、policyの取消では積めない。Taskは再投入を一つしか待たない
- 同じ再投入を積み直すと、保存済みのものが返る。開始時刻（NotBefore）は動かない
- 一回の処理（pass）の確定で、Pendingが消え、Permitが保存され、窓の消費量とsequenceが更新される。同じ内容の再送は何も変えず、同じsequenceの別の内容は拒否される。sequenceは一つずつ進む
- 窓の消費量は、処理をまたいで引き継がれる。古い（巻き戻した）予算から始める処理、消費量を少なく申告する処理、対応するPendingのないPermitは拒否される。policy revisionが変わると新しい窓から始まり、前の予算は持ち越されない
- Permitからの新しいAttemptの作成と、Permitの使用済みの印は、一つのtransactionで行う。期限切れ・開始前・別のpolicy revisionのPermit、Permitが許さないAttempt、受け付けられないTask、すでに存在するAttemptは、Permitを使わずに拒否する。使ったPermitは二度目を拒否する
- SQLiteを再起動しても、待っている再投入（NotBeforeを含む）、窓の予算、Permitとその使用の状態が変わらない
- SQLiteは、Adapterを経由する書き込みだけでなく、Permitの書き換え、二度目の使用、確定した処理の変更、sequenceの飛びをtriggerで拒否する。保存されたPermitの行は、ダイジェストが一致しなければ読み出し時に拒否する

## 未決事項（Open Question）

- `A-ASSIGNED`で保持されたLeaseへの同じPlacement Decisionの再送を許すかどうかは未決です。現在の契約（`reserve-load-replay`、`exact-replay-keeps-attempt-revision-and-fence`）と両Adapterは、この再送が既存のLeaseへ収束することを確認しています。ただし、これは「配送前の段階でも再送を許す」という設計判断を確定したものではありません。Leaseをまだ配送していない段階で再送を許してよいかは、後続の設計判断で決めます。それまで、この挙動は暫定です。

## このHarnessが決めないもの

- SQLite / PostgreSQL等の製品選定
- SQL schema
- migration tool
- connection pool設定
- WAL設定
- retry policy
- Task failure semantics

Backendが変わっても同じState Store semanticsを維持するための共通試験だけを置きます。
