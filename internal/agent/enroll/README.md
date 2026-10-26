# Workerの登録と証明書の更新（Agent側）

[ADR-0027](../../../plan/adr/0027-worker-authentication-and-enrollment.md)の2.2と2.4のAgent側です。`eki-agent enroll`と、実行中の自動更新が使います。

## 登録（`Enroll`）

1. 秘密鍵（ECDSA P-256）をこの端末で作ります。鍵は端末の外へ出ません。Control Planeへ送るのは、公開鍵を含むCSRだけです。
2. 管理者から受け取ったCAの指紋で、Control Planeを確かめます。公開CAには頼りません。Control Planeが送ってくる証明書の鎖のうち、指紋が一致する証明書（葉でも、中間でも、根でもよい）を信頼の起点にして、鎖と接続先のホスト名を検証します。一致しなければ、TLSの握手の段階で止まり、**enrollment資格は送られません**（`ErrNotPinned`）。
3. 握手に成功してから、資格を`Authorization: Bearer`で一度だけ送ります。資格はURLにもログにも出しません。形式が違う文字列（パスワードなど）は、何も送らずに拒否します。
4. 返った証明書が、自分の鍵のもので、Workerの名前を持ち、期限内で、同梱のCAの署名であることを確かめてから保存します。

資格が使えない理由は、Control Planeが区別しません（`ErrRefused`）。既に識別子がある状態ディレクトリは上書きしません。

## 状態ディレクトリ

| ファイル | 内容 |
|---|---|
| `worker-identity.pem` | 秘密鍵とクライアント証明書。一時ファイルへ書いてから置き換えるので、鍵と証明書は組で入れ替わる。0600。Unix系では、他者が読めるファイルを読み込まない |
| `control-anchor.pem` | 信頼の起点として固定した、Control Planeの証明書。以後の接続はこれだけを信頼する |
| `worker-ca.pem` | Control Planeが返したWorker CAの証明書（参考） |

## 更新（`Renew`、`RenewLoop`）

残りが有効期間の3分の1になると、新しい鍵とCSRで、現在の証明書によるmTLSで更新します。更新のたびに鍵を新しくし、同じ鍵のCSRはControl Planeが拒否します。期限が切れた証明書では更新できず、新しいenrollment資格で登録し直します（`ErrExpired`）。無効化・失効されたWorkerも更新できません。更新に失敗しても期限まで再試行し、成功したら新しい接続から新しい証明書を使います。

同じ`WorkerIdentity`を引き継いだ再登録は、まだできません（ADRの未決）。
