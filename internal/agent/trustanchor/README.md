# Workerの信頼の起点（ファイル）

Leaseと WorkOrderを検証する公開鍵とTrust Bundleを、ローカルのJSONファイルから読みます。Control Planeの配送に添えられた鍵は信頼しません。ファイルがない、読めない、未知の項目がある、LSE-1またはWOR-1の鍵がない、Ed25519以外、鍵の形式が誤っている、のどれでも`ErrInvalid`で失敗し、Agentは起動しません。

項目は、信頼領域、受け入れるPolicy revision、bundleのrevisionと検証時刻（`validated_at`。`max_bundle_staleness_seconds`を超えて古いと検証が`TRUST_BUNDLE_STALE`で失敗する）、許容する時計のずれ、決定の最長の寿命、鍵の一覧（ID、役割、主体、Ed25519公開鍵のbase64、署名の有効期間）です。

これは試験用profileの形式です。鍵の配布、更新、失効、bundleの検証を誰が行うかは未決です（#78）。
