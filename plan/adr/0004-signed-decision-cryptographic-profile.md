# ADR-0004 署名付き認可判断の暗号プロファイル

- 状態: **承認済み（M0・企業向け基準）**
- 日付: 2026-09-22
- 対象範囲: P2のCOSE_Sign1による権限判断

## 1. 背景

P2では、作業指示書（WorkOrder）、リース（Lease）、配置（Placement） Approval、実行体（Executor） Approval、成果物（Artifact） Grantについて、署名対象となる正規のペイロードを決定的CBORとして固定しました。

次に固定するのは、次の境界です。署名アルゴリズム、鍵の識別、鍵の交換（rotation）と失効（revocation）、ワーカー（Worker）側での検証、KMSやHSMとの接続。

このとき重要なのは、暗号上の署名が正しいことと、EKI上で権限が認められることを同じものとみなさないことです。

~~~text
valid signature
  != trusted Authority role
  != valid Decision semantics
  != current execution authorization
~~~

## 2. アルゴリズムのプロファイル

EKI 基準（Baseline）では、次の2つのプロファイルを第一級に扱います。

### ソフトウェア参照プロファイル

- COSEアルゴリズム: **EdDSA (-8)**
- 曲線: **Ed25519**
- 鍵の種類: **OKP**
- COSEではPure EdDSAを使う。
- M0のソフトウェアによる参照用の署名器と検証器で、最初に実装する。

### 企業連携プロファイル

- COSEアルゴリズム: **ES256 (-7)**
- 曲線: **P-256**
- ハッシュ: **SHA-256**
- 鍵の種類: **EC2**
- KMS、HSM、企業のPKIと連携するときの第一候補とする。

どのプロファイルを使うかは、導入環境ごとの設定が明示的な許可リストで選びます。クライアント（Client）、ワーカー、タスク（Task）の入力でアルゴリズムを指定して、許可リストを広げることはできません。

## 3. アルゴリズムの切り替え

プロトコルそのものを、Ed25519専用やP-256専用にはしません。

ただし、「どのアルゴリズムでも受け入れる」という意味での柔軟性は設けません。

~~~text
protected alg
  ∩ key record allowed algorithm
  ∩ Authority role policy
  ∩ deployment allowlist
~~~

上の4つがすべて一致する場合にだけ検証します。未知のアルゴリズム、非推奨のアルゴリズム、無効にしたアルゴリズムへ代わりに切り替えることはしません。

## 4. 鍵の分離

少なくとも次の権限主体（Authority）ロールには、それぞれ別の署名鍵を使います。

- EXE-1 実行体 受理審査（Admission）
- PLC-1 配置 Authorization
- LSE-1 リース
- DAT-1 成果物 / Data Capability
- WOR-1 作業指示書

一つの秘密鍵をこれら複数のロールで共有する構成を、基準にはしません。

Small Managed Profileで複数の権限主体を同じプロセスに同居させる場合も、鍵の識別子とハンドルは分けます。

## 5. 標準との整合

COSEのアルゴリズム識別子と、鍵の種類や曲線との整合は、RFC 9053に従います。

- EdDSAは-8とする。
- ES256は-7とする。
- EdDSAでは、OKPと、対応するEdwards曲線を使う。
- ES256では、EC2、P-256、SHA-256を使う。

検証器は、アルゴリズムだけでなく、鍵の種類と曲線も確認します。

FIPS 186-5は、ECDSAとEdDSAをDigital Signature Standardとして扱っています。鍵レジストリ（Key Registry）の設計では、NIST SP 800-57 Part 1 Rev.5が示す鍵のライフサイクルと漏えい時の扱いを参考にします。

## 6. M0でEd25519を先に実装する理由

M0の参照実装では、Ed25519を先に実装します。理由は次のとおりです。

- Goの標準ライブラリで使える。
- 署名が決定的になる。
- 鍵と署名の長さが固定なので、実装が単純になる。
- ASN.1 DERへの変換が要らない。
- ソフトウェアだけで動く参照用の署名器を小さく作れる。

これは、企業環境にEd25519を強制する判断ではありません。

## 7. ES256を第一級にする理由

企業のKMS、HSM、PKIとの接続を考え、P-256とES256を第一級のプロファイルとして扱います。

COSEのES256の署名は、ASN.1 DERではなく、RFC 9053で定められた固定長の`R || S`というバイト列として扱います。

参照実装にES256を加えるときは、Goのcrypto APIとの間のエンコーディングの境界を、テストで固定します。

## 8. COSEの保護ヘッダのプロファイル

M0と基準では、COSE_Sign1の保護ヘッダに、少なくとも次の項目を含めます。

- `alg`
- `kid`
- 署名付き認可判断（Decision）であることを示す内容とプロファイルの識別子

保護ヘッダのマップそのものも、決定的CBORでエンコードします。

`alg`は、鍵レジストリと権限ポリシー（Authority Policy）の許可リストと照合します。

`kid`は中身に意味を持たない識別子で、手元の信頼ストア（Trust Store）や鍵レジストリの中から記録を選ぶための索引です。COSEメッセージの中に入っている未知の鍵を、信頼の起点（Trust Root）として自動的に採用することはしません。

内容とプロファイルは、P2のペイロードとしてデコードする前に確認します。非保護ヘッダだけを判定の根拠にはしません。

MVPでは、外部AAD（external AAD）を空のbstrに固定します。分離型ペイロード、副署名（countersignature）、複数署名（multisignature）は使いません。

## 9. 鍵レジストリ

検証器は、管理プレーン（Administrative Plane）で管理された鍵レジストリを参照します。

論理上の記録は次のとおりです。

~~~text
Key ID / kid
Authority role
Authority Principal
Trust Domain / namespace
Algorithm
Key type
Curve
Public key material or KMS key reference
Public key digest
State
Signing validity start
Signing validity end
Revocation / compromise metadata
Policy revision
Created / updated audit metadata
~~~

秘密鍵をワーカーへ配りません。ワーカーが持つのは、検証に必要な公開の信頼情報だけです。

## 10. `kid`の再利用

同じ信頼ドメイン（Trust Domain）の中では、同じ`kid`を別の公開鍵に使い回しません。

鍵を交換するときは新しい`kid`を発行します。こうして、古い認可判断の`kid`が、将来別の鍵として解釈されないようにします。

## 11. 鍵の状態

基準では、少なくとも次の状態を区別します。

### PENDING

登録は済んでいるが、署名にも検証にもまだ使わない状態です。

### ACTIVE

新しい認可判断の署名と検証に使える状態です。

### VERIFY_ONLY

新しい署名には使わず、有効期間中に発行された既存の認可判断の検証にだけ使う状態です。通常の鍵交換の後の、古い鍵に使います。

### REVOKED

現在の権限の検証には使わない状態です。定期的な引退以外の理由による失効に使います。

### COMPROMISED

秘密鍵が漏えいしたと想定する状態です。この鍵で署名され、まだ期限が切れていない認可判断を、現在の権限として信用しません。

過去のAudit 成果物として署名のバイトを保管しておくことと、それを現在の権限の判断に使えることとは、分けて扱います。

## 12. 鍵の交換

標準的な交換の流れは次のとおりです。

~~~text
K1 ACTIVE
  ↓
K2 PENDING
  ↓ administrative activation
K1 ACTIVE + K2 ACTIVE (short overlap if needed)
  ↓
K1 VERIFY_ONLY
K2 ACTIVE
  ↓ all live K1 decisions expire
K1 archival / removal from online verifier set
~~~

新旧の鍵が重なる期間は認めます。ただし、署名器がどちらの鍵を使うかを決めるのは権限ポリシーです。クライアントやワーカーは`kid`を選べません。

## 13. 失効の伝わり方

ワーカーは、手元の信頼ストアや鍵レジストリのスナップショットを使って検証できます。

ただし、古い信頼ストアのまま、新しい作業指示書を期限なく受け入れ続けることはしません。導入環境（Deployment Profile）で、信頼情報の束のリビジョン、検証した時刻、許容する古さの上限を設定します。

失効の更新を受け取ったら、それ以降の作業指示書の受け入れに反映します。許容する古さの具体的な長さは、MVPの実装の設定で決めます。

## 14. 鍵が漏えいしたときの被害の封じ込め

権限主体ロールごとに鍵を分けているので、たとえばWOR-1の鍵が漏えいしても、それだけでは実行体 受理審査、配置 Authorization、成果物 Grant、リースの有効な認可判断を偽造できません。この構造を保ちます。

ワーカーは、作業指示書だけでなく、その前提となる認可判断もそれぞれ独立に検証します。

## 15. 署名器の境界

署名器SPIを、`Sign(bytes) -> signature`のような汎用の形で、スケジューラなどへ広く渡しません。

論理上のリクエストには、少なくとも次の項目を含めます。権限主体ロール、認可判断の種類、正規のペイロード、期待する権限主体（Authority Principal）、ポリシーのリビジョンや署名の文脈。

署名器アダプタや権限主体サービスは、ロール、種類、ACTIVEな鍵、アルゴリズム、ペイロードのプロファイルを確認してから、COSE_Sign1を生成します。

スケジューラは署名器を直接持ちません。

## 16. 鍵の選び方

呼び出し側が任意の`kid`を渡して署名鍵を選ぶ構造は、基準にしません。

ACTIVEな鍵は、権限主体や署名器側が、ロール、信頼ドメイン、アルゴリズムのプロファイル、鍵の状態、有効化のポリシーにもとづいて選びます。

テストやマイグレーションのために鍵を明示的に選ぶ必要がある場合も、その操作はAdministrativeな権限として分けます。

## 17. 検証器の確認順序

ワーカー側の検証器は、概念上、次の順序で確認します。

~~~text
1. total COSE object size / nesting limit
2. COSE_Sign1 structure
3. protected header is deterministic CBOR
4. required protected fields exist
5. content/profile matches EKI signed-decision profile
6. kid resolves in configured Trust Store
7. key state permits verification
8. protected alg matches Key Registry algorithm
9. key type / curve matches algorithm
10. cryptographic signature
11. P2 payload deterministic CBOR
12. CDDL schema / version
13. Decision kind
14. Authority role and principal match Key Registry / policy
15. issued / expires / bounded clock skew
16. Task / Attempt / Worker / TaskIR binding
17. policy / revocation / current state
18. Decision-specific semantic checks
~~~

署名が正しかったというだけで、手順14以降を省きません。

## 18. 時刻の検証

検証器は、管理された時計を基準に次のことを確認します。発行時刻が不自然に未来でないこと。期限が発行時刻より後であること。現在時刻が有効期間の中にあること。時計のずれは上限の範囲内だけ許すこと。

ワーカー自身の任意の時計を、権限主体の根拠にしません。許容するずれの具体的な秒数は導入環境の設定で固定し、クライアントがタスクごとに変えることはできません。

## 19. 認可判断の有効期間

認可判断の有効期間（TTL）には、種類ごとにポリシー（Policy）で上限を設けます。

- リースの有効期間は短くする。
- 作業指示書の有効期間は、前提となる認可判断の期限を越えない。
- 成果物 Grantの有効範囲は、タスクやリースの用途に必要な、短い範囲に限る。
- 実行体 Approvalは比較的長くできるが、失効させられるようにする。

クライアントがTTLを指定して延ばすことはできません。

## 20. 秘密鍵の保護

### 開発環境

使い捨ての鍵や、開発用のローカルな鍵を認めます。ただし、本番用のプロファイルとは区別します。

### DP-1 Small Managed

少なくとも次のことを求めます。専用の鍵ファイルまたはOSの鍵ストアを使う。ファイルシステムのACLを厳しくする。ロールごとに別の鍵を使う。秘密情報をログや設定のダンプに出さない。

### DP-2 Enterprise

KMSやHSMのように、秘密鍵を制御プレーン（Control Plane）のアプリケーションのメモリへ取り出さないアダプタを、第一候補とします。

署名器SPIは、ローカルのソフトウェア鍵とKMSやHSMの両方を、同じ権限主体の意味で扱います。

## 21. 公開鍵の配布

ワーカーへ渡す公開の信頼情報は、管理者が管理する信頼情報の束です。

ワーカーは、受け取ったCOSEオブジェクトに含まれる公開鍵、任意の証明書、URL、JWKSのエンドポイントを、自動的に信頼の起点へ加えません。

将来x5chainなどを使う場合も、設定された信頼の起点とポリシーに照らして証明書のチェーンを検証する、別のプロファイルとします。

## 22. 連合

他の信頼ドメインの権限主体鍵を手元の信頼ストアに加えただけで、それを手元の作業指示書の権限主体として扱うことはしません。

標準的な連合（Federation）では、プロバイダ（Provider）側のドメインが、外部から来た依頼を自分の受理審査に通します。そのうえで、プロバイダ側の権限主体が、自分のワーカー向けの認可判断を発行します。

## 23. エラーの扱い

検証器は、機械が読める理由の分類を持ちます。

- MALFORMED_COSE
- PROTECTED_HEADER_INVALID
- UNSUPPORTED_PROFILE
- UNKNOWN_KEY
- KEY_NOT_ACTIVE_FOR_VERIFY
- ALGORITHM_NOT_ALLOWED
- KEY_ALGORITHM_MISMATCH
- SIGNATURE_INVALID
- PAYLOAD_NOT_DETERMINISTIC
- SCHEMA_UNSUPPORTED
- AUTHORITY_ROLE_MISMATCH
- EXPIRED
- NOT_YET_VALID
- SUBJECT_MISMATCH
- STALE_DECISION
- POLICY_REVOKED
- TRUST_BUNDLE_INVALID
- TRUST_BUNDLE_STALE
- TIME_WINDOW_INVALID

ワーカーからクライアントへ、内部の詳細をそのまま公開することは標準にしません。

## 24. サイズの上限

M0の参照用検証器は、署名付き認可判断を小さな制御用のメタデータとして扱います。

- COSE_Sign1の全体は256 KiB以下とする。
- エンコードした保護ヘッダは4 KiB以下とする。
- `kid`は128バイト以下とする。

導入環境ごとに、より厳しい上限を設定できます。大きなデータや証拠の成果物を、署名付き認可判断の中に直接入れません。

## 25. 監査

署名と検証については、次の項目を、秘密情報を含めずに監査できるようにします。権限主体ロール、認可判断の種類とID、kid、アルゴリズム、ペイロードのダイジェスト、ポリシーのリビジョン、結果、失敗の分類、タイムスタンプ。

秘密鍵、生の秘密情報、不要な成果物の中身はログに出しません。

## 26. 適合性テスト

### アルゴリズムの取り違え

- Ed25519の鍵とES256のalgの組み合わせを拒否する。
- P-256の鍵とEdDSAのalgの組み合わせを拒否する。
- 未知のalgや無効にしたalgを拒否する。

### 鍵の識別とライフサイクル

- 未知のkidを拒否する。
- kidの再利用や、鍵の素材の変更を、レジストリの読み込み時に拒否する。
- 誤った権限主体ロールや主体（Principal）を拒否する。
- PENDINGやVERIFY_ONLYの鍵での署名を拒否する。
- REVOKEDやCOMPROMISEDの鍵を、現在の権限の根拠としては拒否する。

### COSE

- algが非保護ヘッダにしかない場合は拒否する。
- kidが非保護ヘッダにしかない場合は拒否する。
- 誤ったプロファイルを拒否する。
- 分離型ペイロードを拒否する。
- 形式が不正なCOSEや、大きすぎるCOSEを拒否する。

### ペイロード

- 決定的でないペイロードを拒否する。
- 誤った認可判断の種類を拒否する。
- 期限切れを拒否する。
- タスク、試行（Attempt）、ワーカーの不一致を拒否する。
- 古いリースやポリシーを拒否する。

## 27. 対象外

このプロファイルでは、次のものを定義しません。TLSの証明書のプロファイル、OAuthのトークンの署名、成果物の暗号化、秘密情報の暗号化、公開WebPKI、複数者による承認。

用途の違う鍵を、なんでもできる一つのEKI鍵へまとめることはしません。

## 28. 出典

- RFC 9052: https://www.rfc-editor.org/rfc/rfc9052.html
- RFC 9053: https://www.rfc-editor.org/rfc/rfc9053.html
- RFC 8032: https://www.rfc-editor.org/rfc/rfc8032.html
- FIPS 186-5: https://csrc.nist.gov/pubs/fips/186-5/final
- NIST SP 800-57 Part 1 Rev.5: https://csrc.nist.gov/pubs/sp/800/57/pt1/r5/final

## 29. 実装の状況と今後の作業

M0の署名器と検証器のドメインの境界、メモリ上のTrust Registry、暗号処理の前に行うポリシーの検査、標準ライブラリの暗号機能との境界までを実装しました。

参照用のソフトウェア署名器は、EdDSA/Ed25519とES256/P-256を扱います。ES256の署名には、COSEの固定長64バイトの`R || S`形式を使います。これはM0、開発、DP-1向けの参照実装です。DP-2 Enterpriseでは、引き続き、秘密鍵をアプリケーションのメモリへ取り出さないKMSやHSMのアダプタを第一候補とします。

署名器は、型付きの権限主体のリクエストと、Registryが選んだ主鍵を要求します。汎用の`Sign(bytes)`という権限として、スケジューラなどへ公開することはしません。署名の検証の前後でも、暗号処理の前に行うポリシーの検査をもう一度行います。ただし、最終的な権限判断の原子性は、暗号の層だけでは保証しません。実行の直前に、現在のリース、ポリシー、認可判断を別に検証することを求めます。

次の実装作業は、正規の認可判断ペイロードのデコーダと、COSEの保護ヘッダのパーサです。COSEやCBORのライブラリへの依存は、SQLiteと同じく、Goのツールで取得、ロック、テストができる環境で導入します。検証していない依存関係のロックを、推測でコミットしません。
