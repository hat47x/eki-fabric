# PLC-1 — Placement issuing

`placementissue.Issuer` は Control Plane の PLC-1 署名境界である。`placementauth.Authorize`
が承認した配置を 1 件、署名済み `eki.placement-approval/v1` Decision 1 通に変える。

## 設計

- **状態を持たない。** Placement Approval は永続レコードとして格納されない。永続の関門は
  それを消費する Lease 予約（`leaseissue.Reserve`）であり、署名と前提の再検査をそこで
  やり直す。このため PLC-1 は何もコミットせず、リプレイもしない。
- **前提は呼び出し側が確立する。** Task が Ready であること、Attempt が当該 Worker の
  PLACING であること、policy revision が現行であることは、ディスパッチ側が永続状態から
  読んで `IssueRequest` に渡す。リクエスト欄は署名の入力であって認可ではない。
- **Decision ID は Attempt 単位で決定的**（`PlacementDecisionID`）。クラッシュ後の再試行は
  同じ Decision に再署名する。1 回の配置に 2 つの Decision は発行されない。
- **返却前に自己検証する。** 署名直後のバイトを、依存側と同じ経路
  （`decisiontrust.VerifierBoundary` と現行 Trust Bundle）で検証してから返す。

## 消費側との契約

`IssueResult.SignedPlacement` はそのまま `leaseissue.Request.SignedPlacement` に渡せる
COSE_Sign1 全体である。 Lease 側は `placementverify` で署名・鍵・有効窓・現在のポリシーを
再検査し、TaskIR と Worker が Attempt の保持内容と一致しなければ予約を拒否する。
