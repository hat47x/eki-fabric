# EKI Fabricの全体構成

## このディレクトリの役割

このディレクトリには、設計工程へ進むための条件を満たした後に採用する設計の正本を置きます。

製品価値や製品要求を置き換えるものではありません。

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

要求の意図が変わったのであれば、先に`requirements/`を更新します。設計だけに誤りがある場合は、`architecture/`を修正します。

## 設計工程へ進むための判定

2026年9月21日のGate Uの審査では、設計基準（Architecture Baseline）の策定を始められる状態だと判定しました。

詳細は次の文書にあります。

- ../plan/ARCHITECTURE_ENTRY_REVIEW_2026-09-21.md
- ../plan/UPSTREAM_PLAN.md

この判定は条件付きの合格です。

zkVMの実測による性能比較、学習に基づく処理配置、匿名の参加者によるなりすまし（Sybil）への耐性、複数の構成要素にまたがる機密計算（Confidential Computing）、結果が一意に決まらないAI処理の検証などは、引き続き研究します。

これらの研究が未解決のうちは、特定の実装方式を前提に固定しないことが、設計工程へ進むための条件です。

## 最初に固定するもの

全体構成では、利用する製品やプロセスの数を決める前に、次の境界を定めます。

1. Authority / Trust Boundary
2. External / Internal Contract Boundary
3. Control Plane / Data Plane
4. Deployment Profile
5. Task / Attempt / Lease / Recoveryの状態モデル
6. Extension SPI

現在の設計基準となる文書は、次の6つです。

1. `AUTHORITY_BOUNDARIES.md`: 誰が何を確定し、許可できるか
2. `CONTRACT_BOUNDARIES.md`: External / Internal Contractの意味の変換と結び付け
3. `CONTROL_DATA_PLANE.md`: Controlのmetadataと大きなArtifactの転送の分離、Dataのアクセス、cache、転送の境界
4. `DEPLOYMENT_PROFILES.md`: 小規模な社内、Enterprise、Browserとアドホック、Federation、公益向けGrantなどの物理的な構成
5. `TASK_STATE_MODEL.md`: Task、Attempt、Lease、Result、外部への副作用、回復の状態機械
6. `EXTENSION_SPI.md`: Queue、State、Storage、Identity、Runtime、Verificationなどを差し替える境界

設計基準0.1では、六つの主要領域を横断して点検しました。`BASELINE_REVIEW_2026-09-21.md`には、責務の重大な矛盾が見つからなかったことを記録しています。`MVP_IMPLEMENTATION_PLAN.md`には、設計基準の安全上の境界と状態の意味を守りながら、一連の処理を最小構成で通す実装、M0からM5の段階、各段階の完了条件を定めています。

`plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`では、通信に用いる技術を接続先ごとに決めました。外部の依頼元との接続（External Client Plane）には、HTTPS、JSON、OpenAPIを使います。処理を実行する側との接続（Worker Plane）と、別プロセスで動く接続部（Adapter Plane）には、Protobuf IDLとConnectによる参照通信方式を使います。署名を付けた権限判断（Authority Decision）には、決定論的なCBOR、CDDL、COSE_Sign1を使います。Protobufで符号化したバイト列は、署名や汎用的な内容の指紋値を作る際の正規表現には使いません。

P1のWorker Planeは`spec/worker/v1/worker.proto`に、P2の署名付きAuthority Decisionのpayloadは`spec/decisions/v1/decisions.cddl`に、それぞれ機械可読な形でまとめました。Worker Identityは認証の文脈に置き、HeartbeatとLeaseの更新を分けています。WorkOrder、Lease、Artifact Grantなど、署名の対象になるものは、P2のdeterministic CBORに固定しています。

`plan/adr/0004-signed-decision-cryptographic-profile.md`では、権限上の役割ごとに鍵を分けること、Ed25519によるソフトウェア上の参照実装、ES256による企業システムとの相互運用、鍵の登録・交換・失効、COSEの保護されたヘッダーの検証を定めています。署名が正しいだけでは操作を許可しません。権限上の役割、判断の種類、対象、現在の規則まで確認します。

参照実装のGoによる中核部分では、`internal/core/authority`と`internal/core/decisiontrust`で、この境界の最小限の実装を始めています。判断の種類を考慮しない鍵の解決APIは、外部に公開していません。鍵の有効化と使用終了の時刻を含む管理周期も、検証条件に含めています。

`spec/external/openapi.json`には、P0の外部接続向けに機械可読なスキーマを追加しました。固定したのは、Taskのsubmit、status、cancel、resultと、Artifactのingest、upload、descriptorだけです。Worker、Lease、WorkOrder、ArtifactGrantのtokenなど、内部の概念は露出していません。`spec/external/README.md`と、否定的なfixtureで、悪意ある入力に対する境界も明文化しています。

`plan/adr/0002-reference-implementation-language.md`で、Reference Implementationの言語にGoを採用しました。`plan/IMPLEMENTATION_REPOSITORY_LAYOUT.md`では、`cmd/`、`internal/core`、`internal/control`、`internal/agent`、`internal/spi`、`internal/adapters`、`spec`、`conformance`のそれぞれの責務を定めました。Goのパッケージを外部契約の正本にはしません。最初のCore実装は、原則として`internal/`の中にとどめます。

次は、このRepository Layoutを`main`にまとめたうえで、M0のexecutable skeletonへ進みます。

## 論理上の権限とプロセスを分ける

EKIでは、独立した権限の数とマイクロサービスの数を一致させる必要はありません。

小規模な構成では、複数の権限を一つの実行ファイルにまとめられます。

それでも、次のことは論理的に分けておきます。

- どの鍵を使えるか
- どの永続的な状態を書けるか
- どのCapabilityを発行できるか
- どのPolicyを評価できるか
- どのAuthorityの結果を、検証しなければならないか

同じプロセス内でも、処理の配置を決める部分が、実行者の受け入れ確認に使う鍵や成果物へのアクセス許可に使う鍵を直接扱える構造にはしません。

大規模な配備でプロセスを分離するときも、この論理上の境界を守りながら配置を変更できることを目指します。

## 設計文書の状態

設計文書には、次のいずれかの状態を明示します。

- Draft（草稿）: 比較や検討の途中
- Baseline（設計基準）: 現時点で採用している設計
- Superseded（置き換え済み）: 後のADRなどで置き換えられたもの

設計基準は、永久に変更しないという意味ではありません。

要求、検証根拠、運用で得た知見が変わった場合は、理由と移行方法を記録したうえで更新します。

## 未解決の研究を採用済み設計として扱わない

全体構成には、機能を追加・差し替えするための拡張点を設けられます。ただし、まだ実証されていない技術を、必須の依存先として先取りすることはしません。

たとえば、次のものは接続できる設計にしますが、中核部分が成り立つための必須条件にはしません。

- Remote Attestation
- Verifiable Computation
- Learned Scheduler
- Attested Contribution Metering

標準の構成は、これらがなくても、Requirementが求める最低限の保証を説明できなければなりません。

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
