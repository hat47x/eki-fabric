# COSEデコーダの適合性試験（Decoder Conformance Harness）

このpackageは、EKI Fabricのstrict signed-decision COSE profileを実装するConcrete Adapterへ、repository共通fixtureを適用するためのtest harnessです。

対象interfaceは `internal/spi/cosedecoder` です。

## 何を検証するか

`RunCodecContract`は `conformance/vectors/signed-decision-cose-v1.json` を読み、各入力について次を確認します。

- envelope / protected / payload段階で拒否すべき入力は、その段階のstable `VerificationReason`でfail-closedになる
- errorと同時にpartially populated `Envelope`を返さない
- crypto段階まで進むvectorはdecoder段階では受理される
- 正常系はEdDSA / `kid`を正しく抽出する
- `Signature1` Sig_structure encoderが正常系signatureを検証可能なbytesを構築する
- tampered signatureは同じdecoderを通過してもcrypto検証では失敗する

この分離により、parserがsignature validityまで先読みしたり、逆にmalformed envelopeをcrypto層へ流したりする実装を防ぎます。

## Adapter側の最小test

Concrete Adapterのpackageでは概ね次の形で呼び出します。

~~~go
func TestCodecConformance(t *testing.T) {
    cosedecodertest.RunCodecContract(t, func(t testing.TB) cosedecoder.Codec {
        return newReferenceCodec(t)
    })
}
~~~

fixtureの期待reasonをlibrary固有errorへ合わせて変更しません。Adapter側でlibrary errorをEKIの `VerificationFailure` へ正規化します。

## Scope

このharnessはCOSE/CBOR入口からSig_structure構築までを対象とします。

Trust Bundle freshness、key lifecycle、Decision lifetime、cryptographic primitive、Decision-specific Authorizationは既存 `decisiontrust` boundaryの責務です。
