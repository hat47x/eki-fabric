# State Store Conformance

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

同じPlacement Decisionの再送は、既存のLeaseへ収束させ、FenceEpochもAttempt revisionも増やしません。ただし、現在のAttemptがその同じLeaseを`A-ASSIGNED`で保持している場合に限ります。Attemptが閉じられた後は、保存済みLeaseを返すことも許しません。Attemptのcloseと新しいLeaseの予約が競合した場合は、どちらか一方だけが成功します。

`RunLeaseBypassContract`は、SPIの通常の経路では作れない状態を確かめます。通常の経路では、TaskのTaskIR digestは`CompareAndSwapTask`で変更できません（`RunTaskStoreContract`の`taskir-digest-is-immutable`）。そのため、Task行が外部から書き換えられた場合に備え、Attempt側のTaskIR bindingが単独で予約を拒否することを、adapterが提供する`RebindTaskIRDigest`で確認します。

Store implementationは、これらの予約に対する拒否をErrConflictまたはErrNotFoundとして返してください。Backend固有errorへ置き換えないでください。

## このHarnessが決めないもの

- SQLite / PostgreSQL等の製品選定
- SQL schema
- migration tool
- connection pool設定
- WAL設定
- retry policy
- Task failure semantics

Backendが変わっても同じState Store semanticsを維持するための共通試験だけを置きます。
