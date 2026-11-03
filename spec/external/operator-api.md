# Operator と Administrator の入口（外部契約）

Operator と Administrator は、Client とは**別の入口**から入ります（[ADR-0038](../../plan/adr/0038-operator-and-administrator-authentication.md)）。**別ポート・別 CA・mTLS 必須**で、`openapi.json` の Client API とは**証明書も経路も別**です。

この文書は、その入口の**経路と本文の形**を定めます。

## 共通の前提

- **mTLS 必須。** 証明書は Operator CA が発行したものだけで、**役割 URI**（`eki://<domain>/role/<role>`）を**1つだけ**持ちます。
- **役割が合わなければ 403**（`OPERATOR_ROLE_REQUIRED` / `ADMINISTRATOR_ROLE_REQUIRED`）。
- **CA が設定されていない配備では、入口そのものが存在しません**（404）。
- **監査が書けないときは操作が拒否されます**（503、[ADR-0040](../../plan/adr/0040-audit-records-and-retention.md) §5）。

## 経路

| 経路 | 役割 | 何をするか |
| --- | --- | --- |
| `POST /v1/operator/trust-bundles` | Operator | 版の**承認**（`add` / `remove`）を台帳に積む |
| `GET /v1/operator/trust-bundles/approvals?version=N` | Operator | その版に溜まっている**承認を読む** |
| `GET /v1/operator/trust-bundles/versions?limit=N` | Operator | **受理済みの版の履歴**を読む |
| `POST /v1/admin/trust-bundles` | Administrator | **署名つきの版**を受理する |

## 承認（`POST /v1/operator/trust-bundles`）

```json
{"version": 2, "operation": "add", "issuer": "sha256:...", "not_before": "2026-11-03T00:00:00Z", "not_after": "2027-11-03T00:00:00Z"}
```

- `operation` は `add` か `remove`。
- **`issuer` は発行者の指紋**（16進。`sha256:` 接頭辞なし）。
- 成功は **201**、拒否は **409**（`APPROVAL_REFUSED`）、本文が不正なら **400**（`INVALID_APPROVAL`）。

## 承認の読み出し（`GET .../approvals?version=N`）

```json
{"schema": "eki.trust-bundle-approvals/v1", "version": 2, "approvals": [{"operator": "alice", "operation": "add", "issuer": "sha256:...", "at": "..."}]}
```

- `version` は**必須**。無ければ **400**。

## 版の履歴（`GET .../versions?limit=N`）

```json
{"schema": "eki.trust-bundle-versions/v1", "versions": [{"version": 2, "not_after": "...", "recorded_at": "...", "key_id": "control-key-2", "verified_key": "ab12..."}]}
```

- `limit` は既定20・最大100。範囲外は **400**（`INVALID_LIMIT`）。
- **`key_id` は署名側の自称**、**`verified_key` は実際に検証した公開鍵の SHA-256**です（[ADR-0038 §8](../../plan/adr/0038-operator-and-administrator-authentication.md)）。
- **`verified_key` が空**の版は、**この項目より前に受理された版**です。**推測で埋めません**。

## 受理（`POST /v1/admin/trust-bundles`）

```json
{"version": 2, "signed": "<base64>"}
```

- `signed` は **署名つき版の base64**。
- 成功は **201**、受理できない場合は **409**（`ADMISSION_REFUSED`）。
- **監査の記録を書けないときは 503**（`AUDIT_UNAVAILABLE`）で、**受理しません**。

## 何を保証しないか

- **役割の持ち主が適切か**は EKI の外の判断です（[運用文書](../../docs/operations/operator-ca.md)）。
- **`key_id` と鍵の対応**は検証しません（§8）。**`verified_key` と設定の鍵を突き合わせるのは運用者**です。
- **失効（CRL）は見ません**。失効は**配備が経路を断つ**ことで効かせます。
