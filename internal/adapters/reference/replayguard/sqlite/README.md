# SQLiteに保存するnonceの使用済み記録

Workerを再起動しても、期限内のWorkOrderを同じnonceで受理し直さないための、`replayguard.Guard`の参照実装です。同じローカルDBを開いた複数のプロセスでも、同じnonceの取得に成功するのは一つだけです。

`Open(ctx, path, clock, maxEntries)`で、Workerごとの専用ファイルを開きます。署名検証、参照先の照合、nonceの取得順は既存の`workorderaccept`が担います。このAdapterの追加だけでは、Workerの起動設定や既存のメモリ実装は切り替わりません。

## 保存と排他

`Claim`は、書込みロックを取ってから信頼する時計を読み、次を同じトランザクションで行います。

1. 期限に達した記録を削除する。
2. nonceの使用済み記録を照合する。期限内なら`false`を返す。
3. 残る件数と容量を比較する。満杯なら`ErrGuardCapacity`で拒否し、期限内の記録を追い出さない。
4. nonceのSHA-256と期限を保存し、commitが成功した後にだけ`true`を返す。

生のnonceは保存しません。16〜32 byteのnonceを受け取り、期限をUTCのUnixナノ秒で記録します。時計と期限は、この形式で表せる1970年より後の時刻である必要があります。期限ちょうどでは記録は失効し、同じnonceを再利用できます。重複した呼出しで元の期限を延ばすことはありません。

容量とschema versionもDBに保存します。別接続や再起動時に違う容量を指定した場合は、既存の記録を変えずに`Open`を拒否します。書込みはimmediate transaction、WAL、`synchronous=FULL`を使います。接続内の直列化だけに頼らず、DBのロックで別プロセスとの競合も防ぎます。

Context取消、読取り・書込み・commitの失敗は、成功として扱いません。commitのエラーでは記録されたかどうかを断定できないため、呼出し側も実行を拒否する必要があります。

## 保証の範囲

- ローカルのファイルシステムと、flush要求を守る記憶装置を前提とします。ネットワークファイルシステムや電源断の実機試験を確認したものではありません。
- ファイルの削除・古い版への復元・権限のないSQL変更は防げません。Workerの所有者がファイルのアクセス権とバックアップの扱いを管理します。異なるWorkerのnonce空間は、別の専用ファイルに分けます。
- 時計の正しさは呼出し側の信頼基盤です。時計を進めて記録を削除した後に時刻を巻き戻す操作への保護は、このAdapterでは提供しません。
- 使用済み記録は、Taskを開始・完了した証拠ではありません。取得後、実行前に落ちた場合もnonceは消費済みのままです。実行の一回性、Admission・Outcomeとの原子的な保存、Lease更新時の再受理は保証しません。

## 検証

```bash
go test -race -count=1 ./internal/adapters/reference/replayguard/sqlite
```

再オープン後の再利用拒否、容量の保持、別接続と別プロセスの同時取得、Closeせずに終了した子プロセスの記録の保持、期限境界、保存失敗とContext取消後の再取得を確認します。

`internal/core/workorderaccept`の試験では、既存の合成FixtureへこのAdapterを注入し、署名を改変したWorkOrderがnonceを消費しないこと、有効なWorkOrderの受理後は再オープンしても同じnonceを拒否すること、満杯やDBの停止を受理成功として扱わないことを確認します。Leaseなどの参照先Evidenceは合成データで、実Control PlaneやExecutorの起動を確認したものではありません。
