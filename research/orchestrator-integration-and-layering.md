# 既存オーケストレータとの接続、およびSEI・TEIとの層構造

## 役割

EKI Fabricを「管理されたクラスタと遊休資源を一つの基盤で扱う分散基盤」と位置づける場合に、次の三点を整理する調査メモです。

1. どの既存オーケストレータを、どの構造で、どの順に接続するか
2. 下位のオーケストレータとEKIの間で、既存のTask / Attempt / Lease状態モデルや権限分離がどこで衝突するか
3. SEI・TEIをどの層に置くか

Requirement・Architectureの正本ではありません。[AI時代の価値と位置づけ](ai-era-value-positioning.md)の続きにあたります。製品の採用状況や既定値は、2026年10月時点の検索結果と一般的な知識に基づく見立てです。採用判断の前に、一次情報と利用組織の実環境で確認してください。

## 1. 先に結論

1. 「どれか一つの推奨オーケストレータを選ぶ」のではなく、**資源提供者（Resource Provider）をつなぐAdapterの契約を定め、性質の異なる系統を参照実装として用意する**ことを推奨します。
2. 最初の接続先は **Kubernetes（Kueueによるキューと割当て）**、次に **Slurm**、その次に **Nomad** を推奨します。遊休端末はEKI自身のWorkerで扱い、先行事例の **HTCondor** から設計を学びます。
3. 構造は、EKIが既存オーケストレータを資源の供給元として取り込む **「上に立つ仲介」** を中心にします。既存オーケストレータの中へEKIの遊休資源を偽装ノードとして入れる方式は、EKIの安全性の前提を壊すため、中心にしません（§3）。
4. 接続の最大の難所は、**下位オーケストレータが既定で持つ自動再試行・先取り・成功判定が、EKIのAttempt・Lease・Result検証と二重化すること**です。Adapter契約で、これらを無効化するか宣言させる必要があります（§5）。
5. TEIは、EKIを呼び出して操作する側ではなく、**TEIの実行基盤（Execution Layer / Target）の一つとしてEKI上で稼働する**関係とします。依存はTEI → EKIの一方向で、EKIの中核と運用手段はTEIに依存させません（§7）。
6. SEIは、EKIの利用者（Client）であり、統治の証拠を参照する側です。SEIが企業統治の方針をEKIへ反映する場合は、利用者の経路ではなく、**管理者権限の経路（Administrative Plane）**を通す必要があります（§8）。

## 2. 「上位存在」の意味を限定する

上位に立つと言っても、次の二つは別の話です。

| 意味 | 内容 | 推奨 |
|---|---|---|
| 置換する上位 | 下位のオーケストレータの機能（gang scheduling、公平分配、Pod管理等）を自前で再実装する | しない |
| 契約を共通化する上位 | Task、制約、検証、監査の契約を共通にし、実行は下位へ委ねる | する |

置換する上位を目指すと、最小公倍数の機能しか使えなくなるか、各製品の機能を全部作り直すことになります。成熟した製品との正面からの競合にもなります。

そこで、EKIが所有するのは、既存のEKI設計がすでに定めている範囲に限ります。Task Admission（ADM-2）、Placement（SCH-1、PLC-1）、Lease（LSE-1）、Result受理（RVR-1、RAC-1）、予算と回復の規則です（[Authority Boundaries](../architecture/AUTHORITY_BOUNDARIES.md)）。下位固有の機能は、**能力として宣言させ、そのまま通す**方式を基本とします（[Extension SPI](../architecture/EXTENSION_SPI.md)のCapability Negotiation）。

複数対応が必要という見立てには同意します。一つの下位製品にしか対応しない「上位」は、その製品の拡張機能と区別がつかず、利用者を固定しないという価値（[Product Value](../product/value.md)の「Interoperability before ecosystem lock-in」）とも合いません。少なくとも二系統で契約が成立することを確かめて、初めて「上位の契約」と呼べます。

## 3. 接続の構造：三つの選択肢

### 選択肢A：上に立つ仲介（推奨の中心）

```text
Client ──▶ EKI ──▶ Adapter ──▶ Kubernetes / Slurm / Nomad
                └──▶ EKI Worker（業務端末、Browser等）
```

EKIが配置を決め、下位のクラスタを「内部にノードを持つ資源提供者」として扱います。EKIのAttempt・Lease・Result検証が、下位の実行にもそのまま及びます。

### 選択肢B：既存オーケストレータの中へ遊休資源を入れる

```text
Client ──▶ Kubernetes ──▶ 仮想ノード（実体はEKIの遊休端末群）
```

Kubernetesの仮想ノード機構（virtual kubelet等）を使えば、遊休端末を「Kubernetesのノード」に見せることは技術的には可能です。導入側にとっては、既存のKubernetesの使い方のまま遊休資源が増えるので魅力があります。

しかし、中心の方式にはしません。理由は次のとおりです。

- Kubernetesは、Podが「成功」で終われば、その結果を正しいものとして扱う。EKIの「Resultは検証されるまでClaim」（RQ-019）が効かなくなる。
- Kubernetesのスケジューラは、利用者の復帰による即時停止（RQ-006）を、通常のノード障害と区別しない。結果として再試行の嵐（RQ-052）を招きやすい。
- 悪意あるWorkerを前提にしたノードを、クラスタ内の通常ノードと同じ扱いにすると、クラスタ側の権限モデル（Secretの配布、Service Accountの付与等）がそのまま及ぶおそれがある。

選択肢Bは、「Result検証が不要で、機密を含まない、再実行してよい作業だけを流す出口」として限定するなら、将来の拡張候補になり得ます。その場合も、EKI側のResult受理を迂回しないことが条件です。

### 選択肢C：上にも下にもつながる（Aの将来拡張）

```text
Kueue / SkyPilot / Temporal ──▶ EKI ──▶ Kubernetes / Slurm / Nomad / EKI Worker
       （EKIを資源提供者として使う）
```

既存の上位製品（Kueue、SkyPilot、Temporal等）から見て、EKIが「統治された資源の提供者」として見える形です。EKIは既存の上位製品と競合するのではなく、**遊休資源と異種資源を安全に束ねた供給元**として、その裏側に入れます。

これは、EKIにとっては外部Client Planeの一利用者が増えるだけであり、Kueue等をClientとして扱う限り、既存の契約（RQ-015〜017）で扱えます。ただし、上位製品の側がEKIの「UNKNOWN」や「検証中」を理解できない場合、情報が欠落します。この欠落をどう扱うかは未決です。

**推奨**：Aを中心に設計し、Cを外部Client Planeの拡張として後から足します。Bは中心にせず、条件付きの拡張候補に留めます。

## 4. 候補の系統と推奨順

| 系統 | 代表例 | EKIとの関係 | 推奨 |
|---|---|---|---|
| コンテナ・クラウドネイティブ | Kubernetes。バッチ向けにKueue、Volcano、YuniKorn | 企業内で最も接続需要が高いと見込まれる。GPU・AI用途が多い。Kueueは複数チームの割当てと公平分配に強く、Volcanoは同時起動が必要な分散バッチ（gang scheduling）に強いとされる | 最優先で接続。Kueueを前提にする |
| マルチクラスタ | Kueue MultiKueue、Karmada、Open Cluster Management | 複数のKubernetesクラスタを束ねる。Karmadaは2026年9月にCNCFのgraduated projectになったと報じられている | 個別接続はせず、Kubernetes接続の先として扱う |
| HPC・バッチ | Slurm | 研究機関・大規模AI学習の現場で多い。gang schedulingや公平分配が成熟している | 第二に接続 |
| 軽量な混在基盤 | Nomad | Kubernetes以外のworkload（コンテナ、VM、バイナリ）を同じ基盤で扱える。EKIの調査（[Reference Systems](reference-systems.md)）でもControl Planeとexecution nodeの分離で参考にしている | 第三に接続 |
| 遊休・機会的資源 | HTCondor、BOINC | HTCondorは、キーボードやマウスが止まった端末だけを使い、使い始めたら中断して別の端末へ移すcycle scavengingの先行事例。資源と依頼の双方が条件を出して照合するClassAdを持つ | 設計の参照元。大学等の既存HTCondor資源を公益目的のCompute Grantで使う場合は、将来の接続先にもなり得る |
| マルチクラウドのAI資源仲介 | SkyPilot | Kubernetes、Slurm、20以上のクラウドを一つのインタフェースで扱う。2026年にはLLM生成コードを実行するSandbox機能も告知されている | 目的が一部重なる。選択肢Cで、EKIを供給元として裏側に入れる関係を検討する |
| Workflow / durable execution | Temporal、Argo Workflows、Airflow等 | 長い業務フローの状態管理。EKIのTaskは、その一ステップとして呼ばれる関係になりやすい | 置換せず、上位の呼び出し元として共存 |
| 分散実行Runtime | Ray、Dask | 資源提供者というより、Task内部で使われるRuntime | Executorとして載せる対象。EKIがRayの代わりにならない |

推奨順の根拠は次のとおりです。

- 企業での導入実績と、AI・GPU用途での使われ方から、Kubernetesの接続需要が最大と見込まれる。
- Slurmは、Kubernetesと意味が異なる（ジョブの同時確保、時間枠での割当て、公平分配の考え方）。二系統目として「契約が一般化できるか」の検証に適する。
- Nomadは、軽量で運用しやすく、EKIの小規模構成（DP-1）に近い。

この順は推測であり、実際の利用組織の環境を調べて入れ替える余地があります。

### 遊休端末をHTCondorに任せない理由

HTCondorは遊休端末の利用で長い実績があります。EKIの遊休資源層をHTCondorで代替する案も考えられますが、中心にはしません。

- HTCondorは、管理者が運用する端末群を前提にしており、改変されたWorkerが偽の結果を返すことや、Sybil（一人が多数のWorkerを装う問題）を主な前提にしていない。
- Browser Worker、組織間のFederation、資源提供の記録（Contribution）など、EKIの要求の一部は扱う範囲の外にある。

一方で、ClassAdのような双方向の条件照合と、端末利用の検知と中断の経験は、Local GovernorとPlacementの設計で参考にします。

## 5. 二層スケジューリングで生じる衝突

選択肢Aでは、EKIが「どの資源提供者へ置くか」を決め、下位のオーケストレータが「その中のどのノードで動かすか」を決めます。この二層構造は、EKIの既存の状態モデル（[Task State Model](../architecture/TASK_STATE_MODEL.md)）と、次の点で衝突します。

### 5.1 下位の自動再試行が、見えない再実行になる

Kubernetes Job、Slurm、Nomadは、いずれもノード障害や異常終了の際に自動で再試行・再配置する仕組みを持ち、既定で有効な場合があります（Kubernetes Jobの`backoffLimit`、Slurmのrequeue、Nomadの`restart`・`reschedule`等。既定値は版によって異なるため、採用時に確認する）。

EKIでは、一つのAttemptは一度の実行試行です（Task State Model §2.1）。下位が内部で3回再試行すると、EKIから見て一つのAttemptの中で3回実行されたことになります。外部への副作用を持つTaskでは、RQ-046（副作用を持つTaskの再実行条件を明示する）に反します。

**Adapter契約で求めること**

- 副作用を持つTask、またはidempotencyの根拠がないTaskでは、下位の自動再試行を無効にする。
- 無効にできない下位では、そのTaskを置かない。
- 下位が内部で再試行したことを観測できる場合は、それを記録する。

### 5.2 下位の先取り（preemption）は、EKIからはreclaimに見える

KueueやSlurmは、優先度の高いジョブのために実行中のジョブを止めることがあります。EKIから見ると、これは業務端末で利用者が戻ったときの停止と同じ種類の出来事（reclaim）です。

- 停止を「失敗」と扱わず、Lease Authority（LSE-1）がLeaseを失効させる。
- 副作用の有無が分からなければUNKNOWNとして保持する（Task State Model §2.5）。
- 多数のジョブが同時に先取りされた場合に、EKIが一斉に別の提供者へ再配置して再試行の嵐を起こさない（RQ-052）。

### 5.3 下位の「成功」はClaimである

Kubernetes Jobの`Succeeded`、Slurmの`COMPLETED`は、資源提供者の申告（Claim）です。業務端末のWorkerの申告と同じく、EKIのResult受理（RVR-1、RAC-1）を通るまでAccepted Resultにはしません（RQ-019）。管理されたクラスタであっても、侵害や設定誤りは起こり得るからです。

その一方で、保証の根拠の強さは提供者ごとに違います。管理クラスタでは、Executorのdigest固定（RQ-026）をクラスタ側のadmission（署名検証等）でも強制できる場合があり、Assurance評価の材料になります。

### 5.4 下位の状態問い合わせはObservationである

Adapterが下位のAPIを定期的に問い合わせて得た状態は、Worker heartbeatと同じくObservationです（Task State Model §2.3）。問い合わせが途絶えたことは、実行が止まったことを意味しません。

### 5.5 予算と割当ての二重管理

EKIの予算（RQ-017）と、Kueueのquota、Slurmのfairshareは、別々に存在します。

- EKI経由の依頼は、両方の制約を満たして初めて実行される。
- EKIは、EKIを通らずにクラスタへ直接投入された作業を統治しない。EKIが統治するのは自分を通る依頼だけであり、クラスタ全体の管理者ではない。
- 一方の制約で拒否された場合に、他方の予算を消費したままにしない。

### 5.6 データ局所性が見えにくい

下位がノード選択を隠すため、EKIは「どの提供者に、どのデータの写しがあるか」の単位でしか局所性を判断できません。提供者内部のキャッシュの位置まで最適化しようとすると、下位のスケジューラと二重に判断することになります。

EKIは提供者単位の局所性（RQ-008、PAS-03）に留め、提供者内部の最適化は下位へ委ねるのが妥当です。

### 5.7 内部構成を利用者へ漏らさない

下位のエラーメッセージやログには、ノード名、IPアドレス、名前空間などが含まれます。これをそのまま利用者へ返すと、RQ-018（Client権限から内部構成を列挙できない）に反します。Adapterは、下位の情報を利用者向けとAudit向けに分けて扱う必要があります。

### 5.8 Adapterの資格情報が高価値な攻撃対象になる

AdapterはKubernetesやSlurmへジョブを投入する資格情報を持ちます。これはExtension SPIのEP-2（資源へのアクセスを持つ拡張）にあたり、侵害されたときの影響が大きい部分です。

- 資格情報は提供者ごと、名前空間・partitionごとに限定し、クラスタ全体の管理権限を持たせない。
- 一つのAdapterの侵害が、他の提供者や、EKIのLease・Result受理の権限へ波及しない（RQ-051）。
- 提供者の管理者の信頼ドメインを、Federationと同じ考え方（RQ-031、RQ-049）で区別する。

### 5.9 長時間動き続けるサービスはLeaseの考え方に合わない

EKIのLeaseは「このAttemptをこの時間範囲で割り当てる」一時的な権限です。常時動き続けるWebサービスのような作業は、終わりのあるAttemptとして表しにくく、EKIの得意領域の外です。常駐サービスは下位のオーケストレータに任せ、EKIは扱わないか、配置先の選定と証跡に留めます（§7.3）。

## 6. 信頼性の異なる資源を同じ契約で扱う

資源を、EKIが前提にできる保証の強さで並べると次のようになります。

```text
強い保証の根拠が得やすい                                     得にくい
専用GPUサーバ ― 管理クラスタ ― 組織間の提供枠 ― 業務端末 ― Browser ― 不特定ボランティア
```

どの資源でも同じTask契約（RQ-011の信頼性、RQ-021のResult保証）を書き、配置時に資源側の能力と保証水準（Assurance）を照合します。照合の入力は自己申告だけにしません（RQ-047）。

例えば、ある企業で次の三つのTaskが同時に来た場合を考えます。

| Task | 性質 | 配置 |
|---|---|---|
| 顧客データを含む文書の分類 | 機密、再実行可能 | Assuranceが高い社内Kubernetesだけ |
| 公開文書の埋め込みベクトルの再計算 | 機密なし、再実行可能、決定的に近い | 業務端末も可。一部を別提供者で再計算して照合 |
| 外部システムへの通知を含む集計 | 副作用あり | 自動再試行を無効にできる提供者だけ。不明時はUNKNOWN |

この三つを、利用者は同じAPIで依頼し、配置の違いはPolicyとTask契約から導かれる、という形を目標とします。

### 提供者をまたぐ再計算は、独立性の一つの軸になる

[AI時代の価値と位置づけ](ai-era-value-positioning.md)のV2で述べたとおり、同じモデルを同じ環境で複数回動かしても、独立した確認にはなりません。異なる運営者の資源提供者（社内クラスタと組織間の提供枠など）で再計算することは、「運営者」という軸での独立性を与えます。管理クラスタと遊休資源を同じ基盤で扱うことは、この独立性の選択肢を増やすという利点も持ちます。

## 7. TEIはEKI上で稼働する

### 7.1 提案する整理

TEIの構造は、Authoring、Canonical Semantic、Binding / Realization、Implementation & Executionの4層です（TEIリポジトリの`docs/architecture.md`）。Binding / Realization層は、業務の意味を特定のProvider・Targetへrealizeします。

EKIは、このうち **Implementation & Execution層の実行基盤の一つ** として位置づけられます。TEIが業務意味を定義し、その実行と配置を、EKIが資源の種類を問わず統治します。

```text
SEI（認知・統治）        TEI（業務意味・Application）       その他のOSS・製品
        │                         │ Binding / Realization          │
        └──────── workload contract（意味は渡さない）──────────────┘
                                  ▼
EKI（配置・実行・検証・監査）
                                  │ Resource Provider Adapter / EKI Worker
        ┌─────────────────────────┼──────────────────────────────┐
        ▼                         ▼                              ▼
  Kubernetes+Kueue           Slurm / Nomad               業務端末・Browser・組織間
```

依存の向きは **TEI → EKI** です。EKIがTEIの意味を持ち込むことはなく、EKI Coreは引き続き業務意味を所有しません（Principle 7）。TEIはEKIにとって、SEIやSUIと同じ「workload contractを持ち込む利用側」の一つです。

TEIが「EKIを参照・操作する」のではなく「EKI上で稼働する」と整理する利点は、次の二点です。

- TEIが資源の種類（どのクラスタか、端末か）を知らなくてよい。TEIのBinding層が持つのは「この作業はどの性質の実行基盤を要するか」だけで、EKIの内部構成や運用操作を知らない。
- EKIの運用権限をTEIに渡さずに済む。TEIはEKIの管理者ではなく利用者であり、RQ-016（Clientは自分へ権限を与えられない）がそのまま効く。

### 7.2 載せる作業の範囲と段階

| 段階 | 作業 | EKIとの相性 |
|---|---|---|
| 1 | conformance実行、Gate Evidenceの取得、Projectionの再構築、静的検証 | 再実行可能で、入力と出力が明確。最初の対象に適する |
| 2 | AIによる設計候補の生成と評価（TEI Studio / AI） | 候補と結果を分けて扱える。複数の独立した評価者で比べる用途に向く |
| 3 | 変換、シミュレーション、大量のテストデータ生成 | 計算量が大きく、遊休資源の利点が出やすい |
| 対象外 | 業務Application本体の常時稼働、トランザクション処理、低遅延の対話 | Leaseの考え方に合わない（§5.9）。KubernetesやNomadに任せる |

段階2の具体例として、TEIの現状記録では、第一Web TargetのVisual Fidelityの評価が「単一のAI評価者」によるものだと明記されています。EKI上で、系統の異なる複数の評価モデルを別々の資源提供者で動かせば、評価の独立性を記録付きで高められます。ただし、評価が一致したことは、業務上の正しさを確定しません。最終判断はTEI側と人間に残ります（RQ-045）。

### 7.3 業務Application本体の配置について

業務Application本体の常時稼働を、EKIが「動かす」ことはしません。ただし、TEIが生成したApplicationを「どの管理クラスタへ置くか」を、データ所在やAssuranceの条件から選び、その判断を証跡として残す役割は考えられます。これは配置の判断と記録であって、サービスの運用（常駐、スケーリング、状態管理）ではありません。この役割を持たせるかは未決です（§10）。

### 7.4 依存の循環を作らない

EKIの管理画面や運用画面をTEIで作る方針（[Upstream Plan](../plan/UPSTREAM_PLAN.md)のTEI適用方針）と、TEIがEKI上で稼働する方針を同時に採ると、「EKIの運用画面が、EKI上のTEIで動く」循環が生じます。EKIが故障したときに、EKIを直す画面が動かない状態は避けなければなりません。

- EKI Kernel、Local Governor、署名権限は、TEIにも、EKI自身による配置にも依存させない。native-firstの領域を維持する。
- EKIの管理画面をTEIで作る場合、その画面はEKIの外（EKIに依存しない実行環境）で動かす。または、EKIが停止しても使える最小の運用手段（CLI等）を残す。
- TEIが生成した成果物が、EKI Kernelの意思決定の入力にならないようにする。

### 7.5 意味の混同を避ける

EKIが計算を成功させたこと、複数Workerの結果が一致したこと、EKIの署名付きAuthority Decisionが正しいことは、TEI上の業務的な事実を確定させません。TEIがEKIの記録を自身のEvidenceとして参照する場合も、SEIと同じ読み替えの禁止（[SEI対応ノート](sei-cognition-mapping.md) §6.3）が当てはまります。

### 7.6 TEI側の正本はTEIにある

TEIの実行Target・Providerとして、EKIをどう定義するかはTEIリポジトリで決める事項です。本書はEKI側で成立すべき条件の記録に留め、TEI側の設計を決めません。

## 8. SEIとの関係

SEIは、企業統治の観点で認知処理を束ねる利用者です。EKIからは、次の二つの役割に整理できます。

- **利用者（Client）**：計算の依頼をworkload contractへ投影して渡す。意味はSEIが保持する。
- **証拠の参照者**：EKIの署名付きAuthority Decisionと、配置・検証の記録を、SEIの統治の証拠として参照する。

守る境界は、[SEI対応ノート](sei-cognition-mapping.md)のとおりです。Local Governorの判断はSEIの稼働・承認・助言を必要とせず、EKIの署名が正しいことはSEIの判断が正しいことを意味しません。

### 統治の方針はどの経路でEKIへ入るか

SEIを企業統治の基盤として使う場合、予算、データ分類、承認ルールのような方針をEKIへ反映したくなります。ここで経路を誤ると、権限拡張の入口になります。

```text
誤った経路：SEI（Client）──依頼に「重要度：高」を添える──▶ EKIが予算上限を緩める
正しい経路：組織の管理者 ──Administrative Plane（ADM-1）──▶ EKIのPolicy
            SEI（Client）──依頼──▶ EKIがPolicyの範囲で配置
```

Client経路で渡された情報によってEKIが権限を広げることは、RQ-016に反します。SEIが方針の作成や審査を支援することはできても、その方針をEKIへ有効化する行為は、管理者権限（ADM-1）として、Clientとしての権限と分けて扱う必要があります。SEIのコンポーネントが両方の権限を持つ構成を許すかどうかは、未決です（§10）。

### 統合の利点

管理クラスタと遊休資源を統合することで、SEIは資源の種類を知らずに同じインタフェースで依頼できます。SEIの背景の認知処理（反例探索、夜間の読み直し、相互批評など）は、再実行可能で急がないものが多いため、遊休資源にも管理クラスタにも置け、EKIが状況に応じて選べます。

## 9. 段階の提案

MVPのvertical slice（悪意あるClientから受理し、2台以上のNative Workerへ配置し、reclaim・retry・Result検証まで確認する）は変えません。本書の内容は、MVP以後の候補です。

| 段階 | 内容 | 確認すること |
|---|---|---|
| 0（MVP） | Native Workerのみ | 既存計画どおり |
| 1 | Kubernetes + KueueのAdapterをSpikeとして作る | §5.1〜5.4の衝突を実際に再現し、Adapter契約で抑えられるか |
| 2 | SlurmのAdapterを追加 | 同じAdapter契約で、意味の異なる二系統を表せるか |
| 3 | TEIの段階1の作業（conformance等）をEKI上で動かす | TEIのBinding層が要る契約を、EKIの既存契約で表せるか |
| 4 | 選択肢C（上位製品からEKIを供給元として使う） | UNKNOWNや検証中の状態が欠落する問題の扱い |

### 反映候補のAcceptance Scenario

`ORC-n`は本書内だけの仮の番号です。採用する場合は`PAS-CAND-nn`として登録します。

| 候補 | 状況 | 既存との関係 |
|---|---|---|
| ORC-1 下位の見えない再試行 | 副作用を持つTaskを管理クラスタへ置いたところ、下位のオーケストレータがノード障害で自動再実行する | RQ-046、§5.1 |
| ORC-2 下位の一斉先取り | 優先度の高い学習ジョブのため、管理クラスタ上のEKIの作業が一斉に止められる | RQ-052、PAS-26、§5.2 |
| ORC-3 Adapterの資格情報の侵害 | あるクラスタ向けAdapterの資格情報が漏れる | RQ-051、§5.8 |
| ORC-4 下位のエラーからの内部構成の漏えい | 下位のエラーメッセージにノード名や名前空間が含まれる | RQ-018、§5.7 |
| ORC-5 Client経由の統治方針の注入 | 統治基盤を名乗るClientが、依頼に添えた情報で予算や配置条件を広げようとする | RQ-016、§8 |

## 10. Open Question

1. 最初のAdapter参照実装を、Kubernetes + Kueueのみにするか、Slurmまで広げるか。SEI・TEIの利用環境を含め、実際の利用組織にどの基盤があるかを確認する必要がある。
2. Resource Provider Adapterの契約に、Kueueのquota、Slurmのpartition、Nomadのnamespaceをどこまで共通の語彙として含めるか。
3. 管理クラスタの資源提供者を、既存のFederation（RQ-031）の信頼ドメインとして扱うか、別の概念を置くか。
4. 選択肢Bを、限定した出口として許すか。許す場合、Result受理を迂回しないことをどう保証するか。
5. 選択肢Cで、上位製品がEKIのUNKNOWNや検証中の状態を理解できない場合の扱い。
6. TEIが生成したApplicationの配置先を、EKIが選定・記録する役割を持たせるか（§7.3）。
7. EKIの運用画面をTEIで作る場合の、循環回避の最小要件。
8. SEIのコンポーネントが、Client権限と管理者権限の両方を持つ構成を許すか（§8）。
9. HTCondor、SkyPilotとの関係を、競合として差別化するか、接続先として共存するか。

## 11. 外部情報

- Kueue / MultiKueue、Volcano、YuniKornの比較: [vCluster](https://www.vcluster.com/blog/kubernetes-schedulers-vs-slurm)、[InfraCloud](https://www.infracloud.io/blogs/batch-scheduling-on-kubernetes/)、[The New Stack](https://thenewstack.io/kueue-can-now-schedule-kubernetes-batch-jobs-across-clusters/)
- SkyPilot: [公式ドキュメント](https://docs.skypilot.co/en/latest/docs/)
- HTCondor（cycle scavenging、ClassAd）: [Wikipedia](https://en.wikipedia.org/wiki/HTCondor)、[HTCondor概要](https://research.cs.wisc.edu/htcondor/description.html)
- Karmadaの卒業: [InfoQ](https://infoq.com/news/2026/09/karmada-kubernetes-cncf)
- Workflow / durable executionの比較: [ZenML](https://www.zenml.io/blog/temporal-alternatives)、[Union.ai](https://www.union.ai/blog-post/we-compared-the-data-models-of-every-major-ai-orchestrator)

## 関連文書

- [AI時代の価値と位置づけ](ai-era-value-positioning.md)
- [SEI Cognitionとの対応ノート](sei-cognition-mapping.md)
- [Reference Systems Research](reference-systems.md)
- [Authority Boundaries](../architecture/AUTHORITY_BOUNDARIES.md)
- [Task State Model](../architecture/TASK_STATE_MODEL.md)
- [Extension SPI](../architecture/EXTENSION_SPI.md)
- [Deployment Profiles](../architecture/DEPLOYMENT_PROFILES.md)
- [Upstream Plan](../plan/UPSTREAM_PLAN.md)
