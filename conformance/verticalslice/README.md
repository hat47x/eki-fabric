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
- 回復の判断（`recovery`）と、結果の受理（`resultacceptance`）

次は**合成した入力**です。これらは別の作業単位で本物にします。

| 合成しているもの | 実際には |
|---|---|
| 配置の判断（Placement Approval）とLeaseの署名 | PLC-1とLSE-1が署名する。ここでは、署名の検証を通った後の「配置の入力」だけを渡す |
| WorkOrderの署名と、Workerの受け入れ | WOR-1が署名し、Workerが検証して開始する。ここでは`executor.Spec`を直接組み立てる |
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

V5の次の二つは、このスライスに署名つきWorkOrderがないため、`Skip`として見える形で残しています。変更された実行ファイルは`executor/native`のテスト（`ErrExecutorChanged`）、変更されたWorkOrderは`workorderverify`のテストが確かめています。

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
- 回復の「待ち行列」の耐久化（#154）。V6では、閉じたAttemptから分類をやり直している
- 複数のTaskが回収で一斉に戻る場合の予算（`recovery`の単体テストにある）
- Clientが結果を取得する経路
