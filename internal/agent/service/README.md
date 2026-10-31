# Agentの申告配送ループ（試験用profile）

`cmd/eki-agent`が呼ぶ、申告の配送ループです。Agent専用のSQLite queue（`claimqueue`の参照Adapter）を開き、`claimqueue.DeliverDue`を一定間隔で回して、`claimclient.HTTPClient`でControl Planeへ送ります。

通信の本文は**試験用のprofile**です。Worker Protocolは未決で（[#198](https://github.com/hat47x/eki-fabric/issues/198)）、本文の形式は決定ではありません。申告の配送ループ自体はTaskを実行しません。割当の受け取りと、任意の固定コマンドの実行は、下の二つの節にあります。

認証には二つの方式があります。

- **証明書（mTLS）**: `state_dir`（`--state-dir`）を指定します。`eki-agent enroll`が書いた鍵と証明書で接続し、有効期間の残りが3分の1になると自動で更新します。Control Planeは、enrollmentのときに固定した証明書だけを信頼します。識別子と信頼領域は証明書から読むので、`worker_identity`は書かなくてよく、書く場合は証明書と一致させます。トークンとCAファイルは同時に指定できません。期限が切れていれば、起動せずに、再登録が必要であると伝えて失敗します。詳細は[`enroll`](../enroll/README.md)を参照してください。
- **静的Bearer**: 試験用のprofileです。トークンはログに出しません。

- 失敗した申告はqueueに残り、`RetryBase`から`RetryMax`までの指数的な待ちで再送します。Agentを再起動しても、待ちの計画は保持されます。
- contextの取消（SIGINT・SIGTERM）でループを止め、queueを閉じて正常に終了します。送信中の申告は、queueに残るので失われません。
- 設定の誤り（必須項目の欠落、不正なURL、読めないCA）は、queueを開く前に失敗します。トークンはログに出しません。

## 割当の受け取りと検証（試験用profile、`assignments`）

設定の`assignments`を与えると、Agentは申告の配送と並行して`POST /test-profile/v1/assignments`を呼び、署名済みのLeaseとWorkOrderを受け取って検証し、開始の関門を通します（[`assignment`](../assignment/README.md)）。`state_dir`（mTLS）が必須で、無ければ起動しません。信頼の起点のファイル（[`trustanchor`](../trustanchor/README.md)）、nonceの耐久DB（`replay_db`）、承認するExecutor、機械の容量、天井の割合は、すべて必須で、欠けると、何も開く前に失敗します。

結果は`events_file`にJSON Linesで追記します（`ready`、`replayed`、`deferred`、`rejected`と理由）。`ready`は、検証・受け入れ・開始の関門を通り、実行側のインターフェースへ渡した、という意味です。

## 固定コマンドの実行（`assignments.execution`）

`execution`を与えると、`ready`の割当を固定コマンドで実行します。コマンドと引数はWorkerの信頼済み設定であり、Task入力から組み立てません。

- 実行結果は`events_file`へ`execution_status`、`exit_code`、`output_bytes`、`output_digest`、`failure`として追記します。
- 期限・取消し・出力上限・プロセスツリー停止は`native-runner`が扱います。
- **sandboxではありません。** CPU・メモリ・一時ディスクは実行中に20ms間隔で監視し、上限超過時はプロセスツリーを停止します（Linux/Windows）。networkとGPUの上限は強制しません。一時ディスクは実行ごとの一時ディレクトリ配下のみを対象とします。
- 実行後、観測した`ResultClaim`は、送る前にAgentの耐久queueへ記録します（`result_claim_queued`）。送信はqueueの配送ループが行い、Control Planeの停止・5xx・切断で失敗してもAgentを再起動しても、届くまで再送します。
- 送信の各試行は`events_file`へ残します。失敗は`result_claim_delivery_failed`（`failure`に理由）、届いた回は`result_claim_submitted`（ackを`claim_decision`に）です。`result_claim_queued`だけで`result_claim_submitted`がない間は、申告はAgentの手元にあり、Control Planeへまだ届いていません。
- **失われ得る範囲。** 実行の終了からqueueへの記録（`Enqueue`）までの間にAgentが落ちると、観測した結果は残りません。nonceは開始の関門より前に消費済みなので、同じ割当の再実行はできません。この場合の判定はControl Plane側の`claimreconcile`（Lease期限後）に委ねます。実行の開始前に「実行中」を耐久化するかは未決で、#319に残しています。
- ResultClaimは受理結果ではありません。Result VerificationとResult Acceptanceは別の段階です。

`execution`を省略した場合、Agentは割当の検証結果を記録するだけで、何も実行しません。
