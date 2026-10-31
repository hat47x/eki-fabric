# mainブランチの保護

`main-pr-only.json` は、GitHubの管理者が適用するためのルールセット定義です。**ファイルをリポジトリに置くだけでは、保護設定は変更されません。** 適用・読み戻し・拒否試験の記録は [Issue #331](https://github.com/hat47x/eki-fabric/issues/331) を正本とします。TEI Platform（[tei-platform#1189](https://github.com/hat47x/tei-platform/issues/1189)）、TAI Pleroma（[tai-pleroma#178](https://github.com/hat47x/tai-pleroma/issues/178)）、SEI（[sei-cognition#763](https://github.com/hat47x/sei-cognition/issues/763)）と同じ定義・手順です。

## 適用する規則

- `main` の変更は原則プルリクエストを経由する。承認レビューは0件（一人でも統合できる構成を維持）
- 強制更新（non-fast-forward）とブランチ削除を拒否する
- 例外的な迂回権限は設定しない
- 許可するマージ方式は `merge`・`squash`・`rebase` の3種類（リポジトリ設定と一致）
- 必須ステータスチェックは、開発規則に従い一律には設定しない

## 管理者による適用

```bash
gh auth status
gh api repos/hat47x/eki-fabric/rulesets --jq '.[] | {id, name, enforcement}'   # 既存の重複確認
gh api --method POST repos/hat47x/eki-fabric/rulesets --input .github/rulesets/main-pr-only.json
```

適用後は、一覧で取得した実際のRuleset IDで詳細を読み戻し、定義（名前・対象・強制状態・規則・迂回権限・レビュー数）と一致を確認する。拒否動作の実証は `main` を直接破壊せず一時ブランチで行う。

## 運用上の注意

- この定義はGitHub上の実設定と自動同期されません。変更時は Issue #331 へ適用日時・Ruleset ID・設定内容・検証結果を記録する
- 外部フォーク由来PRの権限確認は、最初の外部フォークPR到着時に実施する常設条件とする
