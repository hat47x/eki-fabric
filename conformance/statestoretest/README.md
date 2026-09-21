# State Store Conformance

このpackageは、`internal/spi/statestore.TaskStore`を実装するAdapterが共通して満たすべき挙動をblack-boxで確認するTest Harnessです。

Architectureの正本は`architecture/TASK_STATE_MODEL.md`、SPIの正本は`architecture/EXTENSION_SPI.md`です。

## `RunTaskStoreContract`

in-memory / SQLite / 将来のPostgreSQL等、すべてのTask Store Adapterが通す基本契約です。

現在確認するもの:

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

現在確認するもの:

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

## このHarnessが決めないもの

- SQLite / PostgreSQL等の製品選定
- SQL schema
- migration tool
- connection pool設定
- WAL設定
- retry policy
- Task failure semantics

Backendが変わっても同じState Store semanticsを維持するための共通試験だけを置きます。
