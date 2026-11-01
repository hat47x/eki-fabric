# Client API（`internal/control/clientapi`）

Client向けのHTTPS・JSONの境界です。契約の正本は[`spec/external/openapi.json`](../../../spec/external/openapi.json)で、認証と受付の判断は[ADR-0006](../../../plan/adr/0006-m0-client-api-authentication-and-admission.md)にあります。この文書は、実装がどの操作をどう扱うかだけを記します。

## 実装済みの操作

| 操作 | 経路 | 備考 |
| --- | --- | --- |
| Taskの提出 | `POST /v1/tasks` | `Idempotency-Key`必須 |
| 状態の取得 | `GET /v1/tasks/{task_id}` | 自分のTaskだけ |
| 取消の要求 | `POST /v1/tasks/{task_id}/cancel` | `Idempotency-Key`必須 |
| 結果の取得 | `GET /v1/tasks/{task_id}/result` | 下記。`admission.Config.Results`を渡したときだけ有効 |


## 起動確認の窓口（`/healthz`・`/readyz`、#289）

待ち合わせる側（統合試験、運用の起動順）が、確かな信号で待てるようにする窓口です。認証の前に答えますが、状態は何も返しません。

| 経路 | 意味 | 応答 |
| --- | --- | --- |
| `GET /healthz` | プロセスが生きてlistenerに乗っている | 常に`200`、本文は固定の`ok` |
| `GET /readyz` | 設定されたlistenerがすべて処理中 |準備ができたら`200 ok`、できていなければ`503` |

- 認証なしで答えるのは、起動を待つ側がBearer tokenを持たないためです。答えは固定の短い本文だけで、識別子・版・設定の値・数を返しません。
- `Ready`が渡されていない構成では、`/readyz`は常に`503`（fail closed）。`GET`と`HEAD`以外は`405`。
- Control Planeの実装では、Worker listenerを設定したときは、それが処理を始めた後に`/readyz`が`200`になります。
- この窓口は`spec/external/openapi.json`の契約には含めず、運用向けの面としています（openapiはClientの操作のみ）。

成果物の経路（`/v1/artifacts/...`）は未実装で、`501 NOT_IMPLEMENTED`を返します。結果の取得も、State Storeの読み出し口が渡されていない構成では`501`のままです。

## 結果の取得

`completed`は状態であって、受理された結果ではありません。結果の取得は、Taskが完了しており、かつ耐久化されたAcceptedResult（RAC-1）が存在するときにだけ`200`を返します。

| 状況 | 応答 |
| --- | --- |
| 認証なし・不正 | `401 AUTHENTICATION_REQUIRED` |
| `client`以外のRole | `403 CLIENT_ROLE_REQUIRED`（他の操作と同じ） |
| 存在しない・形式が不正・他人のTask | `404 TASK_NOT_FOUND`（状態の取得と同一で、存在を推測させない） |
| Taskが終端に達していない（検証待ち・保留・再投入を含む） | `409 RESULT_NOT_READY`（`retryable: true`） |
| 終端だがAcceptedResultがない（取消・失敗・枯渇・放棄、または`completed`なのに記録がない） | `409 RESULT_NOT_AVAILABLE`（再試行しても変わらない） |
| 完了しAcceptedResultがある | `200 TaskResult` |

`200`の本文は`eki.task-result/v1`で、`artifacts`は空の配列、`assurance`は`{profile: <受理時の保証水準>, status: "satisfied"}`、`output_digest`は`{algorithm: "sha256", value: <64桁の16進>}`です。`output_digest`は、耐久化されたAcceptedResultが記録した出力のdigestをそのまま返すもので、入力から再計算しません。Clientはこれで、受理された結果を版固定して参照し、差替えや欠損を検出できます。

Worker識別子、Lease、fence、Attempt、検証者の識別子、policyのdigest、申告（claim）のdigest、検証のdigest、却下の理由は返しません。部分的な結果も返しません。`output_digest`は`RESULT_NOT_READY`、`RESULT_NOT_AVAILABLE`、状態の取得、エラー応答には現れません。

### 仕様とcoreの型の差（仮決め）

- `TaskResult`へ、任意の`output_digest`を`eki.task-result/v1`の中で足しました（[ADR-0001](../../../plan/adr/0001-protocol-surfaces-and-canonical-encoding.md)の互換性のルール「任意で追加されるフィールドは、小さな変更の候補」に沿う追加で、必須にしないため、既存の読み手は壊れません）。形は、`ArtifactDescriptor`が使う既存の`Digest`と同じです。
- `ArtifactDescriptor`は`size_bytes`、`media_type`、`data_class`を必須とします。AcceptedResultはこれらを記録していないため、成果物の記述子を作り出さず`artifacts`を空にしています。成果物の取得経路は引き続き未実装で、`output_digest`は取得できることを意味しません。
- 受理時の保証水準（`RA-1`、`RA-2`）を`assurance.profile`へ写します。Taskの`result_profile`は耐久化されていないため使いません。
- 却下された申告は、Taskを終端にしません（再投入の判断はRecoveryにあります）。そのためClientには`RESULT_NOT_READY`として見え、却下の事実や理由は出ません。
