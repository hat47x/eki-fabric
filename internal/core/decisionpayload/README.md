# P2判断ペイロードの意味（Decision payload semantics）

このpackageは、署名対象となるP2 Decision payloadをGoの型として表します。

最初のReferenceはPlacement Approvalです。二つ目はLSE-1が署名する`eki.lease/v1`の`Lease`です。三つ目はWOR-1が署名する`eki.work-order/v1`の`WorkOrder`です。`Lease`と`WorkOrder`はwireのミリ秒単位の時刻に揃えた値だけを受け付け、encodeとdecodeで値が変わらないようにします。ここで保持するのは、CDDLから読み取った意味だけであり、署名検証済みAuthorityではありません。

`WorkOrder`は、実行してよい仕事の内容を、Task、Attempt、Worker、Placement Approval、Lease、Executor Approval、Artifact Grant、resource limits、sandbox policyへbindingします。参照先のDecisionを検証したことにはなりません。Workerは、参照先が今もcurrentであることを別に確かめます。

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
