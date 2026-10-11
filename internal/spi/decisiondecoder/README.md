# Decision payloadのデコーダSPI

P2のdeterministic CBOR payloadを、Coreの型付きDecisionへ変換する境界です。

Decoderの成功は、payloadのsyntax / closed schema / Decision固有の意味を読み取れたことだけを示します。COSE signature、key lifecycle、current policy、Task / Attempt stateまで検証したことは意味しません。

Reference adapterは`internal/adapters/reference/decisiondecoder/cbor`に置きます。
