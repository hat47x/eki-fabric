# ADR-0003 M0のReference State StoreにSQLiteを採用する

- Status: **Accepted for M0 Reference Adapter**
- Date: 2026-09-21

## 1. Context

M0では、Control Planeのprocessを再起動しても、Taskの識別子、revision、AttemptやLeaseなどのAuthorityの状態を失わない、永続的なState Storeが必要です。

一方、DP-1 Small Managed Organizationでは、10〜50台程度のEndpointを想定しています。初期導入から外部のDatabase serverや分散合意のclusterを必須にすると、Deployment Proportionality（導入規模に見合った運用負担）に反します。

State Store SPIは次の性質を要求しています。

- 永続的な書き込み
- 一意性制約
- 条件付き更新（compare-and-swap）
- transaction、またはそれと同等の原子性
- 単調に増えるrevision
- 再起動後の回復
- outboxのような、配送意図の永続的な記録

このADRで決めるのは、M0のReference Adapterに何を使うかだけです。State Store SPIそのものをSQLite専用にはしません。

## 2. Decision

**M0とDP-1のReferenceの永続State Storeとして、SQLiteを採用します。**

Goのdriverは、**`modernc.org/sqlite`**を第一候補として採用します。

実装では標準の`database/sql`を境界にし、Coreのpackageからdriverを直接参照しません。

置き場所は、概念上は次のとおりです。

~~~text
internal/spi/statestore
  DB非依存のState Store contract

internal/adapters/reference/statestore/sqlite
  SQLite reference implementation
~~~

## 3. 適用範囲

SQLiteのReference Adapterは、次のProfileを対象にします。

- DP-0 Development / Test
- DP-1 Small Managed Organization
- Control Planeのhostが一つ
- local filesystem上のdatabase file

次のことは、このReference Adapterでは保証しません。

- 複数のhostから同じSQLite fileへ同時にaccessすること
- DP-2 Enterpriseで複数instanceによる高可用性を実現すること
- 複数regionにまたがる合意
- network filesystem上でWALのdatabaseを共有すること

DP-2では、同じState Store SPIに、PostgreSQLなどの別のAdapterを追加できることを前提にします。

## 4. SQLiteを選ぶ理由

### 4.1 専用のDatabase serverを要求しない

Small Managed Profileでは、`eki-control`と同じhostにlocalのdatabase fileを置くだけで、永続性を得られます。

これは、QA-12 Deployment Proportionalityと一致します。QA-12は、大規模構成で必要になる運用部品を、10台程度のEndpointの集まりへ無条件に求めないという品質特性です。

### 4.2 transactionの意味を自作しない

Task、Attempt、Leaseの状態では、複数のFieldを一つのDecisionとしてcommitすることが重要です。processがcrashした後に、中途半端な更新を残してはいけません。

M0のためだけに独自のJSON fileやappend logを実装すると、次のものを自分で作り直すことになります。

- crashの途中の書き込み
- fsync
- 原子的なrename
- lock
- schemaのmigration
- 並行するreaderとwriter
- 回復

SQLiteはtransactionを持つdatabaseとして設計されています。programのcrash、OSのcrash、電源断の場合も含めて、transactionの原子性と永続性を提供します。

### 4.3 GoのReference binaryの配布を複雑にしにくい

`modernc.org/sqlite`は、cgoを必要としないGoの実装で、`database/sql`から使えます。

2026-09-15に公開されたv1.59.0は、v1系列のtag付きversionです。M0を始める時点で、固定するversionの有力な候補です。

具体的にどのversionに固定するかと、推移的な依存関係は、実装branchでGo toolchainを使って`go.mod`と`go.sum`へ解決します。その状態でtestを通してからcommitします。

**実装結果（2026-10-06）**: 実装branchで、Go 1.27.0（Linux）の`go get`により`modernc.org/sqlite`の**v1.60.1**（2026-09-29公開）に固定しました。`go mod tidy`と`go mod verify`で整合も確認しています。採用時点の候補はv1.59.0でしたが、実装時点で新しいpatchが出ていたため、v1.60.1を選びました。

## 5. Driverの比較

### `modernc.org/sqlite`（採用）

利点は次のとおりです。

- cgoが不要
- `database/sql`に対応
- v1系列
- 複数platform向けのGo Reference binaryと相性がよい
- 利用実績が比較的多い

注意点は次のとおりです。

- SQLiteのC sourceをGoへ変換した実装なので、依存関係のtreeは小さくない。
- driverと推移的な依存関係は、Goのtoolで整合するversionに固定する必要がある。

### `github.com/mattn/go-sqlite3`（M0のReferenceでは不採用）

非常に成熟しており、利用実績も多いdriverです。ただし、cgoを使います。

M0の時点からEKIのAgentとControlの複数platform向けbuildを単純に保つため、Reference AdapterでC toolchainを必須にしない方を優先しました。

cgoの利用が問題にならないDeployment向けのAdapterとして、将来採用することは妨げません。

### `github.com/ncruces/go-sqlite3`（保留）

cgoが不要で、`database/sql`にも対応している点は有力です。

ただ、M0を始める時点ではv0系列でした。Referenceの基盤としては、v1系列で利用実績の多い`modernc.org/sqlite`を先に選びます。

将来の比較候補として残します。

### PostgreSQL（DP-2の候補。M0の標準としては不採用）

複数のprocessや複数のhostでControl Planeを動かす場合には有力です。しかし、M0やDP-1からserverの運用を必須にすると、導入の負担が大きくなります。

State Store SPIを通じて、後から追加します。

### 独自のfile store（不採用）

M0の目的は、Storage engineを発明することではありません。

transaction、lock、crashからの回復を自作するより、十分に検証された既存のdatabaseを使います。

## 6. Journal mode

M0のReference Adapterでは、**WAL modeを既定の有力な候補**とします。確定するのは、実装branchで再起動と並行性のtestを行ってからです。

WALには、readerとwriterを並行させやすい利点があります。

ただし、SQLiteの公式仕様では、WALを使うprocessはすべて同じhost上にある必要があり、network filesystemでは使えません。

そのため、次のことを明示します。

- database fileはlocal filesystemへ置く。
- NFSやSMBのような共有network filesystemを、M0のReference構成にしない。
- 複数hostのControl Planeへ広げるときは、別のState Store Adapterへ移る。

## 7. Schemaの所有

SQLiteのtable schemaは、State Store Adapterの永続化用のschemaです。

次のものの正本にはしません。

- External API
- Task Contract
- Worker Protocol
- Coreの状態の意味

DBの行を、そのままJSONのresponseへserializeしません。

Coreの`taskstate` modelと永続化の表現との対応付けは、Adapterが受け持ちます。

## 8. Migration

Schemaのversionを明示し、migrationはAdapterの下で管理します。

最初から次の点を考慮します。

- migrationのversion
- 起動時に何度実行しても同じ結果になる確認
- 自分より新しいschemaの拒否
- migrationが途中で失敗したときの安全なrollback

M0では依存関係を増やさず、必要最小限のSQL migration runnerから始めてかまいません。

## 9. 接続とlockの方針

Small Profileでは、`eki-control`のprocessを一つにするのを標準とします。

`database/sql`のconnection poolは、SQLiteのwriterの特性と合うように、Reference Adapterの中で制御します。

State Store SPIを使う側から、SQLiteの接続数やPRAGMAを直接操作しません。

## 10. 永続性の境界

TaskをClientへacceptedとして返す前に、必要な永続化の書き込みがcommit済みであることを求めます。

同じように、WorkOrderの配送を始める前に、Attempt、Lease、配送意図などの正本を永続的にcommitします。これを`commit before dispatch`の原則として守ります。

SQLiteを使うだけで、この順序が自動的に保証されるわけではありません。CoreやApplication serviceの側で、transactionの境界として実装します。

## 11. Security

Database fileには、Task、Principal、内部のDecisionのmetadataが含まれ得ます。

M0のReference Adapterでも、次のことを求めます。

- fileの権限を最小にする。
- 生のsecretを保存しない。
- ArtifactのbytesをState DBへ埋め込まない。
- SQLのparameter bindingを使う。
- Clientから渡されたtable名、column名、SQLの断片を受け取らない。

保存時のDatabaseの暗号化は、M0では必須にしません。必要なDeploymentでは、OSのdisk暗号化や、将来のAdapterやVFSなどを比較します。

## 12. 実装を検証するGate

driverへの依存を実装へ入れるPRでは、少なくとも次を実行します。

~~~text
go mod tidy
go test ./...
go vet ./...
~~~

さらに、State Store固有のtestとして、次のことを確認します。

1. 作成し、processまたは接続を閉じ、開き直して読み込む
2. CASの成功
3. 古いrevisionの拒否
4. 重複したTaskの拒否
5. transactionのrollback
6. schemaのmigration
7. 対応していないschema versionの拒否
8. readerとwriterを並行させた簡易確認

このADRだけでは、driverを`go.mod`へ追加しません。

依存関係のlockを生成も検証もできない環境から、推測で作った`go.sum`をcommitしないためです。

## 13. Consequences

### Positive

- DP-1で、専用のDB serverなしに再起動後の永続性を実現できる。
- CoreのState Storeの意味を、既存のtransaction engineに任せられる。
- cgoを、M0のReference buildの必須条件にしない。
- 将来PostgreSQLなどへ替えても、Coreのcontractを変えずに済む。

### Limitations

- SQLite単体では、複数hostによるControl Planeの高可用性を実現できない。
- WALのdatabaseを、network filesystemで共有できない。
- 書き込みの並行性が高く求められるようになれば、別のAdapterが必要になる。

SQLiteをあらゆる用途に使おうとはしません。これらの制約は、Deployment Profileごとに適切なAdapterを選ぶことで扱います。

## 14. Implementation record（2026-10-06）

Reference Adapterは`internal/adapters/reference/statestore/sqlite`に実装しました。§6、§8、§9、§12で「実装時に確定する」とした点は、次のとおりに決めました。

| 項目 | 確定内容 |
|---|---|
| Journal mode | WALとし、`synchronous=FULL`を併用する。WALで`NORMAL`にすると、電源断で直近のtransactionを失うことがある。そのため、commit before dispatch（§10）を満たす方を選んだ |
| Connection | `database/sql`の接続数を1に固定する。SQLiteのwriterは一つなので、こうすると自前のgoroutine同士の`SQLITE_BUSY`を避けられ、transactionの境界も単純になる。外部processのlockに備えて、`busy_timeout`は5秒にする |
| Schema version | `PRAGMA user_version`を使う。migrationは追記だけで行い、各stepを別々のtransactionで適用する。自分より新しいversionのDBは`ErrUnsupportedSchema`で拒否する |
| Schema | `STRICT` tableを使う。phase、終了時の結果、revision、時刻の順序、終了状態と結果の対応を`CHECK`でも守り、Adapterを経由しない書き込みも拒否する |
| Duplicate / CAS | `INSERT ... ON CONFLICT DO NOTHING`と`UPDATE ... WHERE revision = ?`の影響行数で判定する。driver固有のerror codeには依存しない |
| File permission | driverが開く前に、0600でfileを作る。Windowsではmode bitは意味を持たない |
| 時刻 | UTCのUnixNano（int64）で保存する |

確認したのは、共通のState Store契約とDurability契約に加えて、§12の次の項目です。閉じて開き直した後の保持、CASの成功、古いrevisionの拒否、重複の拒否、migrationのrollback、対応していないschemaの拒否、readerとwriterを並行させた簡易確認。あわせて、Adapterを経由しない不正な行をSchemaが拒否すること、SQLの断片を含むIDがデータとして扱われること、予約文字を含むpathを扱えることも確認しています。

`go test ./...`、`go vet ./...`、State Storeのpackageに対する`-race`、`go mod verify`は、Go 1.27.0（WSL2、Linux）で通りました。Windows上でのbuildとtestは、まだ確認していません。

保存しているのはTaskの記録だけです。AttemptやLease、配送意図（outbox）などのtableは、それぞれの作業単位で追加します。

## 15. Sources

- SQLite, Transactional: https://www.sqlite.org/transactional.html
- SQLite, Write-Ahead Logging: https://www.sqlite.org/wal.html
- SQLite, Atomic Commit: https://www.sqlite.org/atomiccommit.html
- modernc.org/sqlite: https://pkg.go.dev/modernc.org/sqlite
- mattn/go-sqlite3: https://pkg.go.dev/github.com/mattn/go-sqlite3
- ncruces/go-sqlite3: https://pkg.go.dev/github.com/ncruces/go-sqlite3

## 16. Follow-up

このADRを`main`へmergeした後、永続化するSQLite Adapterの実装は別のbranchで行います。

実装環境でGoのtoolを使って依存関係を取得し、`go.mod`と`go.sum`を生成します。そのうえで、上記の再起動、CAS、migrationのtestを通した状態だけをmergeの対象にします。
