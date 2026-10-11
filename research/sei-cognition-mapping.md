# SEI Cognitionとの対応ノート

## 役割

この文書は、SEI Cognitionが認知の処理をEKIへ依頼する場合と、SEIがEKIの判断記録を参照する場合について、両者の概念がどう対応するかを整理する検討メモです。

Product Requirement、Architecture、External Contractの正本ではありません。この文書によって新しいRequirementを追加したり、Architecture Baselineを変更したりはしません。SEI側の概念の正本はsei-cognitionリポジトリにあり、EKI側の正本は本リポジトリの`product/`、`requirements/`、`architecture/`、`spec/`です。

計算の依頼は、SEIからEKIへの一方向の投影として書きます。判断記録の参照は、EKIの記録の意味をSEI側で変えないための約束として書きます。どちらも、EKIがSEIの意味を理解するための表ではなく、SEIの概念をEKI Coreへ取り込むための表でもありません。

## 1. どのような場面で必要になるか

SEI Cognitionは、組織の遊休計算資源を、前景の作業を邪魔しない背景の認知処理に使うことを検討しています。ある仮説について別の説明を探す、反例を探す、夜間に記録を読み直して古くなった前提を探す、複数のモデルに互いの出力を批評させる、といった処理です（SEI `EMERGENT_COGNITIVE_ARCHITECTURE.md` §10〜§11）。

こうした処理は計算量が大きい一方、失敗後も再実行でき、急ぎでない場合が多いため、EKIの想定する利用場面と重なります。

SEIはその際、処理の目的や制約をCognitive Envelopeという一つのデータ構造にまとめる案を持っています（同 §4）。このEnvelopeをそのままEKIへ渡せばよさそうに見えますが、二つの問題があります。

1. EKI CoreがSEIの意味（目的、未解決の問い、判断の重さなど）を読むようになると、EKIはSEI専用の基盤へ近づいていきます。これはRQ-001とProduct Principles §7に反します。
2. `stakes`（判断の重さ）のような項目をEKIが解釈すると、「重要な判断だから優先度を上げる」「重要だから端末の安全上限を超えてもよい」といった、Clientの入力による権限拡張の入口になります（RQ-016）。

逆の方向にも問題があります。SEIがEKIの署名付きAuthority Decisionを判断の証拠として参照するとき、「署名が正しい」ことを「その実行は正当に許可されていた」「SEIの判断が承認された」と読み替えてしまうおそれがあります。

この文書は、どちらの方向でも意味が混ざらないように、何をどう対応づけ、何を対応づけないかを示します。

## 2. 前提

### 2.1 SEI側の位置づけ

- Cognitive Envelopeは、SEI自身の文書でも、評価段階の設計仮説（Evaluation-only Architecture Hypothesis）の中で挙げられた候補です。SEIのUniversal Contractではありません。
- SEI側には、SEIを製品ファミリーの「統合の頂点」と位置づける提案があります。この提案では、SEIは抽象・時間・統合の三つの意味での頂点を担い、指揮・所有・可用性の頂点にはなりません。SEIから他製品への信号は常に依頼であり、受け取った製品が自らの権限で判断します。また、どの製品の局所ループ（反射弓）も、SEIの稼働を必要としません。
- 各製品の判断記録をSEIの語でどう読むかについては、製品ファミリー全体の対応表が提案されています。EKIについては、署名付きAuthority Decisionを外部のAuthority evidence、Result VerificationをAssurance、ContributionをCapability Observationとして読む案です。

2点目と3点目の提案は、2026-09-27時点ではsei-cognitionの未マージbranch `docs/family-apex-backflow-20260927`にだけ存在します（文書の場所は末尾の関連文書を参照）。

### 2.2 EKI側の前提

- EKI Coreは計算の意味だけを扱い、利用側の意味はworkload contractへ投影して受け取ります（Product Value「隣接Productとの境界」、Principles §7）。
- TaskRequestはhostile inputです。認証済みClientから届いたものでも、構造・値・意味を信用しません（RQ-015、`architecture/CONTRACT_BOUNDARIES.md` §7）。
- Clientは条件を安全側へ厳しくできますが、自分で権限を広げることはできません（RQ-016）。
- 業務端末では、Local Governorの判断が中央Schedulerより優先します（Principles §4、RQ-005〜RQ-007）。

## 3. 投影の向き

```text
SEI Cognitive Envelope（SEIの意味）
        │  SEI自身、またはClient側のAdapterが投影する（EKIの外）
        ▼
EKI TaskRequest（計算上の条件だけ）
        │  Admission / Policyが安全側へ確定する（EKIのAuthority）
        ▼
実効的なTask contract
```

投影はEKIの外で行います。SEI自身、またはSEIとEKIの間に置くClient側のAdapterが、Envelopeから計算上の条件だけを取り出して、通常のTaskRequestを作ります。EKIはそのTaskRequestを他のClientからの依頼と同じように扱い、AdmissionとPolicyが実効的な条件を確定します。

EKIはEnvelopeそのものを受け取りません。SEIの記録とEKIのTaskを結びつける必要があれば、SEI側でSEIの参照とEKIの`task_id`を対応づけて保持すれば足ります。EKIへSEIの参照を持ち込む必要はありません。

## 4. Cognitive EnvelopeからEKIのworkload contractへの対応

SEIのCognitive Envelopeの項目ごとに、EKI側で受け取る形と、EKIが決めること・知らないことを並べます。EKI側の項目名は`spec/external/openapi.json`のTaskRequestとArtifact ingestに合わせています。

| SEI Cognitive Envelope | EKI側で受け取る形 | EKIが決めること・知らないこと |
|---|---|---|
| `privacy / data_classification` | 入力ArtifactのData Class。ingest時の`requested_data_class`は下限としての希望で、実効Data ClassはAdmissionとData Authorityが安全側へ確定する | SEIの分類名を組織のData Classへどう対応させるかは、導入組織のPolicyが決める。EKIがSEIの分類を緩める方向へ読み替えることはない |
| `latency_budget` | `deadline`、`priority`（希望）、`resources.wall_time_seconds` | 実効的な優先度はPolicyが決める。Browserや業務端末の余力を、厳しい期限を守る根拠にしない（RQ-010）。数秒で応答が要る前景の処理は、分散すると不利になりやすく、ローカルで処理する方がよいことが多い（RQ-014） |
| `energy_budget` | Taskに直接対応する項目はない。Resource上限（CPU、GPU、wall time）として間接的に表せるだけ | 電力の上限は、ProviderとEndpoint側のEnergy policy、Local Governorが決める（RQ-053）。Clientの予算指定がローカルの安全上限を広げることはない |
| `cost_budget` | 依頼主体に許可されたbudget / quota、Resource上限、Compute Grantの範囲 | 予算はAdmissionが確定し、Clientの記述は上限を突破する権限にならない（RQ-016、RQ-017）。金銭価値や交換レートはEKIが持たない（RQ-038） |
| `stakes` | 安全側にだけ効く形で表す。より強い結果検証（`result_profile`）、より厳しいData Class、配置できるWorkerの制約など | 判断が重いことを理由に、優先度を上げる、他主体のcapacityを奪う、Local Governorの判断を上書きする、といったことはしない。判断の重さそのものの意味をEKIは知らない |
| `allowed_tools` | Executor requirement（登録済みExecutorへresolveされる）、登録されたnetwork profileまたはexternal adapter | 任意のツールやURLを、Taskの入力で許可しない（RQ-026、RQ-029）。「使ってよい」はSEI側の制限であり、EKI上で権限を与えるものではない |
| `allowed_actions` | `reliability.effect_mode`（P0 / MVPでは`none`だけを受け付ける） | 外部への副作用を伴うTaskは、RQ-046による明示が必要になる。SEIが許した行為であっても、EKIが実行の権限を得るわけではない。業務上の行為の実行は、TEIなど各製品の権限に属する |
| `freshness_requirement` | 入力Artifactのdigestによる固定、`deadline` | 何をもって新しいとするかはSEIが判断する。新しいデータが必要なら、SEI側で依頼の前にingestする。EKIは指定されたArtifactを内容で固定して使う |
| `constraints` | 計算上の制約（Resource、データの所在、Data Class）だけ | 意味上の制約は、SEI側またはExecutor側に残る |
| `authority_boundary` | 対応しない（EKIへ渡さない） | SEIの権限境界はSEIの意味である。EKI上のAuthorityは、EKIのAuthority roleだけが持つ |
| `objective`、`task_class`、`scope`、`selected_records[]`、`unknowns[]`、`conflicts[]`、`expected_output_role` | 対応しない。処理に必要な情報は、Admission済みExecutorのparameter、または入力Artifactの中身としてだけ運ばれる | EKIはこれらを解釈しない。`task_class`からResource要求や優先度を推し量ることもしない。Unknown、Conflict、Decisionなどの区別は、EKIの状態やResultには現れない |
| `provenance_refs[]` | 入力ArtifactのReferenceとdigest。SEI側の参照IDはSEI側で保持する | EKIのprovenance（RQ-039）は計算の来歴であり、SEIの判断の来歴ではない |

SEIは、「計算できる端末が、すべてのデータを見てよいわけではない」ことを停止線の一つにしています（同 §12）。これは、EKIがData ClassとData authorityを分けていること（RQ-024、RQ-028）と同じ方向を向いています。この点では、両者は互いの意味を知らなくても整合します。

### 具体例

夜間に「古くなった前提の候補」を探す処理を考えます。SEIのEnvelopeは、`privacy`が社内限定、`latency_budget`が翌朝まで、`energy_budget`が低、`cost_budget`が部門の背景処理枠、`stakes`が中、`allowed_actions`が提案の作成だけ、だとします。

Client側のAdapterは、これを次のようなTaskRequestへ投影します。

- 入力：対象となる記録の書き出しをArtifactとしてingestし、`requested_data_class`に、社内限定に相当する組織のData Classを指定する
- `deadline`：翌朝の始業前
- `priority`：`background`
- `reliability`：`retryable = true`、`preemptible = true`、`effect_mode = none`
- `resources`：CPU、メモリ、wall timeの上限
- `result_profile`：出力が所定の形式に従うかを確かめるValidator

EKIは、「古くなった前提」が何を指すのかも、結果がSEIで提案として扱われるのか判断の材料になるのかも知りません。EKIが確定するのは、社内限定のデータを扱えるWorkerはどれか、始業時に利用者が戻って処理が止まったときにどう回復するか、結果が形式上の検証を通ったか、までです。`energy_budget`はTaskの項目としては運ばれず、端末側のEnergy policyとLocal Governorが実際の上限を決めます。

## 5. EKIから返るものの読み方

EKIがClientへ返すのは、Taskの公開状態、Accepted Result、Assuranceの要約、Artifactのfacetです（`spec/external/README.md`）。SEIがこれらを実行の受領記録や観測として自らの語で記録するかどうかは、SEIが決めることです。その場合も、次の点は変わりません。

- `completed`は、計算が所定の検証を通ったことを示すだけで、SEIの意味で正しかったことを示すものではありません（Product Value「隣接Productとの境界」、RQ-045）。
- `running`は、Workerが今まさに計算していることを証明した状態ではありません。
- 複数の結果が一致したことは、正解の証拠になりません（PAS-14）。

## 6. 署名付きAuthority DecisionとSEIのAuthority evidence reference

### 6.1 EKIの署名付きDecision

EKIは、WorkOrder、Lease、Placement Approval、Executor Approval、Artifact Grantを、署名付きのDecisionとして発行します。payloadはdeterministic CBORで表し、Authority roleごとに分けた鍵でCOSE_Sign1署名します（ADR-0001、ADR-0004、ADR-0005、`spec/decisions/v1/decisions.cddl`）。

ADR-0004は、次の区別を定めています。

```text
valid signature
  != trusted Authority role
  != valid Decision semantics
  != current execution authorization
```

署名が正しくても、それが正しい役割の鍵によるものか、Decisionの内容として妥当か、いまこの実行を許可しているかは、それぞれ別に確かめます。

### 6.2 SEIが参照してよいこと

SEIのDecision Memoryは、権限の確認が必要な場合に、外部のAuthority ProviderやAuthority evidenceへの参照を別の境界として持てます（SEI `SEI_DECISION_MEMORY_v1alpha1.md` §8、§10）。EKIの署名付きDecisionは、この「外部のAuthority evidence」として参照される候補になります。

参照するとき、SEIは次の性質を変えずに運ぶ必要があります。

1. **上の4段の区別を平らにしない。** 「署名を検証できた」という記録を、「EKIがその実行を許可していた」や「いまも有効である」という記録へ変えません。
2. **Decisionの意味はEKIのAuthority roleに属する。** WorkOrderはWOR-1、LeaseはLSE-1、Placement ApprovalはPLC-1が発行したものであり、SEIの判断や承認ではありません。
3. **過去に検証できたことと、いま検証できることを分ける。** ADR-0004では、鍵がVERIFY_ONLY、REVOKED、COMPROMISEDへ移ると、同じ署名bytesでも現在の権限の根拠には使えません（ADR-0004 §11）。SEIの言葉でいえば、過去のPASSは現在のPASSではありません。
4. **SEIの記録からEKIのDecisionを再発行・延長・復活させない。** EKIのDecisionの効力を決めるのは、EKIのKey RegistryとPolicyだけです（RQ-048）。

### 6.3 SEIがしてはならない読み替え

- EKIのDecisionを、SEIのAuthorityとして解釈し直すこと。
- EKIのPlacement Approvalを、「その処理を行うべきだった」というSEI上の判断の根拠にすること。
- 署名が有効であることを、Resultの意味上の正しさの根拠にすること。

### 6.4 参照を手に入れる経路は定義されていない

P0 External Contractは、WorkOrder、Lease、Placement ApprovalなどのInternal Decisionを公開していません（`spec/external/README.md`）。SEIがこれらを参照するには監査出力のような別の面が必要ですが、その面はまだ定義されておらず、この文書もそれを求めません。

将来そのような面を設ける場合でも、RQ-040（不要な開示をしない監査）とRQ-018（Topologyの秘匿）の範囲で、Decision ID、kind、payload digest、`kid`、Authority role、発行・失効の時刻など、参照に必要な最小限に留めることになります。Worker Identityや、Workerがどこにあるかを含めるかどうかは、その時点で別に判断します。

## 7. 資源提供の記録（Contribution）の対応

Contribution RecordとSEIのCapability Observationの対応は、[Contribution Accountingと価値交換の境界](contribution-accounting.md) の「SEIのCapability Observationとの対応」にまとめています。

## 8. 比喩から出た問い — 内受容

SEI側の分析では、神経系の比喩を使い、EKIが持つ計算資源・電力・利用者の在席についての情報を、SEIにとっての「内受容」（体の内側の感覚）に見立てています。そこから、SEIが注意の配分にこうした信号を使えるか、という問いが出ています。

これは比喩から生じた問いであり、EKIの設計の根拠にはしません。検討する場合も、次の制約が先に立ちます。

- 業務端末の利用者が在席しているかどうかは、端末利用者についての情報です。通常のClient権限から列挙できてはならず（RQ-018）、監査や観測のためであっても不要に開示しません（RQ-040）。
- 仮に渡すとしても、個々の端末や利用者を特定できない粒度まで縮約した、容量についての観測に留まるはずです。
- EKIは何も送らなくても成り立ちます。SEIがこの信号を持たないことで、EKIの動作が変わることはありません。

## 9. 保証しないこと・未決事項

- SEIのData Classification名と組織のData Classの対応は、導入組織のPolicyが決めます。この文書は既定の対応を定めません。
- `energy_budget`に直接対応するTaskの項目はありません。Client側から電力についての希望を伝える項目が必要かどうかは未決です。必要と判断した場合も、Requirementとして扱います。
- Cognitive EnvelopeはSEIの評価仮説です。SEI側で項目が変わればこの文書を更新しますが、EKIのContractがそれに合わせて変わることはありません。
- 署名付きDecisionの参照を外部へ出す面は定義されていません（6.4）。
- どの認知処理をEKIへ出し、どれをローカルに留めるかは、SEIまたは利用者側が判断します。EKIはRQ-014に従い、分散が不利な場合に分散しない選択肢を残します。
- 認知処理を題材にした候補のAcceptance Scenarioは、`requirements/ACCEPTANCE_SCENARIOS.md`の「候補Scenario（評価待ち）」に置いています。いずれも評価待ちで、正式なScenarioではありません。

## 関連文書

EKI Fabric:

- [Product Value](../product/value.md) — 隣接Productとの境界
- [Product Principles](../product/principles.md) — §4、§7
- [Product Requirements](../requirements/PRODUCT_REQUIREMENTS.md)
- [Acceptance Scenarios](../requirements/ACCEPTANCE_SCENARIOS.md) — 候補Scenario（評価待ち）
- [Contract Boundaries](../architecture/CONTRACT_BOUNDARIES.md) — §6〜§7
- [ADR-0004 Signed Decision cryptographic profile](../plan/adr/0004-signed-decision-cryptographic-profile.md)
- [P0 External Protocol](../spec/external/README.md)
- [Contribution Accountingと価値交換の境界](contribution-accounting.md)

SEI Cognition（`hat47x/sei-cognition`。特記がなければ`main`に存在します）:

- `evaluation/sei-cognition-orchestration/EMERGENT_COGNITIVE_ARCHITECTURE.md` — §4 Cognitive Envelope、§10 Background Cognition、§11認知の多様性、§12 Locality-first、§13 Distributed Execution Fabricとの分離
- `evaluation/distributed-execution-fabric/README.md` — §3共通Job Envelope候補、§4 Fabricが知らないもの
- `contracts/decision-memory/SEI_DECISION_MEMORY_v1alpha1.md` — §8 Responsibility / Authority、§10 Progressive Explicitness
- `contracts/capability/SEI_CAPABILITY_OBSERVATION_v1alpha1.md`
- `plan/adr/ADR-0032-family-integration-apex-and-control-meaning-boundary.md` — 統合の頂点の提案（未マージbranch `docs/family-apex-backflow-20260927`にだけ存在）
- `product/definition/SEI_FAMILY_SEMANTIC_MAP.md` — 製品ファミリーの判断記録の対応表（同上）
