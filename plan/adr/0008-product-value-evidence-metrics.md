# ADR-0008 — 製品価値の成功指標をWorker数・FLOPSではなく「守られた人」と「検証済みの成果」で測る

- Status: **Proposed**
- Date: 2026-10-06
- Related: RQ-005, RQ-006, RQ-014, RQ-019, RQ-021, RQ-036, RQ-041, Issue #57, #65, #66

## 1. Context

[value.md](../../product/value.md)は、「Worker数や総FLOPSそのものを成功指標にすること」と「全資源を最大使用率まで使い切ること」を、EKIが価値としないものに挙げています。

しかし、分散計算基盤のdemoや評価は、指標を決めないと自然に「何台つながったか」「どれだけ速く処理したか」へ寄ります。その結果、次のような誤った最適化が起きます。

- 例: 業務端末の利用率を上げるほど数字が良くなるため、Local Governorのreclaimが「性能低下」として扱われる。
- 例: Workerが返したResultの件数を成果として数え、検証されていないClaimを成果に混ぜる。
- 例: 分散しなかったTask（ADR-0007の`LOCAL_OR_EXISTING_EXECUTION`）が「使われなかった」と数えられる。

## 2. Decision

MVP以降のdemo、benchmark、受け入れ判定で、次の指標群を**主指標**とする。Worker数、総FLOPS、資源利用率は補助指標としてだけ報告し、単独で成功を主張しない。

| 価値 | 主指標 | 測り方の要点 |
|---|---|---|
| Human first | 端末利用者の復帰からcapacity縮小までの時間、opt-out後に残った実行の有無 | Local Governorの記録とWorker上の実観測を突き合わせる。中央の記録だけで判定しない |
| Claims are not facts | 受理されたResultのうち、Assurance Level付きで検証されたものの割合と、その検証方式 | 未検証Claimは成果として数えない（RQ-019、RQ-021） |
| Economically sensible | Suitability Gateの判定分布、Fabric実行と既存手段の比較（転送・検証コスト込み） | 「分散しなかった」判定も正常な成果として報告する |
| Reliability by contract | reclaimやWorker離脱後のretry回数と、重複実行・Authority増幅が起きなかったこと | RQ-052、#57のvertical sliceで確認する |
| Verifiable contribution | 検証済みContribution Recordの件数と、それが根拠にした事実 | 価格・換算値は出さない（ADR-0010） |

指標は、それぞれが根拠とした記録（Audit、Governor log、Verification record）へ辿れる形で出力する。

## 3. Consequences

### Positive

- reclaimや「分散しない」判定を、製品価値の発揮として説明できる。
- demoの数字が、そのまま導入判断の根拠になる。

### Negative / Limitations

- 指標の収集に、Governor側とVerifier側の記録が必要になり、MVPの実装範囲が増える。
- 「既存手段との比較」は環境依存であり、一般化できる範囲を記録に添える必要がある（Spikeと同じ扱い）。

## 4. Open Questions

- 端末利用者への影響（体感遅延）を、プライバシーを侵さずにどこまで測るか（RQ-040）。
