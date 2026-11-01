# ADR-0027 Workerは、管理されたenrollment資格で登録し、Workerごとの短命なクライアント証明書（mTLS）で認証する

- 状態: **採用（仮承認）**
- 日付: 2026-10-26
- 関連: Issue [#222](https://github.com/hat47x/eki-fabric/issues/222)、[#198](https://github.com/hat47x/eki-fabric/issues/198)、[#173](https://github.com/hat47x/eki-fabric/issues/173)、[#57](https://github.com/hat47x/eki-fabric/issues/57)、[ADR-0006](0006-m0-client-api-authentication-and-admission.md)、[ADR-0016](0016-delegation-class-as-registered-profile.md)、[ADR-0025](0025-lease-policy-currentness-and-key-rotation.md)

## 1. 背景

Workerが何を根拠にControl Planeへ自分の主体を示し、どのように登録されるかを、まだ決めていませんでした。存在するのは、#220の試験用profileだけです。Bearerトークンの SHA-256 を `{worker_identity, trust_domain}` の表と突き合わせる仕組みで、コードのコメントも「#198の決定ではない」と書いています。ところが現在の設定検証は、非loopbackでもTLSがあれば、この表を受け付けます。決定がないまま、静的なトークンが本番に近い形で使えてしまう状態でした。

CONTRACT_BOUNDARIESには「OAuthやmTLSの方式は、後続で比べます」という記述が残っていました。比較の全文と攻撃ごとの評価は、Issue #222に置いています。このADRは、その評価の結論を、仮承認で記録します。

要求の側で守りたいことは次のとおりです。

- 認証されたWorkerも悪意を持ち得る。認証は、申告が誰のものかを確かめる材料であり、申告が正しいことの根拠ではない（RQ-019、RQ-020、SEC-003）。
- 認証に成功しても、包括的なauthorityは得られない。Worker資格からClient・Adminの権限は得られず、その逆もない（SEC-001、SEC-010）。
- 失効した資格を、古いmessageの再送で復活させられない。権限はすべて有限である（SEC-015、RQ-060）。
- Workerへのinboundを求めない（DEPLOYMENT_PROFILES §5.1、RQ-033）。一台や小さな組織でも使え、大きな組織は既存の基盤へ接続できる（RQ-063、RQ-004、DEPLOYMENT_PROFILES §7.7）。EKI独自の大規模なPKIは必須にしない。

## 2. 決定

### 2.1 既定の方式

1. Workerの認証の既定は、**Workerごとのクライアント証明書によるmTLS**とする。証明書は、Control Planeが運用する小さな認証局（CA）が発行する。このCAはWorkerの証明書だけを発行する。汎用のPKIではない。
2. Workerは、**管理者が発行したenrollment資格**を使って、一度だけ登録する。登録の手順は2.2に定める。
3. 認証の結果は、既存の`Principal{WorkerIdentity, TrustDomain}`へ正規化する。認証の方式を差し替えられるように、検証の境界はIDV-1（Identity Verification）のAdapterとして置く。企業のPKI、OIDC、SPIFFEなどの既存の基盤は、同じPrincipalへ正規化できる限り、既定の代わりに使える。これらの具体的な接続は、このADRでは決めない。

### 2.2 登録（enrollment）

4. **enrollment資格は、管理者が発行する。** 管理用の操作であり、Client APIやWorker APIからは発行できない。発行できるのは、管理者のRoleだけである。
5. 資格は、次の性質を持つ。
   - 一回限り。使うと失効する。消費は、Workerの登録と証明書の発行を記録する書き込みと同じトランザクションで行い、同じ資格の同時使用が二つの登録を作らないようにする。
   - 短命。既定は発行から24時間、上限は7日とする。使われないまま期限が過ぎた資格は、使えない。
   - 登録先の信頼領域（Trust Domain）に束縛する。
   - 256bit以上の乱数とし、Control Planeには、そのSHA-256だけを保存する（ADR-0006の2.1と同じ考え方）。識別しやすい接頭辞を付け、秘密を探す走査に引っかかるようにする。
   - 発行の記録は、誰が、いつ、どの信頼領域向けに発行したかを残す。
6. 管理者は、資格と一緒に、Control Planeの接続先と**CAの証明書の指紋**を渡す。Workerは、初回の接続でも、公開CAだけに信頼を頼らず、この指紋でControl Planeを確かめる。偽のendpointに資格を送らされることを防ぐためである。
7. Workerは、**秘密鍵を自分の端末の中で作る**。秘密鍵は端末の外へ出さない。Workerは、証明書署名要求（CSR）とenrollment資格を、TLSで保護した登録のendpointへ送る。資格は`Authorization`ヘッダだけで送り、URLやクエリ文字列には置かない。
8. Control Planeは、資格を検証して消費し、Workerの登録簿に新しい項目を作り、証明書を発行して返す。**CSRの中の主体の名前は無視する。** `WorkerIdentity`は、登録簿が発行する識別子であり、Workerが自称する名前ではない。失効した識別子は、再利用しない。
9. 登録は、**主体の同一性だけを作る**。登録されたWorkerに、Delegation Classは与えない。管理者が等級（ADR-0016）へ登録するまで、委任はできない（fail closed）。認証と登録から、Placementの権限やData Grantは生じない（SEC-001）。

### 2.3 証明書と主体の対応

10. 証明書の主体の別名（SAN）に、信頼領域とWorkerの識別子をURIとして入れる。形は`eki://<trust-domain>/worker/<worker-identity>`とする。拡張鍵用途はクライアント認証だけとする。この名前空間は、Worker専用であり、Client・Adminのものと混ぜない。
11. Control Planeは、検証した証明書の鎖から`Principal`を作る。`WorkerIdentity`はSANから、`TrustDomain`はSANと、証明書を検証したCAの名前の制約（Name Constraint）から導く。ResultClaim、ResourceAdvertisementなどのbodyに主体の項目があっても、Principalを上書きしない。worker.protoの「認証は通信とsessionの文脈であり、Worker identityはbodyの項目から与えない」と一致する。
12. `claimplane.Service.Submit`の所有検査（Claimが名指すWorkerと認証されたWorkerの一致）は変えない。入力が、試験用の表から証明書に替わるだけである。
13. Worker Identityとsessionは分ける。再接続や証明書の更新のたびに新しいsessionを作り、古いsessionのLease、nonce、heartbeatを持ち越さない（TASK_STATE_MODEL §72）。sessionの識別子は、接続した証明書の識別子に束縛する。

### 2.4 有効期間、更新、失効

14. 証明書の有効期間は短くする。既定は72時間とし、残りが3分の1になったら更新する。数値は仮の既定であり、運用の実績で見直す（未決）。
15. 更新は、**現在有効な証明書でmTLS接続し、新しいCSRを送る**。更新のたびに新しい鍵を作る。有効期間を過ぎた証明書では更新できず、期限切れの証明書を、後のmessageのreplayで復活させることはできない（SEC-015）。有効期間を超えて眠った端末は、新しいenrollment資格で登録し直す。同じ`WorkerIdentity`を引き継ぐかは未決とする。Control Planeは、要求ごとに、登録簿に記録した**現在の証明書の通し番号**と提示された証明書を照合し、更新前の証明書を、有効期間内でも受け付けない。これがなければ、更新で鍵を新しくする意味がなく、盗まれた古い鍵が、Workerの更新後も要求と更新を続けられる（#284）。
16. **失効の根拠は、Workerの登録簿の状態とする。** 状態は、登録済み、無効化、失効のいずれかである。Control Planeは、接続の確立時だけでなく、**要求ごとに**登録簿の状態を引く。失効したWorkerの要求は、既存の接続の上でも、次の要求から拒否し、その接続とsessionを閉じる。
17. 失効時の動作は、TASK_STATE_MODEL §73に従う。新しいPlacementの禁止、Leaseのrenewの停止、有効なLeaseのrevoke、可能な範囲でのData Grantのrevoke、有効なTaskのrecoveryである。Workerが切断されていても、先にControl Planeの状態へ反映する。
18. 短い有効期間は、登録簿を引けない構成（失効の情報を遅れて受け取るGatewayなど）の歯止めであり、失効の主な手段ではない。登録簿を引けない構成で、失効が遅れる上限は、**証明書の有効期間**となる。Control Planeは、起動時にWorker CA証明書の現在の有効性を確かめ、期限切れまたは未開始なら起動しない。期限までの残りが30日以内なら、起動時と稼働中（6時間ごと）に警告する。CAの期限で発行する証明書の有効期間が短縮された場合は、その事実を監査ログに残す（#304）。
19. Worker自身は、**自分の識別子だけを失効できる**。持ち主の参加の停止（RQ-006、RQ-042）を、権限を縮める方向の操作として扱うためである。他のWorkerの識別子は失効できない。

### 2.5 試験用profileの扱い

20. #220の静的Bearerの表は、**試験用に限る**。本番向けの方式として扱わない。
21. 実装では、次のとおりにする。Worker listenerが静的な表を使えるのは、listenが loopback のとき、または明示した試験用の設定のときだけにする。それ以外の構成では、起動を拒否する。表を使って起動したときは、起動時に警告を出す。現在の設定検証は、非loopbackでもTLSがあれば表を受け付けるので、この点は後続の実装で直す。実装は、このADRとは別のIssueで行う。
22. Bearerのヘッダは、ログ、エラー応答、クラッシュダンプに出さない。enrollment資格も同じ扱いにする。

### 2.6 Worker・Client・Adminの分離

23. Worker listenerは、Client APIや管理APIとは別のlistenerと別のauthenticatorにする（CONTRACT_BOUNDARIES §32）。Worker証明書では、Client APIも管理APIも呼べない。Client・Adminの資格でも、Worker listenerには入れない。
24. enrollment資格は管理者のRoleでだけ発行でき、Workerとして登録された証明書は、他の資格を発行できない。単一の資格の侵害が、別のRoleへ自動で波及しない（SEC-010）。

### 2.7 CAの鍵

25. WorkerのCAの署名鍵は、Control Planeの高価値のauthorityである。これを使えば、任意のWorker Identityを作れてしまう。したがって、**LSE-1・WOR-1の署名鍵（ADR-0025）とは別の鍵**にする。信頼領域ごとに中間CAを置き、Name Constraintで領域をまたぐ発行を防ぐ。鍵の保管、更新、企業のPKIとの接続は未決とする。

## 3. 理由

攻撃ごとの比較はIssue #222にある。要点だけを書く。

- 静的なBearerは、窃取、replay、失効、ログへの混入のどれも、境界で止まらない。そのため、試験用にとどめる。
- mTLSでは、盗んだ証明書だけでは使えず、秘密鍵が要る。handshakeは接続ごとに新しく、観測した通信の再送では認証できない。通常の通信に秘密を載せないので、ログに残る秘密はenrollment資格だけであり、それは一回限りで短命である。識別子は登録簿が発行するので、Workerが他のWorkerを名乗れない。
- 外部の認可サーバや基盤を必須にしないで、これらを実現できるのは、候補のうちenrollmentとmTLSの組み合わせである。一台や小さな組織でも使える（RQ-063）。
- 大きな組織の既存のPKI、OIDC、SPIFFEは、IDV-1のAdapterとして、同じPrincipalへ正規化して受け入れる。EKI独自の大規模なPKIを必須にしないという方針（DEPLOYMENT_PROFILES §7.7）は、CAをWorker証明書だけの小さなものに限り、差し替えられるようにすることで守る。
- 失効の主な手段を、証明書の失効リストの配布ではなく、Control Planeの登録簿にする。Worker証明書を検証するのがControl Planeだけであれば、要求ごとに登録簿を引けるためである。この形なら、失効が遅れる上限を、証明書の有効期間ではなく、ほぼ0にできる。

## 4. 影響

### 保証すること（成立条件つき）

- 盗んだ証明書、観測した通信、期限切れの証明書だけでは、Workerとして認証できない。ただし、秘密鍵が盗まれていないことが条件である。
- 失効したWorkerの要求は、Control Planeが登録簿を引ける限り、次の要求から拒否する。
- enrollment資格は、一回限りで短命であり、盗まれても、使われる前に管理者が失効でき、使われた後は再利用できない。
- Worker資格とClient・Admin資格は、互いに流用できない。

### 保証しないこと

- 登録済みのWorkerが侵害されたとき、そのWorkerが自己申告を偽ること。この決定は、申告が誰のものかを確かめるだけであり、内容の正しさは検証の側で確かめる（RQ-019、RQ-020、SEC-003）。
- 管理者が悪意のある端末へ、正規にenrollment資格を渡した場合を防ぐこと。登録に管理者の操作が要るので、多数のIdentityを作る費用は上がるが、独立性の保証にはならない（RQ-022、SEC-012）。
- 端末の侵害で秘密鍵が盗まれたとき、有効期間内、またはrevokeまでの間の使用を防ぐこと。非エクスポートの鍵の保管は、保証しない。
- revokeが、すでに配送したLeaseを即座に止めること、すでに開示したDataの機密性を戻すこと（ADR-0025、SEC-017）。
- CAの鍵が侵害されたとき、任意のWorker Identityの発行を防ぐこと。blast radiusは、信頼領域ごとの中間CAで区切る。

### 制約

- Worker証明書の検証はControl Planeが行う。TLSを終端するプロキシや、企業のTLS検査では、mTLSが壊れる。mTLSを素通しにするか、信頼するGatewayが終端して、認証済みの内部の経路で主体を渡す構成が要る。Clientから受けたヘッダを主体として信用する構成は、取らない。
- ブラウザのWorkerは、この既定では扱えない。DP-3の仮名や匿名の参加の認証は、このADRの対象外とする。
- 有効期間を超えて眠った端末は、再登録が要る。

## 5. 未決事項

- CAの運用: 鍵の保管（KMS、HSM、ファイル）、中間CAの切り方、鍵の更新、バックアップ、企業のPKIとの接続。
- 更新の通信手順の詳細: 時計のずれへの対処、有効期間を超えて眠った端末の再登録、同じ`WorkerIdentity`の引き継ぎ。
- 失効の伝播の遅れの上限と、その測定。登録簿を引けない構成での、Gatewayの情報の古さの上限。QA-01の測定候補（`executor_revocation_propagation_delay`と同様のもの）に加えるかを含む。
- 端末のattestationを、登録に結び付けるか（RQ-025）。秘密鍵を非エクスポートにする要件を、登録の条件にするか。
- ブラウザ・仮名のWorker（DP-3）の認証。OAuth＋DPoPや、登録した公開鍵によるアプリケーション層の署名が候補である。
- TLSを終端するプロキシの構成での、代わりの方式。
- enrollment資格の発行を、委任状の操作の区分（ADR-0022）のどれに置くか。まとめて発行するときに、人間の承認を求めるか。
- 有効期間、enrollment資格の期限などの数値の既定。
- 通信契約（#198、worker.proto）への写像。`OpenSession`をmTLSの上でどう束縛するか。
- 信頼領域をまたぐ（Federation）Workerの認証（THREAT_MODEL A8）。
- 試験用profileの制限（2.5の21）の実装。
- 仮承認の見直し: 利用者が本承認するまで、状態は「採用（仮承認）」とする。見直しで方針が変わった場合は、このADRを置き換えるADRを書く。
