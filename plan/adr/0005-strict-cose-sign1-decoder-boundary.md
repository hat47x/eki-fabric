# ADR-0005 — Strict COSE_Sign1 envelope and CBOR codec boundary

- Status: **Accepted; Concrete Adapter implemented and dependency locked (2026-10-06)**
- Date: 2026-09-22
- Scope: P2 signed Authority Decision envelope decoding

## 1. Context

P2 payload、Authority/key lifecycle、pre-crypto verification、EdDSA/ES256 cryptographic primitiveまで実装しました。

残る入口は、Client/transportから受け取ったraw `COSE_Sign1` bytesを、現在のtyped Verifier boundaryへ安全に接続する部分です。

この境界はhostile input parserであり、単に「CBORをdecodeできる」だけでは不十分です。duplicate map key、indefinite-length item、過剰nesting、detached payload、headerのprotected/unprotected移動、unknown profile、非決定的encoding等をfail-closedで扱う必要があります。

## 2. Decision

EKI Baselineでは、signed Decision envelopeを次のstrict profileへ固定します。

~~~text
CBOR tag           = 18 (COSE_Sign1)
outer structure    = 4-element COSE_Sign1 array
protected          = encoded map; Core Deterministic Encoding required
unprotected        = empty map
payload            = attached bstr; nil prohibited
signature          = 64-byte bstr
external AAD       = empty bstr
~~~

tagged/untaggedの両方を受け付けるのではなく、EKI carrierではtag 18を必須とします。

## 3. Why tagged-only

RFC 9052はCOSE_Sign1のtagged/untagged両形式を許容します。

一方、EKI P1 `SignedDecisionCarrier`はopaque bytesとしてCOSE objectを運び、別media type parameterによるmessage-type識別を持ちません。

そのためBaselineではCBOR tag 18を必須にし、message typeの曖昧性を入口で除去します。

## 4. Protected header

Baseline protected headerは次の4項目だけを許可します。

~~~text
1       alg          -8 (EdDSA) / -7 (ES256)
2       crit         [-70000]
4       kid          1..128 byte bstr
-70000  eki-profile  "eki.signed-decision/v1"
~~~

`-70000`はCOSE Header ParametersのPrivate Use integer range（-65536より小さい値）から選びます。

EKI profile headerはcustom security-critical semanticsなので、standard `crit` headerへ`-70000`を必ず含めます。

`alg`と`kid`も必ずprotected側へ置きます。RFC 9052上は`kid`をunprotectedへ置くこともできますが、EKI Baselineでは許可しません。

## 5. Unprotected header

MVP / Baselineのunprotected headerはempty mapへ固定します。

理由は、現在必要なrouting/security semanticsをすべてprotected payload/headerへ持たせており、unprotected extensionを許可する実益がないためです。

countersignature、timestamp、x5chain、network hint等を将来必要とする場合はprofile versionまたは明示的extensionとして追加します。

## 6. Attached payload only

`payload = nil`のdetached formは拒否します。

Workerが署名対象bytesを別channelやCaller contextから補完する構造を作らず、検証対象payloadをCOSE object内で自己完結させます。

## 7. Deterministic encoding

次をRFC 8949 Core Deterministic Encodingとして要求します。

- protected header mapのencoded bytes
- P2 signed Decision payload

concrete decoderはdecode成功だけでcanonicalityを判断しません。decode後に同一semantic valueをCore Deterministic Encodingで再encodeし、元bytesと一致することを確認します。

outer COSE objectについても、transport-level malleabilityを抑えるためdeterministic formを生成側Baselineとします。Decision identityやidempotencyはCOSE object bytesやsignature bytesへ依存せず、signed Decision ID / payload digest / nonceを使います。

## 8. CBOR dependency

Concrete codecの第一候補を`github.com/fxamacker/cbor/v2`とします。

採用理由は次のとおりです。

- RFC 8949対応
- Core Deterministic Encoding preset
- duplicate map key rejection option
- indefinite-length item rejection option
- nesting / map pair / array element上限
- unknown struct field error option
- fuzzingとsecurity-oriented decode controls
- MIT License
- 2026-09時点でもreleaseが継続している

ADR作成時点では、実行環境で`proxy.golang.org`へのegressが許可されず、`go get`によるmodule lockと実testを実行できませんでした（実装は§18の実装記録を参照）。

したがってこのADRではdependencyを`go.mod`へまだ追加しません。取得・lock・testできる環境でversionを固定した時点でConcrete Adapterをmerge対象にします。

## 9. go-cose

`github.com/veraison/go-cose`はCOSE_Sign1の成熟した参照実装として設計・test・interop確認に利用しますが、Baselineの直接dependencyにはしません。

理由は、EKIではすでに次を独自のsecurity boundaryとして固定しているためです。

- Authority role / Decision kind binding
- Admin-managed Key Registry
- algorithm allowlist
- key lifecycle / revocation
- trust bundle freshness
- Decision lifetime
- typed signing key selection
- cryptographic primitive

go-coseのgeneric Signer / Verifier / extensible algorithm/header modelをそのまま採用すると、この責務と重複し、EKI固有のfail-closed profileを迂回する入口を増やす可能性があります。

一方、go-coseが`fxamacker/cbor/v2`を内部利用し、COSE_Sign1やSig_structureをどのように組み立てるかはReferenceとして積極的に照合します。

## 10. Parser limits

ADR-0004の上限を維持します。

- total COSE_Sign1: 256 KiB以下
- protected header encoded bytes: 4 KiB以下
- `kid`: 128 bytes以下

Concrete decoderではこれに加え、CBOR nesting、array elements、map pairsをprotocol必要量より十分小さいhard limitへ設定します。

Baseline structureが小さいため、library defaultを無条件に使いません。

## 11. Unknown and duplicate fields

protected headerは4項目だけを許可し、unknown labelを拒否します。

duplicate map keyは常にmalformedとして拒否します。

protectedとunprotectedに同一labelが現れる問題は、unprotectedをempty mapへ固定することでBaselineでは構造的に排除します。

## 12. Algorithm mapping

Go Coreでは`Algorithm.COSEIdentifier()`と`AlgorithmFromCOSEIdentifier()`を相互変換として持ちます。

~~~text
EdDSA -> -8 -> EdDSA
ES256 -> -7 -> ES256
~~~

unknown COSE algorithm identifierをfallbackやcustom registrationで受け入れません。

## 13. Machine-readable contract

`spec/decisions/v1/cose-profile.cddl`をouter envelope/profileの正本として追加します。

`spec/decisions/v1/decisions.cddl`は引き続きembedded payloadの正本です。

Go constantsとのdriftは`conformance/protocol/cose_profile_schema_test.go`で検出します。

CDDLだけではCore Deterministic Encodingのbyte-level canonicalityを完全には表現できないため、これはConcrete Adapterのconformance testで補います。

## 14. Verification order

Concrete decoder導入後の順序は次です。

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

## 15. Error mapping

Concrete parserはlibrary errorを外部へそのまま公開せず、既存`VerificationReason`へ正規化します。

- malformed CBOR / wrong tag / wrong shape -> `MALFORMED_COSE`
- invalid/missing/duplicate protected header -> `PROTECTED_HEADER_INVALID`
- wrong EKI profile -> `UNSUPPORTED_PROFILE`
- non-deterministic protected header -> `PROTECTED_HEADER_INVALID`
- non-deterministic signed Decision payload -> `PAYLOAD_NOT_DETERMINISTIC`
- unknown/disabled algorithm -> `ALGORITHM_NOT_ALLOWED`

## 16. Non-goals

このwork unitでは以下を行いません。

- COSE_Sign
- countersignature
- detached payload
- arbitrary external AAD
- arbitrary/custom algorithm registration
- x5chain / WebPKI trust bootstrap
- COSE_KeyをTrust Rootとして受理
- generic CBOR RPC/data serializationへの拡張

## 17. Sources

- RFC 8949 — https://www.rfc-editor.org/rfc/rfc8949.html
- RFC 9052 — https://www.rfc-editor.org/rfc/rfc9052.html
- RFC 9053 — https://www.rfc-editor.org/rfc/rfc9053.html
- IANA COSE registry — https://www.iana.org/assignments/cose/
- fxamacker/cbor — https://github.com/fxamacker/cbor
- veraison/go-cose — https://github.com/veraison/go-cose

## 18. Implementation status / Follow-up

dependency取得不能時の先行作業として、strict conformance vectors、`internal/spi/cosedecoder`、`conformance/cosedecodertest`まで実装しました。

SPIはConcrete codecからCoreへCBOR library依存を漏らさず、exact protected-header bytes、attached payload、signature、algorithm、`kid`だけを返します。error時のpartial Envelopeを禁止し、byte sliceはdefensive copyします。Goのnil sliceとempty sliceが混同されないよう、detached nil payloadは拒否し、attached empty bstrは保持します。

共通harnessはrepository fixtureを`go:embed`で利用し、envelope / protected / payload phaseのstable reason、正常系Sig_structure、crypto phaseまで進むtampered signatureをConcrete Adapterへ要求します。

次のConcrete workでは、module dependencyを取得・lock・testできる環境で`fxamacker/cbor/v2`を固定し、strict COSE_Sign1 decoderとSig_structure encoderをこのSPIへ実装します。

### 実装記録（2026-10-06）

`internal/adapters/reference/cosedecoder/cbor`にstrict COSE_Sign1 decoderとSignature1 encoderを実装しました。`fxamacker/cbor/v2` v2.9.2は`go.mod` / `go.sum`で固定済みで、Go 1.27.0（WSL2、Linux）で全conformance vectorを通ります。このほかに、truncation、1 byte変異、payloadの形と上限、protected header、unprotected header、outer arrayのarity、nilのcodec、署名の往復を確認し、40秒のfuzz（約120万実行）でpanicや未正規化のerrorがないことを確認しました。

共通fixtureの`kid_too_long`は、実装に当てて初めて不正なbytesだと分かりました。kidの長さ接頭辞は129 bytesなのに本体は133 bytesあり、protected headerの長さと合わず、outer arrayの段階でMALFORMED_COSEとして拒否されていました。意図した「kidが128 bytesを超える」入力にならないため、期待reasonは変えずに、同じ構造で本体を129 bytesに作り直しました。他の20 vectorは変更していません。

vectorを実装へ当てずに作った結果なので、今後vectorを追加するときは、対象の段階より浅い段階で拒否されないことを実装で確認してください。

dependency取得が引き続き不可能な環境では、parserを独自実装して回避しません。
