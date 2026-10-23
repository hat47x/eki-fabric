# 結果申告を欠落なく運ぶProtobufの往復実験

## 目的と位置付け

Workerが結果を申告するとき、実行したTaskIR、実行体、WorkOrderのDecision、nonce、出力への束縛を失うと、Control Planeは何を受け取ったのかを確かめられません。[#198](https://github.com/hat47x/eki-fabric/issues/198)では、Coreの申告と既存の通信仕様の対応が未決です。

この実験ではCoreの`resultclaim.Claim`の内容と`Claim.Digest`をProtobufのバイナリで往復できるかを確かめます。実験用のschemaは`candidate.proto`です。`spec/worker/v1/worker.proto`への採用案を確定したものではなく、protocol revisionも割り当てていません。既存の通信仕様、生成コード、handler、Agentの起動設定は変更していません。

## 実験する対応

申告には認証済みの文脈から受け取るWorkerの主体を除いたCoreの各値と、申告全体のダイジェストを載せます。`work_order_decision_id`と`fence_epoch`は独立した項目にしました。既存仕様の`work_order_id`や`observed_lease_revision`を読み替えて埋めていません。

| Coreの値 | 実験用の表現 |
| --- | --- |
| TaskID・AttemptID・LeaseID | 形式を検査する文字列 |
| TaskIRDigest・ExecutorDigest・OutputDigest | SHA-256の32 bytes。OutputDigestは完了時のみ必須 |
| WorkerIdentity | 本文に項目を設けず、呼び出し側が渡すPrincipalから復元 |
| FenceEpoch・OutputBytes | uint64。OutputBytesは省略と0を区別 |
| WorkOrderDecision・Nonce | DecisionのIDと16〜32 bytesのnonce |
| Status | 7種類を別々のenum値で表現 |
| ExitCode | 省略と0を区別するsint64。実行環境のintに収まらなければ拒否 |
| StartedAt・EndedAt | Timestamp。ナノ秒を保持し、UnixNanoで表現できる時刻に限定 |
| Claim.Digest | 復元した申告から再計算して照合 |

受理確認はSPIの4種類の判定、7種類のAttemptPhase、VerificationRequiredを運びます。falseと欠落を区別し、フラグと状態が矛盾する場合は拒否します。送信した申告のダイジェストとも照合します。この検査は応答の対応と形の検査であり、結果の正しさや受理を判断する処理ではありません。

省略と値を区別する項目には、Protobufの[explicit presence](https://protobuf.dev/programming-guides/field_presence/)を使っています。[protocompile](https://github.com/bufbuild/protocompile)でschemaを解析し、`dynamicpb`と`proto.Marshal`・`proto.Unmarshal`で実際のバイナリを往復します。Goの生成コードを使った検証ではありません。

## 実行方法

リポジトリのルートから実行します。Go 1.27.0を使いました。依存の版とチェックサムは、この実験の`go.mod`と`go.sum`に固定しています。ルートの依存は変更していません。

```sh
cd spikes/claim-wire-roundtrip
go test -race -count=1 ./...
go vet ./...
GOARCH=386 go test -count=1 ./...
```

独立したGo moduleなので、ルートの`go test ./...`には含まれません。Windowsでは`GOOS=windows GOARCH=amd64 go test -c`で作った実行ファイルをWindows上で実行しました。

## 実験の範囲と残る判断

この実験では1 messageを64 KiB以下に限定し、未知の項目・enum・実験profile、欠落、不正なID・ダイジェスト・時刻を拒否します。これは実験の条件です。製品で使う上限や将来の項目追加との互換方針を決めたものではありません。Coreが受け付ける時刻でも、この実験の時刻範囲を超えれば拒否します。

Principalはテストで与えています。ダイジェストは署名ではなく、本文を作る主体は再計算できます。認証済みの主体を取得する処理、TrustDomain間の分離、CPの現在のTask・Attempt・Leaseとの束縛、session期限、protocolの版の交渉は検証していません。実際のネットワーク、Connect、応答消失後の再送、SQLiteキューの削除との接続も対象外です。

OutputDigestは既存Coreの値をそのまま運びます。Artifactの一覧の正規化やダイジェストの算出は行いません。既存仕様の`outputs`・`claim_id`・`claimed_at`等との対応、加算拡張か別版かの選択、生成コードとhandlerの検証は、引き続き#198で決める必要があります。既存仕様との互換性や、#173・#160の完了を示す実験ではありません。

実行条件と結果は[2026-10-23の記録](RESULTS_2026-10-23.md)にあります。
