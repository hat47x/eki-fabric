# P2署名付き判断を厳格に読むCBORデコーダ

Reference Go実装でP2 Placement Approval payloadを読むadapterです。

`fxamacker/cbor/v2`を使い、次をfail closedにします。

- duplicate map key
- indefinite-length item
- CBOR tag
- unknown struct field
- excessive nesting / collection size
- RFC 8949 Core Deterministic Encodingと一致しないbytes
- P2 v1以外のschema / Decision kind / Authority role
- invalid Task / Attempt ID
- SHA-256以外または32 byteでないDigest
- invalid issued / expiry window
- Evidence上限超過

最後のdeterministic checkは、一度strict decodeした値をCore Deterministic Encodingで再encodeし、元payloadとbyte単位で比較します。

ここを通った`decisionpayload.PlacementApproval`もAuthority tokenではありません。後段でCOSE signature / `kid` / PLC-1 principal / current policy / subject stateを検証する必要があります。
