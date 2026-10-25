# 縦断スライスの統合テスト（Vertical slice）

EKI Fabricの最初の価値を、部品ごとのテストではなく**一続きの流れ**として確かめます（#57）。悪意のあるClientやWorker、利用者の復帰による回収、再試行、Control PlaneとAgentの再起動があっても、実行権限が増えず、検証を経た結果だけが受理されることです。

```text
Client（悪意を持ち得る）
  -> 受付（admission）-> 配置とLease -> Worker上の実行（executor）
  -> 申告（ResultClaim）を再送キューから届ける -> 申告の受理
  -> 回収されたら 回復の判断 -> 次のAttemptを別のWorkerへ
  -> 独立した検証 -> 結果の受理（AcceptedResult）-> Taskが完了
```

## 何が本物で、何が合成か

状態は本物のSQLite（再起動できるファイル）に置きます。次の部品は、本番と同じコードを通ります。

- 受付（`admission`）、Attempt・Lease・Taskの状態とFenceEpoch
- Workerの実行の監督（`executor`）と、Local Governorの停止による回収
- Agentの再送キュー、`claimclient`、Worker Plane、`claimplane`、`claimaccept`
- 回復（`recoveryadmission`）: 閉じたAttemptから再投入を待ち行列に積み、時間窓の予算の中で解放し、Permitから次のAttemptを作る。待ち行列、窓の予算、Permitは、State Storeに耐久的に置かれる
- 結果の受理（`resultacceptance`）

次は**合成した入力**です。これらは別の作業単位で本物にします。

| 合成しているもの | 実際には |
|---|---|
| 配置の判断（Placement Approval）とLeaseの署名（V1〜V6） | PLC-1とLSE-1が署名する。V1〜V6では、署名の検証を通った後の「配置の入力」だけを渡す。署名つきの連鎖（下）では本物にしている |
| WorkOrderの署名と、Workerの受け入れ（V1〜V6） | WOR-1が署名し、Workerが検証して開始する。V1〜V6では`executor.Spec`を直接組み立てる。署名つきの連鎖（下）では本物にしている |
| 受付後にTaskをreadyにする遷移 | 実装した部品がまだない |
| Runner | 決まった出力を返す、またはGovernorの停止まで動き続ける関数。本物のプロセスの実行は`executor/native`のテストにある |
| 検証者の証拠 | RVR-1が作る。ここでは検証者を名乗る入力を直接渡す |
| 結果の方針と、Workerの独立性の値 | 信頼できる境界から読む。ここでは固定の実装 |

ネットワークの通信と認証もありません。Workerの身元は、認証済みとしてテストが渡します。

## シナリオ

| | 確かめること |
|---|---|
| V1 | 正常に完了し、独立した検証者の確認で受理される。Workerの完了の申告だけではTaskは完了しない |
| V2 | 利用者の復帰でLocal Governorが停止し、Attemptがreclaimedで閉じる。回復を経て別のWorkerの新しいAttemptが完了する。FenceEpochが増える |
| V3 | 応答のなくなったWorkerのAttemptが閉じ、別のAttemptが完了した後に、遅れた完了の申告が届く。申告は証拠として残るが、何も変えない |
| V4 | 悪意のあるClient。Workerの直接指定、任意の実行ファイル、任意のURL、データ区分の引き下げ、再試行の予算の拡大、上限を超える要求を、すべて入口で拒否する。拒否は枠を消費せず、同じキーの別の要求は拒否される |
| V5 | 悪意のあるWorker。別のWorkerになりすました申告、古いFenceの申告、Leaseより先のFenceの申告、二度目の別の申告、同じ主体の下に集まった多数の検証者、出力が壊れた結果 |
| V6 | 再起動。Attemptの間、キューに申告が残ったまま、受理の後。再起動をまたいでも、有効な権限は一つだけ |
| 回復 | 待ち行列の再投入が、再起動をまたいで開始時刻も含めて残る。Permitの確定とAttemptの作成の間で落ちても、あとで一度だけ使える。回収の波は、時間窓の予算の中で少しずつ解放され、再起動しても窓が巻き戻らない。再試行してはならないTaskは失敗にせず保持する。Workerが毎回回収しても、Taskのattempt budgetは広がらない。結果が決まったAttemptは再投入できない |

V5の「バイト列が変わった実行ファイル」は、WorkOrderから本物のプロセスを起動する経路をこのスライスが持たないため、`Skip`として見える形で残しています。`executor/native`のテスト（`ErrExecutorChanged`）が確かめています。改変されたWorkOrderは、次の署名つきの連鎖で確かめています。

## 署名つきの連鎖（Signed chain）

V1〜V6の配置とWorkOrderを、本物のAuthorityに置き換えた流れです。鍵の生成、署名、検証、COSEの符号化は、参照実装を使います。

```text
Scheduler（提案。権限を持たない）
  -> PLC-1がPlacement Approvalに署名
  -> LSE-1が検証して予約（FenceEpochを確定）-> 現在の状態で再確認して署名（EMITTED）
  -> WorkerがLeaseを検証
  -> WOR-1がLeaseからWorkOrderを作って署名
  -> WorkerがWorkOrderを検証し、Lease・Placement・Executorの記録と突き合わせ、nonceを一度だけ使う
  -> Local Governorの現在の判断と比べて、実効上限のPlanを作る
  -> 実行。申告は、WorkOrderの決定・実行体・nonceを持つ
```

| | 確かめること |
|---|---|
| 正常系 | 署名つきの連鎖を通って完了し、受理される。実効上限は、WorkOrderの許可とLocal Governorの余力の小さい方になる。申告がWorkOrderの束縛を持つ |
| 回収 | 回収された後の次のAttemptには、新しいPlacement Approvalが要る。古い承認は使えない。FenceEpochが増える |
| Placement Approval | 信頼されない鍵による偽造、署名後の改変、Attemptに結び付いたのと別のWorker宛、期限切れ、現在でない方針のrevisionを、すべて拒否する。拒否はLeaseを残さない。同じ承認の再送は同じLeaseに収束し、期限切れのLeaseは復活させない |
| LeaseとWorkOrder | 経路上で改変されたLease、改変されたWorkOrder（nonceを消費しない）、別のWorker宛のbytes、二度目の使用（nonceの再利用）、Leaseの期限後の使用を拒否する。Local Governorは、Control Planeが許していても開始を拒否できる |
| 方針の取消 | Leaseの予約の後、署名の前に方針が取り消されると、署名されたLeaseは出ず、Attemptは`policy_revoked`で閉じる。次の配置のFenceEpochは前より大きい |

署名つきの連鎖で、まだ合成している入力は次のとおりです。

- Executor Approval（EXE-1）。WorkOrderが参照する決定のIDとダイジェストだけを持つ。署名する主体がまだない。
- 署名済みbytesのWorkerへの配送（`leasedelivery`）。連鎖の各段のbytesを、テストが直接Workerへ渡す。
- Trust Bundleの取得。常に新しく検証された値を返す。失効や古さは、`decisiontrust`と各検証器のテストが確かめている。

## 証跡の再構成

`evidenceOf`は、保存された状態だけから、Attemptごとの実行先のWorker、Lease、FenceEpoch、閉じた理由、申告、受理された結果とその検証者を読み戻します。`assertExplainable`は次を確かめます。

- Attemptの番号に欠けがなく、FenceEpochが単調に増える
- 閉じたAttemptはすべて理由を持つ
- Taskが完了であることと、受理された結果が存在することが一致する
- 受理された結果は、受理されたAttemptの完了した申告と、申告のdigestと出力のdigestまで一致する
- 検証者が二者以上いて、申告したWorker自身を含まない

## 実行

```bash
go test ./conformance/verticalslice/
```

## このテストが見つけたこと

- V2が、回収で閉じたAttemptのLeaseが次の配置を期限まで妨げることを見つけた。Attemptが「Workerが止まった」と確認できる理由か、結果が決まった理由で閉じたとき、Leaseを解放するようにした（`attemptstate.CloseReason.EndsExecutionAuthority`）。

## まだ確かめていないこと

- 署名つきのPlacementとWorkOrderの連鎖（上の合成の表）
- 再投入の公平性（aging、fair share）と、複数のControl Plane instanceにまたがる窓の合意
- 保持（Hold）にしたTaskの印の保存。保持はTaskを失敗にせず返すだけで、保存する表はまだない
- Clientが結果を取得する経路
