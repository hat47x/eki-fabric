# EKI Fabric Product Requirements

## この文書の役割

この文書は、EKI Fabricが満たすべきProduct Requirementの正本です。

現在はArchitectureを選ぶ前の上流工程にあります。ここでは「何を満たす必要があるか」を定め、NATS、WASM、SPIFFE、特定のSandbox製品など「何を使って実現するか」は、Requirement上どうしても必要な場合を除いて固定しません。

初めて読む場合は、先に [要求文書の読み方](README.md) と [はじめてEKI Fabricを読む方へ](../docs/for-readers.md) を参照してください。

状態は次の意味で使います。

- **Baseline** — 現時点で中心要件として採用する
- **Needs research** — 要求する方向は採用するが、保証水準や実現可能性の調査が必要
- **Open** — 要件そのものの境界をさらに検討する

---

## A. プロダクト境界と相互運用性

### RQ-001 — Application-agnostic compute fabric — Baseline

EKIは、SEI Cognition、TEI、SUIなど現在想定しているOSSだけでなく、未知のOSS、他社ソフトウェア、Webアプリ、将来登場するソフトウェアからも利用できなければならない。

EKI Coreは、利用アプリケーション固有の業務概念や認知上の意味を必須知識にしない。特定アプリケーションの意味をCoreへ取り込まなければ接続できない構造は避ける。

### RQ-002 — Language-neutral external contract — Baseline

基準実装に使う言語にかかわらず、EKIの外部利用契約は特定のプログラミング言語専用であってはならない。

Go SDKなどを用意する場合も、それは利用を便利にするための層とし、Go以外から利用可能な相互運用契約を別に持つ。

### RQ-003 — Heterogeneous worker support — Baseline

業務端末、GPUワークステーション、専用ノード、サーバ、ブラウザ、一時参加ノード、外部組織の資源、クラウドなどを、同じ性質のWorkerであると仮定してはならない。

性能だけでなく、利用可能な時間、管理主体、機密性の保証、結果検証能力、離脱しやすさなどの違いを表現できる必要がある。

### RQ-004 — Replaceable infrastructure dependencies — Baseline

Queue、Identity、Artifact Store、Executor Runtimeなどの個別技術を、Product Requirementによって本質的に必要な場合を除き、EKIの外部契約そのものと同一視してはならない。

既存OSSで十分な部分は積極的に再利用する一方、その製品を交換するとEKI利用側の意味まで変わる構造は避ける。

---

## B. 資源利用とhuman-first運用

### RQ-005 — Human-first endpoint governance — Baseline

業務端末や個人端末では、端末利用者の本来の作業をbackground computeより優先しなければならない。

EKIの目的は遊休資源を最大限使い切ることではない。利用者の作業を妨げずに残った余力だけを利用する。

### RQ-006 — Immediate local opt-out — Baseline

端末利用者またはProviderは、許可された範囲で資源提供を即時に縮小・停止できなければならない。

中央Control Planeとの通信が不安定または切断されている場合でも、ローカル側だけで新規Taskの受付停止や資源提供の縮小を実施できる必要がある。

### RQ-007 — Effective capacity is bounded locally — Baseline

中央側が許可した上限より厳しい制約を、ローカルPolicy、利用者設定、温度、電力、ネットワークなどの安全判断が適用できなければならない。

中央Control Planeが「60%まで利用可能」と判断していても、端末側が「現在は0%」と判断した場合、そのローカル上限を中央側が強制的に拡大できることを前提にしない。

### RQ-008 — Network-aware scheduling — Baseline

分散処理によって、業務ネットワークを不必要に圧迫してはならない。

入力・出力量、既存cache、network class、転送コスト、データ局所性などを配置判断へ反映できなければならない。高速なGPUが別端末にあっても、大量データ転送によって全体として不利になる場合は、データに近い端末を選べる必要がある。

### RQ-009 — Resource-bounded execution — Baseline

Task実行は、CPU、GPU、Memory、Disk、Network、Wall time、出力量などについてPolicy上限を持てなければならない。

上限は実行後の請求や監査だけでなく、単一TaskがFabric全体へ無制限な負荷を広げないための境界として機能する必要がある。

### RQ-010 — Opportunistic availability — Baseline

EndpointやBrowserなどが、sleep、shutdown、利用者の復帰、network loss、opt-out、Browser suspensionなどによって随時離脱することを正常系として扱わなければならない。

Worker profileごとに期待できるlifecycleを区別し、Browserのbackground executionのように保証されない性質を、厳しいdeadlineや可用性保証の根拠にしてはならない。

---

## C. Taskの意味と実行信頼性

### RQ-011 — Explicit workload reliability contract — Baseline

Taskは少なくとも、再実行してよいか、途中で停止してよいか、期限があるかを表現できなければならない。必要に応じてcheckpointability、idempotency、determinismなども表現できる余地を持つ。

EKIがTaskの性質を推測し、再実行してよい処理と外部副作用を持つ処理を同じように扱うことは避ける。

### RQ-012 — No universal exactly-once assumption — Baseline

EKI Coreは、一般Taskについてexactly-once実行を暗黙に保証しない。

Workerの離脱や通信断がある分散環境では、実行済みかどうかを即座に確定できない場合がある。重複実行の可能性と、その扱いをTask契約として明示できなければならない。

### RQ-013 — Failures may be absorbed at fabric level — Baseline

個々の一時Workerを高可用機として扱うことを前提にしない。

再配置、再実行、checkpoint、結果検証などを組み合わせ、個々のWorkerが途中で消えてもFabric全体として必要な信頼性を得られることを目指す。

### RQ-014 — Do not distribute when distribution is worse — Baseline

ローカル実行の方が安全、低負荷、低コスト、または速い場合、EKIは分散配置そのものを目的にしてはならない。

「Fabricへ参加しているから他端末へ送る」のではなく、分散することで本当に利益が得られる場合だけ分散する。

---

## D. 悪意あるClientへの耐性

### RQ-015 — Authenticated clients remain untrusted — Baseline

認証、所属、署名、公式SDK利用の有無にかかわらず、Clientから受け取る入力を善意と仮定してはならない。

正規Credentialを持つClientも、侵害・改変されている可能性がある。

### RQ-016 — Client cannot grant itself authority — Baseline

ClientがRequestへ値を書くだけで、Executor登録、特定Workerの直接指定、security classificationの低下、network authorityの拡張、予算拡張、他主体Artifactへのaccessなどが成立してはならない。

Clientが安全側へ条件を厳しくすることと、自分で権限を広げることは分けて扱う。

### RQ-017 — Resource-exhaustion resistance — Baseline

悪意あるClientによる大量Task、巨大入力、fan-out、retry amplification、出力肥大化などが、許可された予算を超えてFabric資源を消費できないこと。

大量Requestをいったん永続Queueへ受理してから後段で抑制するだけではなく、受理前後の適切な境界で最大影響を制限できる必要がある。

### RQ-018 — Topology confidentiality — Baseline

通常のClient権限から、不要なWorker hostname、IP address、端末利用者情報、cache内容、社内構成などを列挙できてはならない。

EKIのTask APIが、内部ネットワークや参加端末を探索するためのAPIにならないことを要求する。

---

## E. 悪意あるWorkerへの耐性

### RQ-019 — Worker result is a claim until verified — Baseline

Workerが返すResult、resource usage、実行時間、cache状態、capabilityその他の自己申告は、必要な保証方式によって確認されるまで事実として扱わない。

「Workerが署名した」ことは、誰がそのClaimを出したかを確認する材料にはなるが、Claimの内容が正しいことまで自動的には証明しない。

### RQ-020 — Modified worker tolerance — Baseline

正規Worker Agentが改変され、WorkOrderを無視する、計算を省略する、偽Resultを返す、性能や利用量を偽装するといった場合でも、他主体の権限拡張や未検証Resultの確定につながってはならない。

Worker Agent自身をSecurity Root of Trustにしないことを要求する。

### RQ-021 — Result assurance is workload-specific — Baseline

結果の完全性について、Taskの性質に応じた検証方法を選べる余地を持たなければならない。

候補には、完全一致、許容誤差を含む数値比較、application-specific validator、独立した複製実行、管理下Workerでの再計算、attestation、verifiable computationなどがある。どれか一つをすべてのTaskへ強制しない。

### RQ-022 — Sybil-aware assurance — Needs research

匿名またはアドホックWorkerの単純多数決だけを、高い保証の根拠としてはならない。

一人の攻撃者が多数のWorker Identityを作れる場合、Worker数は独立した確認者の数を意味しない。独立性をどのように表現・確認するかを、保証水準ごとに具体化する。

### RQ-023 — Reputation is not root of trust — Baseline

過去の成功率や評判は、複製回数を減らす、割当効率を上げるなどの最適化に利用できるが、それだけを結果完全性や機密性の根拠にしてはならない。

長期間正しく動いたWorkerが、後から侵害・改変される可能性を排除しない。

### RQ-024 — Untrusted worker confidentiality boundary — Baseline

平文の機密データを悪意あるWorkerへ渡した後から、機密性を回復できるとはみなさない。

TaskのData Classと、Worker側で実際に成立する機密性保証を配置条件として扱えること。通信路が暗号化されていることだけで、実行中のデータがWorker所有者から秘匿されるとはみなさない。

### RQ-025 — Attestation / proof extensibility — Needs research

TEE remote attestation、verifiable computationなど、Workerを信用せずにより強い保証を得る技術を後から接続できる余地を持つ。

ただし特定方式を全Workerへ必須とはしない。また、Attestationが成立したことと、Application上のResultが意味的に正しいことを同一視しない。

---

## F. Code、Artifact、Dataの完全性

### RQ-026 — Executable provenance — Baseline

業務Endpointなど保護対象Workerで実行するコードについて、許可されたExecutorと実際に実行されるArtifactの同一性を確認できなければならない。

人間が読む名前や可変tagだけを、実行Artifactの完全なIdentityとして扱わない。

### RQ-027 — Content integrity — Baseline

Task input、Executor、Resultなどの主要Artifactは、転送経路や保存先だけに依存せず内容の完全性を確認できる識別を持てなければならない。

Artifact Storeを信用できることと、受け取った内容が期待したArtifactであることを分けて扱う。

### RQ-028 — Data authority is separate from task authority — Baseline

Taskを投入できる権限と、任意のArtifactを読み書きできる権限を同一にしてはならない。

「計算を依頼できる」ことを、他主体のデータへアクセスする迂回路にしない。

### RQ-029 — No arbitrary worker-side fetch by default — Baseline

Clientが指定した任意URL、filesystem path、network endpointなどを、Workerがそのまま取得することを標準動作にしてはならない。

外部accessが必要なTaskでは、対象と権限を明示し、Clientが文字列を指定しただけでWorkerのnetwork/filesystem authorityが広がらないようにする。

---

## G. 企業利用、Federation、アドホック参加

### RQ-030 — Enterprise-compatible default deployment — Baseline

一般的な社内システムとして説明・監査しやすい通信、認証、認可、ログ、端末制御を標準的な利用形態として成立させる。

特殊なP2P通信、任意端末間の直接通信、Firewallの広い開放などを必須条件にしない。

### RQ-031 — Trust-domain federation — Needs research

複数の管理主体が、それぞれのIdentity authorityを保ちながら限定的に協力できなければならない。

Federation関係を結んだこと自体が、相手Trust Domainの全Identity、全Worker、全Dataへの包括的な信頼や権限を意味してはならない。

### RQ-032 — Ad-hoc benevolent participation — Baseline

個人や一時的なBrowser/Native Workerが、強い事前審査なしでも、公開または低機密で、必要な方法により結果を検証できるTaskへ貢献できる構造を許容する。

善意の参加者を受け入れるために、参加者の善意そのものをSecurity Requirementにはしない。

### RQ-033 — Participation without internal network membership — Baseline

外部Workerを受け入れるために、ProviderまたはBeneficiaryの内部ネットワークへ一般参加者を加入させることを必須にしない。

外部の計算資源を使うことと、内部ネットワークの一員として扱うことを分離する。

### RQ-042 — Visible and controllable ad-hoc participation — Baseline

Browserや個人端末によるアドホックな資源提供では、資源提供が行われていることを利用者が認識でき、分かりやすい方法で停止できなければならない。

通常のWeb閲覧に紛れて意図しない計算資源提供を行うことや、hidden iframeなどを使って利用者から資源提供を隠すことを、EKIの標準的な参加方法にしてはならない。

このRequirementは、組織管理端末のEnrollment方式を一律に規定するものではない。業務端末での停止権についてはRQ-006を適用する。

---

## H. 公益的な資源共有とContribution

### RQ-034 — Compute Grant — Baseline

ProviderがResource Class、上限、期間、対象、優先度、Data Class、preemption条件などを制約した計算資源利用枠を、他主体へ提供できるモデルを持つ。

Compute Grantは、内部ネットワークへの参加権や包括的なData accessを意味しない。

### RQ-035 — Provider revocation and reclaim — Baseline

Providerは、Grantによる将来の資源提供を停止・縮小できなければならない。

ただし、資源提供を停止したことによって、すでに正当に生成されたBeneficiaryのResultや成果物に対する権利を自動的に遡及変更するものではない。

### RQ-036 — Contribution accounting — Baseline

資源提供について、必要に応じて検証可能なContribution Recordを生成できること。

少なくとも提供主体、Resource Class、量、対象Work、測定方法、assurance、provenanceを表現できる余地を持つ。Workerの自己申告だけから、価値あるContributionを確定しない。

### RQ-037 — External value mapping — Baseline

Contribution Recordを、社内配賦、相互利用Credit、寄付実績、コミュニティ評価などの外部制度へ接続できること。

Contribution Recordは「何が観測・検証されたか」を表すものであり、それ自体が金銭価値や社会的価値を保証するものではない。どのContributionへ価値を与えるかは外部Policyへ委ねる。

### RQ-038 — No native currency or cash market — Baseline

EKI Coreは、通貨発行、現金化、法定通貨との交換、市場価格形成、投機市場を提供しない。

EKIが扱うのは計算資源提供を記述・検証するための基盤までとし、価値の意味や交換条件は各主体へ委ねる。

---

## I. Accountability、監査、Observability

### RQ-039 — End-to-end provenance — Baseline

TaskRequest、Policy / Admission判断、配置、実行Attempt、Result Claim、Verification、Accepted Result、Contributionの主要な関係を、必要な範囲で追跡できなければならない。

後から「どのWorkerが返したか」だけでなく、「どのExecutor、どの入力、どの検証方法、どの判断を経て受理されたか」を確認できることを目指す。

### RQ-040 — Audit without unnecessary disclosure — Baseline

監査可能性やAssuranceを得るために、不要な機密データ、Client / Worker内部情報、他主体のTopology、Raw Attestation Evidenceなどを広く開示することを前提にしない。

利用側が必要なのが「必要条件を満たした」という判断結果だけである場合、生のEvidenceすべてをClientへ渡す必要はない。

### RQ-041 — Security events are observable — Baseline

rejected request、quota breach、invalid result、verification failure、replay、revocation、opt-outなど、Securityや運用判断に必要な事象を観測できなければならない。

ただし、観測のために新しい情報漏えい面を作らないことはRQ-040に従う。

---

## J. 追加の横断的な安全境界

### RQ-043 — No post-hoc deletion guarantee on untrusted workers — Baseline

通常のuntrusted Workerについて、「Task終了時に入力を削除した」というWorker自身の申告だけを、機密性の保証にしてはならない。

一度平文を渡した相手がコピーを保持している可能性を後から消すことはできない。削除保証が必要なTaskでは、管理下の信頼境界、Confidential Computing、暗号学的に利用不能化できる方式など、別の根拠を必要とする。

### RQ-044 — Verification authority is explicit and bounded — Baseline

ResultをAccepted Resultへ昇格させる判断について、どのValidator、Verifier、Policy authority、Proof verifierなどを根拠にしたかを追跡できなければならない。

Workerが自分で選んだValidatorだけを使って自分のResultを正当化できる構造にしてはならない。

すべてのVerifierを相互に疑う完全なByzantine構成を一律に要求するものではない。どこを信頼基盤に置いたかを明示し、その権限範囲を限定できることを要求する。

### RQ-045 — Honest assurance for semantically open results — Baseline

生成AIの文章や候補生成など、唯一の正解を安価かつ機械的に判定できないTaskについて、EKIが完全なsemantic correctnessを保証したかのように扱ってはならない。

確認できるprovenance、使用したExecutor / Model、複数候補、独立実行、application-specific validationなどを記録できるようにし、最終判断を利用アプリケーションや人間へ委ねるTaskを表現できること。

### RQ-046 — External side effects require explicit retry semantics — Baseline

外部API呼び出し、DB更新、通知送信などの副作用を持つTaskについて、その事実と再実行条件を明示できなければならない。

idempotencyなどの安全根拠がないTaskを、Worker離脱を理由にEKIが自動的に「何度でも再実行してよいTask」と判断してはならない。実行済みか不明な状態を表現し、必要に応じてRecovery判断へ渡せる余地を持つ。

### RQ-047 — Security-relevant capability claims require a trust basis — Baseline

Workerが自己申告した性能値やcapabilityだけを、高保証TaskのSecurity placement条件を満たした根拠にしてはならない。

「GPUがある」という性能上のClaimと、「Confidential Runtimeが成立している」「要求された隔離がある」といったSecurity上のClaimを分ける。後者を利用する場合は、管理Policy、Attestation、外部Evidenceなど、要求する保証水準に見合う根拠を持てること。

### RQ-048 — Freshness and revocation of time-bounded authority — Baseline

期限、失効、revocation、freshnessを持つCredential、Lease、Grant、Assurance Evidence、内部実行権限などについて、古い情報のreplayによって失効済みの権限を復活させてはならない。

具体的なnonce、counter、timestamp、clock sourceなどの方式はArchitectureで決定する。

---

## K. 明示的な非目標

現段階で、EKI Coreの主目的にしないものを次に示す。

- 分散DBまたはsource-of-truth storage
- 汎用共有メモリ
- 低遅延MPI / HPC interconnectの置き換え
- Kubernetes / Nomadなど汎用cluster orchestratorの全面的な置き換え
- VM / IaaS管理基盤
- 汎用VPNまたはP2P networking product
- あらゆるTaskの機密性・完全性を無条件に保証すること
- Workerの善意や未改変性そのものを保証すること
- Cryptocurrency、token market、現金決済基盤
- 一時Workerに渡した平文データの完全な事後回収を保証すること
- 正解を定義できないTaskについて、EKIが意味上の絶対的な正しさを保証すること

## Architectureへ入る前の主な未決事項

次の論点は、引き続き追加調査またはAcceptance Scenarioによる圧力試験が必要です。

1. Worker Assuranceを多軸capabilityとして、外部契約へどの粒度まで表現するか。
2. Sybil耐性のあるreplication independenceを、principal、credential、attestation、operatorなどのどの単位で定義するか。
3. GPUを含むverifiable computationが、どの種類のTaskで実用的になるか。
4. Confidential Computingを任意extensionとして、Task contractへどこまで露出するか。
5. Resource ClassとContribution quantityを、ベンダー中立に比較するための最低限の尺度。
6. Browser Workerで実効的に制御可能なresource limit、identity、利用者への表示方法。
7. Enterprise deploymentとpublic / ad-hoc participationを、同じProtocolで扱う範囲と分離点。
8. Validator / Verifier compromiseに対し、保証Profileごとにどこまで独立性を要求するか。
9. 非決定的AI Taskについて、EKI共通の検証語彙をどこまで持つべきか。
10. 副作用TaskをEKIの主要用途へ含めるか、限定的な拡張用途として扱うか。
