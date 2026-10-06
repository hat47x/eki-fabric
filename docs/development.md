# 開発を始める方へ

この文書は、EKI FabricのReference Implementationをローカルで確認するための最短経路です。

設計の正本ではありません。厳密な責務境界は`architecture/`、Protocolは`spec/`、実装判断は`plan/adr/`を参照してください。

## 現在の実装段階

現在はM0 executable skeletonの最初の段階です。

まだTask APIやWorker sessionは起動しません。

現時点で入っているものは次です。

- `cmd/eki-control` — Control Plane executableのentry point
- `cmd/eki-agent` — Native Worker Agent executableのentry point
- `internal/core/ids` — Task / Attempt / Leaseのtyped ID
- `internal/core/contract` — SHA-256 Digest value
- `internal/core/taskstate` — TaskRecordの最小不変条件
- `internal/spi/statestore` — State Store interface
- `internal/adapters/reference/statestore/memory` — CAS semanticsを確認するin-memory reference adapter
- `internal/adapters/reference/statestore/sqlite` — durable reference adapter（SQLite、WAL、migration）

in-memory adapterは開発用です。Process restartで状態を失うため、M0のrestart gateを満たしません。restart gateはSQLite adapterが満たします。

## 必要なGo version

Reference Implementationの`go.mod`はGo 1.27.0を要求します。

Goのpatch releaseは可能な限り最新を利用してください。

## 最小確認

Repository rootで次を実行します。

~~~bash
go test ./...
go vet ./...
go run ./cmd/eki-control --version
go run ./cmd/eki-agent --version
~~~

期待するversion出力:

~~~text
eki-control dev
eki-agent dev
~~~

`--version`を付けずに実行しても、現在はlistenerを開かず、M0 skeletonであることを表示して終了します。

## Testの読み方

### `internal/core/ids`

生成したIDがprefixと128-bit random suffixを持ち、誤ったprefixや壊れたhexをrejectすることを確認します。

### `internal/core/taskstate`

新しいTaskが`admitted` / revision 1から始まり、terminal outcomeがnon-terminal recordへ紛れ込まないことを確認します。

### in-memory State Store

次を確認します。

- Create / Load
- revisionを使ったCompare-And-Swap
- stale writerのreject
- duplicate Task createのreject
- cancelled contextの尊重

ここで重要なのはMap実装そのものではなく、将来のdurable Storeでも同じSPI semanticsを守れることです。

## ArchitectureとGo packageの関係

Go packageはExternal APIではありません。

~~~text
spec/
  language-neutral contractの正本

internal/
  Go Reference Implementation
~~~

という関係です。

`internal/core`の型をそのままJSONへserializeしてPublic APIにしないでください。

External Client Planeは`spec/external/openapi.json`を基準にします。

## 新しいpackageを追加するとき

先に`plan/IMPLEMENTATION_REPOSITORY_LAYOUT.md`を確認してください。

特に次を避けます。

- `internal/core`からSQLite / NATS / S3 SDKを直接importする
- Schedulerからconcrete signing keyを直接扱う
- HTTP handlerからTask stateを直接書き換える
- State Store adapterにretry / Task failureの意味判断を持たせる
- `pkg/`へCore型を公開して互換性境界を増やす

## Local validationとCI

現在、GitHub ActionsはProject側の利用残量条件により失敗する場合があるため、CIの成否だけを実装Evidenceにしません。

変更時には少なくともローカルで再現可能な`go test ./...`と`go vet ./...`を残してください。

M0 skeleton作成時には、Repositoryの要求versionより古いGo 1.23.2でも同一sourceのcompatibility smoke testが通ることを確認しました。ただしこれはsupported versionを1.23へ下げる意味ではありません。

## Branch運用

作業は最新`main`から目的単位のbranchを切り、レビュー可能になったらPRで`main`へ戻します。

未マージbranchから次の実装branchを派生させません。

詳細は`AGENTS.md`を参照してください。

## 次の実装

M0 / DP-1のReference durable State StoreにはSQLiteを採用します。選定理由と適用境界は`plan/adr/0003-m0-reference-state-store.md`を参照してください。

Reference Adapterは単一Control Plane hostのlocal filesystem向けです。SQLite fileをNFS / SMB等のnetwork filesystemへ置いてmulti-host Control Planeを構成する用途には使いません。

SQLite Reference Adapterは`internal/adapters/reference/statestore/sqlite`にあり、`modernc.org/sqlite` v1.60.1を使います。共通のState Store契約とDurability契約を通り、migration、WAL、restart後のTask identityとrevisionの保持を確認済みです（2026-10-06、Go 1.27.0 / Linux）。

strict COSE_Sign1 decoderとSignature1 encoderは`internal/adapters/reference/cosedecoder/cbor`にあり、`fxamacker/cbor/v2`を使います。共通のconformance vectorを通ります。

次の作業単位は、M0 vertical sliceの最初の部分（Task submit / status）を、durable state上で接続することです。
