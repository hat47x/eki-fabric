# 開発を始める方へ

この文書は、EKI FabricのReference Implementationをローカルで確かめるための、最短の手順です。

設計の正本ではありません。厳密な責務の境界は`architecture/`、Protocolは`spec/`、実装上の判断は`plan/adr/`を参照してください。

## 現在の実装段階

現在は、M0 executable skeletonの最初の段階です。

まだTask APIもWorker sessionも起動しません。

現時点で入っているものは、次のとおりです。

- `cmd/eki-control`: Control Planeの実行ファイルの入口
- `cmd/eki-agent`: Native Worker Agentの実行ファイルの入口
- `internal/core/ids`: Task、Attempt、Leaseの型付きID
- `internal/core/contract`: SHA-256のDigestの値
- `internal/core/taskstate`: TaskRecordが最低限守るべき不変条件
- `internal/spi/statestore`: State Storeのinterface
- `internal/adapters/reference/statestore/memory`: CASの意味を確かめるための、メモリ上の参照アダプタ
- `internal/adapters/reference/statestore/sqlite`: 永続化する参照アダプタ（SQLite、WAL、migration）
- `internal/core/placementverify`: Placement Approvalの検証を、COSEのデコードから現在の状態での再検証までつなぐ
- `internal/core/admission`: Taskの受理審査（厳格なJSON、P0スキーマの検証、ポリシー、冪等性、公開status、Cancel）
- `internal/control/clientapi`: P0のExternal Client PlaneのHTTP実装（認証、サイズの制限、Problem Details）
- `internal/control/service`: Control Planeの設定、起動、停止

メモリ上のアダプタは開発用です。プロセスを再起動すると状態が失われるため、M0のrestart gateを満たしません。restart gateを満たすのは、SQLiteのアダプタです。

## 必要なGoのバージョン

Reference Implementationの`go.mod`は、Go 1.27.0を要求します。

Goのパッチリリースは、できるだけ新しいものを使ってください。

## 最小限の確認

リポジトリのルートで、次を実行します。

~~~bash
go test ./...
go vet ./...
go run ./cmd/eki-control --version
go run ./cmd/eki-agent --version
~~~

出力されるバージョンは、次のとおりです。

~~~text
eki-control dev
eki-agent dev
~~~

`--version`を付けずに実行した場合、現在はlistenerを開かず、M0 skeletonであることを表示して終了します。

## テストの読み方

### `internal/core/ids`

生成したIDが、prefixと128bitの乱数のsuffixを持つことを確かめます。誤ったprefixや壊れた16進の文字列は、拒否されます。

### `internal/core/taskstate`

新しいTaskが`admitted`とrevision 1から始まることを確かめます。また、終端の結果が、終端ではない記録に紛れ込まないことも確かめます。

### メモリ上のState Store

次の点を確かめます。

- 作成と読み込み
- revisionを使ったCompare-And-Swap
- 古い書き込み側の拒否
- 同じTaskを重複して作ることの拒否
- キャンセルされたcontextの尊重

ここで大切なのは、Mapを使った実装そのものではありません。将来の永続的なStoreでも、同じSPIの意味を守れることです。

## ArchitectureとGoのpackageの関係

Goのpackageは、External APIではありません。

~~~text
spec/
  言語に依存しない契約の正本

internal/
  Goによる参照実装
~~~

という関係です。

`internal/core`の型を、そのままJSONにserializeして、公開APIにしないでください。

External Client Planeは、`spec/external/openapi.json`を基準にします。

## 新しいpackageを追加するとき

先に、`plan/IMPLEMENTATION_REPOSITORY_LAYOUT.md`を確認してください。

特に、次のことは避けてください。

- `internal/core`から、SQLite、NATS、S3のSDKを直接importする
- Schedulerから、署名用の具体的な鍵を直接扱う
- HTTPのhandlerから、Taskの状態を直接書き換える
- State Storeのアダプタに、retryやTaskの失敗についての意味の判断を持たせる
- `pkg/`にCoreの型を公開して、互換性を保つべき境界を増やす

## ローカルでの検証とCI

現在、GitHub Actionsは、プロジェクト側の利用残量の都合で失敗することがあります。そのため、CIの成否だけを、実装のEvidenceにはしません。

変更するときは、少なくとも、ローカルで再現できる`go test ./...`と`go vet ./...`の結果を残してください。

### 検証スクリプト

整形、静的検査、Testを一度に確かめるには、リポジトリのルートで次を実行します。

~~~bash
./scripts/verify.sh          # gofmt / go vet / go test
./scripts/verify.sh --quick  # gofmt / go vet だけ
~~~

Windowsでは、`scripts\verify.ps1`を使います。Windowsの既定の実行ポリシーでは、スクリプトを直接起動できない場合があります。その場合は、次のように実行ポリシーを明示的に緩めて起動します。

~~~powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\verify.ps1 -Quick
~~~

どちらのスクリプトも、最後に`VERIFY: PASS`または`VERIFY: FAIL`を出力します。PRには、この行と、失敗した場合の差分を貼ってください。

`gofmt`の判定は、CRLFの作業ツリーでもLFと同じ結果になるようにしています。改行コードの違いを整形の差分として扱いません。

### 検証の記録として残すもの

PRの本文には、次の3点を残します。

1. 実行したコマンド（`scripts/verify.sh`など）と、Goのバージョン
2. `VERIFY: PASS`、または失敗したTest名と差分
3. 実行していない検証と、その理由

「実行環境がないため未検証」と書く場合も、どのコマンドを実行できなかったのかを具体的に書いてください。未検証の依存関係やlockファイルを、検証済みとしてcommitしないでください。

## Branchの運用

作業は、最新の`main`から目的ごとのbranchを切って進めます。レビューできる状態になったら、PRで`main`へ戻します。

マージされていないbranchから、次の実装のbranchを派生させません。

詳しくは、`AGENTS.md`を参照してください。

## 次の実装

M0とDP-1の、参照用の永続的なState Storeには、SQLiteを採用しています。選んだ理由と、適用できる範囲は、`plan/adr/0003-m0-reference-state-store.md`を参照してください。

この参照アダプタは、単一のControl Plane hostのローカルなファイルシステム向けです。SQLiteのファイルをNFSやSMBなどのネットワークファイルシステムに置いて、複数のhostでControl Planeを構成する用途には使いません。

SQLiteの参照アダプタは`internal/adapters/reference/statestore/sqlite`にあり、`modernc.org/sqlite`のv1.60.1を使います。共通のState Storeの契約とDurabilityの契約を通っています。migration、WAL、再起動後もTaskのidentityとrevisionが保たれることも、確認済みです（2026-10-06、Go 1.27.0 / Linux）。

厳格なCOSE_Sign1のデコーダとSignature1のエンコーダは、`internal/adapters/reference/cosedecoder/cbor`にあり、`fxamacker/cbor/v2`を使います。共通のconformance vectorを通っています。

Taskのsubmit、status、cancelは、`eki-control --config`で起動するClient APIとして、実装済みです（[ADR-0006](../plan/adr/0006-m0-client-api-authentication-and-admission.md)）。起動の方法は、次の節を参照してください。

次の作業単位は、AttemptとLeaseの永続化と、LSE-1（Lease Authority）です。

## Control Planeを起動する

設定ファイルを指定すると、Client APIが起動します。指定しなければ、何も起動せずに終了します。

~~~bash
# 1. クライアント用のtokenを作る。tokenは一度しか表示されません。
go run ./cmd/eki-control gen-token

# 2. 設定ファイルを書く（下の例）。tokenそのものではなく、sha256の値だけを書きます。

# 3. 起動する。
go run ./cmd/eki-control --config control.json
~~~

設定ファイルの例です。

~~~json
{
  "listen": "127.0.0.1:8443",
  "database": "state.db",
  "tokens": [
    { "principal": "alice", "role": "client", "token_sha256": "<gen-tokenが表示したsha256>" }
  ],
  "policy": {
    "approved_executors": ["dev.example.transform"],
    "per_task": {
      "cpu_millis": 4000,
      "memory_bytes": 1073741824,
      "wall_time_seconds": 3600,
      "output_bytes_max": 1073741824,
      "network_bytes_max": 1073741824,
      "temporary_disk_bytes": 1073741824
    },
    "max_attempts": 5,
    "max_outstanding_tasks": 10
  }
}
~~~

- 設定ファイルに未知のfieldがあると、起動しません。設定名の綴りを間違えても、黙って既定値に置き換わってしまうことを防ぐためです。
- TLSなしの平文のHTTPで起動できるのは、loopbackアドレスだけです。それ以外のアドレスで起動するには、`tls`を設定してください。手前で信頼できるproxyがTLSを終端する場合に限り、`allow_insecure_listen`を明示してください。
- `max_outstanding_tasks`は必須です。上限のないTaskの受け付けは作れません。
- `database`は、ローカルのファイルシステムに置いてください。ファイルは、所有者だけが読める権限で作られます。
- M0の認証は、固定のBearer tokenです。失効や期限はありません。詳しい内容と制限は、[ADR-0006](../plan/adr/0006-m0-client-api-authentication-and-admission.md)を参照してください。

提出の例です。

~~~bash
curl -s -X POST http://127.0.0.1:8443/v1/tasks \
  -H "Authorization: Bearer $EKI_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: my-first-task-0001" \
  --data @spec/external/examples/valid-task-request.json
~~~

この例には入力のArtifactが含まれています。そのため、Artifact Storeがない現在の構成では、`422`になります。`inputs`を空にしたrequestで確かめてください。
