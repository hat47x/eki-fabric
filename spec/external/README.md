# EKI Fabricの外部接続仕様（P0）

状態: **設計基準0.1（2026年9月21日）**

このディレクトリには、EKI Fabricと外部の依頼元をつなぐ、最初の機械可読な契約を置きます。

正本は次のとおりです。

- `openapi.json` — OpenAPI 3.2.1
- 設計上の判断: `../../plan/adr/0001-protocol-surfaces-and-canonical-encoding.md`

## P0の範囲

P0で固定するのは次です。

- 処理依頼の投入
- 処理依頼の公開状態
- 処理依頼の取り消し要求
- 受理済み結果の取得
- 成果物の取り込み要求
- 上限付きの成果物アップロード
- 公開可能な成果物の記述
- RFC 9457 Problem Detailsに基づくエラーの形式

実行端末との通信、作業指示書、期限付き実行権、成果物の利用許可を示すトークン、配置、内部規則の改訂番号、加工前の検証根拠は、P0の外部接続契約には含めません。

## 信頼できない入力への対処

OpenAPIによる形式の検証だけで、安全を保証できるとは考えません。

受信する入口では、少なくとも次の項目を、スキーマの検証前または検証と同時に制限します。

- 要求本文のバイト数
- 入れ子の深さ
- 配列の長さ
- 文字列の長さ
- オブジェクトの項目数
- JSONの同名項目の重複
- 不正な符号化
- 展開後のデータ量
- 要求の処理時間の上限

中核となるオブジェクトには、原則として`additionalProperties: false`を指定します。

明示的な拡張点は次だけです。

- `TaskRequest.parameters`
- `ExtensionEnvelope.payload`

`parameters`を実行端末へそのまま渡すことはありません。処理依頼の受理審査で、承認済みの実行体が定めるパラメータの形式と照合します。

## 同じ要求の重複処理を防ぐ仕組み

処理依頼の投入・取り消しと成果物の取り込み要求には、`Idempotency-Key`を必須とします。

キーは認証された主体の範囲で扱います。

同じキーを使った場合は、次のように扱います。

- 正規化した意味の指紋値が同じ場合は、同じ論理操作へまとめられる
- 指紋値が異なる場合は、競合として扱う

とします。

元のJSONのバイト列は、指紋値の算出に使いません。

### キーを覚えている期間

この版では、`Idempotency-Key`を覚えている期間は、受理の記録が残っている間ずっとです。受理の記録は削除しないため、実質的に無期限です。

- 同じキーと同じ指紋値で再送すると、そのTaskが終わった後でも、最初に受理したTaskへ収束します。新しいTaskは作りません。
- 同じキーを別の要求に使うと、いつでも競合（`409 IDEMPOTENCY_KEY_CONFLICT`）です。キーは別の操作へ再利用できません。
- 依頼元は、論理的な操作ごとに新しいキーを使ってください。応答を失ったときの再送には、最初と同じキーと同じ要求を使います。

期間を短くする（Taskの終了から一定期間で忘れる）場合は、Task・Attemptの記録と食い違わない削除の手順が要ります。この版では決めていません。

## 認証

P0のOpenAPIでは、認証方式そのものはまだ決めていません。

本番環境では認証を必須とします。

OAuth、OIDC、mTLSなどを使う具体的な方式は、別のセキュリティに関する作業で決定します。

各operationの `x-eki-authentication-required: true` は、OpenAPI上の未選定を「anonymous allowed」と誤読しないためのEKI markerです。

## 成果物のアップロード

制御用の情報と、大きなデータ本体を分けます。

1. 依頼元が取り込み要求を作る。
2. 制御層が容量に上限のあるアップロード用セッションを発行する。
3. データ本体はデータ転送層のアップロード先へ送る。
4. アップロード後も、内容の同一性、安全な処理、来歴を別々の観点で評価する。
5. 利用可能になった成果物だけが処理依頼の入力候補になる。

Clientがdeclared / expected Digestを送っても、それだけでverified contentにはなりません。

## 成果物を評価する側面

`ArtifactDescriptor.facets`は、意図的に一つの状態へまとめていません。

- `content_identity`
- `safe_processing`
- `provenance`
- `availability`

を別々に公開します。

`trusted: true` のような総合bitは作りません。

## 処理依頼の公開状態

公開する状態は、内部の状態モデルから必要な情報に絞ったものです。

External Clientへ返す候補:

- queued
- running
- verifying
- reconciling
- attention-required
- completed
- cancelled
- failed

内部のAttempt、Lease、Worker identity、Policy Revision等は返しません。

特に `running` は「Workerが現在CPU命令を実行していることを証明した」という意味ではなく、Client向けのderived statusです。

## 外部への副作用の扱い

P0/MVPのTaskRequestでは `effect_mode = none` だけを許可します。

これは将来external effectを扱わないという意味ではありません。

Side-effect UNKNOWN / reconciliationのArchitectureは既に定義済みですが、MVPで不完全なgeneric effect executionを先に公開しないためです。

後続versionで追加する場合も、v1の `none` semanticsを変更しません。

## エラー

Error bodyはRFC 9457 Problem DetailsにEKI extensionを追加します。

machine logicは次を利用します。

- HTTP status
- `code`
- `retryable`
- optional `policy_reason_category`

`detail` は人間向けであり、Clientがbranchするstable APIにはしません。

Security上の理由で存在を開示すべきでないTask / Artifactでは、403と404の使い分けをDeployment Policyで情報漏えいしないように扱います。

## データの機密区分

External Clientの `requested_data_class` はSecurity classificationを下げるAuthorityではありません。

Effective Data ClassはAdmission / Data Authorityが入力Artifact、Executor requirement、organization policy等から安全側へ確定します。

## P0で未固定のもの

- Authentication mechanism
- OAuth scope naming
- exact server base URL
- pagination
- event / webhook
- streaming Task status
- Browser-specific CORS policy
- Worker protocol
- signed internal Decision CDDL
- cryptographic signature algorithm
- SDK generation tool
- generic side-effect Task
- Federation

## 適合性の検証で必ず確認すること

### Parser

- JSONの同名項目の重複
- oversized body
- excessive nesting
- invalid UTF-8
- unknown core property

### TaskRequest

- `worker_id` 等のClient-selected Worker fieldをreject
- arbitrary executable pathをreject
- arbitrary input URLをreject
- unsupported effect modeをreject
- Idempotency-Key conflict
- resource numeric bound
- extension count / payload bound

### Artifact

- upload size超過
- expected digest mismatch
- expired upload session
- unknown / unauthorized artifact
- facetをClient入力で昇格できないこと

### Public response

- Worker hostname / IPを含めない
- Lease / Attempt topologyを含めない
- raw verification evidenceを含めない

## 関連する通信仕様の設計

P0に続き、P1の実行端末向け通信仕様を`../worker/v1/worker.proto`で、P2の署名付き権限判断を`../decisions/v1/decisions.cddl`で、いずれも設計基準0.1として定義済みです。

P1の設計に先立って検討した主なメッセージ候補は、次のとおりです。

- WorkerHello
- ResourceAdvertisement
- WorkOrder carrier
- WorkOrderAck
- Heartbeat
- LeaseRenewal
- ResultClaim carrier
- Reclaim / Stop signal

P1はメッセージの配送を扱います。権限判断の署名対象となる内容は、P2のCDDLとCOSEに分けて定義しています。

## M0の参照実装での扱い

参照実装（`internal/control/clientapi`）では、P0のOpenAPIに記した`submitTask`、`getTaskStatus`、`cancelTask`、`getTaskResult`を実装しています。操作ごとの実装状況と応答の一覧は、契約ではなく実装に属するため、[clientapi/README.md](../../internal/control/clientapi/README.md)を正本とします。応答のschemaが`openapi.json`と一致することは、テストが同じ文書を読んで確認します。

認証には、M0に限り固定のBearer tokenを使います（[ADR-0006](../../plan/adr/0006-m0-client-api-authentication-and-admission.md)）。

実装が返す応答は、次のとおり`openapi.json`に宣言しています。

- `401` — 認証がない、または認証に失敗した。`WWW-Authenticate: Bearer realm="eki"`を付ける。
- `405` — 対象pathに対して許可されないmethod。`Allow`ヘッダで許可するmethodを示す。
- `501` — Artifact関連のoperation（Artifact Storeは未実装）。`getTaskResult`も、結果の読み出し口を持たない構成に限って`501`を返す。
- `409`（`getTaskResult`）— 結果をまだ得られない場合（`RESULT_NOT_READY`、待てば得られる）と、得られない場合（`RESULT_NOT_AVAILABLE`、再試行しても変わらない）の2種類。契約の説明で区別している。
- `400` — 要求の本文が`application/json`でない場合も`400`とし、codeは`UNSUPPORTED_MEDIA_TYPE`にする（P0では`415`を使わない）。

応答のstatusが、そのoperationに宣言されているかは、テスト（`internal/control/clientapi/spec_declared_test.go`）が`openapi.json`を読んで確認します。

認証方式は未選定のため、`components.securitySchemes`の`bearerAuth`は暫定です（`http: bearer`）。tokenの形式・有効期間・失効は、認証方式を決める変更で定めます。

Artifactを入力にするTaskは、入力を認可できないため`422`（`ARTIFACT_NOT_AVAILABLE`）で拒否します。
