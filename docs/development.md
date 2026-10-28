# 開発を始める方へ

この文書は、EKI FabricのReference Implementationをローカルで確かめるための、最短の手順です。

設計の正本ではありません。厳密な責務の境界は`architecture/`、Protocolは`spec/`、実装上の判断は`plan/adr/`を参照してください。

## 現在の実装段階

この節が、実装の現状（何が動き、何が動かないか）の正本です。他の文書は、ここを参照します。

M0 executable skeletonのうち、Client API、永続State Store、Worker CA・enrollment・mTLS、Worker→Control Planeの申告の配送までが動きます。Control PlaneからWorkerへLease・WorkOrderを配る経路と、Taskを実際に実行する経路は、まだありません。動かないものと、その作業は、次のIssueで管理しています。

- [#242](https://github.com/hat47x/eki-fabric/issues/242): `eki-control`の起動経路に、Lease発行・WorkOrder発行・結果受理の実行系を配線する
- [#243](https://github.com/hat47x/eki-fabric/issues/243): Control PlaneからWorkerへLease・WorkOrderを配る経路を、Worker Planeに定める
- [#245](https://github.com/hat47x/eki-fabric/issues/245): Clientの入力Artifactの取り込み経路を実装する単位に分ける
- [#267](https://github.com/hat47x/eki-fabric/issues/267): Taskのphaseを`admitted`から`ready`・`active`へ進める遷移の主体を決める

上の一覧は作業の例です。最新の状況は、GitHub Issueで確かめてください。

Task APIは`--config`で起動できます。Worker listenerは任意で、二つの認証の方式があります。

- `worker.auth: "mtls"`: Workerごとのクライアント証明書による認証です。管理者が発行した一回限りのenrollment資格でWorkerを登録し、Worker専用のCAが短命の証明書を発行します（[ADR-0027](../plan/adr/0027-worker-authentication-and-enrollment.md)の段階2）。loopback以外でも使えますが、TLSは必須です。手順は下の「Workerを登録する」にあります。
- 試験用profile（`auth`を省略、または`"static_token"`）: 静的Bearerの表を使う試験用で、#198の決定ではありません。試験用profileは、既定ではloopbackアドレスだけを受け付けます。loopback以外は、`worker.allow_test_profile_off_loopback`（仮の名前）を明示した場合に限り、TLSを付けたうえで許し、起動時に警告を出します（[ADR-0027](../plan/adr/0027-worker-authentication-and-enrollment.md) 2.5、[#224](https://github.com/hat47x/eki-fabric/issues/224)）。

現時点で入っているものは、次のとおりです。

- `cmd/eki-control`: Control Planeの実行ファイルの入口
- `cmd/eki-agent`: Native Worker Agentの実行ファイル。`enroll`でWorkerとして登録し、証明書（mTLS）で耐久queueの申告をControl Planeへ届ける。証明書は自動で更新する（Taskは実行しない）
- `internal/core/workercert`、`internal/adapters/reference/workerca`、`internal/agent/enroll`: Worker証明書の名前、Worker専用のCA、Agent側の登録と更新
- `internal/core/ids`: Task、Attempt、Leaseの型付きID
- `internal/core/contract`: SHA-256のDigestの値
- `internal/core/taskstate`: TaskRecordが最低限守るべき不変条件
- `internal/spi/statestore`: State Storeのinterface
- `internal/adapters/reference/statestore/memory`: CASの意味を確かめるための、メモリ上の参照アダプタ
- `internal/adapters/reference/statestore/sqlite`: 永続化する参照アダプタ（SQLite、WAL、migration）
- `internal/core/placementverify`: Placement Approvalの検証を、COSEのデコードから現在の状態での再検証までつなぐ
- `internal/core/workorderverify`: WorkOrderの署名、宛先、期限、Trust Bundleを、実行の許可と分けて確かめる
- `internal/core/recovery`: 回収後の再投入を、attempt budgetと時間窓の件数・転送量・計算量で区切る
- `internal/core/admission`: Taskの受理審査（厳格なJSON、P0スキーマの検証、ポリシー、冪等性、公開status、Cancel）
- `internal/control/clientapi`: P0のExternal Client PlaneのHTTP実装（認証、サイズの制限、Problem Details）
- `internal/control/service`: Control Planeの設定、起動、停止。任意で、試験用のWorker listenerを開く
- `conformance/process`: `eki-control`と`eki-agent`を別プロセスで起動し、申告の配送と、enrollment・mTLS・更新・失効を確かめる（本文の形式は試験用。#198の決定ではない）

メモリ上のアダプタは開発用です。プロセスを再起動すると状態が失われるため、M0のrestart gateを満たしません。restart gateを満たすのは、SQLiteのアダプタです。

## Workerを登録する（運用者向け）

次の`eki-control`のsubcommandは、**運用者がControl Planeのホスト上で直接実行する管理操作**です。設定ファイルが指す状態のDBを直接開くため、そのホストのアカウントを持つ人だけが使えます。Client APIにもWorker APIにも、同じ操作はありません（ADR-0027 2.2 項4）。`--admin`は、監査用の記録に残す名前の札で、認証ではありません。

~~~bash
# 1. Worker専用のCAを作る（信頼領域ごと。Lease・WorkOrderの署名鍵とは別に保管する）
eki-control gen-worker-ca --trust-domain example.org --cert-out worker-ca.pem --key-out worker-ca.key

# 2. 設定ファイルのworkerに、auth: "mtls"と、mtlsの設定（trust_domain、ca_cert_file、ca_key_file）、
#    listenerのTLS証明書（tls）を書いて起動する。sessions（静的Bearerの表）は書けない。

# 3. 一回限り・24時間（既定）のenrollment資格を発行する。秘密は一度だけ表示される
eki-control enroll-token --config control.json --ttl 24h
#   enrollment_token: eki_enroll_v1_...   ca_fingerprint: <sha256>

# 4. Worker側で登録する。トークンは0600のファイルに置く。鍵は端末の中で作られる
eki-agent enroll --control-url https://control.example.org:9443 \
    --ca-fingerprint <sha256> --token-file enroll.token --state-dir /var/lib/eki-agent

# 5. 登録した状態ディレクトリで配送を始める。証明書は残り3分の1で自動更新される
eki-agent --control-url https://control.example.org:9443 --queue-db agent.db --state-dir /var/lib/eki-agent

# Workerを止める（無効化。registeredで戻せる）、または失効する（最終。次の要求から拒否される）
eki-control worker-state --config control.json --worker wrk_... --state disabled
eki-control worker-state --config control.json --worker wrk_... --state revoked
~~~

`ca_fingerprint`は、`worker.tls.cert_file`が送る証明書の鎖の、いちばん上の証明書のSHA-256です。Agentは、公開CAではなくこの指紋でControl Planeを確かめ、一致しなければ資格を送らずに止まります。管理者は、資格と指紋を、信頼できる経路でWorkerの持ち主へ渡してください。有効期間（既定72時間）を超えて停止していた端末は更新できません。新しい資格で登録し直し、新しい識別子になります。

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

`eki-control`は`--config`なしでは、`eki-agent`は`--control-url`や`--queue-db`なしでは、何も起動せずに表示して終了します。`eki-agent`の試験用の起動は、`conformance/process/README.md`を参照してください。

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

Attempt・Leaseの永続化（`internal/adapters/reference/statestore/sqlite`）と、LSE-1（`internal/core/leaseissue`）は実装済みです。次の作業は、固定の文章にせず、「現在の実装段階」の節に挙げたIssueとGitHub Issueの一覧を参照してください。

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
