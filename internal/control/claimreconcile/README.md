# 申告が届かないAttemptの扱い（Claim reconciliation）

Leaseが期限切れになっても、ResultClaimが届かない場合があります。Workerが落ちた、networkが切れた、Agentのqueueに残っている、といった理由です。これを、ただちに「失敗」や「再実行してよい」へ丸めません。

```text
Attemptが開いている かつ Claimがない
  ├ Leaseがまだ期限内          -> AwaitClaim
  ├ 期限切れ + grace内          -> AwaitClaim
  ├ grace後 + 外部副作用の可能性 -> Reconcile
  └ grace後 + 副作用なし         -> CloseLeaseExpired
```

## なぜgraceを置くか

claimqueueはat-least-onceで送ります。Leaseの期限直後は、Claimがまだ届いていないだけかもしれません。すぐにAttemptを閉じると、届いたClaimが`late`になり、正しい結果を検証へ送れません。

## なぜ外部副作用で分けるか

TaskがEKIの外へ副作用を持つ場合、実行されたかどうかはControl Planeの状態だけでは分かりません。Receiptが得られるまでAttemptを閉じず、`Reconcile`として残します。閉じて再実行すると、二重の副作用になり得ます。

副作用がない場合も、`CloseLeaseExpired`はAttemptを閉じるだけです。再実行してよいかは、Taskの信頼性契約とattempt budgetで`recovery`が決めます。

## 何を保証しないか

- 実行が実際に止まったこと。Leaseの期限は、Workerが止まった証明ではありません。
- 外部副作用の有無の判定。Task契約から読みます。
- Claimのlate受信の扱い。`resultclaim.Decide`の`late`が担当します。
- graceの値。運用とTaskの性質から決める必要があります。

## 2026-11-10 M0のEffect出典を確認する純粋な検査（#745）

[`effect_evidence.go`](effect_evidence.go)の`ProvenM0EffectNone`は、**保存されたM0のTask要求が`effect_mode=none`と宣言しており、しかもその要求が対象Attempt／Leaseに一致すること**を確認するための純粋な検査である。従来の`PossibleExternalEffect:false`を引数なしで推定することを避けるための、実行前の最小部品に限定する。

検査対象は次のすべてとする：保存されたContentの存在とM0 Requestの再解析、RequestFingerprint、TaskIR digest、AdmissionのTask ID・MaxAttempts、AttemptのTask/Lease/Worker、LeaseのTask/Attempt/Worker、各永続Recordの構造。欠損・不一致・未対応Effect区分は`true`にせず、安定した非機密の理由コードで`false`を返す。正しいデータ同士を組み合わせても、**現在のPolicy、Trust、Grant、実Executorの副作用の有無、外部Provider Receiptの真実性**を保証するものではない。

[`effect_evidence_test.go`](effect_evidence_test.go)に、正常系と11種類の反例（Content欠損、非対応Effect、内容改変、Fingerprint相違、TaskIR／Task／Attempt／Lease／Workerの結合不一致、attempt budgetの相違、不正JSON）を追加した。

**まだ配線していない**：`internal/control/service/recall.go`は今も`PossibleExternalEffect:false`を固定している。この関数を追加したことをもって回収処理が安全になったとは扱わない。次の[#745](https://github.com/hat47x/eki-fabric/issues/745)の作業では`LoadAdmission`で**同じTaskの正本から読み取り**、検査が`true`の場合だけ副作用なしの候補とする。DB読み取り失敗・Content欠損・不一致ではAttemptと台帳を勝手に閉じず、原因を診断して保留する。現行の合成Taskの回収テストはContentを持たないため、正当なM0 Admissionを使う正例と、合成Taskの保留負例へ分ける必要がある。

この検査自体はGoテストをまだ実行していない。独立した関数の追加を、実回収・自動再投入・Provider Effectの受理と混同しない。
