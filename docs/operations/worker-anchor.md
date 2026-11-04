# Worker の信頼の起点を更新する（`anchor.json`）

Worker（Agent）は、Control Plane から届いた Lease と WorkOrder を**自分の手元の鍵**で検証します。その鍵と版を `anchor.json` に持ちます。この文書は、**鍵を交代するときに何をするか**の手順です。

判断の背景は [ADR-0036](../../plan/adr/0036-trust-bundle-distribution-and-rotation.md) §11（**起動時にだけ置き換える**）と [ADR-0038](../../plan/adr/0038-operator-and-administrator-authentication.md) §7（**鍵は集合で受ける**）にあります。

## 前提: 何が書かれているか

| 欄 | 何を表すか |
| --- | --- |
| `keys` | 受理する鍵の一覧（**役割ごと**に LSE-1 と WOR-1 の両方が要ります） |
| `bundle_revision` / `validated_at` | 版の番号と、その版を確かめた時刻 |
| `trust_bundle_versions` | 受け取った版の履歴（**古い版も残ります**） |

**鍵は `keys` に複数書けます。** 交代の途中では、**古い鍵と新しい鍵を両方**書きます。

## 鍵を交代する手順

1. **新しい鍵を Control Plane に配る**（`trust_bundle_keys` に**追加**。既存の鍵は残す）。
   - この時点で、Control Plane は**古い鍵でも新しい鍵でも**署名を受け付けます。
2. **Agent の `anchor.json` の `keys` に新しい鍵を追加する。**
   - **両方**が入っている状態が、交代の「つなぎ」です。
3. **Worker を再起動する。** 版の置き換えは**起動時だけ**なので、反映には再起動が要ります（ADR-0036 §11）。
4. **Control Plane で新しい鍵で署名した版を作り、受理する**（`POST /v1/admin/trust-bundles`）。
5. **版の履歴で `verified_key` を見る**（`GET /v1/operator/trust-bundles/versions`）。**新しい鍵の指紋**になっていることを確かめます。
6. **古い鍵を外す。** まず Control Plane の `trust_bundle_keys` から、そのあと各 Agent の `anchor.json` から。**外す前に、5 が全版で新しい鍵になっていることを確かめてください。**

## してはいけないこと

- **古い鍵を先に外さない。** 先に外すと、**その鍵で署名された版を検証できず**、Agent が**止まります**（fail closed）。
- **`anchor.json` を稼働中に書き換えない。** 反映されません（**起動時に読むだけ**）。**書き換えたつもりで再起動を忘れる**のがいちばん危ない形です。
- **`validated_at` を手で進めない。** 版の古さの判定に使う値です。**進めると、古い版を使い続けていることが見えなくなります**。

## 失敗したときの見え方

| 症状 | 意味 |
| --- | --- |
| `lease_trust_bundle_invalid` が続く | 版の検証に失敗している。**鍵の集合と版の署名鍵**を突き合わせる |
| 起動はするが版が上がらない | `anchor.json` が**読めていない**か、**署名が合っていない**（`AcceptBundleToFileWithKeys` は**失敗時に何も書かない**） |
| 版の履歴の `verified_key` が古いまま | **新しい鍵の版がまだ作られていない**（手順4が済んでいない） |

## 何を保証しないか

- **鍵の公開**は確かめません。**`anchor.json` を配る経路の安全性**は配備の責任です。
- **交代の途中で止まらないこと**は、**両方の鍵を両側に書いた場合だけ**成り立ちます（片側だけだと止まります）。
