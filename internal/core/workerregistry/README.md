# Worker登録簿とenrollment資格の記録

[ADR-0027](../../../plan/adr/0027-worker-authentication-and-enrollment.md)の段階1です（Issue [#222](https://github.com/hat47x/eki-fabric/issues/222)）。Control Planeが「どのWorkerの識別子が存在し、どの信頼領域に属し、まだ動いてよいか」を耐久的に覚えておくための記録と規則を置きます。操作は[`workerenrollment`](../workerenrollment/README.md)、保存は`statestore.WorkerRegistryStore`のAdapterが担います。

このpackageには保存も時計もありません。遷移の規則をここに一つだけ置き、memoryとSQLiteの両Adapterがトランザクションの中で同じ規則を呼びます。

## 何を守るか

登録簿が答えるのは、申告が誰のものかという問いだけです。登録されたWorkerも悪意を持ち得るので、申告の内容が正しいことの根拠にはなりません（RQ-019、RQ-020、SEC-003）。登録はWorkerの同一性だけを作り、Delegation Classも権限も与えません（ADR-0027 §2.2 項9）。

## Worker

- `Identity`は登録簿が発行する不透明な識別子（`wrk_`と128bitの乱数）です。Workerは選べず、失効した後も別のWorkerに発行しません。行は削除しません。
- `State`は`registered`、`disabled`、`revoked`です。動いてよいのは`registered`だけで、`Usable()`がそれを返します。`disabled`は管理者が`registered`へ戻せます。`revoked`は終端で、どの状態へも戻せません。
- `Revision`は1から始まり、変更のたびに1だけ増えます。`StateChangedBy`は`admin:<id>`か、自己失効の`worker:<identity>`です。
- `Certificate`は、段階2のCAが記録する現在の証明書の通し番号と有効期限です。段階1では空です。
- `UpdatedAt`は、すでに記録された時刻より前になりません。壁時計は巻き戻り得る（NTPの補正、VMの同期、別ホストでの再起動）ため、`LaterOf`で保ちます（#226と同じ扱い）。

## enrollment資格

- 秘密は256bitの乱数で、`eki_enroll_v1_`で始まります。この接頭辞は、秘密を探す走査がログやリポジトリに漏れた秘密を見つけられるようにするためです。
- 保存するのはSHA-256だけです。秘密は発行の結果に一度だけ現れ、`Secret`型は`String`、`%v`、JSON、`slog`のどれでも伏せ字になります。
- 資格は信頼領域に束縛され、発行者（`admin:<id>`）と発行・期限の時刻を持ちます。有効期間は既定24時間、上限7日で、上限は`Validate`とDBのCHECKの両方が拒否します。
- 状態は`issued`、`consumed`、`revoked`です。`expired`は保存せず、`Status(now)`が、期限の時刻以後の`issued`から導きます。
- 一回限りです。使うとWorkerの作成と同じトランザクションで`consumed`になります。

## 拒否の外部と内部

期限切れ、失効済み、使用済み、別の信頼領域、未知、形式の誤りの資格は、すべて同じ`*Refusal`になります。文面と`errors.Is(err, ErrCredentialRefused)`は理由によらず同一なので、呼び出し側は推測した秘密が存在したかどうかを知れません。監査ログには`RefusalReason(err)`で内部の理由を書き、秘密を提示した相手には返しません。

資格はダイジェストで引き、引いた行の照合も定数時間で行います。

## 段階2に続くもの

CA、enrollmentのendpoint、mTLSのauthenticator、要求ごとの登録簿の確認の配線、プロセス単位の通し試験は、段階2の別のPRで加えます。
