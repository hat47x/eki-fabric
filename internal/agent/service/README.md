# Agentの申告配送ループ（試験用profile）

`cmd/eki-agent`が呼ぶ、申告の配送ループです。Agent専用のSQLite queue（`claimqueue`の参照Adapter）を開き、`claimqueue.DeliverDue`を一定間隔で回して、`claimclient.HTTPClient`でControl Planeへ送ります。

通信の本文は**試験用のprofile**です。Worker Protocolは未決で（[#198](https://github.com/hat47x/eki-fabric/issues/198)）、本文の形式は決定ではありません。Taskは実行しません。

認証には二つの方式があります。

- **証明書（mTLS）**: `state_dir`（`--state-dir`）を指定します。`eki-agent enroll`が書いた鍵と証明書で接続し、有効期間の残りが3分の1になると自動で更新します。Control Planeは、enrollmentのときに固定した証明書だけを信頼します。識別子と信頼領域は証明書から読むので、`worker_identity`は書かなくてよく、書く場合は証明書と一致させます。トークンとCAファイルは同時に指定できません。期限が切れていれば、起動せずに、再登録が必要であると伝えて失敗します。詳細は[`enroll`](../enroll/README.md)を参照してください。
- **静的Bearer**: 試験用のprofileです。トークンはログに出しません。

- 失敗した申告はqueueに残り、`RetryBase`から`RetryMax`までの指数的な待ちで再送します。Agentを再起動しても、待ちの計画は保持されます。
- contextの取消（SIGINT・SIGTERM）でループを止め、queueを閉じて正常に終了します。送信中の申告は、queueに残るので失われません。
- 設定の誤り（必須項目の欠落、不正なURL、読めないCA）は、queueを開く前に失敗します。トークンはログに出しません。
