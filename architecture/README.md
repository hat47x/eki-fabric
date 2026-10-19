# EKI Fabricの全体構成

## このディレクトリの役割

Architecture Entry Gateを通過して採用した設計は、このディレクトリで正本として管理します。

ここにある設計は、製品価値や製品要求に代わるものではありません。

関係は次のとおりです。

~~~text
product/
  守る価値・原則
    ↓
requirements/
  満たすべき条件・脅威・保証
    ↓
research/ / spikes/
  比較材料・実現可能性Evidence
    ↓
plan/
  Gate・工程・判断履歴
    ↓
architecture/
  採用する論理構造・責務境界
    ↓
implementation
~~~

製品要求と設計が食い違っても、実装を正当化するために要求を読み替えることはしません。

要求の意図自体が変わった場合は、まず`requirements/`を更新します。要求を変える必要がなく設計だけが誤っている場合は、`architecture/`を修正します。

## 設計工程へ進むための判定

2026年9月21日のGate Uでは、設計基準（Architecture Baseline）の策定を始められる状態と判定しました。

詳細は次の文書にあります。

- ../plan/reviews/ARCHITECTURE_ENTRY_REVIEW_2026-09-21.md
- ../plan/UPSTREAM_PLAN.md

ただし、これは条件付きの合格です。

zkVMの実測に基づく性能比較、学習を用いた処理の配置、匿名参加者によるなりすまし（Sybil）への耐性、複数の構成要素にまたがる機密計算（Confidential Computing）、答えが一つに決まらないAI処理の検証などは、引き続き研究します。

これらの研究が未解決でも、特定の実装方式を中核の前提にはしません。必要に応じて接続できる拡張点として扱います。

## 最初に固定するもの

全体構成では、製品やプロセスの数を決める前に、次の責務境界を定めます。

1. 権限と信頼の境界（Authority / Trust Boundary）
2. 外部・内部の契約の境界（External / Internal Contract Boundary）
3. 制御とデータ転送の境界（Control Plane / Data Plane）
4. 配備形態（Deployment Profile）
5. 処理依頼・実行試行・期限付き実行権・復旧の状態モデル（Task / Attempt / Lease / Recovery）
6. 拡張のための接続仕様（Extension SPI）

現在の設計基準は、次の六つの文書に定めています。

1. `AUTHORITY_BOUNDARIES.md`: 誰が何を確定し、許可できるか
2. `CONTRACT_BOUNDARIES.md`: External / Internal Contractの意味の変換と結び付け
3. `CONTROL_DATA_PLANE.md`: Controlのmetadataと大きなArtifactの転送の分離、Dataのアクセス、cache、転送の境界
4. `DEPLOYMENT_PROFILES.md`: 小規模な社内、Enterprise、Browserとアドホック、Federation、公益向けGrantなどの物理的な構成
5. `TASK_STATE_MODEL.md`: Task、Attempt、Lease、Result、外部への副作用、回復の状態機械
6. `EXTENSION_SPI.md`: Queue、State、Storage、Identity、Runtime、Verificationなどを差し替える境界

設計基準0.1では、六つの領域が互いに矛盾しないかも点検しました。[`plan/reviews/BASELINE_REVIEW_2026-09-21.md`](../plan/reviews/BASELINE_REVIEW_2026-09-21.md)に、その結果を記録しています。`MVP_IMPLEMENTATION_PLAN.md`では、安全上の境界と状態の意味を守りながら一連の処理を通す最小構成を示し、M0からM5までの段階と完了条件を定めています。

`plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`では、通信に使う技術を、接続先ごとに決めました。外部の依頼元との接続（External Client Plane）には、HTTPS、JSON、OpenAPIを使います。実行端末との通信（Worker Plane）と、別プロセスで動く接続部との通信（Adapter Plane）には、Protobuf IDLとConnectを組み合わせた参照方式を使います。署名付きの権限判断（Authority Decision）には、決定論的なCBOR、CDDL、COSE_Sign1を使います。Protobufで符号化したバイト列は、署名や汎用的な内容の指紋値を作る際の正規のバイト列には使いません。

P1の実行端末との通信は`spec/worker/v1/worker.proto`に、P2の署名付き権限判断の本体は`spec/decisions/v1/decisions.cddl`に、それぞれ機械可読な形式で定めています。実行端末の識別情報（Worker Identity）は認証の文脈に置き、定期的な稼働通知（Heartbeat）と期限付き実行権（Lease）の更新は区別します。作業指示書（WorkOrder）、期限付き実行権、成果物の利用許可（Artifact Grant）など、署名の対象になるものはP2の決定論的なCBORで定義しています。

`plan/adr/0004-signed-decision-cryptographic-profile.md`では、権限上の役割ごとに鍵を分けること、Ed25519によるソフトウェア上の参照実装、ES256による企業システムとの相互運用、鍵の登録・交換・失効、COSEの保護されたヘッダーの検証を定めています。署名が正しいことだけを理由に操作は許可しません。権限上の役割、判断の種類、対象、現在の規則まで確認します。

Goによる参照実装では、`internal/core/authority`と`internal/core/decisiontrust`に、この境界の最小限の処理を実装しています。判断の種類を考慮しない鍵の解決APIは外部に公開していません。鍵の有効化から使用終了までの期間も検証します。

`spec/external/openapi.json`には、P0の外部接続向けに機械可読なスキーマを追加しました。固定したのは、Taskのsubmit、status、cancel、resultと、Artifactのingest、upload、descriptorだけです。Worker、Lease、WorkOrder、ArtifactGrantのtokenなど、内部の概念は露出していません。`spec/external/README.md`と、否定的なfixtureで、悪意ある入力に対する境界も明文化しています。

`plan/adr/0002-reference-implementation-language.md`で、Reference Implementationの言語にGoを採用しました。`plan/IMPLEMENTATION_REPOSITORY_LAYOUT.md`では、`cmd/`、`internal/core`、`internal/control`、`internal/agent`、`internal/spi`、`internal/adapters`、`spec`、`conformance`のそれぞれの責務を定めました。Goのパッケージ自体を外部向けの契約の正本にはしません。最初のCore実装は、原則として`internal/`の中にとどめます。

当初は、この実装構成を`main`へ統合してから、M0の実行可能な最小構成へ進む計画でした。

## 論理上の権限とプロセスを分ける

EKIでは、論理的に独立した権限ごとにマイクロサービスを一つずつ用意する必要はありません。

小規模な構成では、複数の権限を一つの実行ファイルにまとめられます。

それでも、次のことは論理的に分けておきます。

- どの鍵を使えるか
- どの永続的な状態を書けるか
- どのCapabilityを発行できるか
- どのPolicyを評価できるか
- どのAuthorityの結果を、検証しなければならないか

同じプロセス内でも、処理の配置を判断する部分が、実行者の受け入れ確認に使う鍵や成果物へのアクセスを許可する鍵を直接扱える構造にはしません。

大規模な配備でプロセスを分離するときも、この論理上の境界を守りながら配置を変更できることを目指します。

## 設計文書の状態

設計文書には、次のいずれかの状態を明示します。

- Draft（草稿）: 比較や検討の途中
- Baseline（設計基準）: 現時点で採用している設計
- Superseded（置き換え済み）: 後のADRなどで置き換えられたもの

設計基準は、いったん採用したら変更できないという意味ではありません。

要求、検証根拠、運用で得た知見が変わった場合は、理由と移行方法を記録したうえで更新します。

## 未解決の研究を採用済み設計として扱わない

全体構成には、機能を追加・差し替えするための拡張点を設けられます。ただし、まだ実証されていない技術を、必須の依存先として先取りすることはしません。

たとえば、次のものは接続できる設計にしますが、中核部分が成り立つための必須条件にはしません。

- Remote Attestation
- Verifiable Computation
- Learned Scheduler
- Attested Contribution Metering

標準構成では、これらを使わなくても、要求（Requirement）が求める最低限の保証を説明できなければなりません。

## 作業ブランチの運用

設計作業でも、レビュー可能な作業単位ごとにブランチを作成します。

~~~text
latest main
  ↓
architecture/authority-map
  ↓ PR / merge
main
  ↓
architecture/task-contract
  ↓ PR / merge
main
~~~

統合されていないブランチから次の設計作業用ブランチを派生させません。
