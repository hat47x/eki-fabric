# Workerが受け取った割当の検証と開始の関門（試験用profile）

Control Planeが配る署名済みのLeaseとWorkOrderを、Workerが自分の信頼の起点と自分の状態に照らして確かめ、開始してよいものだけを実行側（`Handler`）へ渡します。通信は#198のWorker Protocolではなく、試験用profileです（`POST /test-profile/v1/assignments`）。Taskは実行しません。

受け取ったbytesは、確かめるまで信用しません。`Service.Poll`は割当ごとに次を順に確かめます。

1. `leaseverify`: このWorker宛てで、信頼の起点に載るLSE-1の鍵の署名があり、有効期間内で、Trust Bundleが新しいこと。
2. LeaseとWorkOrderがWorkerの受け入れるPolicy revisionを名指すこと。
3. `workorderverify`と`workorderaccept`: このWorker宛てで、WOR-1の鍵の署名があり、そのLease（Decision IDとpayload digest）に結び付き、Workerが自分で承認したExecutor Approvalを名指し、一回限りのnonceをWorker自身の耐久的な重複検査に登録できること。
4. `startgate`: Local Governorと機械の容量が開始を許し、実効の上限が、付与された上限とローカルの天井の小さい方になること。

荷札のIDは参考情報です。署名済みのLeaseと食い違えば拒否します。

結果は`Outcome`で返ります。`ready`（`Handler`へ渡した）、`replayed`（nonceは使用済み。同じbytesが再配送されたときの通常の答え）、`deferred`（Local Governorが新規を受け付けず、nonceは消費していない）、`rejected`（理由付き）です。nonceは、署名・結び付き・承認の確認がすべて通ってから消費します。改ざんや食い違いのbytesはnonceを消費しません。Governorが開始を許したあとで開始の関門が拒否した場合は、nonceは消費済みです。

## 確かめないこと

- **Placement Approval**: 署名つきbytesをControl Planeが保存していない（decision IDとdigestだけ）ため、Workerは独立に検証できません。`workorderaccept`に渡す配置の証拠はWorkOrder自身の参照から作るので、その比較は実質的に意味を持ちません。検証したとは主張しません。
- Policy revisionの現在性は、信頼の起点のファイルが名指す値との一致だけです。取り消しの伝播は#78の未決です。
- Local Governorは固定値（`StaticGovernor`）です。実際の`agent/governor`はAgentのプロセスにまだつながっていません。
- Executor Approval（EXE-1）は署名されていません。Workerの設定にある一覧との一致だけです。
