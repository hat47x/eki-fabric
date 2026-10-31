# ADR-0030 Assignmentの配送はWorker発のpullを正とし、Lease更新と回収も同じpoll答えに乗せる

- 状態: **提案中**
- 日付: 2026-10-31
- 関連: Issue [#243](https://github.com/hat47x/eki-fabric/issues/243)、[#242](https://github.com/hat47x/eki-fabric/issues/242)、[#198](https://github.com/hat47x/eki-fabric/issues/198)、[ADR-0027](0027-worker-authentication-and-enrollment.md)、[ADR-0028](0028-result-claim-wire-mapping.md)、[ADR-0029](0029-workload-suitability-gate-before-placement.md)

## 1. 背景

Control Planeが署名済みLease・WorkOrderをWorkerへ届ける経路を、まだ決めていませんでした。実装が先に進んだ現状を、#242と#243の間で整理します。

あるもの（#242マージ後のorigin/main、`a2eb2cb`時点）:

- Control平面のディスパッチループ（`internal/control/dispatch`）が、READYなTaskをsuitability → scheduler → placementauth → PLC-1 → LSE-1 → WOR-1の順で進め、State Storeにemitted記録を残します。
- Worker listener（mTLS、ADR-0027）に、試験用profileの割当一覧の窓口（`testprofile.ContentType`の`serveAssignments`）があります。`AssignmentService.ListAssignments`が`ListEmittedAssignments`（割当済み・期限内）を読み、そのWorkerの証明書の主体に一致する記録だけを返します。
- Agent側は`runAssignments`のpollで一覧を受け取り、leaseverify → workorderverify → 開始関門（startgate）の順で検証し、readyになったものをHandlerへ渡します。拒否はeventに`rejected`と理由を残します。
- `conformance/process`が、別プロセスのAgentが署名済みWorkOrderを受け取り、開始関門まで進むことを確かめています（`TestAgentVerifiesAndGateStartsDeliveredAssignments`）。

ないもの:

- Leaseの更新（heartbeat）と、停止・回収の通知を、Agentが受け取る経路。
- 配送の方式（pull・push・長い接続）を選んだ記録。proto（`spec/worker/v1/worker.proto`）には`PollCommands`・`WatchCommands`がありますが、CoreのAssignmentを運ぶ項目はありません（ADR-0028と同じ土俵の欠け）。

## 2. 決定

### 2.1 配送の方式: Worker発のpullを正とする

Assignmentの配送は、**Agent発の定期poll**を正とします。Control PlaneからWorkerへ送り出すpush（サーバ発の接続）も、長い接続（stream）も、このADRでは採用しません。

根拠:

1. **認証の向きと責任の向きが一致する。** ADR-0027のmTLSは、Workerが自分の証明書を示してControl Planeへ接続する形です。pullなら、一覧を出す前に主体が確定し、`ListEmittedAssignments`が「別のWorkerの記録を返す」事故を、証明書の主体との一致検査で閉じられます（`AssignmentService`はすでにそうしている）。pushなら、Control Planeが接続先のWorkerを認証して呼び出すことになり、Worker側が受信の窓口を開けて置く必要があります。NATの後ろに居るWorkerに、到達可能な窓口を要求しないで済むのはpullの利点です。
2. **耐久性の順序が既にそうなっている。** emitted記録はState Storeにあり、Agentが取りそこなしても次のpollで再び見えます。配送の再試行をWorkerのqueueに押し込むpushと違って、Control Plane側の再試行は「一覧に載せ続ける」だけで済み、Leaseの期限が自然な取りやめになります。
3. **既存の申告経路と同じ形になる。** ResultClaimの配送（`claimqueue`）は、Agentが耐久queueから送り出すpoll型です。配布も申告もpollなら、Agentの停止・再起動の扱いが一つの方法にそろいます。
4. protoの`WatchCommands`（stream）は使わずに残します。#198でWorker Protocolを定め直すときに、pollとstreamのどちらを契約に残すかを、その時に決めます（このADRは実装の決定であり、wire契約の決定はしない）。

### 2.2 Leaseの更新と停止・回収: 同じpoll答えに乗せる

Lease更新（heartbeat）と停止・回収の通知は、**Assignmentのpoll答えに載せます**。別の窓口を開きません。

- poll答えの各Assignmentに、Leaseの現行状態（`current`・`renewed`・`revoked`・`expired`）を添えます。Agentは`renewed`を受けたら現在のfence epochのまま実行を続け、`revoked`を受けたら実行を止めてHandlerに取り消しを伝えます。`expired`は検証の既存の拒否（`lease_expired`）と同じ扱いです。
- **更新の申告はResultClaimではなくpoll要求側に置く。** Agentはpollの要求に「今実行中のAttemptとLeaseのID」を載せ、Control Planeはそれを「このLeaseはまだ生きている」との申告（heartbeat）として扱います。実行が続く限りpollが来ることを、生存の根拠とします（ADR-0013の「更新が止まれば権限は失効する」を通信で表す形）。
- **回収（reclaim）は、一覧から消すことで行う。** Control PlaneがLeaseを回収したら、emitted記録から外すか、`revoked`印を付けます。消えたことにAgentが気づくのは次のpollであり、それ以上の即時性は要求しません。即時の停止が必要な作業は、Leaseの期限を短くして設計します（ADR-0025のPolicy Epochの更新と同じ発想）。
- **停止・回収の専用pushは作らない。** 到達可能性の要求（2.1の1）を壊すためです。Workerが止まっているときに届けられるべきものは「次に起きたときに読めるもの」であり、それはState Storeの一覧です。

### 2.3 検証に失敗したら実行しない

pollで受けたAssignmentは、leaseverify → workorderverify → 開始関門の順の検証を全部通るまでHandlerへ渡しません。1つでも失敗したら`rejected`のeventを残し、実行もstartgateの受理もしません（nonceもfenceも消費しない）。これは既存の`runAssignments`の実装の通りであり、ADRとして固定します。改竄されたWorkOrderは署名検証で落ち、実行に至りません。

## 3. 影響と移行

- **実装に足りないもの**: poll答えへのLease状態の添付（`current`/`renewed`/`revoked`/`expired`）、poll要求への実行中Leaseの申告（heartbeat）、`revoked`を受けたときの実行停止の伝播。いずれも`testprofile`の拡張と、Agent側の分岐で足します。protoへの反映は#198に譲ります。
- **ADR-0028との関係**: 申告（Worker→Control）のwire契約は0028に任せ、配布（Control→Worker）の方式だけをこのADRで決めます。poll答えのwire形式を0028のprotocol revision 2へ加算するとき、このADRの状態項目を同じ拡張に入れます。
- **試験**: 決めた経路は、既存の`TestAgentVerifiesAndGateStartsDeliveredAssignments`に加え、(a) 改竄したWorkOrderを実行しないこと、(b) 回収（一覧から消す）の次のpollでAgentが止まることを、`conformance/process`で確かめます。

## 4. 選ばなかったもの

1. **Control Plane発のpush（gRPC双方向stream、WebSocketなど）**: Workerが受信窓口を開けて置く必要があり、NAT越しのWorkerへの到達と、Control Plane側の接続管理を持ち込む。2.1の根拠1。
2. **Lease更新の専用endpoint（heartbeatのPOST）**: 生存の申告がpollの要求に載るなら、endpointは増えません。別に作ると「pollは成功したがheartbeatが途切れた」という二つの真実が生じます。
3. **回収の即時通知**: 2.2のとおり。pullの周期が上限です。
4. **protoの`PollCommands`/`WatchCommands`の使用**: CoreのAssignmentの項目がなく、ADR-0028と同じ「運べない」状態です。#198で定め直すまで使いません。
