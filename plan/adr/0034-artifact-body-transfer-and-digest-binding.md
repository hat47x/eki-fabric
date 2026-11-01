# ADR-0034 出力の本体はWorker側のAttempt作業盤に置き、Claim報告と同じmTLS平面で送り、Control Planeが受信時に再計算して束縛する

- 状態: **提案中**
- 日付: 2026-11-01
- 関連: Issue [#320](https://github.com/hat47x/eki-fabric/issues/320)（分割2として起票した [#407](https://github.com/hat47x/eki-fabric/issues/407)）、[#406](https://github.com/hat47x/eki-fabric/issues/406)、[#408](https://github.com/hat47x/eki-fabric/issues/408)、[#409](https://github.com/hat47x/eki-fabric/issues/409)、[#198](https://github.com/hat47x/eki-fabric/issues/198)、[ADR-0027](0027-worker-authentication-and-enrollment.md)、[ADR-0028](0028-result-claim-wire-mapping.md)、[ADR-0030](0030-assignment-delivery-pull-and-lease-currentness.md)

## 1. 背景

この版では、Taskの出力の本体がどこにも残りません。native runnerは標準出力をメモリ上のバッファへ溜め、SHA-256とbyte数だけを`Result`へ入れて捨てます（`internal/agent/nativerunner/runner.go`）。ResultClaimが運ぶのはダイジェストとbyte数だけで、`GET /v1/artifacts/{artifact_id}`は常に`501 NOT_IMPLEMENTED`を返します。READMEはこの事実を「成果物の取得経路は、まだありません」の節に書きました（PR #405）。

#320は判断単位へ切ることを完了条件とし、4件の分割Issueを起票済みです。このADRは分割2（#407）に答えます: 出力の本体をどの通信路で送り、誰がダイジェストを信じるか。

あるもの（origin/main `7723692` 時点）:

- Agent発のmTLS平面（ADR-0027）。ResultClaimの報告（`claimqueue`→Worker listener、testprofileのJSON、ADR-0028）と、Assignmentの一覧のpoll（ADR-0030）が同じ平面を通ります。
- ResultClaimとAcceptedResultの欄に、`OutputDigest`（SHA-256）と`OutputBytes`があります。受理（`resultacceptance`）はClaimの検査とコミットをしますが、本体が無いため比較の相手がいません。
- 出力上限はWorkOrderの値（`OutputBytesMaximum`、無指定なら1MiB）で、runnerの`limitWriter`が超過で実行を止めます。

ないもの:

- 出力の本体の保持場所と保持期間（#406）。
- 本体をControl Planeへ運ぶ経路と、受信時のダイジェストの再計算。
- AcceptedResultへの記録項目（size・media type・data class）と、取得API（#408、#409）。

## 2. 決定

### 2.1 保持: 出力の本体はWorker側のAttempt作業盤のファイルに置く

出力の本体は、**Worker側のAttemptごとの作業盤（ディスク上のファイル）に置きます**。メモリ上のバッファは、上限内の小さな出力の実装の簡便さとして残しますが、設計の正はファイルです。保持期間は**Claimが受理されるまで**（またはLeaseの期限が切れるまで）とし、受理の後はWorker側で捨てて構いません。

根拠:

1. Workerは割当ごとに独立した作業盤を既に持ちます（runnerの一時ディレクトリ、`TemporaryDiskBytes`）。出力も同じ扱いにすれば、追加の状態機構が要りません。
2. メモリに置くと、Agentのプロセス全体の上限（Local Governor）と出力上限の二つの真実が生じます。ファイルなら、出力上限（WorkOrderの値）だけが効きます。
3. Workerが再起動したら本体は失われます。**これは受け入れる**仕様です。受理の記録（AcceptedResult）はダイジェストと取得経路を持ちますが、本体を取り損ねたAttemptの再取得は「同じLeaseでの再実行」ではありません。取りこぼしが許されない作業は、Leaseの期限と本体の転送のタイミング（2.2）で設計します。

### 2.2 転送: Claim報告の後続の要求として、同じmTLS平面でWorkerから送る

本体の転送は、**ResultClaimの報告に続く、Worker発の別要求**とします。Claimの報告と同じmTLS平面（ADR-0027）を通し、Workerが接続を发起します。

- Claimは「ダイジェストの申告」であり、本体はその後続です。順序は、(1) Claim報告、(2) 本体の送信、(3) Control Planeが受信した本体のダイジェストを再計算してClaimと比較、(4) 一致したら`CommitAcceptedResult`、(5) 不一致なら受理を拒否（Claimの再報告を許す）です。
- 本体要求の認証はmTLSだけです。要求の欄は（Worker身份、AttemptのAttempt ID、ダイジェスト）で束ね、別のWorkerの本体を受け付けません。Attempt IDはLeaseの署名で確定しているので、Workerが知りうる値だけで偽れません。
- **wire契約（JSON項目、endpointの形）は#198（ADR-0028のrevision 2）に譲ります。** このADRは方式だけを決めます。PoCの実装は`testprofile`の拡張として入れます。
- Control PlaneからWorkerへ取りに行く（pull）方式は採りません。AgentはAssignmentのpoll（ADR-0030）とClaimの報告で既にWorker発の接続を持ちます。本体のためだけに、Control PlaneがWorkerの窓口を認証して呼び出す形は、ADR-0030が採らなかったpush（サーバ発の接続）と同じ負担を持ち込みます。

### 2.3 ダイジェストの束縛: 再計算はControl Planeが行い、保管はダイジェストを鍵にする

「誰が信じるか」の答えは、**誰も単独では信じない**です。

- Control Planeは受信した本体のbyte列からSHA-256を再計算し、Claimの`OutputDigest`とbyte数（`OutputBytes`）が一致することを要求します。Claimはダイジェストの申告でしかなく、一致する本体が届かなければ受理しません。
- 一致した本体は、**ダイジェストを鍵にして（content-addressed）保管**します。同じダイジェストの本体は同じものなので、二度同じ書き込みはしません。AcceptedResultは、この保管を指す参照（ダイジェスト・size・media type・data class。#408で欄を決める）を持ちます。
- 依頼元は取得API（#409）で本体を受け取った後、**自分でもう一度ダイジェストを計算できます**。この三段の束縛（Claim ⇔ 受信時の再計算 ⇔ 取得後の検算）が、署名されたClaim、転送経路、保管のどれかが壊れても検知できる根拠です。
- 試験の方針: (a) Claimと本体のダイジェストが違えば受理が拒否され、Attemptの結果は`failed`のままであること。(b) 受理された本体を取得APIから取り出すと、ダイジェストがAcceptedResultとResultClaimと同じ値に一致すること。(c) 出力上限を超える本体の送信は途中で拒否されること。いずれも`conformance`で確かめます。

### 2.4 信頼できない入力: 保管も取得も、本体を決して解釈しない

出力は、依頼元にとって**信頼できない入力**です（requirements/README.mdの「署名済みのArtifactであっても、脆弱なparserにとって安全な入力とは限りません」）。

- Control Planeは本体を**解釈しない**。media typeの欄は申告値を記録するだけで、Content-Typeに沿った処理をしません。既定は`application/octet-stream`です。
- 取得APIの応答は、ダウンロードとしての面（`Content-Type`に申告値、`Content-Length`に記録のsize）に限ります。HTMLとしての表示を意図した面を作りません。browserが誤って解釈しないよう、既定の`X-Content-Type-Options: nosniff`を付けます。
- 保管はopaque bytesの書き込みだけです。ファイル名はダイジェストから作り、依頼元の入力や出力の内容を名前に使いません。

## 3. 影響と移行

- **実装に足りないもの**: runnerの出力のファイル化（メモリのままの小さい出力も許す）、本体送信のwire（testprofile拡張→#198へ反映）、受信時の再計算と保管（`artifactstore`相当のSPI）、AcceptedResultへの記録項目（#408）、取得API（#409）。この順に実装します。#406（保持）から着手するのが自然です。
- **#198との関係**: 本体の転送はWorker Protocolの契約の一部なので、protoとJSON profileのどちらにも、このADRの欄（Attempt ID、ダイジェスト、byte数）を加算する必要があります。ADR-0028のrevision 2に含めます。
- **#320の他の分割との関係**: #406（保持）→ #407（このADR）→ #408（記録）→ #409（取得）の順。#407の受信と保管の実装は、#409の取得APIが無くても試験できます（受信したbyte列を直接読む）。
- **README**: 取得経路が入ったら、READMEの「まだありません」の節を更新します。

## 4. 選ばなかったもの

1. **Claim報告のJSONに本体をinlineで入れる**: 小さいメッセージを運ぶClaim報告の契約（ADR-0028）を壊し、二重のメモリ消費（送信側のバッファと受信側の構築）を持ち込みます。上限いっぱいの出力で現実的でありません。
2. **Control PlaneがWorkerから取りに行く（pull）**: 2.2のとおり。Workerの窓口を要求する形はADR-0030と同じ理由で採りません。
3. **独立したArtifact Storeの配備（S3等）**: 選定はOwner判断として#409に残します。M0の実装は、Control Planeのデータ領域に置くcontent-addressedなファイルで足ります。SPIの向き（保管の背後を差し替えられる）だけを、実装のときに守ります。
4. **圧縮・暗号化の追加**: 転送経路はmTLSで機密性が保たれ、保管の暗号化は要件にまだありません。要件が来たときに、このADRの上に重ねます（ダイジェストの束縛は暗号化と独立です）。
