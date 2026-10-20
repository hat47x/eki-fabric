# Worker側のWorkOrder検証（WorkOrder verification）

Workerは、Control Planeから受け取ったWorkOrderのbytesを、そのまま実行指示として扱いません。まず、このpackageで、署名と自分宛であることを確かめます。

```text
WorkOrder bytes（hostile input）
  -> strict COSE_Sign1 decode
  -> strict work-order-v1 decode
  -> decision kind / authority role
  -> 自分（Worker Identity）宛か
  -> 発行時刻・期限・Trust Bundleの鮮度
  -> key / principal / Trust Domain / 署名
  -> Verified（一つの観測）
```

## 確かめること

- COSE envelopeがEKI strict profileに沿っていること。
- payloadが`eki.work-order/v1`で、Authority roleが`WOR-1`であること。
- `WorkerIdentity`が、このWorkerの設定と一致すること。
- 署名が、このTrust Domainの`WOR-1`の鍵で作られていること。
- 発行時刻、期限、Trust BundleのRevisionと検証時刻が、現在のpolicyの範囲にあること。

## 確かめないこと

`Verified`は、実行の許可ではありません。とくに、次は別の境界です。

- 参照先のPlacement Approval、Lease、Executor Approvalが、今もcurrentであること。
- `LeaseRef`のfencingの番号が古くないこと。
- WorkOrderのnonceが未使用であること。
- resource limitsとsandbox policyが、Local Governorの上限と矛盾しないこと。
- Artifact Grantが、実際に読めるArtifactと一致すること。

```text
Verified != 実行開始
Verified != 参照先Decisionのcurrentness
Verified != nonceの使い捨て
```

実行を始める側は、`Verified.Order()`を材料に、参照先Decisionの確認、nonceの記録、Local Governorの判断を順に行います。`Verified`から直接Executorを起動してはいけません。

## 失敗の扱い

失敗すると、zeroの`Verified`と、`decisiontrust.VerificationFailure`を返します。理由は安定した`VerificationReason`です。呼出し側は、理由を監査へ記録し、同じbytesを闇雲に再試行しません。鍵の失効やTrust Bundleの更新が原因の場合は、現在のTrust Bundleを受け取ってからやり直します。
