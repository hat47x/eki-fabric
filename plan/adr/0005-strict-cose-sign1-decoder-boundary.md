# ADR-0005 COSE_Sign1の外枠を厳格に扱う範囲と、CBORコーデックとの境界

- 状態: **承認済み。具体的なアダプタを実装し、依存関係も固定済み（2026-10-06）**
- 日付: 2026-09-22
- 対象範囲: P2の署名付き権限判断の外枠（envelope）のデコード

## 1. 背景

ここまでに、P2のペイロード、権限主体（Authority）と鍵のライフサイクル、暗号処理の前に行う検証、EdDSAとES256の暗号機能を実装しました。

残っている入口は、クライアント（Client）や通信路から受け取った生の`COSE_Sign1`のバイトを、現在の型付き検証器の境界へ安全につなぐ部分です。

この境界は、悪意のある入力を読むパーサです。「CBORをデコードできる」だけでは足りません。次のような入力を、処理を進めずにすべて拒否する（fail closed）必要があります。マップの重複したキー、長さを定めない要素、深すぎる入れ子、分離型ペイロード、ヘッダを保護と非保護の間で移したもの、未知のプロファイル、決定的でないエンコーディング。

## 2. 決定

EKI 基準（Baseline）では、署名付き認可判断（Decision）の外枠を、次の厳格なプロファイルに固定します。

~~~text
CBOR tag           = 18 (COSE_Sign1)
outer structure    = 4-element COSE_Sign1 array
protected          = encoded map; Core Deterministic Encoding required
unprotected        = empty map
payload            = attached bstr; nil prohibited
signature          = 64-byte bstr
external AAD       = empty bstr
~~~

タグの付いた形と付かない形の両方を受け付けることはしません。EKIの運搬用の型（carrier）では、タグ18を必須とします。

## 3. タグ付きの形だけを受け付ける理由

RFC 9052は、COSE_Sign1について、タグの付いた形と付かない形の両方を認めています。

一方、EKIのP1の`SignedDecisionCarrier`は、COSEオブジェクトを中身を解釈しないバイトとして運びます。メディアタイプのパラメータなど、メッセージの種類を別に示す手段も持ちません。

そのため基準では、CBORのタグ18を必須にし、メッセージの種類が曖昧になる余地を入口でなくします。

## 4. 保護されたヘッダ

基準の保護ヘッダで認めるのは、次の4項目だけです。

~~~text
1       alg          -8 (EdDSA) / -7 (ES256)
2       crit         [-70000]
4       kid          1..128 byte bstr
-70000  eki-profile  "eki.signed-decision/v1"
~~~

`-70000`は、COSE Header ParametersのPrivate Use用の整数の範囲（-65536より小さい値）から選んだものです。

EKIのプロファイルヘッダは、EKI独自のセキュリティ上重要な意味を持ちます。そのため、標準の`crit`ヘッダに、必ず`-70000`を含めます。

`alg`と`kid`も、必ず保護側へ置きます。RFC 9052では`kid`を非保護側へ置くこともできますが、EKI 基準では認めません。

## 5. 保護されないヘッダ

MVPと基準では、非保護ヘッダを空のマップに固定します。

現在必要な経路制御とセキュリティの意味は、すべて保護されたペイロードとヘッダに持たせています。非保護側の拡張を認めても、実際の利益がありません。

将来、副署名（countersignature）、タイムスタンプ、x5chain、ネットワークのヒントなどが必要になった場合は、プロファイルのバージョンを上げるか、明示的な拡張として追加します。

## 6. ペイロードは同梱の形だけを認める

`payload = nil`という、ペイロードを外に置く形（分離型）は拒否します。

署名対象のバイトを、ワーカー（Worker）が別の経路や呼び出し側の文脈から補う構造は作りません。検証するペイロードは、COSEオブジェクトの中だけで完結させます。

## 7. 決定的エンコーディング

次の2つには、RFC 8949のCore Deterministic Encodingを求めます。

- 保護ヘッダのマップをエンコードしたバイト
- P2の署名付き認可判断のペイロード

具体的なデコーダは、デコードに成功したことだけでは、正規の形かどうかを判断しません。デコードした後、同じ意味の値をCore Deterministic Encodingでエンコードし直し、元のバイトと一致することを確かめます。

外側のCOSEオブジェクトも、通信路の途中で形を変えられる余地を減らすため、生成する側では決定的な形を基準とします。認可判断の識別や冪等性の判定は、COSEオブジェクトや署名のバイトに頼りません。署名付き認可判断のID、ペイロードのダイジェスト、nonceを使います。

## 8. CBORへの依存

具体的なコーデックの第一候補は、`github.com/fxamacker/cbor/v2`とします。

採用の理由は次のとおりです。

- RFC 8949に対応している。
- Core Deterministic Encodingの設定が用意されている。
- マップの重複したキーを拒否する設定がある。
- 長さを定めない要素を拒否する設定がある。
- 入れ子の深さ、マップのペアの数、配列の要素数に上限を設けられる。
- 未知の構造体フィールドをエラーにする設定がある。
- ファジングと、セキュリティを意識したデコードの制御がある。
- MIT Licenseである。
- 2026-09の時点でもリリースが続いている。

このADRを作った時点では、実行環境から`proxy.golang.org`への通信が許可されていませんでした。そのため、`go get`でモジュールをロックすることも、実際にテストすることもできませんでした（実装については§18の実装記録を参照）。

したがって、このADRの時点では、依存関係を`go.mod`へまだ追加しません。取得、ロック、テストができる環境でバージョンを固定した時点で、具体的なアダプタをマージの対象にします。

## 9. go-cose

`github.com/veraison/go-cose`は、成熟したCOSE_Sign1の参照実装です。設計、テスト、相互運用の確認には使いますが、基準の直接の依存関係にはしません。

EKIは、次の項目をすでに独自のセキュリティの境界として固定しているからです。

- 権限主体ロールと認可判断の種類との結び付け
- 管理者が管理する鍵レジストリ（Key Registry）
- アルゴリズムの許可リスト
- 鍵のライフサイクルと失効
- 信頼情報の束の新しさ
- 認可判断の有効期間
- 型付きの署名鍵の選び方
- 暗号機能

go-coseの汎用の署名器、検証器、拡張できるアルゴリズムとヘッダのモデルをそのまま採用すると、これらの責務と重なります。そして、疑わしい入力を拒否するEKI固有のプロファイルを迂回する入口が増えるおそれがあります。

一方で、go-coseは内部で`fxamacker/cbor/v2`を使っています。go-coseがCOSE_Sign1やSig_structureをどう組み立てているかは、参照として積極的に照らし合わせます。

## 10. パーサの上限

ADR-0004で決めた上限を維持します。

- COSE_Sign1の全体は256 KiB以下とする。
- エンコードした保護ヘッダは4 KiB以下とする。
- `kid`は128バイト以下とする。

具体的なデコーダでは、これに加えて、CBORの入れ子の深さ、配列の要素数、マップのペアの数に、プロトコルで必要な量より十分に小さい固定の上限を設けます。

基準の構造は小さいので、ライブラリの既定値をそのまま使うことはしません。

## 11. 未知のフィールドと重複したフィールド

保護ヘッダでは4項目だけを認め、未知のラベルは拒否します。

マップの重複したキーは、常に形式が不正なものとして拒否します。

同じラベルが保護と非保護の両方に現れる問題は、非保護を空のマップに固定することで、基準では構造的に起こらなくします。

## 12. アルゴリズムの対応

Go Coreでは、`Algorithm.COSEIdentifier()`と`AlgorithmFromCOSEIdentifier()`で、相互に変換できるようにします。

~~~text
EdDSA -> -8 -> EdDSA
ES256 -> -7 -> ES256
~~~

未知のCOSEアルゴリズム識別子を、代替のアルゴリズムへの切り替えや独自の登録によって受け入れることはしません。

## 13. 機械可読な契約

外側の枠とプロファイルの正本として、`spec/decisions/v1/cose-profile.cddl`を追加します。

中に入るペイロードの正本は、引き続き`spec/decisions/v1/decisions.cddl`です。

Goの定数との食い違いは、`conformance/protocol/cose_profile_schema_test.go`で検出します。

CDDLだけでは、Core Deterministic Encodingのバイト単位の正規性を完全には表せません。この部分は、具体的なアダプタの適合性テストで補います。

## 14. 検証の順序

具体的なデコーダを入れた後の順序は、次のとおりです。

~~~text
1. transport / total byte limit
2. CBOR well-formedness + parser resource limits
3. tag 18 / four-element COSE_Sign1 shape
4. empty unprotected map / attached payload / signature shape
5. protected header strict decode
6. alg / crit / kid / profile validation
7. protected-header deterministic byte check
8. payload deterministic byte check
9. P2 Decision CDDL / kind decode
10. pre-crypto trust/policy gate
11. Sig_structure construction
12. cryptographic signature verification
13. post-crypto trust/policy recheck
14. Decision-specific subject / prerequisite / current-state validation
~~~

## 15. エラーの対応付け

具体的なパーサは、ライブラリのエラーをそのまま外へ公開しません。既存の`VerificationReason`へ正規化します。

- 不正なCBOR、誤ったタグ、誤った形は`MALFORMED_COSE`にする。
- 保護ヘッダの不正、欠落、重複は`PROTECTED_HEADER_INVALID`にする。
- 誤ったEKIプロファイルは`UNSUPPORTED_PROFILE`にする。
- 決定的でない保護ヘッダは`PROTECTED_HEADER_INVALID`にする。
- 決定的でない署名付き認可判断のペイロードは`PAYLOAD_NOT_DETERMINISTIC`にする。
- 未知のアルゴリズムや無効にしたアルゴリズムは`ALGORITHM_NOT_ALLOWED`にする。

## 16. 対象外

この作業単位では、次のことを扱いません。

- COSE_Sign
- 副署名（countersignature）
- 分離型ペイロード
- 任意の外部AAD（external AAD）
- 任意のアルゴリズムや独自のアルゴリズムの登録
- x5chainやWebPKIによる信頼の立ち上げ
- COSE_Keyを信頼の起点（Trust Root）として受け入れること
- 汎用のCBORによるRPCやデータのシリアライズへの拡張

## 17. 出典

- RFC 8949: https://www.rfc-editor.org/rfc/rfc8949.html
- RFC 9052: https://www.rfc-editor.org/rfc/rfc9052.html
- RFC 9053: https://www.rfc-editor.org/rfc/rfc9053.html
- IANA COSE registry: https://www.iana.org/assignments/cose/
- fxamacker/cbor: https://github.com/fxamacker/cbor
- veraison/go-cose: https://github.com/veraison/go-cose

## 18. 実装の状況と今後の作業

依存関係を取得できなかった間の先行作業として、厳格な適合性テストベクタ、`internal/spi/cosedecoder`、`conformance/cosedecodertest`までを実装しました。

このSPIは、具体的なコーデックからCoreへ、CBORライブラリへの依存を漏らしません。返すのは、保護ヘッダの正確なバイト、同梱のペイロード、署名、アルゴリズム、`kid`だけです。エラーのときに途中まで埋めた外枠（Envelope）を返すことを禁止し、バイトスライスは防御的にコピーします。Goのnilスライスと空のスライスを取り違えないよう、nilの分離型ペイロードは拒否し、同梱された空のbstrは保持します。

共通のテストハーネスは、リポジトリのフィクスチャを`go:embed`で読み込みます。そして、具体的なアダプタに次のことを求めます。外枠、保護、ペイロードの各段階で安定した理由を返すこと。正常系のSig_structureを組み立てること。署名を改ざんした入力では、暗号の段階まで進んでから拒否すること。

次の具体的な作業では、モジュールの依存関係を取得、ロック、テストできる環境で`fxamacker/cbor/v2`を固定します。そのうえで、厳格なCOSE_Sign1のデコーダとSig_structureのエンコーダを、このSPIに実装します。

### 実装記録（2026-10-06）

`internal/adapters/reference/cosedecoder/cbor`に、厳格なCOSE_Sign1のデコーダとSignature1のエンコーダを実装しました。`fxamacker/cbor/v2`のv2.9.2を`go.mod`と`go.sum`で固定し、Go 1.27.0（WSL2、Linux）ですべての適合性テストベクタが通ります。ほかに次の項目も確認しました。途中で切れた入力、1バイトだけ変えた入力、ペイロードの形と上限、保護ヘッダ、非保護ヘッダ、外側の配列の要素数、nilのコーデック、署名の往復。また、40秒のファズ（約120万回の実行）で、パニックや正規化されていないエラーが出ないことも確認しました。

共通フィクスチャの`kid_too_long`は、実装に当てて初めて、バイトが不正だと分かりました。kidの長さを示す接頭辞は129バイトなのに、本体は133バイトありました。そのため保護ヘッダの長さと合わず、外側の配列の段階でMALFORMED_COSEとして拒否されていました。これでは、意図した「kidが128バイトを超える」入力になりません。期待する理由は変えずに、同じ構造のまま本体を129バイトに作り直しました。ほかの20個のテストベクタは変えていません。

このテストベクタは、実装に当てずに作ったために誤っていました。今後テストベクタを追加するときは、狙った段階より手前の段階で拒否されていないことを、実装で確かめてください。

依存関係を引き続き取得できない環境でも、パーサを独自に実装して回避することはしません。
