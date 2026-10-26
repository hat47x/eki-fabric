# Worker専用のCA（参照実装）

[ADR-0027](../../../../plan/adr/0027-worker-authentication-and-enrollment.md)の2.1・2.3・2.7に従い、Workerのクライアント証明書だけを発行する小さな認証局です。汎用のPKIではありません。署名鍵はファイルから読みます（KMSやHSMは未対応で、ADRの未決です）。

## 発行する証明書

- 主体の別名（SAN）は、登録簿が発行した識別子から作る`eki://<trust-domain>/worker/<identity>`の一つだけです。CSRの主体、名前、拡張は**すべて無視**し、使うのはCSRの公開鍵だけです（自己署名の検証で、秘密鍵の保持は確かめます）。たとえば、別のWorkerの識別子を名乗るCSRを送っても、登録簿の識別子の証明書しか得られません。
- 拡張鍵用途はクライアント認証だけ、CAではありません。鍵はECDSA P-256だけを受け付けます（RSAやP-384は拒否）。
- 通し番号は127bitの乱数で、発行のたびに`RecordWorkerCertificate`で登録簿へ記録します（記録に失敗した証明書は返しません）。
- 有効期間は既定72時間で、CA自身の期限を超えません。

## CA証明書の制約

`GenerateCA`が作るCA証明書は、パス長0で、許可するURIの領域を1つの信頼領域に限る、重要（critical）なName Constraintを持ちます。`New`は、この制約がないCA、別の信頼領域のCA、鍵が一致しないCA、P-256以外の鍵、DNSやIPも許可するCAを、起動時に拒否します。盗まれたCA鍵で別の信頼領域の証明書を作っても、このCAを信頼する検証側は受け入れません。

GoのURI名制約は、領域のサブドメインも許可します。たとえば`example.org`の制約は`sub.example.org`も通します。そのため、認証側（`internal/control/workerplane`）が信頼領域を完全一致で比べ、サブドメインを拒否します。

## 鍵の扱い

CAの鍵は、LeaseとWorkOrderの署名鍵（ADR-0025）とは別の鍵にします。`eki-control gen-worker-ca`が、既存のファイルを上書きせずに、鍵を0600で書きます。鍵の保管、更新、中間CA、バックアップは未決です。
