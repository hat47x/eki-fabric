# 厳格なCOSE_Sign1参照コーデック

Reference Go実装で、raw `COSE_Sign1` bytesをEKI Fabricのsigned-decision profile（ADR-0005）へ変換するadapterです。`internal/spi/cosedecoder.Codec`を実装します。

`fxamacker/cbor/v2`を使います。Coreは直接importしません。このadapterはhostile inputを読むため、decodeに成功しただけではcanonicalityを認めず、decodeした値をCore Deterministic Encodingで再encodeして元bytesと比較します。

## DecodeSign1が行うこと

ADR-0005 §14の順序で、最初に失敗した段階のstableな`VerificationReason`を返します。

1. total byte limit（256 KiB）
2. CBOR tag 18を最短形で必須にする（untagged、別tag、非最短のtagを拒否）
3. 長さが確定した4要素のarray、trailing dataなし
4. unprotected headerは確定長の空map、payloadはattachedのbstr、signatureは64 bytes
5. protected headerのstrict decode（unknown label、duplicate key、indefinite length、tagを拒否）
6. `alg`、`crit`、`kid`、EKI profileの検証
7. protected headerのbytesがCore Deterministic Encodingと一致すること
8. payloadが一つのdeterministic CBOR mapであること

hard limitはlibrary既定値より小さくしています（nesting 8、array 64要素、map 32組）。

## 失敗の分類

| 状況 | Reason |
|---|---|
| 形が不正（tag、array、unprotected、detached、signature長、trailing data、payloadがmapでない、payload自体がwell-formedでない） | `MALFORMED_COSE` |
| protectedのdecode失敗、必須labelの欠落、`crit`、`kid`、非deterministic | `PROTECTED_HEADER_INVALID` |
| 未知のEKI profile | `UNSUPPORTED_PROFILE` |
| 未知のalgorithm | `ALGORITHM_NOT_ALLOWED` |
| payloadがwell-formedだがdeterministic profileの外（非shortest、map順序、indefinite length、duplicate key、tag、上限超過） | `PAYLOAD_NOT_DETERMINISTIC` |

payloadのduplicate keyやindefinite lengthの分類は、ADR-0005 §15が明示していない部分です。「well-formedだがdeterministic encodingの外」として`PAYLOAD_NOT_DETERMINISTIC`にまとめています。分類を変える場合はADRも更新してください。

error text（library由来を含む）はcontractではありません。失敗時にpartially populatedな`Envelope`を返しません。

## EncodeSignature1

`["Signature1", protected, h'', payload]`をdeterministic CBORで組み立てます。external AADは常に空です。protected headerはenvelopeから得たexact bytesを渡してください。

## このadapterが行わないこと

- 署名の検証（`decisiontrust`のcryptographic primitiveの責務）
- P2 payloadの意味のdecode（`decisiondecoder`の責務）
- Trust Bundle、key lifecycle、Decision lifetimeの検証

このadapterが返す`Envelope`はAuthorityではありません。
