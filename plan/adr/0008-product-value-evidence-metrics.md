# ADR-0008 製品価値の成功指標をWorker数・FLOPSではなく「守られた人」と「検証済みの成果」で測る

- Status: **Proposed**
- Date: 2026-10-06
- Related: RQ-005, RQ-006, RQ-014, RQ-019, RQ-021, RQ-036, RQ-041, Issue #57, #65, #66

## 1. Context

[value.md](../../product/value.md)は、EKIが価値としないものとして「Worker数や総FLOPSそのものを成功指標にすること」と「全資源を最大使用率まで使い切ること」を挙げています。

それでも、指標を決めずにおくと、分散計算基盤のdemoや評価は「何台つながったか」「どれだけ速く処理したか」に寄っていきます。その結果として起きる誤った最適化には、次のようなものがあります。

- 業務端末の利用率を上げるほど数字が良く見えるので、Local Governorが資源を利用者へ返すこと（reclaim）が性能低下として扱われる。
- Workerが返したResultの件数を成果として数え、検証されていないClaimまで成果に混ざる。
- 分散しないと判断したTask（ADR-0007の`LOCAL_OR_EXISTING_EXECUTION`）が、「使われなかった」として数えられる。

## 2. Decision

MVP以降のdemo、benchmark、受け入れ判定では、次の表の指標を主指標とする。Worker数、総FLOPS、資源利用率は補助指標としてだけ報告し、それだけで成功を主張しない。

| 価値 | 主指標 | 測り方 |
|---|---|---|
| Human first | 利用者が端末へ戻ってから計算資源が縮小されるまでの時間。opt-outの後に実行が残っていないか | Local Governorの記録と、Worker上で実際に観測した結果を突き合わせる。中央の記録だけでは判定しない |
| Claims are not facts | 受理したResultのうち、Assurance Level付きで検証されたものの割合と、その検証方式 | 未検証のClaimは成果として数えない（RQ-019、RQ-021） |
| Economically sensible | Suitability Gateが各判定をどれだけ返したか。Fabricで実行した場合と既存手段との比較 | 比較には転送と検証のコストも含める。「分散しなかった」という判定も正常な成果として報告する |
| Reliability by contract | reclaimやWorkerの離脱の後に起きたretryの回数。重複実行やAuthorityの増幅が起きなかったか | RQ-052と#57のvertical sliceで確認する |
| Verifiable contribution | 検証済みのContribution Recordの件数と、その根拠になった事実 | 価格や換算値は出さない（ADR-0010） |

各指標は、根拠にした記録（Audit、Governor log、Verification record）までたどれる形で出力する。

## 3. Consequences

### Positive

- reclaimや「分散しない」という判定を、製品価値を発揮した結果として説明できる。
- demoの数字を、そのまま導入判断の根拠に使える。

### Negative / Limitations

- 指標を集めるには、Governor側とVerifier側の両方の記録が要る。そのぶんMVPで実装する範囲が広がる。
- 既存手段との比較は環境によって結果が変わる。Spikeと同じく、一般化できる範囲を記録に添える必要がある。

## 4. Open Questions

- 端末利用者が感じる遅れを、利用者のプライバシーを侵さずにどこまで測れるか（RQ-040）。
