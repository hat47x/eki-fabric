# セキュリティポリシー

EKI Fabricは初期開発中です。本番利用向けのサポート済みバージョンはまだありません。

## 脆弱性の報告

セキュリティ上の問題は、**公開Issueを作らず**、GitHubのPrivate vulnerability reportingから報告してください。

- リポジトリの「Security」タブ → 「Report a vulnerability」
- 対象のcommit、再現手順、影響、想定する攻撃条件を含めてください。
- 実在の顧客情報・個人情報・認証情報・非公開の実行ログを添付しないでください。合成fixtureで再現できる形に置き換えてください。

初期開発中のため、応答時間や修正期限は保証しません。

## 対象

- Control Plane、Agent、Client API、Worker test profile。
- Result claim、Task state、署名済みLease、Trust Bundleの検証。
- `spec/`の外部契約と`conformance/`の試験。

## 対象外

- 本リポジトリが提供していない本番運用環境・運用ポリシー。
- 第三者依存ライブラリの脆弱性。上流へ報告してください。
- 実在の委任先・組織・利用者の運用上の問題。

## 安全境界

- EKIは委任の核を扱いますが、実在の委任・Authority・業務効果をこのリポジトリで保証しません。
- ClientもWorkerも善意を仮定しませんが、実装がすべての攻撃を防ぐことを主張しません。
- 非公開リソースの内容は、このリポジトリへ複製しません。
