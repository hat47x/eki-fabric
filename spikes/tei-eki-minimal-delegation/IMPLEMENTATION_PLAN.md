# TEI→EKI最小委任の実装計画（後続実装者向け）

この文書は、[README](README.md)のSpikeと[RESULTS_2026-11-11](RESULTS_2026-11-11.md)の観測を、後続の実装者が迷わずに引き継げる形へ落としたものです。設計の正本ではなく、作業の順序・変更するファイル・試験・受入条件・Owner判断の待ち方を定めます。

## 0. 読み方と現在地

読む順序は、[README](README.md)（何を検証したか）→[RESULTS_2026-11-11](RESULTS_2026-11-11.md)（何が観測でき、何ができなかったか）→この文書（次に何をするか）→`adapter.go`以下のコードです。

現在地（2026-11-11時点）:

- 完了: 一件の`tei validate`をEKIの公開APIへ投影するアダプター、Receiptの判定、Mock試験、実EKIでの入力・受理・重複・拒否の観測。PR [#827](https://github.com/hat47x/eki-fabric/pull/827)。
- 未完: 実際のAttempt、EKIによる結果受理、実行体digestとToolchainの固定、ビルドなどの重い処理。
- この文書はPR #827と一緒にレビューし、マージ後も計画として残します。観測の記録（RESULTS）は日付ごとに追加し、書き換えません。

用語:

| 用語 | 意味 |
|---|---|
| Operation | TEI側の一件の処理。今回は`tei validate -workspace <dir> -json` |
| Projection | OperationをEKIの`eki.task-request/v1`とingest intentへ写したもの |
| Delegation | 入力の持ち込みからReceiptの決定までの一回 |
| Binding | 「EKIが強制する」「記録だけ」「欄がない」の区別 |
| Receipt | TEI側の三値の判定。`accepted`・`rejected`・`undetermined` |

## 1. 完了の定義（T1 EKI Native）

TEIのIssue [#4194](https://github.com/hat47x/tei-platform/issues/4194)のT1を「実行済み」とする条件を、次のD1〜D6に具体化します。**D1〜D4がすべて観測できたときだけ**、T1を実行済みと呼びます。

- **D1 実際のAttempt**: 実EKIでTaskがWorkerへ配送され、Attemptが1件以上作られる。観測は`attempt_count >= 1`と、EKIの試験コードが読むAttemptレコードの存在。**2026-11-11に満たした**（`conformance/process/tei_validation_test.go`）。
- **D2 実行結果の一致**: Workerが実行した`tei validate`の出力digest（stdoutとstderrを合わせたもの）が、同じ入力・同じ実行体でのローカル実行と一致する。**2026-11-11に満たした**。
- **D3 EKIの受理**: 独立した検証2件によりAcceptedResultが作られ、`GET /v1/tasks/{id}/result`が200と`output_digest`を返す。**2026-11-11に満たした（ただし検証は試験が注入）**（`conformance/process/tei_result_acceptance_test.go`）。製品の受理経路が独立した検証者を得るのはDR-3のOwner判断待ち。
- **D4 Receipt**: TEIのReceiptが`accepted`になり、`basis`にD2とD3、`not_checked`に実行体・Toolchain・キャッシュの未固定が残る。**2026-11-11に満たした**（D3と同じ制約下）。
- **D5 負例**: 重複・期限・結果未達・Lease失効・古いAttempt・部分Artifact・キャッシュ差異のそれぞれについて、実EKIで作れたものは実試験、作れないものは模擬と明記する。**2026-11-11に到達**（[RESULTS_2026-11-11_wp5](RESULTS_2026-11-11_wp5.md)の表）。Fence不一致と古いAttemptの結果拒否は公開経路で作れないため模擬。
- **D6 契約乖離**: `expected_digest`、`ArtifactDescriptor.digest`の型、Problemコードの3件の扱いが決まり、試験またはIssueで追跡される。**整理済み、Owner判断待ち**（#112・#245）。

**結論（2026-11-11）**: D1〜D4は同じ経路で観測できた。ただしD3の受理は試験が検証を注入しており、製品の独立検証ではない。したがってT1は「**実行済み（受理は試験注入）**」と記録し、製品の受理経路が決まった時点で限定を外して再判定する。T1を無条件に実行済みとは宣言しない。

D1〜D6に含めないもの: ビルド・コード生成・テストの実行、本番配備、外部サービス、連合。これらはT1の後です。

## 2. 境界（触ってよいもの、いけないもの）

| 対象 | 触ってよいか | 条件 |
|---|---|---|
| `spikes/tei-eki-minimal-delegation/` | よい | この計画の主対象。EKIの内部パッケージはimportしない |
| TEIの製品コード（提案先は`internal/execution/ekidelegation`） | 移す場合はTEI Ownerの承認 | 移動はWP-9。EKIのGoパッケージをimportしない |
| `spec/external/openapi.json` | **Ownerのみ** | 契約変更はIssueで決める。Spikeからは変えない |
| `internal/control`・`internal/core` | EKIの実装者 | 変更は既存Issue（#245・#242・#112）に紐づける |
| `conformance/process` | レビュー付きでよい | 実際のAttemptを作る試験（WP-4）を追加する場所 |
| EKIのSQLite・ログ・Workerの内部状態 | **いけない** | TEIは公開APIだけを読む。結果の判定に内部状態を使わない |
| 本番DB・外部サービス | **いけない** | 試験は一時DBとloopbackに限る |

依存の向きはTEI → EKIです。SpikeのコードがEKIの`internal/`や`cmd/`をimportしたら、`conformance/productboundary`の検査と設計の両方に反します。

## 3. 作業パッケージ

各WPは、変更するファイル、追加する型・関数、試験、受入条件、依存、Owner判断の要否を持ちます。WP-1〜WP-3とWP-8はOwner判断なしで進められます。

### WP-1: アダプターの入口を固定する（完了）

- 変更: `adapter.go`（追加）、`delegate.go`（`Delegate`を`API` interfaceへ変更）
- 型:
  - `type API interface { CreateIngestIntent / UploadArtifact / GetArtifact / SubmitTask / GetTaskStatus / GetTaskResult / CancelTask / RequestCount }`
  - `func NewAdapter(api API, options DelegateOptions) (*Adapter, error)`
  - `func (a *Adapter) Execute(ctx, op Operation, uploads map[string][]byte) (Delegation, error)`
  - `func (a *Adapter) ExecuteProjection(ctx, projection Projection, uploads map[string][]byte) (Delegation, error)`
- 試験: `TestNewAdapterRejectsNilAPI`、`TestAdapterExecuteRejectsIncompleteOperation`、`TestAdapterExecuteRunsTheProjectionAndReturnsTheTrace`、`TestAdapterExecuteProjectionReusesTheExactRequest`、`TestSpikeDoesNotImportEKIGoPackages`（依存の向きを構文木で検査する）、`var _ API = (*Client)(nil)`
- 受入: `go test ./spikes/tei-eki-minimal-delegation/...`が通り、依存の向きの試験が通る。`go list -deps`に`github.com/hat47x/eki-fabric/internal/`が出ない。
- 判断: 製品コードの置き場所はWP-9で決める。当面はSpikeに置く。

### WP-2: Receiptの形を固定する（完了）

- 変更: `examples/receipts.json`、`examples_test.go`
- 受入: 三値すべての例があり、`basis`と`not_checked`が空でない。`outcome`ごとの不変条件（acceptedは200とdigest、undeterminedは結果を持たない）が試験で固定される。
- 次の実装者へ: Receiptの消費者を書くときは、このJSONの形だけを読む。EKIの内部識別子を期待しない。

### WP-3: 入力の不一致でTaskを送らない（完了）

現状は、アップロード応答の`artifact_id`が意図したdigestと違っても、そのままTaskを送り、EKIが`422 INPUT_ARTIFACT_UNRESOLVABLE`で拒否する。これは安全側だが、無駄なTaskを作る。TEI側で止める。

- 変更: `adapter.go`、`delegate.go`
- 追加: 入力ループで`accepted.Artifact.ArtifactID != in.SHA256Hex`なら、`Delegation`を`rejected`で返し、`SubmitTask`を呼ばない。`UploadRecord.DigestAgreed == false`を残す。
- 試験: `TestUploadDigestMismatchStopsBeforeSubmit`（Mockで、ingest/upload後に`artifact_id`を別digestへ差し替える。TaskのPOST回数が0であることを数える）
- 受入: 不一致時に`SubmitTask`が一度も呼ばれない。Receiptの`reason`に、宣言digestと実測digestの両方が出る。
- 依存: なし。Owner判断: なし（狭く安全な既定）。

### WP-4: 実際のAttemptを作る（完了。D1・D2を満たす。2026-11-11の実測）

EKIの`conformance/process`の既存の仕組みを使う。新しい共通RuntimeやRegistryは作らない。

- 追加: `conformance/process/tei_validation_test.go`（EKIリポジトリ）
- 参考にする既存コード:
  - `conformance/process/http_submit_agent_test.go`の`TestHTTPTaskSubmissionIsExecutedByTheAgent`（:97）
  - `controlSigningKeys`（:45）、`assignmentAgentWithSigningKeys`（:65）
  - `conformance/process/dispatch_loop_test.go`の`writeSigningKeys`（:40）、`enableDispatch`（:69）、`assignedAttempt`（:120）
  - `conformance/process/mtls_test.go`の`newMTLSEnv`（:64）、`writeConfig`（:87）、`enrollAgent`（:157）、`seedPolicyNow`（:280）
  - `conformance/process/execution_agent_test.go`の`reexecuteOutputDigest`（:256）
- 環境変数で外部のTEI実行体を受ける。未設定なら`t.Skip`する（CIをTEIのビルドへ依存させない）。
  - `TEI_EKI_TEI_BINARY`: `tei`の絶対パス
  - `TEI_EKI_WORKSPACE`: `reference/workspace`の絶対パス
  - `TEI_EKI_EXPECTED_SHA256`: ローカル実行の出力digest（stdout+stderr）
- 手順:
  1. `newMTLSEnv`→`seedPolicyNow`→`writeConfig`→`writeSigningKeys`→`enableDispatch`→`startControl`は既存テストと同じ。
  2. Agentの`ExecutionConfig`に、**入力を展開して`tei validate`を実行するラッパー**を指定する。`ControlURL`と`ArtifactBaseURL`はどちらも`e.workerURL`（Worker Planeのorigin）にする。`InputsDir`は一時ディレクトリ。Agentは入力を`<inputs_dir>/<digest>`へ0600で書く（`internal/agent/assignment/fetcher.go`・`assignment.DirSaver`）。
     - 既存の`assignmentAgentWithSigningKeys`は`ArtifactBaseURL`と`InputsDir`を設定しない。この試験用に同じ形のヘルパーを足すか、既存ヘルパーへ引数を追加する。既存の試験の挙動は変えない。
     - `ArtifactBaseURL`はoriginだけを渡す。`HTTPFetcher`が`<base>/test-profile/v1/artifacts/<digest>`を組み立てる。
  3. ラッパーは小さなGo実行体にする（テストが`go build`で作る）。引数は`<archive path>`と`<tei binary>`。tar.gzを一時ディレクトリへ展開し、`tei validate -workspace <dir> -json`を実行して、stdoutとstderrをそのまま自分のstdout・stderrへ流す。終了コードを引き継ぐ。展開は、絶対パス・`..`・シンボリックリンクを拒否し、通常ファイルとディレクトリだけを書く。約40行で足りる。

     ```go
     // wrapper/main.go — the test builds this into a temp dir and names it in
     // ExecutionConfig.Command.
     func main() {
         archive := flag.String("archive", "", "input tar.gz")
         tei := flag.String("tei", "", "tei binary")
         flag.Parse()
         dir, err := os.MkdirTemp("", "tei-workspace-")
         if err != nil { log.Fatal(err) }
         defer os.RemoveAll(dir)
         if err := extractTarGz(dir, *archive); err != nil { log.Fatal(err) }
         cmd := exec.Command(*tei, "validate", "-workspace", dir, "-json")
         cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
         if err := cmd.Run(); err != nil {
             if exit, ok := err.(*exec.ExitError); ok { os.Exit(exit.ExitCode()) }
             log.Fatal(err)
         }
     }
     ```

     `ExecutionConfig`の形は次のとおり。

     ```go
     inputsDir := t.TempDir()
     execution := &agentservice.ExecutionConfig{
         Command: wrapperPath,
         Args: []string{
             "-archive", filepath.Join(inputsDir, inputDigest),
             "-tei", teiBinary,
         },
     }
     // AssignmentConfig側: ArtifactBaseURL: e.workerURL, InputsDir: inputsDir,
     // Execution: execution
     ```
  4. Client APIからTaskを投入し、`assignedAttempt`でAttemptの存在を待つ。Agentのeventsで実行完了と`ResultClaim`送信を待つ。
  5. `attempt_count >= 1`、`LoadResultClaim`が成功、Claimの`OutputDigest`が`TEI_EKI_EXPECTED_SHA256`と一致することを確かめる。
  6. Taskの公開状態が`verifying`であることを確かめる。結果は`409 RESULT_NOT_READY`のまま（WP-5の範囲）。
  7. 可能なら、Runnerが保持した出力bodyがArtifactとして保管され、`GET /v1/artifacts/{id}`で取得できることも確かめる（`conformance/process/execution_body_test.go`の経路）。取得できた本文のdigestがClaimの`OutputDigest`と一致することを確認する。取得できない場合は、その事実を記録し、成功として扱わない。
- 受入: 環境変数ありで試験が通り、D1とD2が観測できる。環境変数なしでは`t.Skip`し、他の開発者の実行を止めない。Claimのdigestが期待値と違う場合は、期待値を書き換えずに失敗させる。
- 実測（2026-11-11、WSL2 Ubuntu 22.04）: 成功。Task `tsk_27e89d…` にAttempt `atm_858981…` が作られ、Agentの`OutputDigest`がTEIのローカル期待値`d1d220ad…`と一致した。詳細は[RESULTS_2026-11-11_wp4](RESULTS_2026-11-11_wp4.md)。
- 実装の実際の形（計画時の見積もりとの差）: ラッパーは`<archive> <tei binary> <report file>`の3引数を取り、実行後にreportへ`exit=`と展開したエントリ数を書く。試験は、ラッパーを`go build`で作り、`ExecutionConfig.Command`に渡す。`ArtifactBaseURL`と`InputsDir`は`AssignmentConfig`側に置く。
- 実行の前提として、EKI側の未配線2件を直した（入力ArtifactをWorker Planeから配る口と、AgentのfetcherのmTLS）。この2件がなければ、Agentは`input_unavailable`で割当を拒否する。
- 3つの試験（WP-4・WP-5・WP-11）が同じ経路を使うように、`conformance/process/tei_delegation_helpers_test.go`の`runTEIValidation`に共通化した。片方だけ別の経路を通ると、共通部分の変更でどちらが通るか分からなくなるため。
- 注意:
  - Runnerの出力digestは**stdoutとstderrを合わせたバイト列**のSHA-256（`internal/agent/nativerunner/runner.go`）。ローカル比較も同じ結合で測る。`validate`は通常stderrを出さないが、測り方を合わせる。
  - 展開先のパスが出力に混ざると、ローカルとWorkerでdigestが変わる。`tei.cli.validate/v0`の出力に絶対パスが入らないことは2026-11-11の手元の実行で確認したが、実装を変えたら必ず再確認する。混ざる場合は、同じパスで実行するか、比較前に正規化する。正規化を「同じ結果」の根拠にしない。
  - 実行条件は`dispatch`の`Limits`とSandbox、Agentの`Machine`に合わせて広げる。既定値のままだと小さすぎる。
  - Windowsではプロセスツリー停止の既知の失敗がある（[#312](https://github.com/hat47x/eki-fabric/issues/312)）。WP-4の試験は、まずLinux（WSLにGoを入れた環境）で実行し、Windowsでは結果を記録してから対応を決める。2026-11-11にこのホスト（Windows）で既存の`TestHTTPTaskSubmissionIsExecutedByTheAgent`を10分の上限で試したが、完了しなかった（依存モジュールの取得を含む）。WP-4の実行環境はLinuxとする。
- Owner判断: この試験をEKIの`conformance/process`へ置くこと自体はレビューで決める。製品コードは変えない。

### WP-5: EKIの結果受理をどう観測するか（既定案Cで完了。Owner判断待ちは残る）

D3は現行の起動経路では作れない。`resultacceptance`は既定で`RA2`・`threshold 2`・`principal`と`operator`の2次元を要求し、`defaultProducers`はどちらもWorker identityを返す。Claim 1件ではquorumに届かない（EKI #242）。

選択肢:

- **A. 試験用の検証入力を配備に足す**: `eki-control`の試験用profileに、署名済みの検証Observationを受け取る経路を足す。製品の保証には数えない。EKIの実装変更。
- **B. 単一Worker配備向けのポリシーを設定可能にする**: 設定ファイルで`threshold`と次元を下げられるようにする。保証水準が下がることを起動時に警告する。EKIの実装変更。
- **C. 試験コードが`resultacceptance`を直接呼ぶ**: 既存の`TestHTTPTaskSubmissionIsExecutedByTheAgent`と同じ方法。製品経路ではないと明記する。**当面の既定**。

既定はC。WP-4の試験の続きとして、2件の合成Verificationで`Accept`を呼び、AcceptedResultを作り、動いている`eki-control`の`GET result`が200を返すことまで観測する。ラベルは「試験が受理を注入した」と明記する。AまたはBがOwnerに選ばれたら、Cを置き換える。

覆す条件: 本番の結果保証をT1の受入に含める場合はAかBが必須。Cのまま「EKIが独立に受理した」と書かない。

- 実装（2026-11-11）: `conformance/process/tei_result_acceptance_test.go`。`runTEIValidation`で実プロセスを回し、`resultacceptance.Accept`へ2件のVerificationを渡してAcceptedResultを記録し、`GET /v1/tasks/{id}/result`が200・`output_digest`一致・本文の再測定一致・Taskが`terminal/completed`であることを確かめた。実測は[RESULTS_2026-11-11_wp5](RESULTS_2026-11-11_wp5.md)。
- 残るOwner判断: DR-3（#242）。AかBが選ばれたら、この試験の受理部分を製品の経路へ差し替え、D3の限定（「試験注入」）を外す。

### WP-6: 実行体digestとToolchain（Owner判断待ち）

現状は`parameters`に記録するだけで、EKIはWorkerへ渡さず、Receiptも保証しない。

選択肢:

- **A. 契約に欄を足す**: `TaskRequest`またはWorkOrderに実行体digestとToolchainを足し、EKIが登録済みExecutorと照合する。外部契約の変更でOwner判断。
- **B. 記録だけを続ける**: TEIはローカル期待値とReceiptの`not_checked`で限界を示す。**当面の既定**。
- **C. 配備のExecutor記述を公開する**: 登録済みExecutorのdigestとToolchainを読める公開経路を作り、Clientが比較する。新しい契約。

既定はB。WP-3とWP-4はBのままで進められる。AまたはCが選ばれたら、`project.go`のBinding表、`contract_test.go`、Receiptの`not_checked`を更新し、実EKI試験で照合を足す。

### WP-7: 契約と実装の不一致（一部Owner判断待ち）

- `expected_digest`が強制されない（EKI #245）。既定は「宣言のみ」としてTEI側で照合する（WP-3）。Ownerが強制を選んだら、`internal/control/clientapi/upload.go`の`sink.Put`にsessionの期待digestを渡し、`TestUploadRejectsBodyThatDoesNotMatchTheDeclaredDigest`を足す。Spike側は`DigestAgreed`のままでよい。
- `ArtifactDescriptor.digest`の型が契約と違う。既定は両形式を受け取る。契約を直す場合は`openapi.json`をOwnerが変え、Spikeの`Digest.UnmarshalJSON`を厳格化して試験を更新する。
- Problemコードが契約に列挙されない。既定は`ARTIFACT_NOT_AVAILABLE`と`INPUT_ARTIFACT_UNRESOLVABLE`の両方を「入力が使えない」として扱う。列挙を足す場合は`client.go`の判定表と`probe.go`の期待を更新する。

### WP-8: ローカル実行との比較を、判断できる形にする（完了）

現状の比較は1回の実測で、ローカル実行の時間は同じホストでも0.15〜9秒台まで変動した。費用の結論には使えない。

- 変更: `compare.go`（`Comparison`・`Compare`）、`cmd/tei-eki-delegation/compare.go`（`compare`サブコマンド）、`compare_test.go`。
- 形: ローカルのサンプル数・min・中央値・max・出力の安定性・終了コード・キャッシュ状態と、EKI往復のサンプル数・min・中央値・max・**待ちを除いた封筒の費用**・リクエスト数・receiptの内訳・観測したAttempt数を1つのJSONにする。
- 受入: 空の入力では標本数を0とし、時間を発明しない。`caveat`が空でない。`attempts_observed`が0なら「EKIはこの操作を実行していない」と読める。
- 実行: `tei-eki-delegation compare -local ... -trace ...`。既存の証拠から`evidence/2026-11-11/comparison.json`を作った。ローカルは3標本（158 / 398 / 836 ms、出力は一致）、EKI往復は2標本（中央値3,088 ms、待ち3,000 msを除いた封筒は88 ms、12リクエスト、Attemptは0）。
- Owner判断: なし。

### WP-10: Lease失効と古いAttemptを、実EKIで作る（完了。回復は未達）

WP-4でAttemptが実在するようになったので、これまで模擬でしか確かめられなかった負例を、実EKIで作れるようになりました。EKIの試験用profileには、Leaseを回収するループ（`leaserecall`）と、期限切れのAttemptを閉じる`recallCloser`（`internal/control/service/recall.go`）が既にあります。

- 追加: `conformance/process/tei_lease_loss_test.go`（EKI側）。
- 手順:
  1. WP-4と同じ手順でTaskを受理・配送し、Attemptを`dispatched`にする。
  2. `eki-control`の設定に`lease_registry`を足して起動する（`run.go`の`recallCloser`が動く配備にする）。
  3. `dispatch`の`LeaseLifetime`を短く（例: 2秒）し、Agentを起動しない、または起動して実行させない。Leaseが期限切れになるまで待つ。制約が2つある。`RecallGrace`は30秒（`internal/control/service/recall.go`）、回収ループの間隔は`time.Minute`固定（`run.go`の`leaserecall.Run`）である。したがって回収が走るまで最長で約90秒待つ。試験時間を短くしたい場合は、回収の間隔を設定にする変更が要る。これは起動経路の変更なので、EKIの実装者と相談する（Owner判断ではなく実装判断）。
  4. `attempts`の`phase`が`closed`になり、`close_reason`が失効由来であることをState Storeで確かめる（試験コードは内部状態を読んでよい。製品のClientは読まない）。
  5. そのあとAgentを起動し、失効したLeaseのAssignmentが配られないことをeventsで確かめる（期限切れのLeaseはlistingから落ちる）。
  6. 新しいAttemptが作られ、Taskが`ready`へ戻ることを確かめる（回復）。**このrevisionでは観測できなかった。**
- 受入: 実EKIで「Leaseが失効すると、そのAttemptの結果は採用されない」ことが観測できる。観測できなければ、模擬のままであることをRESULTSへ明記する。
- 関連: EKI #745（回収ループ）、#173（Outcomeの報告）、#155（回復の公平性）。
- 実測（2026-11-11）: `conformance/process/tei_lease_loss_test.go`。Attemptが`lease_expired`で閉じ、Claimは無く、registryから除去された。閉鎖後に起動したAgentはそのAttemptのeventを一つも受けなかった。**Taskは`active`のまま、Attemptは1件のままで、新しいAttemptは作られなかった**（#745の回復は未達）。試験時間は約77秒。詳細は[RESULTS_2026-11-11_wp5](RESULTS_2026-11-11_wp5.md)。
- Owner判断: なし（試験用profileの範囲）。ただし製品の回復保証として扱わないことをRESULTSに書く。

### WP-11: 部分的なArtifact受領を、実EKIの応答で作る（完了）

`TaskResult.artifacts[].available`が`false`になる実例を作り、TEIのReceiptが`undetermined`になることを確かめます。EKIが受理した結果の本文を退役させたあとで取得する経路（ADR-0037の退役）が必要です。

- 追加: `conformance/process/tei_partial_result_test.go`（EKI側）、またはSpike側のprobe。
- 受入: 本文が取得できない状態で、Receiptが`accepted`にならないこと。
- 依存: D3（結果受理）が先に必要。WP-5の後。
- 実測（2026-11-11）: `conformance/process/tei_partial_result_test.go`。AcceptedResultを作ったあと、メタの`expires_at`を過去にし本文を削除すると、結果は`available:false`・`artifact_id`なし・`sha256`と`size_bytes`は残る形になった。取得は410/404。TEIのReceiptは`undetermined`（`available 0`、`fetched 0`）で、`accepted`にならなかった。詳細は[RESULTS_2026-11-11_wp5](RESULTS_2026-11-11_wp5.md)。
- 注意: 退役は公開経路が無いため、試験が一時配備のメタを書き換えている。実配備の退役（ADR-0037）と同じ効果を狙ったものであり、Clientの能力ではないとRESULTSに明記した。

### WP-9: 製品化と後片付け

- アダプターをTEIの`internal/execution/ekidelegation`へ移すか、Spikeのまま参照実装にするかをTEI Ownerが決める。
- 移す場合: `API` interfaceとReceiptの型をそのまま持ち、EKIのGoパッケージをimportしない。`tei validate`のCLI配線とfeature flagを足す。
- 移さない場合: Spikeを参照実装として残し、製品コードからは呼ばない。READMEにそう書く。
- 期間を過ぎたSpikeの証拠は削らない。日付ごとの記録として残す。

## 4. 判断記録（既定案と覆す条件）

| ID | 論点 | 既定案（いま進める形） | 覆す条件 | 記録先 |
|---|---|---|---|---|
| DR-1 | `expected_digest`を強制するか | 宣言のみ。TEIが`artifact_id`を照合し、不一致ならTaskを送らない（WP-3） | Ownerが取り込み時の強制を選ぶ | EKI #245 |
| DR-2 | 実行体digest・Toolchainを契約へ入れるか | `parameters`へ記録し、Receiptの`not_checked`に残す | Ownerが契約変更（A）か公開経路（C）を選ぶ | EKI #112 |
| DR-3 | 独立検証の入力をどこから得るか | 当面は試験コードが`resultacceptance`へ2件の検証を渡す（C）。製品の保証とは呼ばない | OwnerがAかBを選ぶ、またはT1の受入に製品の受理を含める | EKI #242 |
| DR-4 | `digest`の型の不一致 | クライアントはobjectとhex文字列の両方を受ける | Ownerが契約をobjectへ揃える、または実装をobjectへ直す | EKI #245 |
| DR-5 | Problemコードの列挙 | 2つのコードを同じ意味に写す | Ownerが`openapi.json`へ列挙を足す | EKI #112 |
| DR-6 | アダプターの製品コードの置き場所 | Spikeに置き、TEI製品コードからは呼ばない | TEI Ownerが`internal/execution/ekidelegation`への移動を承認する | TEI #4194 |

判断が変わるたびに、この表と該当Issueへ同じ結論を書く。Spikeの成功を要求や契約の暗黙の変更として扱わない。

## 5. 試験の対応表

| 事象 | 実EKIで作れるか | 現在の確かめ方 | 追加する試験 |
|---|---|---|---|
| 入力Artifactの往復 | 作れる | `delegate_test.go`（Mock）、`trace-1.json`（実）、`conformance/process/tei_validation_test.go`（実プロセス） | なし |
| 重複実行（同じ冪等キー） | 作れる | `delegate_test.go`、`trace-2.json`（実） | Attemptがある状態での重複（WP-4の次の作業） |
| 期限切れ（受理後に経過） | 作れる | `probe.json`（実） | Attemptがある状態での期限切れ |
| 期限切れ（投入時に経過） | 作れる | `probe.json`（実） | なし |
| 結果未到着 | 作れる | `trace-1.json`（実）、`conformance/process/tei_validation_test.go`（Attempt後の`verifying`） | なし |
| Lease失効 | 作れない（公開されない） | `simulate_test.go`（模擬） | EKIの試験用profileでLeaseを失効させ、Attemptが閉じることを観測する試験 |
| 古いAttempt | 作れない（公開されない） | `simulate_test.go`（模擬） | 同上 |
| Fence不一致 | 作れない（公開されない） | `simulate_test.go`（模擬） | 同上 |
| 部分的なArtifact受領 | 作れる（応答の形として） | `delegate_test.go`（Mock） | 実EKIの応答で`available:false`を作る試験 |
| キャッシュ差異 | 作れる（digest不一致として） | `delegate_test.go`、`simulate_test.go` | 実EKIで別実行体・別入力のdigest不一致を作る試験（任意） |

## 6. 実行順序と停止点

1. **Phase A（Owner判断不要）**: WP-3（完了）・WP-4（完了。D1・D2を満たす）・WP-8（完了）。残りはWP-2の維持。
2. **Phase B（EKIの試験コード）**: WP-4。環境変数ありで実行し、D1・D2を観測する。Windowsで不安定ならLinuxで実行し、結果を記録する。
3. **判断待ち（停止点）**: DR-1（#245）、DR-2（#112）、DR-3（#242）、DR-6（TEI #4194）。ここで製品の契約・保証に関わる実装を始めない。
4. **Phase C（判断後）**: WP-5・WP-6・WP-7の該当部分。
5. **Phase E（Owner判断不要・WP-4の次）**: WP-10（Lease失効と古いAttemptを実EKIで作る）→ WP-11（D3の後）。
6. **Phase D**: WP-9の製品化。T1の受入は、D1〜D4が同じ実行で揃ったときに宣言する。

Phase AとBは並行できる。Phase BはEKIの`conformance/process`を触るので、EKIのレビューを先に依頼する。

## 7. 後続実装者のチェックリスト

作業を始めるとき:

- [ ] `git fetch origin && git log --oneline origin/main -1`で基点を確認する。
- [ ] `go test ./spikes/tei-eki-minimal-delegation/... ./conformance/doclinks`を通す。
- [ ] `go vet ./...`を通す（Windowsで`internal/agent/nativerunner`などが落ちる場合は、未変更パッケージの環境差として記録する）。
- [ ] `evidence/2026-11-11/trace-1.json`と`probe.json`を読み、現在の観測を把握する。

WP-3:

- [ ] `adapter.go`の入力ループで、アップロード応答のdigestを要求digestと比較する。
- [ ] 不一致なら`SubmitTask`を呼ばず、Receiptを`rejected`にする。
- [ ] `TestUploadDigestMismatchStopsBeforeSubmit`を足し、POST回数0を確かめる。
- [ ] Mock試験と実EKI試験のどちらで何を確かめたかをRESULTSへ追記する。

WP-4:

- [ ] `conformance/process/tei_validation_test.go`を、既存の`TestHTTPTaskSubmissionIsExecutedByTheAgent`を写して作る。
- [ ] 環境変数3つを読み、未設定なら`t.Skip`する。
- [ ] ラッパーをテスト内で`go build`し、`ExecutionConfig`と`ArtifactBaseURL`・`InputsDir`を設定する。
- [ ] `attempt_count >= 1`、Claimの`OutputDigest`、Taskの`verifying`を確かめる。
- [ ] 環境変数ありの実行結果を`spikes/tei-eki-minimal-delegation/evidence/YYYY-MM-DD/`へ保存し、日付入りのRESULTSを追加する（11-11の記録は書き換えない）。
- [ ] 実行できなかった環境では、その理由と残る危険をRESULTSに書く。

WP-5（完了）:

- [x] `runTEIValidation`の経路を使い、`resultacceptance.Accept`へ2件のVerificationを渡す。
- [x] `GET /v1/tasks/{id}/result`が200、`output_digest`が期待値と一致、本文が名前に一致することを確かめる。
- [x] Taskが`terminal/completed`であることを確かめる。
- [x] 「検証は試験が注入した」とRESULTSに明記する。

WP-10（完了。回復は未達）:

- [x] `lease_registry`を設定し、`lease_lifetime`を短くして起動する。
- [x] Attemptが`lease_expired`で閉じ、Claimが無いことを確かめる。
- [x] 閉鎖後に起動したAgentが、そのAttemptのeventを受けないことを確かめる。
- [x] Taskが回復しないことを記録する（成功として扱わない）。

WP-11（完了）:

- [x] 受理後にメタの期限を過去にし、本文を削除する。
- [x] 結果が`available:false`で、`sha256`と`size_bytes`は残ることを確かめる。
- [x] Spikeの`EvaluateTask`でReceiptを読み、`accepted`にならないことを確かめる。

次の作業（WP-10の回復、D3の製品化）:

- [ ] 新しいAttemptが作られる経路をEKI #745で実装し、この試験に足す。
- [ ] 製品の受理経路（#242）が決まったら、WP-5の試験の受理部分を差し替え、D3の限定を外す。

Owner判断が出たとき:

- [ ] 該当するIssueの結論を読み、この文書の判断記録と一致するか確かめる。
- [ ] DRの既定案を実装から外す場合は、外した理由と新しい試験を同じPRに含める。
- [ ] 契約を変える場合は`openapi.json`の変更をOwnerのPRへ分離し、Spike側は追随する。

PRを出すとき:

- [ ] `gofmt`・`go vet`・対象試験・`conformance/doclinks`の結果をPR本文へ書く。
- [ ] 実行していない検証を「合格」と書かない。
- [ ] 実EKI試験とMock試験を区別して書く。
- [ ] Owner判断が必要な点を列挙し、決まったことと決まっていないことを分ける。

## 8. 症状からの切り分け

| 症状 | 考えられる原因 | 確かめる場所 |
|---|---|---|
| `422 INPUT_ARTIFACT_UNRESOLVABLE` | 入力の本文がない、または`artifact_id`が実測digestと違う | `GET /v1/artifacts/{id}`、`probe.json`、WP-3の照合 |
| `422 DEADLINE_IN_PAST` | 投入時点で期限が過ぎている | リクエストの`deadline`、時計 |
| `409 RESULT_NOT_READY`が続く | 実行されていない、または結果受理が未配線 | `attempt_count`、`dispatch`の設定、#242 |
| `409 RESULT_NOT_AVAILABLE` | 取消・失敗・EXHAUSTEDで終端した | `terminal_code`、取消の有無 |
| Agentが実行しない | trust anchor、ExecutorApproval、dispatch、mTLSの不一致 | `conformance/process`の既存試験、Agentのevents |
| Claimのdigestが合わない | 実行体・入力・環境の違い、キャッシュ | ローカルで同じコマンドを実行し、stdout+stderrを測る |
| 入力Artifactが取得できない | そのAttemptが宣言していない、または配備が本文を持たない | `ArtifactFetchAllowed`、`artifacts_dir` |

## 9. 非目標

- EKIのスケジューリング、Lease、Fencing、回復の再実装。
- 本番DBの更新、外部サービスへの実作用、実Providerの呼び出し。
- 新しい共通Runtime・Registry・Schedulerの導入。
- EKIの外部契約を、Spikeの都合で変えること。
- 実装できない機能を、Mockだけで成功として扱うこと。

## 10. 参照

- [README](README.md)、[RESULTS_2026-11-11](RESULTS_2026-11-11.md)、[Receiptの例](examples/receipts.json)
- EKI: [PR #827](https://github.com/hat47x/eki-fabric/pull/827)、[#112](https://github.com/hat47x/eki-fabric/issues/112)、[#242](https://github.com/hat47x/eki-fabric/issues/242)、[#245](https://github.com/hat47x/eki-fabric/issues/245)、[#111](https://github.com/hat47x/eki-fabric/issues/111)、[#312](https://github.com/hat47x/eki-fabric/issues/312)
- TEI: [#4194](https://github.com/hat47x/tei-platform/issues/4194)
- EKIの実装: `internal/control/clientapi/upload.go`、`internal/agent/service/assignments.go`、`internal/agent/assignment/fetcher.go`、`internal/agent/nativerunner/runner.go`
- EKIの試験: `conformance/process/http_submit_agent_test.go`、`conformance/process/dispatch_loop_test.go`、`conformance/process/execution_agent_test.go`
