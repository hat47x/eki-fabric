# P2判断ペイロードの意味（Decision payload semantics）

このpackageは、署名対象となるP2 Decision payloadをGoの型として表します。

最初のReferenceはPlacement Approvalです。二つ目はLSE-1が署名する`eki.lease/v1`の`Lease`です。`Lease`はwireのミリ秒単位の時刻に揃えた値だけを受け付け、encodeとdecodeで値が変わらないようにします。ここで保持するのは、CDDLから読み取った意味とpayload digestであり、署名検証済みAuthorityではありません。

```text
strict CBOR decode
  -> decisionpayload.PlacementApproval
       (closed schema / subject binding / payload digest)
  -> COSE signature + key / role verification
  -> current policy / Task / Attempt revalidation
  -> relying Authority decision
```

`PlacementApproval.Valid()`や、このpackageのconstructorを通ったことだけでLeaseを発行してはいけません。P2 payloadは署名検証とcurrent-state検査の入力です。

この分離により、次を避けます。

- Scheduler ProposalをPlacement Approvalとして扱う
- caller supplied `verified=true`をsignature verificationの代用にする
- CBOR parserの都合をLSE-1のdomain modelへ持ち込む
- valid signatureだけでcurrent Lease authorityを決める
