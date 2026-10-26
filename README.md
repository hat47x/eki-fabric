# EKI Fabric（益）

> 本リポジトリは、生成AIを用いた初期開発中のソースコードを公開するものです。現時点で人的レビューを伴っておらず、仕様や実装は変更される場合があります。

EKI Fabricは、**仕事をほかの計算資源へ、安全に任せるための仕組み**を目指す実験的なOSSプロジェクトです。

業務端末、個人のワークステーション、専用サーバー、ブラウザー、クラウドなどを対象にします。計算能力に余裕があっても、利用者の作業、データの所在、通信負荷、安全性、費用を優先して扱います。

## EKIの中心にある「委任」

EKIの中心は、計算資源を集めること自体ではありません。**誰に、どの仕事を、どの条件と期限で任せ、その結果をどう確認するか**という委任の仕組みです。

例えば、ある計算を別の端末に任せる場合でも、次の点を分けて考えます。

| 段階 | 確認したいこと |
| --- | --- |
| 依頼 | 誰が、どの範囲の仕事を依頼したか |
| 受理 | 委任先が、その仕事を受け付けたか |
| 実行 | 実際に仕事が行われたか |
| 結果 | 返ってきた結果を、そのTaskに必要な方法で確認したか |
| 記録 | 後から依頼・処理・検証の経緯をたどれるか |

仕事の委任先は利用者が指定することも、許可された範囲でEKIに選ばせることも想定しています。将来的には、余剰資源・管理対象の資源・クラウドを、制約と予算を守りながら組み合わせることも検討します。

この「委任の核」を、広域の資源活用、AIの推論やツール実行の統治、AI生成コードの隔離実行、軽量な常駐処理などに利用する構想です。詳しくは[Product Value](product/value.md)と[ADR-0013](plan/adr/0013-product-core-and-faces.md)を参照してください。

## なぜ業務端末の余力を考えるのか

日常の作業に十分な性能を持つ端末には、使われていない時間があります。その余力を組織や社会の計算へ活用できれば、専用の計算資源に頼る場面を減らせる可能性があります。

ただし、利用者の仕事が最優先です。

```text
利用者の本来の作業
  ↓
端末・ネットワーク・電力の安全な余力
  ↓
条件に合う計算処理
```

業務端末を使っている人が作業を再開したら、委任した計算を縮小・停止できることを重視します。通信による負荷が大きい場合は、別の端末へ送るよりローカルで処理する方が適切な場合もあります。

途中で失敗してもやり直せる処理には、一時的なWorkerを使う選択肢があります。すべての仕事に同じ水準の可用性を求めるのではなく、仕事の性質に応じて必要な信頼性を確保する設計を検討しています。

## 認証・信頼・結果検証を分ける

EKIでは、Client（仕事を依頼する側）とWorker（実行する側）を無条件には信頼しません。社内の端末や公式のAgentであっても、認証されたことだけで任意の操作を認めるわけではありません。

次の違いが重要です。

| 確認できたこと | それだけでは確定しないこと |
| --- | --- |
| Clientを認証できた | あらゆるTaskの実行権限 |
| Workerが結果を返した | 結果の正しさ |
| Workerが資源提供を申告した | 実際の貢献量 |
| Taskが受付状態になった | Taskの実行完了 |

こうした条件を明確にすることで、信頼関係の異なる端末や組織の参加を検討できます。実際の検証方法と許可範囲は、Taskと利用場面によって定めます。[Threat Model](requirements/THREAT_MODEL.md)と[Worker Assurance Model](requirements/ASSURANCE_MODEL.md)に設計上の前提があります。

## 想定する利用場面

まずは企業内の余剰計算資源を、安全に利用することを重視します。その先には、組織間の共同利用や研究・OSS・公益目的への資源提供も想定しています。

このような資源提供の利用枠を、現段階では**Compute Grant**と呼びます。提供実績を検証可能な形で記録し、別の制度が配賦・寄付・評価などへ利用できる余地を設けます。

EKI Core自体が通貨、現金化手段、市場価格、交換レートを提供する構想ではありません。どのような価値と対応させるかは、それぞれの主体が判断します。

EKIは特定のOSSや組織に専属する基盤ではなく、SEI Cognition、TEI、SUIなどの関連製品や、他社製品、将来の新しいソフトウェアからも利用できることを目指します。

## 現在の開発状況

**Architecture Baseline 0.2**を定義し、GoによるReference Implementationの開発を進めています。BaselineはAuthority、Contract、Control-Data Plane、Deployment Profile、Task State Model、Extension SPIの六つの領域から成ります。

外部Client向けにはHTTPS・JSON・OpenAPI、Worker／Adapterとの接続にはProtobuf IDL・Connect、署名付きAuthority Decisionにはdeterministic CBOR・CDDL・COSE_Sign1を設計基準として採用しています。P0の機械可読な契約は[OpenAPI仕様](spec/external/openapi.json)で確認できます。

現在、M0の実行可能な最小構成を検証しています。Control APIで合成Taskを受け付け、`queued`状態を観測する参照試験があります。一方で、**Workerによる実処理、結果の独立検証、本番環境での安全な委任までが完成したという意味ではありません。**

初回MVPでは、悪意のあるClientも考慮したTask受理から、2台以上のNative Workerへの配置、仕事の回収、再試行、結果検証までを一続きで確認することを目標にしています。Federation、Browser Worker、TEE、zkVM、学習型Schedulerなどは将来の拡張候補で、初回MVPの成立条件には含めません。

技術要素を先に増やすより、利用者への影響、責任と権限の範囲、検証可能な結果を重視します。

## 読み進めるには

初めて読む方は、[はじめてEKI Fabricを読む方へ](docs/for-readers.md)から始めてください。分散処理の専門知識がなくても、余剰資源の活用、悪意のある参加者、結果検証の考え方を順に読めます。

| 関心 | 資料 |
| --- | --- |
| 文書全体の入口と用語 | [ドキュメント案内](docs/README.md)・[用語集](docs/glossary.md) |
| 満たすべき要求 | [要求文書の読み方](requirements/README.md)・[Product Requirements](requirements/PRODUCT_REQUIREMENTS.md) |
| 想定する攻撃と保証 | [Threat Model](requirements/THREAT_MODEL.md)・[Assurance Model](requirements/ASSURANCE_MODEL.md) |
| 実際の利用・攻撃場面 | [Acceptance Scenarios](requirements/ACCEPTANCE_SCENARIOS.md) |
| 設計基準と実装計画 | [Architecture](architecture/README.md)・[MVP Implementation Plan](architecture/MVP_IMPLEMENTATION_PLAN.md) |
| 関連技術の調査 | [既存システム調査](research/topics/reference-systems.md) |
| AI時代の位置づけと接続案 | [価値の検討](research/topics/ai-era-value-positioning.md)・[オーケストレータ連携](research/integration/orchestrator-integration-and-layering.md) |

要求やArchitecture Baselineの確定した部分と、研究・将来の構想は別に読んでください。[Upstream Plan](plan/UPSTREAM_PLAN.md)には初期の検討順序が、[ADR-0014](plan/adr/0014-authorities-for-delegation-budget-capacity.md)と[ADR-0018](plan/adr/0018-single-machine-delegation-profile.md)〜[ADR-0021](plan/adr/0021-observation-plane-and-disclosure-tiers.md)には設計判断の経緯が残されています。文書の正本と開発時の作業規則は[AGENTS.md](AGENTS.md)にあります。脆弱性の報告は[SECURITY.md](SECURITY.md)、変更の進め方は[CONTRIBUTING.md](CONTRIBUTING.md)を参照してください。

## ライセンス

[MIT](LICENSE)
