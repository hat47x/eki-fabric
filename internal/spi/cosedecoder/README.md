# 署名付き判断のCOSE Decoder SPI

このpackageは、raw `COSE_Sign1` bytesをEKI Fabricのstrict signed-decision profileへ変換するConcrete codecと、Core側の間に置く**小さな内部Adapter interface**です。

public Go plugin APIではありません。

## なぜSPIを分けるか

`internal/core/decisiontrust`は、Authority role / Decision kind、key lifecycle、Trust Bundle、verification policy、cryptographic primitive等のsecurity semanticsを保持します。

一方、CBOR libraryの選択やparser optionはConcrete implementationの詳細です。Coreが`fxamacker/cbor`等を直接importすると、codec差し替えとsecurity boundaryが混ざります。

そのため依存方向を次へ固定します。

~~~text
decisiontrust Core
      ↑
cosedecoder SPI
      ↑
Concrete CBOR / COSE Adapter
~~~

Concrete AdapterからCore型を利用することはできますが、CoreからConcrete Adapterをimportしません。

## `Decoder`

`DecodeSign1(raw)`はhostile inputを受けます。

Concrete implementationは少なくともADR-0005の順序で次を検証します。

- total bytes / parser resource limit
- CBOR tag 18
- 4-element COSE_Sign1
- empty unprotected map
- attached payload
- 64-byte signature shape
- protected headerのstrict field set
- `alg` / `crit` / `kid` / EKI profile
- protected headerのCore Deterministic Encoding
- signed Decision payloadのCore Deterministic Encoding

hostile inputによるrejectはlibrary固有error textではなく、`decisiontrust.VerificationFailure`へ正規化します。

errorを返す場合、partially populated `Envelope`を返しません。

## `Envelope`

`Envelope`はdecoderが上記入口検査を通した結果を保持します。

保持するのは次だけです。

- exact protected-header bytes
- attached payload bytes
- signature bytes
- resolved EKI algorithm
- Key Registry用`kid`

byte sliceはconstructorとaccessorの双方でdefensive copyします。

attached empty bstrとdetached payloadは同じ意味ではありません。Goでは空sliceとnil sliceが混同されやすいため、`NewEnvelope`はnil payloadを拒否し、空の非nil payloadは保持します。empty payload自体がP2 Decisionとして妥当かは後段payload decoderの責務です。

`Envelope`は「署名済み」「Authorization済み」を意味しません。

## `Signature1Encoder`

`EncodeSignature1(protectedHeader, payload)`は、empty external AADを使うCOSE `Signature1` Sig_structureをdeterministic CBORで生成します。

generic CBOR encoderとして公開するinterfaceではありません。

この出力を既存`decisiontrust` cryptographic verifierへ渡します。

## Conformance

Concrete Adapterは`conformance/cosedecodertest.RunCodecContract`を実行します。

Repository共通fixtureに対し、first-failure phaseとstable `VerificationReason`、正常系Sig_structure、tampered signatureの層分離まで同一contractとして検証します。

## Reference implementation

第一候補は`github.com/fxamacker/cbor/v2`です。

ただしdependency取得・lock・testできる環境でのみConcrete Adapterを追加します。dependency取得不能を理由に独自CBOR parserをCoreへ実装しません。
