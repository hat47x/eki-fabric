# Workerが受け取った割当の検証と開始の関門（試験用profile）

Control Planeが配る署名済みのLeaseとWorkOrderを、Workerが自分の信頼の起点と自分の状態に照らして確かめ、開始してよいものだけを実行側（`Handler`）へ渡します。通信は#198のWorker Protocolではなく、試験用profileです（`POST /test-profile/v1/assignments`）。このpackage自体は実行せず、開始してよいと判断した割当を`Handler`へ渡すだけです。固定コマンドの実行は、`Handler`の実装である[`service`](../service/README.md)の`assignments.execution`と、[`nativerunner`](../nativerunner/README.md)が担います。

受け取ったbytesは、確かめるまで信用しません。`Service.Poll`は割当ごとに次を順に確かめます。

1. `leaseverify`: このWorker宛てで、信頼の起点に載るLSE-1の鍵の署名があり、有効期間内で、Trust Bundleが新しいこと。
2. LeaseとWorkOrderがWorkerの受け入れるPolicy revisionを名指すこと。
3. `workorderverify`と`workorderaccept`: このWorker宛てで、WOR-1の鍵の署名があり、そのLease（Decision IDとpayload digest）に結び付き、Workerが自分で承認したExecutor Approvalを名指し、Taskのfenceが受理済みの最大より古くなく、一回限りのnonceをWorker自身の耐久的な重複検査に登録できること（#296 項目3）。
4. `startgate`: Local Governorと機械の容量が開始を許し、実効の上限が、付与された上限とローカルの天井の小さい方になること。

荷札のIDは参考情報です。署名済みのLeaseと食い違えば拒否します。

結果は`Outcome`で返ります。`ready`（`Handler`へ渡した）、`replayed`（nonceは使用済み。同じbytesが再配送されたときの通常の答え）、`deferred`（Local Governorが新規を受け付けない、または開始の関門が容量や期限で拒否した。nonceは消費していないので、Control Planeは同じTaskに新しいnonceで再発行できる）、`rejected`（理由付き）です。nonceとfenceは、署名・結び付き・承認・開始関門の確認がすべて通ってから記録します。改ざんや食い違いのbytesはどちらも記録しません。開始関門の拒否も記録しません（#296 項目2の修正）。

再起動をまたぐ重複排除は二層です。同じAttemptの再配送はnonceの記録が`replayed`に落とし、同じTaskの古いAttempt（fenceが古い）はfenceの記録が`work_order_superseded`で拒否します。メモリ上の処理済み記録はAttempt単位で再起動で消えますが、この二つの記録は`replayguard`に残ります。拒否された割当は処理済みにしないので、期限が切れるまで毎回、再判定します。

## 確かめないこと

- **Placement Approval**: 署名つきbytesをControl Planeが保存していない（decision IDとdigestだけ）ため、Workerは独立に検証できません。`workorderaccept`に渡す配置の証拠はWorkOrder自身の参照から作るので、その比較は実質的に意味を持ちません。検証したとは主張しません。
- Policy revisionの現在性は、信頼の起点のファイルが名指す値との一致だけです。取り消しの伝播は#78の未決です。
- Local Governorは固定値（`StaticGovernor`）です。実際の`agent/governor`はAgentのプロセスにまだつながっていません。
- Executor Approval（EXE-1）は署名されていません。Workerの設定にある一覧との一致だけです。
