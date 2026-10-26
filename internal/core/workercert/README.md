# Worker証明書の名前と更新の時点

[ADR-0027](../../../plan/adr/0027-worker-authentication-and-enrollment.md)の2.3と2.4に従い、Workerのクライアント証明書に入れる名前の形と、更新する時点を定めます。鍵も署名も持たないので、Control PlaneとAgentのどちらからも読み込めます。署名するCAは[`workerca`](../../adapters/reference/workerca/README.md)にあります。

- 名前は`eki://<trust-domain>/worker/<worker-identity>`です。`URI`が作り、`ParseURI`が厳密に読み戻します。別のscheme、ポート、userinfo、query、fragment、余分なpathの区切り、登録簿が発行できない識別子は、すべて拒否します。Clientや管理者の名前（`/client/`、`/admin/`）はこの名前空間に入りません。
- `RenewAt`は、有効期間の残りが3分の1になる時点です。既定の有効期間は72時間（`DefaultValidity`）で、数値は仮の既定です。設定できる範囲は10秒から30日です。下限は、期限切れと更新を試験で速く確かめるためにあり、運用の推奨ではありません。
- `Fingerprint`と`ParseFingerprint`は、管理者がenrollment資格と一緒に渡す、Control Planeの指紋（証明書のDERのSHA-256）を扱います。コロン区切りや`sha256:`の接頭辞を受け付けます。
