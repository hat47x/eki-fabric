# Placement Approval verification pipeline

このpackageは、署名付きPlacement Approvalを検証する各段階を決まった順序でつなぎます。各段階の実装は別packageにあり、ここでは順序を固定し、段階を飛ばせないようにするだけです。

~~~text
raw COSE_Sign1
  1. strict COSE envelope decode          internal/spi/cosedecoder
  2. strict P2 payload decode             internal/spi/decisiondecoder
  3. key / role / principal / algorithm   internal/core/decisiontrust
     / window / signature verification
  -> Verified
  4. current Task / Attempt / Worker /    Verified.Revalidate
     policy revalidation
  -> LSE-1が自分で判断する
~~~

## Verifyが保証すること

`Verified`が得られたとき、次が成り立っています。

- envelopeとpayloadが厳格なprofileに合っている
- 署名鍵が、payloadの主張するAuthority role・principalと、**このverifierの信頼ドメイン**に属している
- 鍵がその時点で検証に使える状態で、署名が`Sig_structure`に対して正しい
- 決定の有効期間がpolicyの範囲内で、現在時刻が窓の中にある
- 署名検証の間に鍵が失効・侵害されていない（検証後にもう一度確認する）

信頼ドメインは設定から来ます。署名された内容から取りません。決定が、自分を保証してよい鍵の持ち主を選べないようにするためです。

`Verified`は、このpackageの`Verify`だけが作ります。`verified=true`のようなcallerが立てられるフラグはありません。

## Verifyが保証しないこと

`Verified`はLeaseではありません。署名が正しいことは、そのTaskがまだ存在すること、まだ割り当てられること、同じpolicyの下にあることを意味しません。

## Revalidate

LSE-1がLeaseを出す直前の現在状態に対して、承認がまだ同じ対象を指しているかを確認します。次のすべてが成り立つときだけ`nil`を返します。

- 承認の有効期間がまだ開いている
- Task ID、Task IRのdigest、Attempt ID、Worker identityが現在の状態と一致する
- policy revisionが現在のものと一致する
- Taskが`ready`または`active`で、取り消し要求がない

`nil`は、Leaseを出すための必要条件であって十分条件ではありません。LSE-1は、Lease固有の前提（Attemptの状態、Workerの在籍など）を自分で確認します。

## 失敗

すべての失敗は`*decisiontrust.VerificationFailure`で、安定した`VerificationReason`を持ちます。adapter固有のerrorは契約にしません。

## 時刻と信頼バンドル

`Verify`は時計を読みません。現在時刻と信頼バンドルのメタデータは呼び出し側が渡します。これにより、検証が決定的になり、時刻の扱いをテストで固定できます。
