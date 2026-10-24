# 結果の受理（Result Acceptance、RAC-1）

Workerが「実行が終わった」と報告しても、それだけでは結果として認められません（RQ-019）。この境界は、独立した検証の証拠を集めて、報告された結果を**受理済みの結果（AcceptedResult）**へ昇格させてよいかを決め、その決定をControl Planeの状態に反映します。

```text
A-VERIFYING のAttempt（completed の申告が記録済み）
  + 検証の証拠（Verification。RVR-1が作る）
  + 結果の方針（Policy、出力の種類）  <- 信頼できる境界から。申告やWorkerからは受け取らない
  + 申告したWorkerの独立性の値       <- 身元の記録から。Workerの自己申告ではない
  -> resultaccept.Decide
       accepted -> AcceptedResultを記録 / Attemptを accepted で閉じる / Taskを completed にする（一つのcommit）
       rejected -> 却下の記録（RejectedResult）を残し / Attemptを rejected で閉じる（一つのcommit）。Taskは変えない（再試行は回復の判断）
       held     -> 何も変えない。証拠が足りないか、食い違っている
```

## なぜ三つの変更を一つにするか

AcceptedResultの記録、Attemptの終了、Taskの完了のどれかだけが残ると、利用者へ誤った答えを返します。たとえば、Taskだけが完了していて受理済みの結果がなければ、「完了した」と言いながら根拠がありません。逆に、受理済みの結果があるのにAttemptが開いたままだと、別の理由でAttemptが閉じられる余地が残ります。`statestore.ResultAcceptanceStore.CommitAcceptedResult`は、この三つを一つのtransactionで行います。

## 受理のたびに確かめること

ストアは、commitの時点で次を確かめ、一つでも外れれば何も変えずに`ErrConflict`で拒否します。

- AttemptがA-VERIFYINGで、指定したrevisionのまま。
- そのAttemptについて記録された申告が`completed`で、申告のdigestと出力のdigestが、受理する内容と一致する。Workerが報告していない出力は、受理できません。
- Taskが、指定したrevisionのまま、終端に達していて、取消しの要求もない。
- 同じTaskに、別の受理済みの結果がない。同じ内容の再送は`ErrAlreadyExists`です。

## 却下の理由を残す

却下でAttemptを`result_rejected`で閉じるだけでは、後から「誰が、どの方針の下で、何を見て退けたか」を説明できません（#57の証跡）。`statestore.ResultRejectionStore.CommitRejectedResult`は、却下の記録とAttemptの終了を一つのtransactionで行います。記録（`internal/core/rejectedresult`）には、申告と出力のdigest、方針のdigest、理由、食い違った検証すべて（検証者、検証の種類、観測したdigest）、根拠にできた検証者、除外した検証者とその理由が入ります。

ストアは、AttemptがA-VERIFYINGで指定のrevisionのままであることと、記録された申告が`completed`で申告と出力のdigestが記録と一致することを確かめます。Attemptごとに記録は一つで、同じ内容の再送は`ErrAlreadyExists`、別の内容は`ErrConflict`です。既に却下したAttemptへの再呼び出しは、保存済みの記録を返し、判定し直しません。

## 保持の判断（held）は失敗ではない

検証者が足りない、検証者の間で意見が分かれる、検証者が申告したWorkerと同じ主体の下にいる、といった場合は`held`になり、状態を変えません。後から証拠が加われば、同じAttemptをもう一度受理できます。`rejected`は、方針が指名した権威が出力を明示的に退けた場合だけです。

## 何を保証しないか

- 結果が正しいこと。受理が保証しないことは、`resultaccept.Decision.NotGuaranteed`に毎回列挙されます（入力の秘匿、実行後の削除、資源使用量の自己申告、外部副作用、検証者の健全性、宣言されていない共謀）。
- 検証者の証拠の作成。RVR-1は、この境界の入力であり、まだ実装していません。
- 方針と、申告したWorkerの独立性の値の出どころ。`PolicySource`と`ProducerSource`の実体は、まだありません。読めないときは`ErrPolicyUnavailable`で止まり、「要件なし」とは読みません。
- 受理済みの結果をClientへ返す経路。

## 実装上の補足

ストアの確認のうち、「Attemptが対象のTaskに属すること」と「申告が`completed`であること」は、他の確認でも拒否される冗長な防御です。前者は遷移の検証（AttemptのTaskは変えられない）、後者は出力のdigestの突き合わせ（`completed`以外の申告は出力のdigestを持てない）が、同じ拒否をします。意図を読み取れるように、明示的な確認として残しています。

関連: [architecture/AUTHORITY_BOUNDARIES.md](../../../architecture/AUTHORITY_BOUNDARIES.md)のRVR-1・RAC-1、[internal/core/resultaccept](../../core/resultaccept)、[internal/core/acceptedresult](../../core/acceptedresult)。
