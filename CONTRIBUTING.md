# コントリビューション

EKI Fabricは初期開発中です。変更は小さく、Requirement・Architecture・Evidenceの区別を保って送ってください。

## 進め方

1. 既存のIssue、Requirement、Architecture、ADRを確認する。
2. 変更の目的、境界、未検証事項をIssueまたはPR本文へ書く。
3. 最新の`main`から短命ブランチを作る。
4. 変更に対応するtestを追加・更新する。
5. CIを確認してからPRを統合する。

## 配置

- 要求は`requirements/`、設計は`architecture/`、実現可能性検証は`spikes/`、判断履歴は`plan/`へ置く。
- 日付付きの記録は、Spike結果なら`spikes/<名前>/RESULTS_YYYY-MM-DD.md`、レビューなら`plan/reviews/`へ置く。
- 調査メモは`research/topics/`または`research/integration/`へ置く。
- 同じ要求を複数文書へ複製しない。

## 禁止事項

- 実在の顧客情報、個人情報、認証情報、非公開の実行ログをコミットしない。
- 非公開リソースの内容を、この公開リポジトリへ複製しない。
- Spikeの成功を、Product RequirementやArchitectureの暗黙の変更として扱わない。
- 認証済みのClient・Workerでも悪意を持ち得る前提を崩さない。

## 検証

- `gofmt -l .`
- `go vet ./...`
- `go test ./... -count=1`
- `spikes/`の下は、`go.mod`を持つ独立したmoduleで、ルートの`go vet`・`go test`の対象に含まれない。ルートmoduleを`replace`で取り込む（`internal`をimportする）Spikeだけは、`scripts/verify.sh`・`scripts/verify.ps1`が`go vet ./...`を実行し、ルートの変更でコンパイルできなくなっていないかを確かめる。Spikeの`go test`は、そのSpikeのREADMEに従って各自で実行する。
- `spec/`・`conformance/`の契約変更は、対応するCDDL・OpenAPI・testを同時に更新する。

## 文書

- 日本語を基本とし、Requirement ID・API名・コマンドは原表記を保つ。
- 主張、保証範囲、未決事項、Evidenceの強さを混ぜない。
- 実データを使わず、合成fixtureで再現できる形にする。

## ライセンス

貢献はMITライセンスの下で受け入れます。詳細は[LICENSE](LICENSE)を参照してください。
