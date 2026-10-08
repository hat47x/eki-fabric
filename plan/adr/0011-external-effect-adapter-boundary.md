# ADR-0011 — External Effect Adapter の境界と SPI（暫定）

- Status: **Provisional（暫定）**
- Date: 2026-10-08
- 有効期間: 上位要件（業務フローとユースケース）が確定するまで。確定後に再判断する。
- Scope: 外部副作用を送る経路の境界と、Adapter に渡す契約。Effect Authority による Receipt の検証は含まない。
- 関連: ADR-0007（再実行規則）、ADR-0012（純粋な Core の永続化境界）

## 1. 背景

外部へ副作用を送る経路は、ADR-0007 §4 の前提（commit-before-dispatch）に依存する。前提を破る経路が一つでもあると、UC2 で重複が起きうる。

ADR-0007 §8 は「dispatch 試行の記録を書く主体」を未決としていた。また、Adapter の SPI が決まっていなかったため、適合試験（`conformance/externaleffecttest`）は暫定の `Sender` の形で書かれていた。

## 2. 問題

- Adapter が記録用の Log を持つと、記録より先に呼び出すかどうかが Adapter の実装に依存する。
- Adapter が再試行を自分で判断すると、ADR-0007 の規則を Adapter ごとに破りうる。
- Adapter が Lease や Receipt などの権限の状態を直接書くと、Core の不変条件を破る。Extension SPI の原則（`plan/UPSTREAM_PLAN.md`）に反する。

## 3. 決定（暫定）

1. **記録は Core が書く。** 外部呼び出しは必ず `dispatchguard.Guard.Send` を経由する。Core が Guard を組み立て、Adapter には記録用の Log も Guard も渡さない。これにより、ADR-0007 §8 の「記録を書く主体」は Core とする。
2. **Adapter は宣言と呼び出しだけを提供する。** Adapter が Core に渡すのは、(a) 宣言（EffectProfile）と (b) Invoke（外部呼び出し 1 回分）の二つである。Invoke は、Guard の send 関数として一度だけ呼ばれる。
3. **Adapter は再試行しない。** 再試行は Core が ADR-0007 の規則で判定し、新しい世代の Send として行う。Invoke が失敗を返しても、外部へ届いたかどうかは分からないものとして扱う（ADR-0007 の結果不明と同じ扱い）。
4. **宣言の中身。** EffectProfile は次を含む。Adapter の名前と版、冪等性（外部側が Operation ID で重複排除するか）、要求する権限クラス。冪等性の宣言は Core が検証しない（ADR-0007 §4 の前提 3）。
5. **Receipt は Adapter の自己申告で確定しない。** Adapter が返す Receipt 候補は、Effect Authority の検証を経てはじめて確定扱いになる。検証の SPI は未決（§8）。

## 4. Adapter に渡すもの、渡さないもの

| 項目 | Adapter に渡すか | 理由 |
| --- | --- | --- |
| Effect ID、世代 | 渡す（読み取り専用の値） | 冪等キーとして使う |
| Operation ID | 渡す | 冪等な効果で、外部側の重複排除に使う |
| dispatchlog.Log | 渡さない | 記録の書き込みを Core に限定する |
| Guard、Send | 渡さない | 再試行の判定を Core に限定する |
| 状態ストア、署名鍵 | 渡さない | 任意の DB と鍵へのアクセスを禁止する（Extension SPI の原則） |

## 5. 試験

- `conformance/externaleffecttest` を、Core の Guard 経由の呼び出しに対して適用する（実装済み、コミット 6f5b702）。
- Adapter 側の試験は未実装である。確認すべきことは、Invoke が冪等キーを送ること、失敗時に自分で再送しないこと、の二点である。

## 6. 結果

- 利点: 記録の順序を Core が一箇所で守る。Adapter を差し替えても、再試行の規則は変わらない。
- 欠点: Adapter は外部側の状態を直接観測できない。状態の照会は Effect Authority の仕事になる。
- 欠点: 守りは同一プロセス内に限られる。Adapter を別プロセスの外部プラグインとして動かす場合の隔離は未決である。

## 7. 見直しの条件

- 上位要件が確定した（特に UC2 の扱い）。
- Adapter を別プロセスで動かす必要が出た（隔離の方式を再判断する）。
- Receipt 検証の SPI が決まった。
- Attempt / Lease（LSE-1）の記録が入り、dispatch 記録の保存先が変わった。

## 8. 未決事項

- EffectProfile の具体的な型と、版・権限クラスの表し方。
- Receipt 検証の SPI（Effect Authority との境界）。
- Operation ID の生成主体（Core か Task か）。
- Adapter の起動方式（同一プロセス内の関数か、別プロセスか）。
