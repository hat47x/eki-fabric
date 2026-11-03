# 監査の記録（`audit_sink_url` へ POST する本文）

Control Plane は、**監査の記録を設定された URL に1件ずつ POST** します（[ADR-0040](../../plan/adr/0040-audit-records-and-retention.md)）。この文書は、**その本文の形**を定めます。保存先を作る人が読む前提です。

## 封筒

```json
{
  "schema": "eki.audit.artifact-access/v1",
  "record": { "at": "2026-11-03T05:00:00Z", "principal": "alice", "artifact": "sha256:...", "outcome": "served" }
}
```

- **`schema` は記録の種類を表します。** 形が似た2種類があるので、**名前が無いと取り違え**が起きます。
- **`v1` は交渉の版ではありません。** **知らない版は拒否**してください（読めない記録を**別の意味で読まない**ため）。

## 種類

### `eki.audit.artifact-access/v1`

成果物の取得の記録です。**成功だけでなく拒否も**送ります（拒否のほうが調査に要ります）。

| キー | 型 | 意味 |
| --- | --- | --- |
| `at` | RFC 3339（UTC） | 応答した時刻 |
| `principal` | 文字列 | 認証された呼び出し元。**認証前に落ちた場合は空** |
| `artifact` | 文字列 | 要求された成果物のダイジェスト |
| `outcome` | 文字列 | `served`・`not_found`・`expired`・`withdrawn` |

### `eki.audit.operator-access/v1`

Operator／Administrator の操作の記録です。**拒否も**送ります。

| キー | 型 | 意味 |
| --- | --- | --- |
| `at` | RFC 3339（UTC） | 応答した時刻 |
| `name` | 文字列 | 証明書の主体名 |
| `role` | 文字列 | `operator` または `administrator` |
| `operation` | 文字列 | 操作（`add`・`remove`・`admit`、拒否時は空になり得る） |
| `target` | 文字列 | 対象（発行者の指紋や版番号） |
| `outcome` | 文字列 | `approved` または `refused` |

## 保存先が満たすこと

- **2xx を返す**こと。**それ以外は失敗**として扱われます。
- **リダイレクトを返さない**こと。**リダイレクトは失敗**として扱われます（記録が**運用者の名指していない先**へ行くため）。
- **5秒以内に応答**すること。**タイムアウトは失敗**です。
- **追記のみ**であること（[ADR-0040 §2](../../plan/adr/0040-audit-records-and-retention.md)）。**EKI は確かめません**。

## 失敗したときに何が起きるか

| 記録 | 失敗したとき |
| --- | --- |
| 成果物の取得 | **取得は続きます。** `audit_unrecorded` が数えられ、ログに出ます（[ADR-0040 §5](../../plan/adr/0040-audit-records-and-retention.md)） |
| Operator の操作 | **操作は行われません。** 503 `AUDIT_UNAVAILABLE` を返します |

**再送はしません。** 遅れて書かれた記録が**その時刻の記録に見える**のを避けるためです。

## 何を保証しないか

- **記録の到達**は保証しません。失敗は**数に出るだけ**です。
- **保存先の完全性**は確かめません。
- **HTTPS を強制しません。** **平文なら、その区間は読まれ得ます**。
