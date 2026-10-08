# EKI Fabric Product Requirements

## この文書の役割

この文書は、EKI Fabricが満たすべきProduct Requirementの正本です。

現在は、Architectureを選ぶ前の上流工程にあります。ここで定めるのは「何を満たす必要があるか」です。NATS、WASM、SPIFFE、特定のSandbox製品のように「何を使って実現するか」は、Requirementとしてどうしても必要な場合を除き、固定しません。

初めて読む場合は、先に[要求文書の読み方](README.md)と[はじめてEKI Fabricを読む方へ](../docs/for-readers.md)を読んでください。

状態は、次の意味で使います。

- **Baseline**: 現時点で、中心となる要件として採用している。
- **Needs research**: 要求する方向は採用しているが、保証の水準や実現できるかどうかの調査が必要である。
- **Open**: 要件そのものの境界を、さらに検討する必要がある。

---

## A. プロダクトの境界と相互運用性

### RQ-001 — Application-agnostic compute fabric — Baseline

EKIは、SEI Cognition、TEI、SUIなど、現在想定しているOSSだけでなく、未知のOSS、他社のソフトウェア、Webアプリ、将来登場するソフトウェアからも利用できなければならない。

EKI Coreは、利用するアプリケーション固有の業務概念や、認知上の意味を、必須の知識にしてはならない。特定のアプリケーションの意味をCoreに取り込まなければ接続できない構造は、避ける。

### RQ-002 — Language-neutral external contract — Baseline

基準実装にどの言語を使っても、EKIの外部利用の契約は、特定のプログラミング言語に専用のものであってはならない。

Go SDKなどを用意する場合も、それは利用を便利にするための層にとどめる。Go以外からも使える、相互運用のための契約を別に持つ。

### RQ-003 — Heterogeneous worker support — Baseline

業務端末、GPUワークステーション、専用ノード、サーバ、ブラウザ、一時的に参加するノード、外部組織の資源、クラウドなどを、同じ性質のWorkerだと仮定してはならない。

性能だけでなく、使える時間、管理している主体、機密性の保証、結果を検証する能力、離脱しやすさなどの違いを、表現できなければならない。

### RQ-004 — Replaceable infrastructure dependencies — Baseline

Queue、Identity、Artifact Store、Executor Runtimeなどの個々の技術を、EKIの外部契約そのものと同一視してはならない。ただし、Product Requirementにとって本質的に必要な場合は、この限りではない。

既存のOSSで足りる部分は、積極的に再利用する。一方で、その製品を交換すると、EKIを利用する側にとっての意味まで変わってしまう構造は、避ける。

---

## B. 資源の利用と、人を優先する運用

### RQ-005 — Human-first endpoint governance — Baseline

業務端末や個人の端末では、端末の利用者の本来の作業を、バックグラウンドの計算より優先しなければならない。

EKIの目的は、遊休資源を最大限に使い切ることではない。利用者の作業を妨げずに残った余力だけを、利用する。

ここでいう利用者の本来の作業には、利用者が操作している作業だけでなく、利用者のために自律的に動いている処理も含まれる。たとえば、本人のAI Assistant、同期、バックアップなどである。キーボードやマウスの操作がないことだけを根拠に、端末が空いているとみなしてはならない。利用者のための処理が使っている資源をLocal Governorが把握できない場合には、安全な側、つまりバックグラウンドの計算を控える側に判断できなければならない。

### RQ-006 — Immediate local opt-out — Baseline

端末の利用者やProviderは、許された範囲で、資源の提供を即座に縮小または停止できなければならない。

中央のControl Planeとの通信が不安定な場合や、切れている場合でも、ローカル側だけで、新しいTaskの受け付けを止めることと、資源の提供を縮小することができなければならない。

この停止する権限は、単なる使い勝手のための機能ではない。中央側が誤動作したり侵害されたりした場合にもEndpointを守る、Security boundaryの一つとして扱う。

### RQ-007 — Effective capacity is bounded locally — Baseline

中央側が許可した上限よりも厳しい制約を、ローカルのPolicy、利用者の設定、温度、電力、電源の種類、ネットワーク、端末の保護などの安全上の判断によって、適用できなければならない。

たとえば、中央のControl Planeが「60%まで使える」と判断していても、端末側が「いまは0%」と判断した場合がある。この端末側の上限を、中央側が強制的に広げられることを前提にしてはならない。

Local Governorは、性能を最適化するための補助ではない。Endpoint側に残る、Resource Enforcement Boundaryとして扱う。

### RQ-008 — Network-aware scheduling — Baseline

分散処理によって、業務のネットワークを不必要に圧迫してはならない。

入力と出力の量、既存のcache、network class、転送のコスト、データの局所性などを、配置の判断に反映できなければならない。たとえば、速いGPUを持つ端末が別にあっても、大量のデータを転送すると全体として不利になる場合がある。そのときは、データに近い端末を選べなければならない。

### RQ-009 — Resource-bounded execution — Baseline

Taskの実行には、CPU、GPU、Memory、Disk、Network、Wall time、出力量などについて、Policyとしての上限を持たせられなければならない。

この上限は、実行後の請求や監査のためだけのものではない。一つのTaskがFabric全体に無制限の負荷を広げないための、境界として機能する必要がある。

### RQ-010 — Opportunistic availability — Baseline

EndpointやBrowserなどが、sleep、shutdown、利用者の復帰、ネットワークの喪失、opt-out、Browserの一時停止などによって、いつでも離脱し得ることを、正常な場合として扱わなければならない。

Worker profileごとに、期待できるlifecycleを区別する。Browserのバックグラウンド実行のように保証されない性質を、厳しい期限や可用性の保証の根拠にしてはならない。

---

## C. Taskの意味と、実行の信頼性

### RQ-011 — Explicit workload reliability contract — Baseline

Taskは、少なくとも次の3点を表現できなければならない。再実行してよいか、途中で止めてよいか、期限があるか。必要に応じて、checkpointできるか、冪等か、決定的かなども、表現できる余地を持つ。

EKIがTaskの性質を推測して、再実行してよい処理と、外部への副作用を持つ処理とを、同じように扱うことは避ける。

### RQ-012 — No universal exactly-once assumption — Baseline

EKI Coreは、一般的なTaskについて、exactly-onceの実行を暗黙のうちに保証してはならない。

Workerが離脱したり、通信が途切れたりする分散環境では、実行が済んだのかどうかを、すぐには確定できない場合がある。重複して実行される可能性と、その場合の扱いを、Taskの契約として明示できなければならない。

### RQ-013 — Failures may be absorbed at fabric level — Baseline

一時的なWorkerを、一台ずつ高可用な機器として扱うことを、前提にしてはならない。

再配置、再実行、checkpoint、結果の検証などを組み合わせる。個々のWorkerが途中で消えても、Fabric全体として必要な信頼性を得られることを、めざす。

### RQ-014 — Do not distribute when distribution is worse — Baseline

ローカルで実行する方が、安全で、負荷が低く、コストが安く、または速い場合に、EKIは、分散して配置すること自体を目的にしてはならない。

「Fabricに参加しているから、ほかの端末に送る」のではない。分散することで本当に利益が得られる場合にだけ、分散する。

---

## D. 悪意のあるClientへの耐性

### RQ-015 — Authenticated clients remain untrusted — Baseline

認証されていても、所属が分かっていても、署名があっても、公式のSDKを使っていても、Clientから受け取る入力を善意だと仮定してはならない。

正規のCredentialを持つClientであっても、侵害されていたり、改変されていたりする可能性がある。

### RQ-016 — Client cannot grant itself authority — Baseline

Clientが、Requestに値を書くだけで、次のことが成立してはならない。Executorの登録、特定のWorkerの直接の指定、security classificationを下げること、network authorityを広げること、予算を広げること、他の主体のArtifactへのアクセス。

Clientが、条件を安全な側に厳しくすることと、自分で権限を広げることとは、分けて扱う。

### RQ-017 — Resource-exhaustion resistance — Baseline

悪意のあるClientが、大量のTask、巨大な入力、fan-out、retryの増幅、出力の肥大化などを使っても、許可された予算を超えてFabricの資源を消費できないこと。

大量のRequestを、いったん永続的なQueueに受理してから、後段で抑えるだけでは足りない。受理する前と後の、適切な境界で、最大の影響を制限できなければならない。

ClientがAI Agentなどの自動化された主体である場合がある。あるTaskの結果を読んだ別の主体が、追加のTaskを出すという連鎖（委譲の連鎖）でも、同じ制限が及ばなければならない。委譲された側の予算と権限は、委譲した側のものを超えてはならない。連鎖全体で消費できる資源は、最初の依頼に与えられた予算の範囲に収まる必要がある。正規の資格情報を使っていることや、悪意がないことは、この上限を緩める理由にならない。

### RQ-018 — Topology confidentiality — Baseline

通常のClientの権限から、不要なWorkerのhostnameやIPアドレス、端末の利用者の情報、cacheの内容、社内の構成などを、列挙できてはならない。

EKIのTask APIが、内部ネットワークや参加している端末を探索するためのAPIになってはならない。これを要求する。

---

## E. 悪意のあるWorkerへの耐性

### RQ-019 — Worker result is a claim until verified — Baseline

Workerが返すResult、resource usage、実行時間、cacheの状態、capabilityなどの自己申告は、必要な保証の方式によって確認されるまで、事実として扱ってはならない。

「Workerが署名した」ことは、そのClaimを誰が出したかを確かめる材料にはなる。しかし、Claimの内容が正しいことまでは、自動的には証明しない。

### RQ-020 — Modified worker tolerance — Baseline

正規のWorker Agentが改変される場合がある。たとえば、WorkOrderを無視する、計算を省略する、偽のResultを返す、性能や利用量を偽る、といった場合である。このような場合でも、他の主体の権限が広がったり、検証されていないResultが確定したりしてはならない。

Worker Agent自身を、Security Root of Trustにしてはならない。

### RQ-021 — Result assurance is workload-specific — Baseline

結果の完全性について、Taskの性質に応じて検証の方法を選べる余地を、持たなければならない。

候補には、次のものがある。完全な一致、許容誤差を含む数値の比較、アプリケーション固有のvalidator、独立した複製の実行、管理下のWorkerでの再計算、attestation、verifiable computation。どれか一つを、すべてのTaskに強制してはならない。

### RQ-022 — Sybil-aware assurance — Needs research

匿名のWorkerやアドホックなWorkerの単純な多数決だけを、高い保証の根拠にしてはならない。

一人の攻撃者が多数のWorker Identityを作れる場合、Workerの数は、独立した確認者の数を意味しない。独立性をどう表現し、どう確認するかを、保証の水準ごとに具体化する。

独立性が失われる原因は、攻撃者だけではない。同じモデル、同じ重み、同じ実装、同じ学習データに由来するものは、複数回動かしても、悪意がなくても同じ誤りを繰り返し得る。複数の実行や検証が一致したことは、何が独立していたかを示せない限り、高い保証の根拠にしてはならない（[Worker Assurance Model](ASSURANCE_MODEL.md)のAxis E）。

### RQ-023 — Reputation is not root of trust — Baseline

過去の成功率や評判は、複製の回数を減らしたり、割り当ての効率を上げたりする最適化には使える。しかし、それだけを、結果の完全性や機密性の根拠にしてはならない。

長い間、正しく動いてきたWorkerが、後から侵害されたり改変されたりする可能性は、排除できない。

### RQ-024 — Untrusted worker confidentiality boundary — Baseline

平文の機密データを悪意のあるWorkerに渡した後で、機密性を回復できるとみなしてはならない。

TaskのData Classと、Worker側で実際に成り立つ機密性の保証とを、配置の条件として扱えなければならない。通信路が暗号化されていることだけを理由に、実行中のデータがWorkerの所有者から秘匿されているとみなしてはならない。

### RQ-025 — Attestation / proof extensibility — Needs research

Workerを信用せずに、より強い保証を得るための技術を、後から接続できる余地を持つ。たとえば、TEEのremote attestationやverifiable computationである。

ただし、特定の方式をすべてのWorkerに必須としない。また、Attestationが成立したことと、Application上のResultが意味の上で正しいこととを、同一視しない。

---

## F. Code、Artifact、Dataの完全性

### RQ-026 — Executable provenance — Baseline

業務Endpointなど、保護の対象となるWorkerで実行するコードについて、許可されたExecutorと、実際に実行されるArtifactとが同一であることを、確認できなければならない。

人間が読む名前や、変更できるtagだけを、実行するArtifactの完全なIdentityとして扱ってはならない。

### RQ-027 — Content integrity — Baseline

Taskのinput、Executor、Resultなどの主要なArtifactは、転送の経路や保存先だけに頼らずに、内容の完全性を確認できる識別子を持てなければならない。

Artifact Storeを信用できることと、受け取った内容が期待したArtifactであることとを、分けて扱う。

### RQ-028 — Data authority is separate from task authority — Baseline

Taskを投入できる権限と、任意のArtifactを読み書きできる権限とを、同一にしてはならない。

「計算を依頼できる」ことを、他の主体のデータへアクセスするための迂回路にしてはならない。

### RQ-029 — No arbitrary worker-side fetch by default — Baseline

Clientが指定した任意のURL、filesystem path、network endpointなどを、Workerがそのまま取得することを、標準の動作にしてはならない。

外部へのアクセスが必要なTaskでは、対象と権限を明示する。Clientが文字列を指定しただけで、Workerのnetworkやfilesystemに対する権限が広がることがないようにする。

---

## G. 企業での利用、Federation、アドホックな参加

### RQ-030 — Enterprise-compatible default deployment — Baseline

一般的な社内システムとして説明しやすく、監査しやすい、通信、認証、認可、ログ、端末の制御を、標準的な利用形態として成り立たせる。

特殊なP2P通信、任意の端末間の直接通信、Firewallの広い開放などを、必須の条件にしてはならない。

### RQ-031 — Trust-domain federation — Needs research

複数の管理主体が、それぞれのIdentity authorityを保ったまま、限定された範囲で協力できなければならない。

Federationの関係を結んだこと自体が、相手のTrust Domainのすべてのidentity、すべてのWorker、すべてのDataに対する、包括的な信頼や権限を意味してはならない。

Federationの関係は、明示的に構成する。相手のDomainが、さらに別のDomainとFederationしているからといって、推移的で包括的なTrustを自動的に生成してはならない。

### RQ-032 — Ad-hoc benevolent participation — Baseline

個人や、一時的なBrowserやNative Workerが、強い事前審査なしでも、貢献できる構造を認める。対象は、公開または低機密で、必要な方法により結果を検証できるTaskである。

善意の参加者を受け入れるために、参加者の善意そのものをSecurity Requirementにしてはならない。

### RQ-033 — Participation without internal network membership — Baseline

外部のWorkerを受け入れるために、ProviderやBeneficiaryの内部ネットワークへ、一般の参加者を加入させることを、必須にしてはならない。

外部の計算資源を使うことと、内部ネットワークの一員として扱うこととを、分ける。

### RQ-042 — Visible and controllable ad-hoc participation — Baseline

Browserや個人端末によるアドホックな資源提供では、資源が提供されていることを利用者が認識でき、分かりやすい方法で停止できなければならない。

通常のWeb閲覧に紛れて、意図しない計算資源の提供を行うことや、hidden iframeなどを使って、利用者から資源の提供を隠すことを、EKIの標準的な参加方法にしてはならない。

このRequirementは、組織が管理する端末のEnrollmentの方式を、一律に定めるものではない。業務端末での停止する権限には、RQ-006を適用する。

---

## H. 公益のための資源共有とContribution

### RQ-034 — Compute Grant — Baseline

Providerが、計算資源の利用枠を、他の主体に提供できるモデルを持つ。利用枠には、次の制約を付けられる。Resource Class、上限、期間、対象、優先度、Data Class、preemptionの条件。

Compute Grantは、内部ネットワークへの参加権や、包括的なDataへのアクセスを意味しない。

### RQ-035 — Provider revocation and reclaim — Baseline

Providerは、Grantによる将来の資源提供を、停止または縮小できなければならない。

ただし、資源の提供を止めたことによって、すでに正当に生成されたBeneficiaryのResultや成果物に対する権利が、自動的に遡って変わるわけではない。

### RQ-036 — Contribution accounting — Baseline

資源を提供したことについて、必要に応じて、検証できるContribution Recordを生成できること。

少なくとも、提供した主体、Resource Class、量、対象のWork、測定の方法、assurance、provenanceを表現できる余地を持つ。Workerの自己申告だけから、価値のあるContributionを確定してはならない。

### RQ-037 — External value mapping — Baseline

Contribution Recordを、外部の制度へ接続できること。たとえば、社内の配賦、相互利用のCredit、寄付の実績、コミュニティでの評価である。

Contribution Recordが表すのは、「何が観測され、検証されたか」である。Contribution Record自体が、金銭の価値や社会的な価値を保証するわけではない。どのContributionに価値を与えるかは、外部のPolicyに任せる。

### RQ-038 — No native currency or cash market — Baseline

EKI Coreは、通貨の発行、現金化、法定通貨との交換、市場価格の形成、投機的な市場を提供してはならない。

EKIが扱うのは、計算資源の提供を記述し、検証するための基盤までである。価値の意味や、交換の条件は、各主体に任せる。

---

## I. 説明責任、監査、観測

### RQ-039 — End-to-end provenance — Baseline

次の主要な関係を、必要な範囲で追跡できなければならない。TaskRequest、PolicyとAdmissionの判断、配置、実行のAttempt、Result Claim、Verification、Accepted Result、Contribution。

あとから、「どのWorkerが返したか」だけでなく、「どのExecutor、どの入力、どの検証の方法、どの判断を経て受理されたか」を確認できることを、めざす。

### RQ-040 — Audit without unnecessary disclosure — Baseline

監査できることや、Assuranceを得るために、次のものを広く開示することを、前提にしてはならない。不要な機密データ、ClientやWorkerの内部情報、他の主体のTopology、Raw Attestation Evidenceなど。

利用側が必要としているのが、「必要な条件を満たした」という判断の結果だけである場合は、生のEvidenceをすべてClientに渡す必要はない。

### RQ-041 — Security events are observable — Baseline

Securityや運用の判断に必要な事象を、観測できなければならない。たとえば、rejectしたrequest、quotaの超過、不正なresult、verificationの失敗、replay、revocation、opt-outなどである。

ただし、観測のために、新しい情報漏えいの経路を作ってはならない。これについては、RQ-040に従う。

---

## J. 追加の、横断的な安全の境界

### RQ-043 — No post-hoc deletion guarantee on untrusted workers — Baseline

通常のuntrustedなWorkerについて、「Taskの終了時に入力を削除した」というWorker自身の申告だけを、機密性の保証にしてはならない。

一度、平文を渡した相手がコピーを持っている可能性は、あとから消すことができない。削除の保証が必要なTaskでは、別の根拠が必要になる。たとえば、管理下の信頼の境界、Confidential Computing、暗号によって利用できなくする方式などである。

### RQ-044 — Verification authority is explicit and bounded — Baseline

ResultをAccepted Resultに昇格させる判断について、何を根拠にしたかを追跡できなければならない。たとえば、どのValidator、Verifier、Policy authority、Proof verifierかである。

Workerが自分で選んだValidatorだけを使って、自分のResultを正当化できる構造にしてはならない。

これは、すべてのVerifierを互いに疑う、完全なByzantineの構成を、一律に求めるものではない。どこを信頼の基盤に置いたかを明示し、その権限の範囲を限定できることを、要求する。

### RQ-045 — Honest assurance for semantically open results — Baseline

生成AIの文章や候補の生成のように、唯一の正解を、安価に、機械的に判定できないTaskがある。このようなTaskについて、EKIが、意味の上での正しさを完全に保証したかのように扱ってはならない。

確認できるprovenance、使用したExecutorやModel、複数の候補、独立した実行、アプリケーション固有の検証などを、記録できるようにする。最終的な判断を、利用するアプリケーションや人間に任せるTaskを、表現できること。

### RQ-046 — External side effects require explicit retry semantics — Baseline

外部のAPIの呼び出し、DBの更新、通知の送信など、副作用を持つTaskについて、その事実と、再実行してよい条件を、明示できなければならない。

冪等であるといった、安全の根拠がないTaskについて、Workerの離脱を理由に、EKIが自動的に「何度でも再実行してよいTask」と判断してはならない。実行が済んだのか分からない状態を表現できなければならない。必要に応じて、その状態をRecoveryの判断に渡せる余地を持つ。

### RQ-047 — Security-relevant capability claims require a trust basis — Baseline

Workerが自己申告した性能の値やcapabilityだけを、高い保証が必要なTaskの、Security placementの条件を満たした根拠にしてはならない。

「GPUがある」という性能についてのClaimと、「Confidential Runtimeが成り立っている」「要求された隔離がある」といったSecurityについてのClaimとを、分ける。後者を利用する場合は、要求する保証の水準に見合った根拠を持てること。たとえば、管理者が定めるPolicy、Attestation、外部のEvidenceである。

### RQ-048 — Freshness and revocation of time-bounded authority — Baseline

期限、失効、revocation、freshnessを持つ、次のようなものがある。Credential、Lease、Grant、Assurance Evidence、内部の実行権限。これらについて、古い情報をreplayすることで、失効済みの権限を復活させてはならない。

nonce、counter、timestamp、clock sourceなどの具体的な方式は、Architectureで決める。

### RQ-049 — Federation identity namespace binding — Baseline

FederationされたIdentityは、どのTrust DomainやIdentity authorityが発行したものかという結び付き（binding）を、失ってはならない。

複数のDomainのTrust Materialを、単純に一つに混ぜて、「どれかの鍵で検証できたから有効」と扱ってはならない。Identityのnamespaceが衝突したり、Trust Domainを取り違えたりすることで、別の組織のIdentityに権限が移ってしまうことを、防げなければならない。

Federationの設定自体も、価値の高いTrustの設定として扱う。通常のClientの入力から変更できることを、前提にしてはならない。

### RQ-050 — Bounded artifact ingestion and expansion — Baseline

Artifactの受理、検査、展開、変換、索引付けなど、Taskの実行の前後の処理も、Resource Budgetの対象にしなければならない。

圧縮されたArtifactでは、受信したbyte数だけでなく、展開した後のサイズ、fileの数、入れ子の深さ、処理の時間などを制限できなければならない。Archive内のentryの名前やpathを指定するだけで、Sandboxの外にあるHostのfilesystemへ書き込めてはならない。

Artifactのprovenance、content integrity、安全に処理できることは、それぞれ別の保証として扱う。

### RQ-051 — Control-plane authority separation — Baseline

Control Plane内部の一部のcomponentが侵害されたり、誤動作したりしても、その役割に不要な権限まで自動的に得られない構造を、選べなければならない。

影響の範囲が異なる権限を、論理的に分けられることを要求する。対象は、Taskのadmission、placement、データに対する権限、Executorのadmission、Federationの管理、Verificationの権限、Contributionの確定である。

これは、必ずMicroserviceに分割するという意味ではない。一つのプロセスの中であっても、権限の境界を設計の上で区別できればよい。

### RQ-052 — Coordinated reclaim without retry amplification — Baseline

Providerや端末の利用者によるResourceのreclaimは、Taskを完了させることより優先されなければならない。一方で、大量のWorkerが同時に離脱したあとに、retryやmigrationが集中して、別のResource Exhaustionを無制限に引き起こしてはならない。

大量のGrantの縮小、拠点の停止、勤務の開始などによる、まとまったcapacityの喪失を、Fabricで起こり得る通常のEventとして扱えなければならない。

backoff、jitter、priority、queueの規律の具体的な内容は、Architectureで決める。

### RQ-053 — Energy / thermal / device-safety aware resource policy — Baseline

Resourceが使えるかどうかを、CPUやGPUのidle率だけで決めてはならない。

ProviderやEndpoint側が、次のことを理由にして、capacityを縮小または停止できなければならない。Energy budget、BatteryかACかという状態、利用する時間帯、Thermal state、端末の保護。ContributionやCreditの仕組みがあっても、それを理由にして、ローカルのSafety Governorの上限を迂回できてはならない。

---

## K. 明示的な非目標

現段階で、EKI Coreの主目的にしないものを、次に示します。

- 分散DB、またはsource-of-truthとなるstorage
- 汎用の共有メモリ
- 低遅延のMPIやHPC interconnectの置き換え
- KubernetesやNomadなど、汎用のcluster orchestratorを全面的に置き換えること
- VMやIaaSの管理基盤
- 汎用のVPN、またはP2P networkingのプロダクト
- あらゆるTaskの機密性と完全性を、無条件に保証すること
- Workerの善意や、改変されていないことそのものを保証すること
- 暗号通貨、token market、現金決済の基盤
- 一時的なWorkerに渡した平文データを、あとから完全に回収することを保証すること
- 正解を定義できないTaskについて、EKIが、意味の上で絶対に正しいと保証すること
- あらゆるControl Plane componentを、互いに信頼しないByzantine fault tolerantな構成にすること
- すべてのArtifactの形式を、EKI Core自身が安全に解析できるようにすること

## Architectureに入る前の、主な未決事項

次の論点は、引き続き、追加の調査か、Acceptance Scenarioによる厳しい確認が必要です。

1. Worker Assuranceを、複数の軸を持つcapabilityとして、外部契約にどの粒度まで表現するか。
2. Sybilに耐えるreplication independenceを、principal、credential、attestation、operatorのどの単位で定義するか。
3. GPUを含むverifiable computationが、どの種類のTaskで実用になるか。
4. Confidential Computingを任意のextensionとして、Task contractにどこまで出すか。
5. Resource ClassとContributionの量を、ベンダーに依存せずに比較するための、最低限の尺度。
6. Browser Workerで、実際に制御できるresource limit、identity、利用者への表示の方法。
7. Enterpriseでの導入と、公開やアドホックな参加とを、同じProtocolで扱う範囲と、分ける点。
8. ValidatorやVerifierが侵害された場合に、保証のProfileごとに、どこまで独立性を求めるか。
9. 非決定的なAI Taskについて、EKIとして共通の検証の語彙を、どこまで持つべきか。
10. 副作用を持つTaskを、EKIの主要な用途に含めるか、限定された拡張の用途として扱うか。
11. FederationのTrust chainを、どこまで認めるか。また、どのbindingの情報を、外部契約に出すか。
12. Artifactの展開とParserの隔離について、最低限の保証を、Worker profileごとにどこまで共通にできるか。
13. Control Plane内部の権限の分割を、最小の構成と大規模な構成とで、どのように両立させるか。
14. Mass reclaimのときの再配置の制御を、Taskのpriorityや期限、Providerのpolicyと、どう整合させるか。
15. Energy / thermalのpolicyを、OSやhardwareの差を吸収しつつ、どこまで共通のResource Modelに含めるか。
