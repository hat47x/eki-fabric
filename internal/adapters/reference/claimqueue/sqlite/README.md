# Workerの申告と再送予定をSQLiteへ保存する

`claimqueue.Store`を実装する参照Adapterです。Workerを再起動しても、未送信のResultClaimと、送信に失敗した後の再送予定を保持します。`Open(ctx, path)`でWorkerごとの専用ローカルDBを開き、`claimqueue.Config.Store`へ渡します。既存のメモリ実装やAgentの起動設定は、自動では切り替えません。

## 保存するもの

- Claimの全項目と、そのdigest。
- 最初にqueueへ入れた時刻。
- 再送回数、次回時刻、直前の送信エラー。

Claimの保存形式は、このAdapter専用のJSONです。Worker Planeの通信形式ではありません。64 bitの符号なし値は、SQLiteの符号付き整数へ丸めずに保存します。読取り時はClaimを再構築し、digestとAttempt IDが保存行に一致するか確かめます。壊れた行を空のqueueとして扱いません。

時刻の検索にはUTCのUnixナノ秒を使います。queueへの登録時刻、次回時刻、検索時刻は、正のUnixナノ秒で往復できる範囲に限ります。送信エラーの本文やnonceもDBに残るため、ファイルのアクセス権と保管先はWorkerの所有者が管理します。

## 再送の状態を戻さない

同じAttemptの異なるClaimは`ErrConflict`で拒否します。Claim本体と登録時刻は不変で、変更できるのは再送の状態だけです。登録直後の状態を再び`Save`しても、既存の回数や次回時刻を初期化しません。古い回数への更新や、同じ回数で異なる次回時刻への更新も拒否します。

照合と更新は、一つのimmediate transactionで行います。WALと`synchronous=FULL`を使い、保存・削除が完了してから呼出し側へ成功を返します。`Delete`はAttempt IDとdigestの両方が一致する行だけを削除します。`Due`は次回時刻、Attempt IDの順に、指定件数まで返します。

このAdapterは、複数の送信処理を一つに束ねる所有権ロックではありません。queueは従来どおり逐次利用し、二重配送が起きた場合はControl Plane側の重複判定に委ねます。再送回数が`uint32`の上限に達した状態で送信に失敗した場合、queueは初期値へ戻さずエラーにし、申告を保持します。

## 確認した範囲と残る条件

試験では、全7種のClaimの復元、再送回数・時刻・エラーの保持、別接続からの競合保存、digestを照合した削除、壊れた保存行の拒否を確認します。子プロセスが保存後にCloseせず終了した場合も、親プロセスが記録を復元します。

実queueとの接続では、受信後に応答を失う送信先を合成し、再オープンしても同じClaimを保持すること、予定時刻前には再送せず、成功後には削除が残ることを確認します。実ネットワーク、認証、Control Planeの受理、Executorの動作を証明するものではありません。

ローカルファイルシステムとflush要求を守る記憶装置を前提にします。電源断、ネットワークファイルシステム、ファイルの削除や古い版への復元は検証していません。Outcome生成とqueue登録の間は同じトランザクションではなく、登録前のcrashによる欠落は防ぎません。保存容量の上限や履歴の暗号化も、このAdapterでは提供しません。

```bash
go test -race -count=1 ./internal/adapters/reference/claimqueue/... ./internal/agent/claimqueue ./internal/agent/claimclient
```
