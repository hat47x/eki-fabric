# ADR-0003 — M0 Reference State StoreにSQLiteを採用する

- Status: **Accepted for M0 Reference Adapter**
- Date: 2026-09-21

## 1. Context

M0では、Control Plane processを再起動してもTask identity、revision、Attempt / Lease等のAuthority stateを失わないdurable State Storeが必要です。

一方、DP-1 Small Managed Organizationでは10〜50台程度のEndpointを想定しており、初期導入から外部Database serverやdistributed consensus clusterを必須にすると、Deployment Proportionalityに反します。

State Store SPIは次を要求しています。

- durable write
- unique constraint
- conditional update / compare-and-swap
- transactionまたは同等のatomicity
- monotonic revision
- restart recovery
- outbox等のdurable delivery intent

このADRでは、M0 Reference Adapterとして何を使うかだけを決めます。State Store SPI自体をSQLite専用にはしません。

## 2. Decision

**M0 / DP-1のReference durable State StoreとしてSQLiteを採用します。**

Go driverは **`modernc.org/sqlite`** を第一候補として採用します。

実装時は標準`database/sql`境界を利用し、Core packageからdriverを直接参照しません。

配置先は概念的に次です。

~~~text
internal/spi/statestore
  DB非依存のState Store contract

internal/adapters/reference/statestore/sqlite
  SQLite reference implementation
~~~

## 3. 適用範囲

SQLite Reference Adapterは次のProfileを対象にします。

- DP-0 Development / Test
- DP-1 Small Managed Organization
- 単一Control Plane host
- local filesystem上のdatabase file

次を保証するReference Adapterではありません。

- 複数hostから同じSQLite fileへ同時access
- DP-2 Enterpriseのmulti-instance HA
- multi-region consensus
- network filesystem上のWAL shared database

DP-2では同じState Store SPIへPostgreSQL等の別Adapterを追加できることを前提にします。

## 4. SQLiteを選ぶ理由

### 4.1 専用Database serverを要求しない

Small Managed Profileでは、`eki-control`と同じhostへlocal database fileを置くだけでdurabilityを得られます。

これは「大規模構成で必要な運用componentを10台程度のEndpoint poolへ無条件に要求しない」というQA-12 Deployment Proportionalityと整合します。

### 4.2 Transaction semanticsを自作しない

Task / Attempt / Lease stateでは、複数Fieldを一つのDecisionとしてcommitし、process crash後にも中途半端な更新を残さないことが重要です。

独自JSON fileやappend logをM0のためだけに実装すると、

- crash途中のwrite
- fsync
- atomic rename
- locking
- schema migration
- concurrent reader / writer
- recovery

を自前で再発明することになります。

SQLiteはtransactional databaseとして、program crash、OS crash、power failureを含む場合でもtransactionのatomicity / durabilityを提供する設計を持ちます。

### 4.3 Go Reference binaryの配布を複雑にしにくい

`modernc.org/sqlite`はcgoを必要としないGo実装で、`database/sql`から利用できます。

2026-09-15公開のv1.59.0はv1系列のtagged versionで、M0開始時点の有力なpin候補です。

具体的なversion pinとtransitive dependencyは、実装branchでGo toolchainによって`go.mod` / `go.sum`へ解決し、その状態をtestしたうえでcommitします。

## 5. Driver比較

### `modernc.org/sqlite` — 採用

利点:

- cgo不要
- `database/sql`対応
- v1系列
- Cross-platform Go Reference binaryと相性がよい
- 利用実績が比較的大きい

注意:

- SQLite C sourceをGoへ変換した実装であり、dependency treeは小さくない
- driver / transitive dependencyはGo toolingで整合するversionをlockする必要がある

### `github.com/mattn/go-sqlite3` — M0 Referenceでは不採用

非常に成熟し利用実績も大きい一方、cgoを利用します。

EKI Agent / ControlのCross-platform buildをM0から単純に保つ観点では、Reference AdapterへC toolchainを必須化しない方を優先します。

cgo利用が問題にならないDeployment向けAdapterとして将来採用することは妨げません。

### `github.com/ncruces/go-sqlite3` — 保留

cgo-freeで`database/sql`対応という点は有力です。

一方、M0開始時点ではv0系列であり、Reference基盤としてはv1系列で利用実績の大きい`modernc.org/sqlite`を先に選びます。

将来の比較候補として残します。

### PostgreSQL — DP-2候補、M0標準では不採用

multi-process / multi-host Control Planeでは有力ですが、M0 / DP-1からserver運用を必須にすると導入負荷が大きくなります。

State Store SPIにより後から追加します。

### 独自file store — 不採用

M0の目的はStorage engineを発明することではありません。

Transaction / locking / crash recoveryを自作するより、既存の十分に検証されたdatabaseを利用します。

## 6. Journal mode

M0 Reference Adapterでは **WAL modeを有力なdefault** としますが、実装branchでrestart / concurrency testを行ってから確定します。

WALにはreaderとwriterを並行しやすい利点があります。

ただしSQLite公式仕様上、WALを利用する全processは同一host上に存在する必要があり、network filesystemでは利用できません。

そのため:

- database fileはlocal filesystemへ置く
- NFS / SMB等のshared network filesystemをM0 Reference configurationにしない
- 複数host Control Planeへ拡張するときは別State Store Adapterへ移る

を明示します。

## 7. Schema ownership

SQLite table schemaはState Store AdapterのPersistence schemaです。

次の正本にはしません。

- External API
- Task Contract
- Worker Protocol
- Core State semantics

DB rowをそのままJSON responseへserializeしません。

Core `taskstate` modelとPersistence representationのmappingをAdapterが担当します。

## 8. Migration

Schema versionを明示し、migrationをAdapter配下で管理します。

初回から、

- migration version
- idempotent startup check
- unsupported newer schemaのreject
- partial migration failureの安全なrollback

を考慮します。

具体migration toolはM0では追加dependencyを増やさず、必要最小限のSQL migration runnerから開始できます。

## 9. Connection / locking policy

Small Profileでは一つの`eki-control` processを標準とします。

`database/sql`のconnection poolはSQLiteのwriter特性と整合するようReference Adapter内で制御します。

State Store SPI利用側がSQLite connection countやPRAGMAを直接操作しません。

## 10. Durability boundary

Task stateをClientへacceptedとして返す前に、必要なdurable writeがcommit済みであることを要求します。

同様にWorkOrder deliveryを始める前に、Attempt / Lease / delivery intent等の正本をdurableにcommitする`commit before dispatch`原則を守ります。

SQLiteを使うこと自体がこの順序を自動保証するわけではありません。Core / application service側のtransaction boundaryとして実装します。

## 11. Security

Database fileにはTask / Principal / internal Decision metadataが含まれ得ます。

M0 Reference Adapterでも:

- file permissionを最小化する
- raw secretを保存しない
- Artifact bytesをState DBへ埋め込まない
- SQL parameter bindingを利用する
- Client supplied table / column / SQL fragmentを受け取らない

ことを要求します。

Database-at-rest encryptionはM0必須にはしません。必要DeploymentではOS disk encryptionや将来のAdapter / VFS等を比較します。

## 12. Implementation verification gate

driver dependencyを実装へ入れるPRでは、少なくとも次を実行します。

~~~text
go mod tidy
go test ./...
go vet ./...
~~~

さらにState Store固有testとして:

1. Create → process/connection close → reopen → Load
2. CAS success
3. stale revision reject
4. duplicate Task reject
5. transaction rollback
6. schema migration
7. unsupported schema version reject
8. concurrent reader / writer smoke

を確認します。

このADRだけではdriverを`go.mod`へ追加しません。

依存lockを生成・検証できない環境から、推測した`go.sum`をcommitしないためです。

## 13. Consequences

### Positive

- DP-1で専用DB serverなしにrestart durabilityを実現できる。
- Core State Store semanticsを既存transaction engineへ委ねられる。
- cgoをM0 Reference buildの必須条件にしない。
- 将来PostgreSQL等へ交換してもCore contractを変えない。

### Limitations

- SQLite単体ではmulti-host Control Plane HAを実現しない。
- WALをnetwork filesystemで共有できない。
- High write concurrencyが必要になれば別Adapterが必要になる。

これらはSQLiteを万能化せず、Deployment Profileごとに適切なAdapterを選ぶことで扱います。

## 14. Sources

- SQLite — Transactional: https://www.sqlite.org/transactional.html
- SQLite — Write-Ahead Logging: https://www.sqlite.org/wal.html
- SQLite — Atomic Commit: https://www.sqlite.org/atomiccommit.html
- modernc.org/sqlite: https://pkg.go.dev/modernc.org/sqlite
- mattn/go-sqlite3: https://pkg.go.dev/github.com/mattn/go-sqlite3
- ncruces/go-sqlite3: https://pkg.go.dev/github.com/ncruces/go-sqlite3

## 15. Follow-up

このADRを`main`へmergeした後、durable SQLite Adapterの実装は別branchで行います。

実装環境で依存をGo toolingから取得し、`go.mod` / `go.sum`を生成し、上記restart / CAS / migration testを通した状態だけをmerge対象にします。
