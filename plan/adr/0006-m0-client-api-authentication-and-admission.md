# ADR-0006 M0のClient API認証とTask Admissionの境界

- Status: **Accepted for M0 / DP-0, DP-1**
- Date: 2026-10-06

## 1. Context

P0 External Contract（`spec/external/openapi.json`）は、すべてのoperationで認証を必須（`x-eki-authentication-required: true`）にしていますが、認証方式そのものは固定していません。OIDCやmTLSなどの具体的な方式は、別のSecurityの作業単位で決める前提です。

一方、M0 vertical sliceでは、悪意あるClientからTaskを受理するところから確認する必要があります。認証なしで動かす暫定版は作りません。「あとで認証を足す」前提で作ると、匿名で動く経路がテストと設計の既定になり、後から塞ぎにくくなるためです。

このADRは、M0で何を実装し、何を実装しないかだけを決めます。

## 2. Decision

### 2.1 認証

M0では **固定のBearer token** を使います。

- tokenは設定ファイルに平文で置かず、SHA-256のhexだけを置く。設定ファイルが漏れても、使えるtokenは漏れない。
- tokenは`Authorization: Bearer`ヘッダだけから読む。query stringやbodyからは読まない（URLはログやproxyに残る）。
- 照合は、全entryを最後まで比較する定数時間の比較で行う。どのentryが一致したか、いくつあるかを時間から推測させない。
- 受理するtokenは20文字以上、512文字以下。生成は`eki-control gen-token`が行い、256bitの乱数をbase64urlにする。
- tokenにはPrincipalとRoleを紐づける。Roleは`client`と`admin`で、Client APIは`client`だけを受け付ける。`admin`のtokenでTaskを提出することはできない（RQ-015、RQ-016）。管理用のAPIはM0にはなく、Roleの分離だけを先に固定する。
- `Handler`は認証器なしでは作れない。匿名で配信する経路をコードに残さない。

M0の固定tokenには、失効、期限、ローテーション、利用者ごとの監査がありません。これらはProduction向けの方式で扱います。M0の固定tokenを、Production認証の代わりにしません。

### 2.2 通信路

- TLSを設定できる（TLS 1.2以上）。
- TLSなしの平文HTTPは、loopbackアドレスでだけ起動する。それ以外のアドレスでは、`allow_insecure_listen`を明示しない限り起動を拒否する。信頼できるproxyが手前でTLSを終端する構成のための、意図的で目に見えるopt-outである。
- Serverには、header、読み取り、書き込み、idleの各timeoutと、headerの大きさの上限を設定する。

### 2.3 Task Admission

`internal/core/admission`がADM-2を担当し、HTTPを知りません。次の順序で、最初に失敗した段階で拒否します。

1. Idempotency-Keyの形式
2. JSONの構造検査（UTF-8、重複key、深さ、配列長、object幅、文字列長、末尾データ）
3. P0 schemaの検証（未知のfieldを拒否し、必須fieldと範囲を確認）
4. policy（承認済みExecutor、per-task資源上限、retry上限、deadline、GPU・Artifact・拡張の拒否）
5. 原子的な受理（冪等性、principalごとの未完了Task数上限、Task作成）

M0で意図して拒否するものは次のとおりです。いずれも「後で使えるようにする」のではなく、対応する権限と部品がまだないために、安全側で拒否します。

- Artifactを入力にするTask（Artifact Storeがなく、入力を認可できない）
- GPU資源
- 拡張（`extensions`）
- Executorのversion制約の解決（Executor Admission、EXE-1が未実装。要求された制約はTask IRに記録するだけ）

### 2.4 冪等性

- Keyは**principalごと**。別のprincipalが同じKey文字列を使っても衝突しない。
- 同じKeyで、正規化した後のrequestも同じなら、同じTaskを返す（HTTPは同じ`202`）。同じKeyで別のrequestなら`409`。
- 比較に使うfingerprintは、key順、空白、省略されたdefaultに依存しない。一方、数値は送られたliteralのまま扱う（`1`と`1.0`は別のrequest）。誤るとしても「別のrequestを同じものとみなす」のではなく、「同じrequestを別のものとしてconflictにする」側を選んだ、ということである。
- 受理と冪等性の記録と未完了Task数の確認は、一つのtransactionで行う。同時に来た同じKeyの提出が二つのTaskを作ることも、同時の提出が上限を超えることもない。
- 再提出（replay）は、上限に達していても拒否しない。
- Cancelの`Idempotency-Key`は契約上は必須だが、保存はしない。Cancelは繰り返しても結果が変わらないからである。

### 2.5 可視性

- Taskを観測・Cancelできるのは、提出したprincipalだけ。
- 存在しないTask、他のprincipalのTask、形式が不正なTask IDは、**同じ`404`**を返す。Task IDの探索を許さない（RQ-018）。
- 公開statusは内部状態の縮約で、Worker、Attempt、Lease、policy revision、principal、digestを含まない。

### 2.6 Cancel

- まだAttemptを持たないTask（admitted、ready）は、副作用を持ち得ないため、Cancel要求と同時に`cancelled`で終了する。
- 実行中のTaskは、Cancellation Intentを記録するだけにする。実際の停止は、実行を所有する部品（今後のScheduler、Lease Authority）が行う。
- 終了済みTaskのCancelは`409`。すでにCancel済みのTaskへの再Cancelは成功する。

## 3. Consequences

### Positive

- 匿名の経路がない。
- 設定ファイルの漏えいがtokenの漏えいにならない。
- 再起動をまたいでも、Task、冪等性、未完了Task数の上限が保たれる。
- OpenAPIの応答schemaとの一致を、テストで機械的に確認できる。

### Limitations

- 固定tokenは失効できない（設定を変えて再起動する）。
- 一つのprocessの未完了Task数上限だけで、request rateの制限は持たない。
- P0 OpenAPIが宣言していない`401`、`405`、`501`を、認証と未実装のために返す。P0へ反映する場合は別途contractを更新する。
- Admission後にpolicyが厳しくなった場合、同じKeyの再提出がpolicyの検査で`422`になる。元のTaskが返るのは、policyを通る間だけである。この順序は、新規の不正な作業を先に拒否することを優先した結果である。

## 4. 実装記録（2026-10-06）

- `internal/core/admission`、`internal/control/clientapi`、`internal/control/service`、`cmd/eki-control`に実装した。
- State Store SPIに`AdmissionStore`を追加し、memoryとSQLiteの両方が共通のcontractを通る（SQLiteはmigration 2、全transactionを`BEGIN IMMEDIATE`にした）。
- 検証はGo 1.27.0（WSL2、Linux）。実processでのsmoke（提出、未認証、SIGTERMでの正常終了、state fileのmode 0600、tokenがログに出ないこと）も確認した。
