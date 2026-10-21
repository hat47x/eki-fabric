# EKI Fabric 脅威モデル（Threat Model）

## この文書の役割

この文書は、EKI Fabricがどのような攻撃者を想定し、何を安全上の不変条件として守るかを定める、Threat Modelの正本です。

Architecture Baselineは定めていますが、この文書では具体的な暗号方式、通信方式、SandboxやIdentityの製品を固定しません。脅威モデルでは、攻撃者と守る性質を先に定めます。

## 基本姿勢

EKIでは、**ClientもWorkerも悪意を持ち得る**ことを前提にします。

次のことは、それだけでは、善意であること、改変されていないこと、結果が正しいことを保証しません。公式のClientであること。正規のWorker Agentを使っていること。所属する組織の端末であること。認証に成功したこと。長い間、問題なく参加してきたこと。

一方で、善意のアドホックな参加者を、積極的に排除することも目標にしません。

安全性を、参加者の人柄の審査に頼らずに成り立たせます。そのために、権限の分離、資源の上限、結果の検証、Artifactの完全性、必要に応じたAttestationやProofを使います。こうして、強い事前の信頼関係がない善意の参加者も、受け入れられる状態をめざします。

2026-10-20の[ADR-0013](../plan/adr/0013-product-core-and-faces.md)で、この姿勢を「暗黙には信頼しない。信頼は委任先ごとに、管理者が根拠とともに明示して定める」と言い直しました。管理者が高い信頼の水準（委任の等級。RQ-058）を定めた委任先にも、誤登録、侵害、設定の誤りは起こり得ます。高い水準は、確認を軽くする管理者の判断であり、悪意がないことの証明ではありません。

## 「完全な悪意への耐性」の意味

EKIがめざす悪意への耐性は、「すべての構成要素が同時に侵害されても、絶対に安全」という意味ではありません。

主に、次のことを目標にします。

- 悪意のあるClientが、許可された以上の権限や資源を、自分で作り出せない。
- 悪意のあるWorkerが、偽のResultを返すだけで、それを確定した結果にできない。
- 一つのCredentialや一人のParticipantの侵害が、別のRoleや他の主体へ、自動的に波及しない。
- untrustedなWorkerに与えてよいDataと、与えてはいけないDataとを、分けられる。
- 古いCredentialやEvidenceを再利用して、失効済みの権限を復活させられない。
- 検証の基盤そのものを、どこまで信頼したかを隠さない。
- Control Planeの一部が侵害されても、そのComponentに不要な権限まで自動的に得られない構成を、選べる。

EKIが、明示的な信頼の基盤として置いたものまで、同時に侵害された場合は、通常の保証の範囲外です。たとえば、Control Planeのroot signing authority、Administratorのpolicy authority、利用する暗号のprimitiveなどです。

ただし、Architectureの段階では、次の検討をします。信頼の基盤を、不必要に一か所へ集中させないこと。key rotation、権限の分離、独立した承認などによって、侵害されたときの影響の範囲（blast radius）を小さくできること。

## 保護の対象

EKIでは、少なくとも次のものを、保護の対象とします。

- 他の主体のArtifact、Result、Secret、Credential
- Provider内部の端末の情報、network topology、利用者の情報
- Worker hostのfilesystem、process、network、device
- ClientやBeneficiaryの入力データと成果物
- Fabric全体の可用性と、資源の予算
- ExecutorとArtifactの完全性
- Accepted Resultの完全性
- Contribution Recordの正確性とprovenance
- Policy、Grant、quota、identityのbinding
- Federationにおける、Trust DomainやIdentity Authorityのbinding
- Auditとprovenanceの記録の完全性
- Assurance Evidenceに含まれ得る、端末や構成の情報
- 資源を提供する人の意思と、local opt-out
- Endpointのthermal、power、device-safetyの境界
- 委任先の登録と委任の等級、利用の許可（RQ-058）
- 予算の総額と配分、費用を生む容量の確保（RQ-057）
- 管理者権限の委任状と、そのもとで出された方針（RQ-061）
- 外部へ資源を提供する組織の、内部のnetworkとData

## 想定する攻撃者

### A1 — 悪意のある匿名Client

大量のTask、巨大なinput、異常に深いmetadata、SSRFに相当するアクセス、fan-out、retryの増幅、parserへの攻撃、他の主体のIDの推測などを試みます。

### A2 — 侵害された正規Client

正当なCredentialを持ったまま、許可された範囲の拡大、他のResourceへの横移動、内部情報の列挙を試みます。

### A3 — 悪意のあるWorker

次のような行為をします。WorkOrderを無視する。計算を省略する。偽のResultを返す。処理時間やresourceの量を偽る。入力を保存する。別のWorkerと結託する。

### A4 — 改変された公式Worker

OSSとして配布されたAgentを改造します。Protocolの上では、正規のWorkerに見えるまま、resource governorやsecurity checkを無効にします。

### A5 — Sybil operator

一人の攻撃者が、多数のClientやWorkerのIdentityを作ります。そして、quorum、reputation、Contribution、fair-shareなどを操作します。

### A6 — 悪意または脆弱性のあるExecutor Artifact

承認のプロセスをすり抜けたExecutor、侵害された依存ライブラリ、または脆弱性のあるExecutorが、host resourceや他のTaskの情報に到達しようとします。

### A7 — Network attacker

盗聴、改変、replay、downgrade、Credentialの窃取、endpointのなりすましなどを試みます。

### A8 — 悪意のあるFederation peer

Federationの関係を利用して、次のような行為を試みます。自分のTrust Domainの外にあるIdentityを偽装する。限定された関係を、包括的なauthorityに拡大する。

### A9 — Contributionを狙う、経済的な攻撃者

外部のCreditや評価の制度に有利な記録を、作ろうとします。たとえば、実際には提供していないcomputeを申告する。意味のないWorkを自作自演する。複数のIdentityを使って、Contributionを水増しする。

### A10 — Resourceに迷惑をかける参加者

正しいResultを返す意思がなく、限られた範囲でも、他者に運用のコストを負わせようとします。たとえば、deadlineの直前までLeaseを保持する。巨大なResultを返す。意図的にtimeoutを繰り返す。

### A11 — 悪意のある、または侵害されたValidatorとVerifier

Workerを検証する境界そのものを、侵害します。たとえば、偽のResultを正しいと判定する。Attestation Evidenceを誤って評価する。古いEvidenceを、新しいものとして扱う。

### A12 — 意図しないBrowserの資源利用を行う、提供する側

利用者が認識していない計算資源の利用を行います。たとえば、通常のWeb閲覧に紛れてCPUやGPUを使う。停止する方法を分かりにくくする。hidden iframeなどで資源の提供を続ける。

### A13 — 悪意のあるArtifactと、parserの悪用

Task本体を実行する前後の、取り込み、展開、変換の処理を攻撃します。使うのは、圧縮率の高いArtifact、過剰なfileの数、深い入れ子、異常なmetadata、archiveのpath traversal、脆弱なparserを狙う入力などです。

Digestや署名が正しいことは、この攻撃に対して安全であることを意味しません。

### A14 — 侵害されたControl Plane component

Scheduler、Admission、Verification、Artifact capabilityの発行、Federationの管理などの一部のComponentを侵害します。そして、そのComponentが本来持つ役割を越えて、Secret、Dataに対する権限、Executorのadmission、Trustの設定などへ到達しようとします。

Control Planeの一部が侵害されることと、root authority全体が侵害されることとを、区別します。

### A15 — Federationの設定や、namespaceをすり替える攻撃者

Trust Domainの名前、Bundle、Issuerのmetadata、Federationのendpointなどの対応関係を改変します。そして、別のDomainが発行したIdentityを、目的のDomainのIdentityとして扱わせようとします。

悪意のあるFederation peerだけでなく、設定の配布経路や、管理の権限の侵害も含みます。

### A16 — Reclaimを増幅させる攻撃者、または障害のパターン

多数のWorkerの一斉の離脱やGrantの縮小を引き起こします。または、自然に起きた大量のreclaimにTaskを集中させます。そして、retryやmigrationの嵐によって、残った資源やNetworkを枯渇させます。

攻撃者が存在しなくても、通常の運用の中で起こり得る障害のパターンとして扱います。

以下のA17〜A22は、ADR-0013で広げたスコープ（委任の核、自律の最適化、組織の判断との連動、外部への提供、常駐）に伴う攻撃者と障害のパターンです（2026-10-20追加）。候補Scenarioは[Acceptance Scenarios](ACCEPTANCE_SCENARIOS.md)のPAS-CAND-24〜30にあります。

### A17 — 委任先の内部の実行管理系

EKIが配置した仕事を、管理クラスタ、クラウドの関数実行、キューなどが、EKIの知らない間に内部で再実行、再配置、横取りします。多くは悪意のない既定の挙動ですが、副作用を持つ仕事では二重の実行になります。委任先の内部の実行管理系が侵害された場合は、「成功」の報告や状態の照会の結果を偽ります。接続に使う資格情報が漏れることも含みます（RQ-054）。

### A18 — 委任の等級を誤って、または不正に高くする主体

委任先を、実際より高い委任の等級に登録します。管理者の操作の誤り、登録の経路の侵害、根拠となるEvidenceの偽造を含みます。高い等級の委任先では確定に要る検証が軽くなるため、偽の結果が確定しやすくなります（PAS-CAND-24）。

### A19 — 委任状を持つ主体の誤りや乗っ取り

管理者権限の一部を委任状で委ねられた主体（SEIなどの自動化された意思決定）が、委任状の範囲の中で偏った方針を出します。原因は、主体自身の誤り、読んだ外部の文書に仕込まれた指示（プロンプトインジェクション）、主体の侵害です。委任状の自己更新や、範囲を越えた変更も試みます（PAS-CAND-25、26）。

### A20 — 組織に費用を負わせる依頼者

自律に委ねる依頼を、厳しい期限で大量に出し、クラウドの拡張などの費用を生む容量の確保を誘発します。結果を受け取るつもりはなく、組織に費用を負わせることが目的です（PAS-CAND-27）。

### A21 — 外部へ提供した資源の上で動く、提供先の仕事

組織がCompute Grantで外部へ提供した資源の上で、提供先の仕事が、組織の内部のアドレスへの接続、端末のfilesystemやほかの仕事の一時ファイルの読み取りを試みます（PAS-CAND-28）。

### A22 — 常駐の権限を更新し続けて、余力を占有する主体

常駐する処理の権限の更新を自動で続け、役に立たなくなった処理で、業務端末の余力を使い続けます。悪意がなくても、更新の自動化の誤りとして起こり得ます（PAS-CAND-30）。

## Security Invariants

以下は、具体的なArchitectureにかかわらず守るべき、安全上の不変条件です。

### SEC-001 — Authentication does not imply authorization

認証に成功したことだけで、Task、Artifact、Worker、Grant、管理APIに対する、包括的なauthorityを得てはならない。

### SEC-002 — Client cannot directly command a Worker

外部のClientから受けたRequestを、検証、認可、Policyの適用、必要な正規化を経ずに、そのままWorkerへの実行命令に変換してはならない。

### SEC-003 — Worker cannot self-certify correctness

Worker自身の署名、reputation、Identity、自己申告だけで、ResultをAccepted Resultに昇格させてはならない。

### SEC-004 — Modified endpoint must not widen authority

ClientやWorkerのsoftwareを、任意に改変しても、Control Planeなどが許可した以上のArtifactへのアクセス、networkへのアクセス、compute budget、Roleのauthorityを得られてはならない。

### SEC-005 — Security properties are monotonic toward safety

Clientの入力によって、Data Class、必要なassurance、quota、Policyなどを、安全性の低い方向へ上書きできてはならない。

### SEC-006 — Resource use is bounded before damage becomes unbounded

Admissionや実行の境界で、compute、memory、storage、network、fan-out、retry、outputなどの最大の影響を、制限できなければならない。

### SEC-007 — Retry and fan-out consume bounded budget

再実行や子Taskの生成によって、元のRequestに許可された予算を、無制限に増幅できてはならない。

### SEC-008 — Arbitrary URL or path is not authority

ClientがURL、host path、UNC pathなどの文字列を指定しただけで、Workerがその場所へアクセスするauthorityが成立してはならない。

### SEC-009 — Artifact identity survives transport

主要なArtifactの完全性は、保存先や転送のチャネルだけに頼らずに、確認できなければならない。

### SEC-010 — Role credentials are non-transitive

Client Credentialを取得しても、WorkerやControl Planeにはなれない。Worker Credentialを取得しても、管理者のauthorityを得られない。

### SEC-011 — Confidentiality requires an explicit trustworthy boundary

通常のuntrustedなWorkerに平文を渡しながら、そのWorkerのownerに対する機密性も保証する、という矛盾した主張をしてはならない。

### SEC-012 — Quorum independence must be justified

同一の攻撃者が支配できる複数のWorkerを、「独立した多数」とみなして、高い保証を主張してはならない。

### SEC-013 — Contribution cannot rely on self-reported usage alone

外部の価値交換や評価に使われ得るContributionを、Workerの自己申告によるCPUやGPUの時間などだけから、確定してはならない。

### SEC-014 — Audit must not become an exfiltration surface

監査やAssuranceのために、不要なRaw input、Secret、内部のTopology、Raw Attestation Evidenceなどを、広い範囲に保存したり開示したりしてはならない。

### SEC-015 — Stale authority cannot be revived by replay

期限が切れた、失効した、revokeされた、Credential、Lease、Grant、Evidence、内部の実行権限などを、古いMessageや署名済みの情報のreplayによって、再び有効にできてはならない。

### SEC-016 — Verification authority must be traceable

ResultをAccepted Resultに昇格させた判断について、どのValidator、Verifier、Policy authority、Proof verifierを根拠にしたかを、追跡できなければならない。

### SEC-017 — Untrusted deletion claims do not restore confidentiality

untrustedなWorkerが「入力を削除した」と申告しても、それだけを根拠に、一度開示した平文データの機密性が回復したとみなしてはならない。

### SEC-018 — Resource donation must remain visible and stoppable where participation is voluntary

Browserや個人の端末など、利用者自身の意思によるアドホックな参加では、資源の提供を本人から隠すことを、安全な標準の動作としてはならない。

### SEC-019 — Federated identity keeps its namespace binding

Federationされたidentityを検証するとき、発行したTrust DomainやIdentity Authorityとのbindingを、失ってはならない。

異なるTrust Domainの、Trust Materialを暗黙のうちに併合し、どれか一つで検証できれば同じIdentity namespaceとして扱うことを、禁止する。

### SEC-020 — Artifact expansion remains resource-bounded

Artifactの受理、検査、展開、変換によって、CPU、Memory、Disk、inode、fileの数、入れ子の深さ、wall timeなどを、無制限に消費できてはならない。

Archive内のentryの名前やpathだけで、Sandboxの外にあるfilesystemに対するauthorityを得られてはならない。

### SEC-021 — Partial control-plane compromise does not imply total authority

Control Plane内部の一つのComponentが侵害されたことだけで、Task admission、Dataに対する権限、Executor admission、Federationの管理、Verificationの権限、root signing authorityなど、すべての権限へ自動的に昇格してはならない。

Architectureの上で、役割に応じた権限の分離を、可能にする。

### SEC-022 — Resource reclaim cannot override local safety

ProviderやEndpointのlocalなSafety Governorが、capacityを縮小または停止した場合に、Taskを完了させること、Contributionを得ること、中央のSchedulerの判断を理由にして、その上限を強制的に広げてはならない。

### SEC-023 — Reclaim and retry must not amplify without bound

多数のWorkerの同時のreclaim、network partition、拠点の停止などをきっかけに、retryやmigrationが際限なく増幅して、Fabric全体の資源を枯渇させてはならない。

以下のSEC-024〜SEC-030は、2026-10-20に、RQ-054〜RQ-063とA17〜A22に合わせて追加しました。

### SEC-024 — Designation narrows, never widens

Clientが委任先を指名したことによって、予算、Data Class、必要な信頼の水準、利用の許可が、安全性の低い方向へ変わってはならない。個々のWorkerを指名だけで選べてはならない（RQ-016、RQ-059）。

### SEC-025 — Delegation class is registered, not asserted

委任先の自己申告、評判、Schedulerの推定によって、委任の等級が上がってはならない。等級に属さない委任先へ、仕事を任せてはならない。等級を上げる操作は、管理者権限の操作として、根拠とともに監査の記録に残らなければならない（RQ-058）。

### SEC-026 — No authority without expiry

実行の権限、常駐の権限、管理者権限の委任状のいずれも、期限を持たない形で発行してはならない。更新されなかった権限は、期限で失効しなければならない（RQ-060）。

### SEC-027 — Mandates cannot exceed or renew themselves

管理者権限の委任状を持つ主体は、委任状の範囲、越えさせない線、人間の承認を求める閾値を越えてはならず、委任状を自分で更新してはならない。委任状の期限が切れた後に、その委任状のもとで新しい予算の配分や容量の確保を行ってはならない（RQ-061）。

### SEC-028 — Cost-generating capacity is separately authorized and reserved first

費用を生む容量の確保は、依頼の受理と予算の配分から分けた権限で承認しなければならない。確保の費用は、確保の前に、依頼者と組織の予算の枠から予約しなければならない。依頼者の申告だけで、容量を確保する権限が生まれてはならない（RQ-057）。

### SEC-029 — Delegated execution success is a claim

委任先の内部の実行管理系が報告した成功や状態を、確定した事実として扱ってはならない。副作用を持つ仕事、または再実行の安全の根拠がない仕事を、内部の自動の再実行を無効にできない委任先へ配置してはならない（RQ-054）。

### SEC-030 — Providing resources outward confers no inward authority

組織が外部へ資源を提供したことによって、提供先の仕事が、組織の内部のnetworkの一員となったり、組織のDataやほかの仕事への権限を得たりしてはならない（RQ-033、RQ-034）。

## 可用性についての前提

EKIは、あらゆるDoSを完全に防げるとは主張しません。

次のような事象は、運用やcapacity planningを含めて扱う必要があります。十分な規模の攻撃者が、自分に正当に割り当てられたquotaを使い続ける。network pathそのものを遮断する。Control Planeの外部の依存先を停止させる。

EKIが要求するのは、次のものが、許可された境界を越えて被害を無制限に増幅しないことです。単一のClient、単一のWorker、単一のCredential、単一の不正な形式の入力、単一のControl Plane component、単一のreclaim event。

## Result Integrityについての原則

Result Verificationに、単一の方式を強制しません。

使える方式には、たとえば次のものがあります。

- 決定的な処理での、完全な一致
- 数値の許容誤差や、canonicalizationを使った比較
- アプリケーション固有のvalidator
- replicationとquorum
- 管理下のWorkerによるrecomputation
- Remote Attestation
- 暗号学的なverifiable computation

ただし、「複数の方式を選べる」ことと、「すべてのTaskについて完全に検証できる」こととは、別です。

生成AIの文章のように、正解を安価に判定できないTaskでは、確認できるprovenanceや実行の条件を返したうえで、semantic correctnessの保証の上限を明示します。

## 機密性についての原則

一度、untrustedなWorkerに平文を渡した場合、そのWorkerがコピーを持っていないことを、一般には証明できません。

そのため、機密性が必要なTaskでは、どこを信頼の境界にするかを明示する必要があります。候補は、組織の管理下にあるWorker、契約や運用の上の管理の境界、Remote Attestationで確認したConfidential Computingの環境などです。

暗号化された通信路だけでは、実行中の平文をWorkerのownerから守るのに、十分ではありません。

## Federationについての原則

Federationは、相手のTrust Domainが発行したIdentityを、検証できるようにするための関係です。この関係そのものを、包括的なAuthorizationとして扱いません。

Identityの意味は、そのIdentityを発行したnamespaceやauthorityと、一体のものとして扱います。Federationの設定、Trust Domainの名前、Trust Materialのbindingは、Securityの上で重要な設定です。

また、AがBを認証でき、BがCを認証できるからといって、AがCを暗黙のうちに信頼する、推移的なTrustを自動的に作りません。

## Artifactの処理についての原則

Artifactの署名、Attestation、Digestが正しいことは、その内容を安全にparseできることを意味しません。

EKIでは、少なくとも次の3つを分けます。

```text
Provenance
  どこから作られたか

Content Integrity
  期待したbyte列か

Safe Processing
  許可されたResourceとAuthorityの範囲で処理できるか
```

## 信頼の基盤まで侵害された場合

次のものがすべて同時に敵対的であると仮定することは、通常のEKIのSecurity Guaranteeの外に置きます。

- Control Planeのroot signing authority
- Administratorのpolicy authority（委任の等級の登録と、委任状の発行を含む）
- OS、hypervisor、TEEのrootを、信頼の境界として採用した場合の、そのroot
- 土台となる暗号のprimitive

特定のTaskで、一つのValidatorを明示的に信頼の基盤とした場合、そのValidatorが侵害されると、その保証のProfileが破れる可能性があります。

一方で、SchedulerやTelemetryなどの、一部のComponentの侵害については、すぐにこの保証の外に出すことはしません。権限の分離によってblast radiusを限定できる構成を、Architectureで評価します。

委任状を持つ主体（SEIなど）は、信頼の基盤ではありません。委任状を持つ主体が侵害されても、被害は委任状の範囲と期限の中に収まらなければならず（SEC-027）、この保証の外に出しません。

## Architectureに入る前に、追加で調査する攻撃面

- Worker Sybilとcollusionに対する、independence model
- Browserのidentity、storage、worker lifecycle、明示的に参加するためのUI
- GPU driverと、利用者が持ち込むGPU workloadのattack surface
- Executorのsupply-chain compromise
- Artifactの展開、parser bomb、安全な展開
- Federationのbundleや、identity namespaceの取り違え
- Federationの設定の変更に対する、authority
- Contributionのlaundering、self-dealing
- 悪意のある、または侵害された、ValidatorとVerifier
- Attestation Evidenceのfreshness、privacy、replay
- 侵害されたControl Plane component同士の、blast-radiusの分離
- Mass reclaimとretry storm
- Endpointのenergy、thermal、device-safetyの強制
- 副作用を持つTaskで「実行済みか不明」になった場合の、安全なRecoveryの境界
- 委任先の内部の実行管理系（管理クラスタ、クラウドの関数実行）の、自動の再実行を制御できる範囲
- 委任の等級の引き上げに、複数人の承認を求めるかどうか
- 委任状を持つ自動化された意思決定が、外部の文書に仕込まれた指示に影響される場合の、影響の範囲
- 費用を生む容量の確保を誘発する攻撃に対する、予約と上限の実効性
- 外部へ提供した資源の上での、提供先の仕事の隔離の水準
