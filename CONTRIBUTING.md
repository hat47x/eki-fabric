# コントリビューション

EKI Fabricは初期開発中です。変更は小さく、Requirement・Architecture・Evidenceの区別を保って送ってください。

## 進め方

1. 既存のIssue、Requirement、Architecture、ADRを確認する。
2. 変更の目的、境界、未検証事項をIssueまたはPR本文へ書く。書けない場合は、PRの作成後に埋める。
3. 最新の`main`から短命ブランチを作る。既存の作業ブランチがある作業では、それを再利用する。
4. 変更に対応するtestを追加・更新する。変更範囲に応じた検証を自分で選ぶ。
5. 変更範囲の検証を実行し、結果をPR本文へ記録してから統合する。全体の`scripts/verify.sh`（Windowsは`scripts/verify.ps1`）は任意の広範囲診断であり、統一した統合条件にはしない。実行できない検証は、その制約と残る危険性を記録する。このリポジトリにはCI workflowが無い（#244）。
6. Draft PRは作業中の表示であり、内容が揃ったら解除して統合する。統合後はブランチを削除する。並行作業と衝突した場合は、その変更だけを保留し、他の作業を続ける。

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
- 並行に動くコードを変えたときは、`./scripts/verify.sh --race`（Windowsでは`scripts\verify.ps1 -Race`）も実行する。cgoとCコンパイラが要る。理由と実行時間は`docs/development.md`の「データ競合の検出」を参照
- Markdownの相対リンクの先が存在することは、`conformance/doclinks`の`go test`が確かめる（ルートの`go test ./...`に含まれ、`--quick`では走らない）。文書を移したり名前を変えたりしたときは、リンクを直してから実行する。コード書式（`` `...` ``）で書いたパスの言及や、`http(s)`のリンク、見出しの存在は対象外。
- `spikes/`の下は、`go.mod`を持つ独立したmoduleで、ルートの`go vet`・`go test`の対象に含まれない。ルートmoduleを`replace`で取り込む（`internal`をimportする）Spikeだけは、`scripts/verify.sh`・`scripts/verify.ps1`が`go vet ./...`を実行し、ルートの変更でコンパイルできなくなっていないかを確かめる。Spikeの`go test`は、そのSpikeのREADMEに従って各自で実行する。
- `spec/`・`conformance/`の契約変更は、対応するCDDL・OpenAPI・testを同時に更新する。
- 決定境界（[ADR-0033 §6](plan/adr/0033-three-element-realignment.md)）を増やす・変えるときは、**ADR の対応表と `conformance/actorboundary` を同じ PR で更新する**。境界は「誰が決めるか」の単位なので、**表だけ・型だけ**を直すと、次の読者が対応を追えなくなる。

## 文書

- 日本語を基本とし、Requirement ID・API名・コマンドは原表記を保つ。
- 主張、保証範囲、未決事項、Evidenceの強さを混ぜない。
- 実データを使わず、合成fixtureで再現できる形にする。

## ライセンス

貢献はMITライセンスの下で受け入れます。詳細は[LICENSE](LICENSE)を参照してください。
