# EKI Product Requirements

## 役割

この文書は、EKI Fabricが満たすべきProduct Requirementの初期正本です。現時点は上流要件の具体化段階であり、すべてのRequirementはArchitecture選定前に相互矛盾、検証可能性、実現可能性を継続レビューします。

状態表記:

- **Baseline**: 現時点で中心要件として採用
- **Needs research**: 方向は採用するが保証水準・方式の調査が必要
- **Open**: 要件自体の境界をさらに検討する

---

## A. Product boundary / interoperability

### RQ-001 — Application-agnostic compute fabric — Baseline

EKIはSEI Cognition、TEI、SUIその他の既知OSSだけでなく、未知のOSS、他社ソフトウェア、Webアプリ、将来のソフトウェアから利用できなければならない。

EKI Coreは利用アプリケーション固有の業務・認知意味論を必須知識にしない。

### RQ-002 — Language-neutral external contract — Baseline

基準実装言語にかかわらず、利用契約は特定プログラミング言語専用であってはならない。Go SDK等は利便層とし、相互運用可能な外部契約を別途持つ。

### RQ-003 — Heterogeneous worker support — Baseline

業務端末、GPUワークステーション、専用ノード、サーバ、ブラウザ、一時参加ノード、外部組織資源、クラウド等を同一種類と仮定せず、それぞれの能力・信頼保証・利用可能性の差を表現できなければならない。

### RQ-004 — Replaceable infrastructure dependencies — Baseline

Queue、Identity、Artifact Store、Executor Runtime等の個別技術は、Product Requirementによって本質的に必要な場合を除きEKIの外部契約と同一視しない。

---

## B. Resource use / human-first operation

### RQ-005 — Human-first endpoint governance — Baseline

業務端末・個人端末では利用者の本来作業をbackground computeより優先しなければならない。

### RQ-006 — Immediate local opt-out — Baseline

端末利用者またはProviderは、許可された範囲で資源提供を即時に縮小・停止できなければならない。中央Control Planeとの通信断があっても、ローカル側が停止要求を実施できることを要求する。

### RQ-007 — Effective capacity is bounded locally — Baseline

中央側が許可した上限より厳しい制約を、ローカルpolicy、利用者設定、thermal/power/network safety判断が適用できなければならない。中央側が安全上限を強制的に拡大できることを前提にしない。

### RQ-008 — Network-aware scheduling — Baseline

分散処理によって業務ネットワークを不必要に圧迫してはならない。入力・出力量、既存cache、network class、転送コスト、データ局所性を配置判断に反映できなければならない。

### RQ-009 — Resource bounded execution — Baseline

Task実行はCPU、GPU、Memory、Disk、Network、Wall time、出力量等についてpolicy上限を持てなければならない。

### RQ-010 — Opportunistic availability — Baseline

Endpointやbrowser等がsleep、shutdown、user return、network loss、opt-out等で随時離脱することを正常系として扱わなければならない。

---

## C. Workload semantics / reliability

### RQ-011 — Explicit workload reliability contract — Baseline

Taskは少なくともretryability、preemptibility、deadline、必要に応じcheckpointability / idempotency / determinism等の性質を表現できなければならない。

### RQ-012 — No universal exactly-once assumption — Baseline

EKI Coreは一般Taskに対してexactly-once実行を暗黙保証しない。重複実行の可能性と、その扱いを明示できなければならない。

### RQ-013 — Failures may be absorbed at fabric level — Baseline

個々の一時Workerを高可用機として扱わず、再配置、再実行、検証等によってFabric全体として必要な信頼性を得られること。

### RQ-014 — Do not distribute when distribution is worse — Baseline

ローカル実行の方が安全・低負荷・低コストである場合、EKIは分散配置を目的化しない。

---

## D. Malicious client resistance

### RQ-015 — Authenticated clients remain untrusted — Baseline

認証・所属・署名・公式SDK利用の有無にかかわらず、Clientからの入力を善意と仮定してはならない。

### RQ-016 — Client cannot grant itself authority — Baseline

Client入力だけによってExecutor登録、Worker選択、security classification低下、network authority拡張、予算拡張、他主体Artifact access等が成立してはならない。

### RQ-017 — Resource-exhaustion resistance — Baseline

悪意Clientによる大量Task、巨大入力、fan-out、retry amplification、出力肥大化等が、許可された予算を超えてFabric資源を消費できないこと。

### RQ-018 — Topology confidentiality — Baseline

通常のClient権限から、不要なWorker hostname、IP、利用者情報、cache内容、社内構成等を列挙できないこと。

---

## E. Malicious worker resistance

### RQ-019 — Worker result is a claim until verified — Baseline

Workerが返すResult、resource usage、実行時間、cache状態その他の自己申告は、要求された保証方式によって検証されるまで事実として扱わない。

### RQ-020 — Modified worker tolerance — Baseline

正規Worker Agentが改変され、WorkOrderを無視、偽結果を返却、処理を省略、性能や利用量を偽装しても、他主体のauthority拡張や未検証結果の正本化につながってはならない。

### RQ-021 — Result assurance is workload-specific — Baseline

結果完全性について、exact comparison、tolerance comparison、application validator、independent replication/quorum、trusted recomputation、attestation、verifiable computation等、Task特性に応じた検証方法を選べる余地を持たなければならない。

### RQ-022 — Sybil-aware assurance — Needs research

匿名・アドホックWorkerの多数決だけを高保証の根拠としてはならない。独立性をどのように定義・検証するか、保証レベルごとに具体化する。

### RQ-023 — Reputation is not root of trust — Baseline

過去の成功率や評判は効率向上に利用できるが、それだけを結果完全性または機密性の根拠にしてはならない。

### RQ-024 — Untrusted worker confidentiality boundary — Baseline

平文機密データを悪意Workerへ渡した後から機密性を回復できるとはみなさない。TaskのData ClassとWorker側の機密性保証能力を配置条件として扱えること。

### RQ-025 — Attestation / proof extensibility — Needs research

TEE remote attestation、verifiable computationその他、Workerを信用せずにより強い保証を得る技術を後付け可能にする。ただし特定方式を全Workerへ必須とはしない。

---

## F. Code / artifact / data integrity

### RQ-026 — Executable provenance — Baseline

業務endpoint等の保護対象Workerで実行するコードについて、許可されたExecutorと実際に実行されるartifactの同一性を検証可能にしなければならない。

### RQ-027 — Content integrity — Baseline

Task input、Executor、Result等の主要artifactは、転送経路や保存先に依存せず完全性を検証できる識別を持てなければならない。

### RQ-028 — Data authority is separate from task authority — Baseline

Taskを投入できることと、任意Artifactを読み書きできることを同一権限にしてはならない。

### RQ-029 — No arbitrary worker-side fetch by default — Baseline

Client指定の任意URL、filesystem path、network endpoint等をWorkerが直接取得することを標準動作にしてはならない。必要な外部accessは明示的authorityとして扱う。

---

## G. Enterprise / federation / ad-hoc participation

### RQ-030 — Enterprise-compatible default deployment — Baseline

一般的な社内システムで説明・監査しやすい通信、認証、認可、ログ、端末制御を標準利用形態として成立させる。特殊なP2P通信やFW開放を必須にしない。

### RQ-031 — Trust-domain federation — Needs research

複数の管理主体が自身のidentity authorityを保ちながら限定的に協力できること。Federationしていること自体が相手主体への包括的trustを意味してはならない。

### RQ-032 — Ad-hoc benevolent participation — Baseline

善意の個人や一時的なBrowser/Native Workerが、強い事前審査なしに公開・低機密かつ検証可能なTaskへ貢献できる構造を許容する。

### RQ-033 — Participation without internal network membership — Baseline

外部Workerを受け入れるために、ProviderまたはBeneficiaryの内部ネットワークへ一般参加者を加入させることを必須にしない。

---

## H. Public-interest resource sharing / contribution

### RQ-034 — Compute Grant — Baseline

ProviderがResource Class、上限、期間、対象、優先度、Data Class、preemption条件等を制約した計算資源利用枠を他主体へ提供できるモデルを持つ。

### RQ-035 — Provider revocation and reclaim — Baseline

ProviderはGrantによる将来の資源提供を停止・縮小できなければならない。ただし既に正当に生成されたBeneficiaryのResult所有権等を自動的に遡及変更するものではない。

### RQ-036 — Contribution accounting — Baseline

資源提供について、必要に応じて検証可能なContribution Recordを生成できること。少なくとも提供主体、Resource Class、量、対象Work、assurance/provenanceを表現できる余地を持つ。

### RQ-037 — External value mapping — Baseline

Contribution Recordを、社内配賦、相互利用Credit、寄付実績、コミュニティ評価その他の外部制度へ接続できること。

### RQ-038 — No native currency or cash market — Baseline

EKI Coreは通貨発行、現金化、法定通貨との交換、市場価格形成、投機市場を提供しない。価値の意味と交換条件は外部主体へ委ねる。

---

## I. Accountability / observability

### RQ-039 — End-to-end provenance — Baseline

TaskRequest、policy/admission判断、配置、実行attempt、ResultClaim、verification、AcceptedResult、Contributionの主要関係を必要な範囲で追跡できること。

### RQ-040 — Audit without unnecessary disclosure — Baseline

監査可能性を得るために、不要な機密データ、Client/Worker内部情報、他主体のトポロジを広く開示することを前提にしない。

### RQ-041 — Security events are observable — Baseline

rejected request、quota breach、invalid result、verification failure、replay、revocation、opt-out等を運用側が観測できること。

---

## J. Non-goals / explicit exclusions

現段階でEKI Coreの主目的にしないもの:

- 分散DBまたはsource-of-truth storage
- 汎用共有メモリ
- 低遅延MPI/HPC interconnectの置換
- Kubernetes / Nomad等の汎用cluster orchestrator全面置換
- VM/IaaS管理基盤
- 汎用VPNまたはP2P networking product
- あらゆるTaskの機密性・完全性を無条件に保証すること
- Workerの善意や未改変性を保証すること
- Cryptocurrency、token market、現金決済基盤

## 未決事項

特に次はArchitectureへ入る前に追加調査が必要です。

1. Worker assurance levelを単一階層でなく多軸capabilityとしてどこまで標準化するか。
2. Sybil耐性のあるreplication independenceをどの主体・credential・attestation単位で定義するか。
3. GPUを含むverifiable computationの現実的適用範囲。
4. Confidential Computingを任意extensionとしてどの粒度までTask contractへ露出するか。
5. Resource Class / Contribution quantityをベンダー中立に比較するための最低限の尺度。
6. Browser Workerで保証可能なresource limit、background execution、identityの境界。
7. Enterprise deploymentとpublic/ad-hoc participationを同じProtocolで扱う範囲と分離点。
