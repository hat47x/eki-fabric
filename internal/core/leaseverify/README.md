# Worker向けLease署名検証

`leaseverify`は、LSE-1が発行した`eki.lease/v1`の署名付きbytesを検証します。Workerの識別子と信頼ドメインはローカル設定で固定し、受信したLeaseから選びません。

検証は次の順序で進めます。

1. COSE envelopeと閉じたLease payloadを厳格にdecodeする。
2. Leaseの宛先がローカルWorkerと一致することを確認する。
3. 既存の`decisiontrust.VerifierBoundary`で鍵の役割・主体・信頼ドメイン、有効期間、Trust Bundleの鮮度、署名、署名検証後の鍵の状態を確認する。

成功時だけ、非公開フィールドを持つ`Verified`を返します。結果には署名されたLease、鍵ID、信頼ドメイン、Trust Bundleのrevision、検証時刻を保持します。失敗時はゼロ値と既存の`VerificationFailure`を返します。時計と検証済みTrust Bundleのメタデータは、信頼できる呼出し側が渡します。

この結果は、指定した時点で署名を検証できたことを表します。実行許可、EMITTEDの耐久commit、現在のpolicyの有効性、Task入力との一致、FenceEpochの耐久的な受理、Local Governorによる資源利用の許可は証明しません。検証後の失効を検出する仕組みやWorkerへの配送・実行経路も接続していません。policy currentnessの扱いは#78の未決事項として残します。

`conformance/statestoretest`では、memoryとSQLiteの両方で実Emitterが署名・保存したbytesを検証します。宛先や信頼ドメインの不一致、署名・payloadの改変、鍵の失効・侵害、有効期間外、古いTrust Bundle、時計・Bundleの欠落を負例に含めます。これらは端末上での実行やネットワーク配送の証拠ではありません。
