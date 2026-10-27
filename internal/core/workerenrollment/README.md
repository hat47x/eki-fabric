# Worker登録簿の操作（enrollmentサービス）

[ADR-0027](../../../plan/adr/0027-worker-authentication-and-enrollment.md)の段階1と段階2です（Issue [#222](https://github.com/hat47x/eki-fabric/issues/222)）。記録と規則は[`workerregistry`](../workerregistry/README.md)にあります。このpackageは、誰が何をしてよいかの検査と、乱数の生成を受け持ちます。HTTPのendpointとmTLSのauthenticatorは[`internal/control/workerplane`](../../control/workerplane/README.md)、CAは[`workerca`](../../adapters/reference/workerca/README.md)にあり、ここには鍵が入りません。

| 操作 | 呼べる者 | 内容 |
|---|---|---|
| `IssueEnrollmentCredential` | 管理者（`Admin`） | 信頼領域に束縛された、一回限りの資格を発行する。有効期間は既定24時間、上限7日。秘密は戻り値に一度だけ現れる |
| `RevokeEnrollmentCredential` | 管理者 | 未使用の資格を失効する。使用済みの資格は失効できない（Workerを失効する） |
| `ConsumeEnrollmentCredential` | 資格を提示した者 | 資格を消費し、新しい識別子の`registered`なWorkerを同じトランザクションで作る。同時の使用でもWorkerは一つ |
| `SetWorkerState` | 管理者 | `disabled`、`registered`（再有効化）、`revoked`へ変える。`revoked`からは戻せない |
| `WorkerSelfRevoke` | Worker本人 | 自分の識別子だけを失効する（ADR-0027 §2.4 項19）。他の識別子、別の信頼領域は`ErrNotSelf` |
| `Enroll` | 資格を提示した者 | 資格を消費して登録し、渡された公開鍵の証明書を`CertificateIssuer`（CA）に発行させ、通し番号と期限を登録簿へ記録してから返す。呼ぶ側が先にCSRを検証する（資格を消費するため） |
| `Renew` | 現在の証明書で認証されたWorker | 登録簿で`registered`であることを確かめてから、新しい証明書を発行して記録する。無効化・失効したWorkerは`ErrWorkerNotUsable` |
| `LookupWorker` | Control Plane | 登録簿の項目を返す |
| `Authorize` | Control Plane | 要求ごとの確認。未知、別の信頼領域、`disabled`、`revoked`を同じ`ErrWorkerNotUsable`で拒否する。mTLSのauthenticatorが全要求で呼ぶ |

## 権限

`Admin`は、管理者の認証を通った後にだけ作る能力です。零値は拒否されます。Worker証明書やClientのトークンからは作れないので、Workerは資格を発行できず、Clientも登録簿を変えられません（SEC-010）。既存の管理者のRoleは`clientapi`にあるため、型付きの引数として受け取ります。段階2では、`eki-control`の運用者向けsubcommand（`enroll-token`、`worker-state`）が、ホストの運用者として`Admin`を作ります。Client APIのRoleとの接続は、まだありません。

## 外部に見せるもの

提示された資格が使えないとき、`ConsumeEnrollmentCredential`は`workerregistry.ErrCredentialRefused`に一致する同じエラーを返します。`workerregistry.RefusalReason`の値は監査ログ専用で、提示した相手へ返してはいけません。秘密はログにもエラーにも出しません。

## Workerの識別子

登録簿が乱数から作ります。呼び出し側は指定できません。識別子の衝突でStoreが失敗したときは、資格を消費しないまま別の識別子で再試行します。
