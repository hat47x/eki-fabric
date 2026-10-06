# ADR-0001 通信面ごとのProtocolと、署名対象の正規エンコーディング

- Status: **Accepted for MVP Baseline 0.1**
- Date: 2026-09-21
- Scope: External Client Plane、Worker Plane、out-of-processのAdapter Plane、署名付きのAuthority Decision

## Context

EKI Fabricは、未知のApplication、Native Worker、将来のBrowser Worker、企業のNetwork、複数の言語で書かれたAdapterを同時に扱う必要があります。

ところが、次の要件は一つのProtocol技術では同時に満たせません。

- External APIは、curlなどから扱いやすく、特定の言語に依存せず、企業のProxyと共存しやすい必要がある。
- Worker Planeでは、Workerから外向きに張るsession、streaming、deadline、型付きのmessage、効率のよいbinary encodingが欲しい。
- Securityに関わるDecisionでは、言語、build、schemaの進化が違っても、署名対象のbytesが一つに定まらなければならない。
- 大きなArtifactは、RPCのmessageへbase64で埋め込まず、Data Planeでstreamする必要がある。

特にProtocol Buffersの公式文書は、deterministic serializationはcanonical serializationではないと説明しています。同じ文書は、言語やbuildが違う場合やunknown fieldがある場合に、Protobufのbytesを安定したfingerprintとして使うべきではないとも述べています。

そのため、通信に使うIDLと、署名に使う正規形（canonical form）を同じものにはしません。

## Decision Summary

### External Client Plane

**HTTPS + JSON HTTP API + OpenAPI**をBaselineとします。

- Architecture上で目標とするOpenAPI Specificationのversionは3.2.1とする。
- P0 schemaでは、toolとの互換性を優先する。3.2だけにある機能へ依存しない、控えめな部分集合から始める。
- JSON bodyは、厳格にparseしてからschemaで検証する。
- HTTPのerror bodyは、RFC 9457のProblem Detailsを基本の形にする。
- Artifactの大きなbytesは、JSONへbase64で埋め込まず、Data Planeのupload / downloadへ分ける。
- 初回MVPでは、TaskのPublic statusをpollingで取得できれば足りるとする。event streamingは後の拡張とする。

### Worker Plane

**Protocol Buffers IDL + Connect protocol over HTTPS**をReference Transportとします。

- Workerは、自分から外向きに接続を張る（outbound-first）。
- MVPの参照実装ではConnect protocolを使い、HTTP/1.1やHTTP/2の環境と共存できることを優先する。
- 同じProtobufのservice定義から、将来gRPCやgRPC-Webとも相互運用できる構造を保つ。
- 企業のproxyが長時間のstreamingを通さない環境もある。そうした環境向けに、unaryやpollingを一定回数で区切るfallback profileを、Protocolの意味の上で認める。
- 内部のQueueやBrokerのprotocolを、Workerへ直接公開しない。

### Out-of-process Adapter Plane

**Protocol Buffers IDL + Connectの参照protocol**を基本とします。

同じプロセス内で動くGoのAdapterは、同じ論理SPIをGoのinterfaceとして実装できます。ただし、GoのinterfaceをSPIの唯一の正本にはしません。

### Signed Authority Decisions

**Deterministically Encoded CBOR + CDDL + COSE_Sign1**をBaselineとします。

- CBORは、RFC 8949のcore deterministic encodingの要件を基礎にする。
- schemaの記法にはCDDLを使う。
- 署名の外枠（envelope）にはCOSE_Sign1を使う。
- 署名algorithmとkeyの種類は、別のADRかSecurity Profileで決める。
- Securityに関わるpayloadでは、原則として浮動小数点を使わない。
- timestamp、revision、byte数、budgetなどは、単位を固定した整数で表す。
- 未知のcritical fieldや、対応していないDecisionのversionを受け取ったら、処理を進めずに拒否する（fail closed）。

### Semantic Fingerprint

Taskの冪等性（idempotency）の判定やDecisionとの結び付けでは、受け取ったJSONのbytesやProtobufをserializeしたbytesを、そのままfingerprintにしません。

External JSONを厳格にparseし、検証し、正規化します。そのうえで、用途ごとに決めた意味上の射影（semantic fingerprint projection）をdeterministic CBORへencodeし、そのbytesのhashを取ります。

Artifactの内容を表すContent Digestだけは、Artifactの生のbytesそのものを対象にします。

---

## Why this split

### External HTTP / JSON

未知のClientに、SDKの使用を強制しないためです。

HTTP APIには、次の利点があります。

- curlや汎用のHTTP clientから使える。
- Browser、CLI、Python、Go、Rustなどから扱いやすい。
- 企業のproxy、gateway、監視の仕組みに組み込みやすい。
- OpenAPIで、人にも機械にも説明しやすい。

OpenAPIの公式Specification一覧には、2026-09-10に公開された3.2.1が載っています。3.2系ではstreamingのmedia typeの記述も強化されていますが、MVPのP0ではstreaming機能そのものを必須にしません。

### Worker Connect / Protobuf

Worker Planeでは、公開RESTのresource modelよりも、次の性質が重要です。

- 型付きのrequestとresponse
- server側とclient側のstreaming
- deadline
- 自動生成されるclient
- binary payloadの効率
- messageの進化への対応

Connectは、Protobufのschemaからコードを生成し、Connect、gRPC、gRPC-Webのそれぞれと互換の口を持ちます。Connect protocol自体は、HTTP/1.1とHTTP/2の上で動きます。

EKIの企業向けの既定構成は、外向き通信だけで動き、proxyと共存できることです。また、将来はBrowser Workerを持ちます。Connectはこの二つと相性がよいと判断しました。

ただし、Connectのlibrary自体をEKI Coreの意味上の契約にはしません。

Transportを別の実装へ替えても、Worker Contractの意味は変わりません。

### Deterministic CBOR / COSE

Authority Decisionでは、署名を検証する側が、同じ意味のpayloadから同じbytesを組み立て直せる必要があります。

RFC 8949は、deterministic CBORの基本要件を定めています。

COSE_Sign1は、署名者が一人のsigned data objectを標準化したものです。

CBORは64-bitの整数やbyte stringをそのまま扱えます。そのため、JCSが前提とするI-JSONの数値の制約を、Securityのpayloadへ持ち込まずに済みます。

---

## Alternatives Considered

### A. すべてをREST / JSONにする

利点は次のとおりです。

- 最も分かりやすい。
- curl、Browser、proxyと相性がよい。
- OpenAPI一つで統一できる。

採用しなかった理由は次のとおりです。

- Workerの長時間streamingや、型付きで生成されるprotocolには、追加の設計が必要になる。
- 署名付きDecisionの正規化を、別に解決しなければならない。
- Workerとの細かいbinaryのやり取りの効率が下がる。

External Planeには採用しますが、すべてのPlaneへは広げません。

### B. すべてをgRPC / Protobufにする

利点は次のとおりです。

- 成熟したRPCである。
- 双方向streamingがある。
- 多くの言語向けにコードを生成できる。
- binary encodingが効率的である。

採用しなかった理由は次のとおりです。

- 公開APIを使う未知のClientに、gRPCのtoolchainを求めたくない。
- Browserやproxyの環境では、gRPC-Webやgatewayの追加検討が要る。
- Protobufのserializationは、暗号用の正規形ではない。

Worker PlaneとAdapter Planeの候補としては利用します。

### C. すべてをConnect / Protobufにする

利点は次のとおりです。

- HTTPと相性がよい。
- Browserと相性がよい。
- gRPCと互換性がある。
- clientを自動生成できる。

採用しなかった理由は次のとおりです。

- 公開resource APIが、特定のRPCの生態系へ寄りすぎる。
- OpenAPIや素のHTTPとの統合を、第一級のまま保ちたい。
- 暗号用の正規化の問題は残る。

### D. 署名付きDecisionにJSON + JCSを使う

JCS（RFC 8785）はJSONの正規表現を定めており、有力な候補でした。

採用しなかった理由は次のとおりです。

- I-JSONとIEEE 754の倍精度の制約を、Securityに関わる数値fieldへ持ち込むことになる。
- byte stringや大きな整数を、文字列へ変換して表す必要がある。
- 内部のAuthority payloadでは、wire上で人が読めることの必要性が低い。

External requestのlogや比較の用途には使えますが、Baselineの署名encodingには採用しません。

### E. Protobufのdeterministicなbytesに署名する

採用しません。

Protobufの公式文書は、deterministic serializationとcanonical serializationをはっきり区別しています。そして、言語、build、schemaの進化をまたいだ汎用のfingerprintとして使うことを推奨していません。

---

## External Client API Profile

### HTTP

- TLSで保護したHTTPSを使う。
- 最低限の互換性としてHTTP/1.1を認める。
- HTTP/2を使える。
- HTTP/3はMVPでは必須にしない。

### JSON

External JSONに対しては、次の処理を行います。

- 重複したpropertyを拒否する。
- bodyの最大sizeを制限する。
- 入れ子の最大の深さを制限する。
- 文字列、配列、metadataの最大数を制限する。
- 整数の範囲を検証する。
- 未知のcore fieldを拒否する。

JSONのnumberが任意の精度の整数を表せるとは期待しません。

Securityやbudgetの都合で64-bitの精度が必要なfieldは、schema上で範囲を明示します。必要であれば、10進の文字列のような明示的な表現を使います。

### OpenAPI

API記述の正本として目標とするのはOpenAPI 3.2.1です。

ただし、2026-09の時点で3.2.1は新しいため、P0では次の控えめな部分集合を使います。

- 通常のJSONのrequestとresponse
- 標準のHTTP method
- 3.2にしかないstreamingの構文を必須にしない

Toolchainが3.2.1を処理できない場合に備えて、元の意味のschemaを弱めずに、3.1互換の派生物を生成できる余地を残します。

### Error

HTTPのerrorは、RFC 9457の`application/problem+json`を基本にします。

EKI固有の拡張として、次の項目を追加できます。

- 安定したerror code
- 開示しても安全な範囲での、TaskやAttemptとの対応付け
- retryできるかどうかの分類
- policyによる拒否理由の分類

`detail`の文字列を、機械側の処理の判断に使わせません。

---

## Worker Plane Profile

### Protobuf

Protobufは、Worker PlaneのIDLと、通信するmessageの形式として使います。

用途は次のとおりです。

- WorkerHello
- ResourceAdvertisement
- WorkOfferとWorkOrderの運搬
- WorkOrderAck
- Heartbeat
- LeaseRenewalRequestとその結果
- ResultClaimの運搬
- StopとReclaimの通知

Securityに関わるAuthority Decisionの、署名対象そのものには使いません。

### Connect

Reference transportはConnectです。

理由は次のとおりです。

- 標準的なHTTPの基盤に載せやすい。
- HTTP/1.1とHTTP/2を扱える。
- gRPCやgRPC-Webの生態系と相互運用する道がある。
- Browser Workerへ広げやすい。
- Goのnet/httpと軽く統合できる。

### Session modes

MVPでは次の二つの方式を認めます。

1. streaming session
2. pollingまたはunaryによるfallback

どちらの方式でも、次の値は同じにします。

- WorkOrder ID
- Attempt ID
- Lease revision
- 重複して届いたときの扱い

通信の方式によって、状態の意味を変えません。

---

## Bulk Artifact Profile

ArtifactのbytesはRPCのmessageに入れません。

Control PlaneのAPIでUpload IntentやArtifactGrantを取得し、Data Planeのendpointへ転送します。転送には`application/octet-stream`や、Adapterごとのmultipart、再開可能な転送方式などを使います。

Content Digestは、Artifactの生のbytesを対象にします。

圧縮する場合は、保存した識別子が圧縮後のbytesを指すのか、展開した論理上のbytesを指すのかを、ArtifactのMediaやProfileで明示します。

archiveを展開した後の中身を、元のArtifactのDigestと取り違えません。

---

## Signed Decision Profile

### Payload

Decisionのpayloadは、deterministic CBORでencodeします。

### Schema

各versionで認めるmap、field、型をCDDLで定義します。

### Envelope

COSE_Sign1を使います。

Protected metadataでは、少なくとも次の項目を扱える構造にします。

- algorithmの識別子
- keyの識別子
- contentまたはDecisionの種類

Authority ID、Decision Kind、schemaのversionは、署名で保護される意味の中に必ず含めます。

### Cryptographic algorithm

このADRでは固定しません。

次の観点を、別のSecurity Profileで比較するためです。

- 企業のPKIやKMSとの適合性
- platformが対応する暗号
- algorithmを後から替えられること
- 現行の標準

Workerが、Clientからの入力に従ってalgorithmを選ぶ構造にはしません。

---

## Fingerprint Profile

冪等性の判定やbinding digestには、用途ごとの射影（Projection）を定義します。

たとえば次のようなものです。

```text
TaskRequestFingerprintV1
  principal scope
  logical executor
  normalized inputs
  normalized params
  resource request
  reliability semantics
```

External JSONの次のような違いでは、Fingerprintが変わらないことを目指します。

- 空白
- objectのpropertyの順序
- 同じ意味になる表記の違い

一方で、未知のfield、省略されたfield、既定値のfieldは、意味が違うことがあります。これらを誤って同じものとみなさないよう、Projectionのversionを固定します。

---

## Security Parser Boundary

External JSONのparser、Protobufのdecoder、CBORのdecoderは、いずれも攻撃を受ける入口です。

共通して次の制約を求めます。

- messageの最大bytes数
- 入れ子の最大の深さ
- 繰り返しfieldの数の上限
- 文字列とbytesの長さの上限
- 展開後のsizeの上限
- 際限なく再帰する拡張を認めない
- 未知のcriticalな意味を拒否する
- parserのpanicの隔離とfuzzing

---

## Compression

Control messageの圧縮は任意です。

圧縮爆弾（展開すると巨大になるデータ）を避けるため、次の制約を適用します。

- 圧縮後のsizeの上限
- 展開後のsizeの上限
- 対応するalgorithmの許可リスト
- 入れ子になった透過的な圧縮を認めない

大きなArtifactの圧縮は、ArtifactのprofileとSafe Processing Budgetで別に扱います。

---

## Compatibility Rules

### External API

- 任意で追加されるfieldは、minorな進化の候補とする。
- 意味を変える場合は、versionを上げる。
- MVPの厳格modeでは、未知のcore fieldを拒否する。
- 名前空間付きの任意の拡張だけを、前方互換として扱う。

### Protobuf

- field番号を再利用しない。
- 削除したfieldはreserveする。
- Security Decisionでは、未知のenumの値を、権限を与えない側の値として処理する。
- Securityに必須の意味を、未知のfieldに持たせない。

### Signed CBOR Decision

- Decisionのschemaのversionを必須にする。
- 未知のfieldの扱いを、versionごとに定義する。
- Securityに関わる未知の要素は拒否する。
- 署名済みの古いversionを、知らないうちに別の意味で解釈しない。

---

## MVP Technology Cut

### P0 External

- HTTPS
- JSON
- OpenAPI 3.2.1による記述
- RFC 9457のerror

### P1 Worker

- Protobufのschema
- Connectの参照transport
- pollingによるfallback

### P2 Security Decision

- deterministic CBOR
- CDDL
- COSE_Sign1

### Bulk

- Data Planeでのbinary転送

---

## Consequences

### Positive

- External Clientは特定のSDKに依存しない。
- Worker Planeは型付きで、streamingを扱いやすい。
- 将来のBrowser Workerで、同じProtobufのservice modelを使い回しやすい。
- 署名付きDecisionが、Protobufのserializationの安定性に依存しない。
- Controlのmetadataと大きなdataが分かれる。
- 特定の言語に依存しない性質を保ちながら、Goの参照実装を簡潔にできる。

### Cost

- JSON、Protobuf、CBORの3つのデータ表現を扱う必要がある。
- 意味のmodelとwireのmodelの間の変換をテストする必要がある。
- CDDLとCOSEのtoolchainが加わる。
- どのschemaを正本とするかの管理ルールが必要になる。

署名のbytesと、公開APIやWorkerの使いやすさを、一つの形式へ無理に押し込むことはしません。この複雑さを抱える方が、それより明示的だと判断しました。

---

## Required Conformance Tests

### JSON / OpenAPI

- 重複したproperty
- 未知のcore field
- 数値の境界
- 大きすぎるbody
- 不正なUTF-8と文字列の制約
- 意味上のfingerprintの安定性

### Protobuf / Connect

- 未知のenumに対するSecurity上の振る舞い
- 重複した配送
- streamの再接続
- HTTP/1.1でのfallback
- messageのsizeの上限
- deadlineとcancel

### CBOR / COSE

- deterministicでないencodingを拒否するか、正規の形に再encodeするかの方針
- mapの重複したkey
- 誤ったDecision Kind
- 誤ったkey IDやAuthority
- 改変されたpayload
- 古いversion
- 未知のcritical field
- 署名algorithmの許可リスト

---

## Sources

- OpenAPI Specification 3.2.1: https://spec.openapis.org/oas/v3.2.1.html
- RFC 9457 Problem Details: https://www.rfc-editor.org/rfc/rfc9457.html
- gRPC Core Concepts: https://grpc.io/docs/what-is-grpc/core-concepts/
- Connect: https://connectrpc.com/
- Protocol Buffers, Serialization Is Not Canonical: https://protobuf.dev/programming-guides/serialization-not-canonical/
- RFC 8949 CBOR: https://www.rfc-editor.org/rfc/rfc8949.html
- RFC 8610 CDDL: https://www.rfc-editor.org/rfc/rfc8610.html
- RFC 9052 COSE Structures: https://www.rfc-editor.org/rfc/rfc9052.html
- RFC 8785 JCS: https://www.rfc-editor.org/rfc/rfc8785.html

## Follow-up

次は、このADRにもとづいてP0の機械可読なschemaを作ります。

対象は次のとおりです。

- TaskRequest
- Taskの公開status
- RFC 9457のEKI向けProblem拡張
- Artifactの取り込みintentとdescriptor

WorkerのProtobufと、署名付きDecisionのCDDLは、P1とP2として別の作業単位で追加します。
