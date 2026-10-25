# Agentの申告配送ループ（試験用profile）

`cmd/eki-agent`が呼ぶ、申告の配送ループです。Agent専用のSQLite queue（`claimqueue`の参照Adapter）を開き、`claimqueue.DeliverDue`を一定間隔で回して、`claimclient.HTTPClient`でControl Planeへ送ります。

これは**試験用のprofile**です。Workerの認証・登録とWorker Protocolは未決で（[#198](https://github.com/hat47x/eki-fabric/issues/198)）、ここのトークンと本文は決定ではありません。Taskは実行しません。

- 失敗した申告はqueueに残り、`RetryBase`から`RetryMax`までの指数的な待ちで再送します。Agentを再起動しても、待ちの計画は保持されます。
- contextの取消（SIGINT・SIGTERM）でループを止め、queueを閉じて正常に終了します。送信中の申告は、queueに残るので失われません。
- 設定の誤り（必須項目の欠落、不正なURL、読めないCA）は、queueを開く前に失敗します。トークンはログに出しません。
